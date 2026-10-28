// Command loadtest boots a portila server+client in-process and drives N
// browser-like users (each: login, then load pages whose CSS/JS assets are
// fetched in parallel and cached) through the CONNECT proxy, then reports
// latency, per-site concurrency, and the shared exit IP.
package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"portila/example/site"
)

func main() {
	users := flag.Int("users", 10, "concurrent simulated users")
	pages := flag.Int("pages", 5, "pages each user visits")
	perHost := flag.Int("perhost", 3, "per-site concurrency cap (0 = unlimited)")
	streams := flag.Int64("streams", 32, "global tunnel stream limit")
	work := flag.Duration("work", 30*time.Millisecond, "per-asset delay at the site")
	think := flag.Duration("think", 100*time.Millisecond, "user pause between pages")
	flag.Parse()

	if *pages > len(site.Pages) {
		*pages = len(site.Pages)
	}

	handler, stats := site.New(*work)
	origin := httptest.NewTLSServer(handler)
	defer origin.Close()

	proxyAddr, stop := bootTunnel(*perHost, *streams)
	defer stop()
	proxyURL, _ := url.Parse("http://" + proxyAddr)

	if err := waitReady(proxyURL, origin.URL); err != nil {
		fmt.Println("tunnel not ready:", err)
		return
	}

	accounts := make([]string, 0, len(site.Creds))
	for u := range site.Creds {
		accounts = append(accounts, u)
	}

	var reqs, fails atomic.Int64
	var mu sync.Mutex
	var pageLat []time.Duration

	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < *users; i++ {
		wg.Add(1)
		user := accounts[i%len(accounts)]
		go func(user string) {
			defer wg.Done()
			lat := runUser(newBrowser(proxyURL), origin.URL, user, site.Creds[user], site.Pages[:*pages], *think, &reqs, &fails)
			mu.Lock()
			pageLat = append(pageLat, lat...)
			mu.Unlock()
		}(user)
	}
	wg.Wait()

	report(reportData{
		users: *users, pages: *pages, perHost: *perHost, streams: *streams,
		work: *work, wall: time.Since(start),
		reqs: reqs.Load(), fails: fails.Load(),
		pageLat: pageLat, peak: stats.Peak.Load(), ips: stats.IPs(),
	})
}

// newBrowser builds an http.Client that proxies via the tunnel, trusts the
// demo site's self-signed cert, and opens several connections per host like a
// real browser.
func newBrowser(proxy *url.URL) *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Timeout: 60 * time.Second,
		Jar:     jar,
		Transport: &http.Transport{
			Proxy:               http.ProxyURL(proxy),
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
			MaxConnsPerHost:     6,
			MaxIdleConnsPerHost: 6,
			IdleConnTimeout:     300 * time.Millisecond,
		},
	}
}

func runUser(c *http.Client, base, user, pass string, pages []string, think time.Duration, reqs, fails *atomic.Int64) []time.Duration {
	get := func(u string) {
		reqs.Add(1)
		resp, err := c.Get(u)
		if err != nil {
			fails.Add(1)
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			fails.Add(1)
		}
	}

	// Log in (sets the session cookie in the jar).
	reqs.Add(1)
	if resp, err := c.PostForm(base+"/login", url.Values{"user": {user}, "pass": {pass}}); err != nil {
		fails.Add(1)
	} else {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			fails.Add(1)
		}
	}

	cache := map[string]bool{} // browser cache: shared assets fetched once
	var lat []time.Duration
	for _, pg := range pages {
		t := time.Now()
		get(base + "/p/" + pg) // the HTML page
		var wg sync.WaitGroup
		for _, a := range site.AssetURLs(pg) {
			if cache[a] {
				continue
			}
			cache[a] = true
			wg.Add(1)
			go func(u string) { defer wg.Done(); get(u) }(base + a)
		}
		wg.Wait()
		lat = append(lat, time.Since(t))
		time.Sleep(think)
	}
	return lat
}

func waitReady(proxy *url.URL, base string) error {
	c := newBrowser(proxy)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := c.Get(base + "/whoami"); err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for tunnel")
}

type reportData struct {
	users, pages, perHost int
	streams               int64
	work, wall            time.Duration
	reqs, fails           int64
	pageLat               []time.Duration
	peak                  int64
	ips                   map[string]int
}

func report(d reportData) {
	sort.Slice(d.pageLat, func(i, j int) bool { return d.pageLat[i] < d.pageLat[j] })
	pct := func(p float64) time.Duration {
		if len(d.pageLat) == 0 {
			return 0
		}
		return d.pageLat[int(float64(len(d.pageLat)-1)*p)].Round(time.Millisecond)
	}

	fmt.Println("\n================ Portila local simulation ================")
	fmt.Printf("config : %d users, %d pages each, per-site cap=%d, global streams=%d, asset work=%v\n",
		d.users, d.pages, d.perHost, d.streams, d.work)
	fmt.Printf("traffic: %d requests, %d failed, wall=%v\n", d.reqs, d.fails, d.wall.Round(time.Millisecond))
	fmt.Printf("page   : p50=%v p95=%v p99=%v (HTML + uncached assets per page)\n", pct(0.50), pct(0.95), pct(0.99))
	fmt.Printf("site   : peak concurrency=%d (per-site cap=%d) -> same-page bursts paced\n", d.peak, d.perHost)
	fmt.Printf("exit IP: site saw all users as %d source IP(s): %v\n", len(d.ips), d.ips)
	if d.fails == 0 {
		fmt.Println("result : all users completed (queued when busy, never dropped) [OK]")
	} else {
		fmt.Printf("result : %d requests failed\n", d.fails)
	}
	fmt.Println("===========================================================")
}
