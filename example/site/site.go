// Package site is a small demo website used to exercise the tunnel locally.
// It mimics a real site: a login form, an "exit IP" view, and pages that each
// pull a few shared + page-specific CSS/JS assets (which browsers cache).
package site

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Creds are the demo login accounts (username -> password).
var Creds = map[string]string{
	"alice": "password1",
	"bob":   "hunter2",
	"carol": "letmein3",
	"dave":  "qwerty45",
	"erin":  "s3cr3t6",
}

// Pages are the content pages; each pulls shared + page-specific assets.
var Pages = []string{"dashboard", "profile", "billing", "reports", "settings"}

// AssetURLs returns the asset URLs a page references: 2 shared + 2 page-specific.
func AssetURLs(page string) []string {
	return []string{
		"/static/common.css", "/static/app.js",
		"/static/" + page + ".css", "/static/" + page + ".js",
	}
}

// Stats records concurrency and the source IPs the site observes.
type Stats struct {
	inFlight atomic.Int64
	Peak     atomic.Int64
	Requests atomic.Int64

	mu  sync.Mutex
	ips map[string]int
}

func (s *Stats) enter(ip string) {
	s.Requests.Add(1)
	if n := s.inFlight.Add(1); n > s.Peak.Load() {
		s.Peak.Store(n)
	}
	s.mu.Lock()
	s.ips[ip]++
	s.mu.Unlock()
}

func (s *Stats) leave() { s.inFlight.Add(-1) }

// IPs returns a copy of source-IP -> request-count.
func (s *Stats) IPs() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int, len(s.ips))
	for k, v := range s.ips {
		out[k] = v
	}
	return out
}

// New builds the demo site handler. workDelay is added to each asset response
// to make concurrency observable in load tests (use 0 for full speed).
func New(workDelay time.Duration) (http.Handler, *Stats) {
	st := &Stats{ips: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", landing)
	mux.HandleFunc("/login", login)
	mux.HandleFunc("/p/", pageView)
	mux.HandleFunc("/whoami", whoami)
	mux.HandleFunc("/static/", staticAsset(workDelay))
	return track(st, mux), st
}

func track(st *Stats, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.enter(clientIP(r))
		defer st.leave()
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func landing(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	writeHTML(w, `<h1>Portila Demo</h1>
<link rel="stylesheet" href="/static/common.css"><script src="/static/app.js"></script>
<form method="post" action="/login">
  user: <input name="user"> pass: <input name="pass" type="password">
  <button>login</button>
</form>`)
}

func login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	user := r.FormValue("user")
	if pass, ok := Creds[user]; !ok || pass != r.FormValue("pass") {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "session", Value: user, Path: "/"})
	writeHTML(w, "logged in as "+user)
}

func pageView(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie("session")
	if err != nil || Creds[c.Value] == "" {
		http.Error(w, "login required", http.StatusUnauthorized)
		return
	}
	page := strings.TrimPrefix(r.URL.Path, "/p/")

	var b strings.Builder
	fmt.Fprintf(&b, "<h1>%s</h1><p>logged in as %s</p><p>exit IP seen by site: %s</p>",
		page, c.Value, clientIP(r))
	for _, a := range AssetURLs(page) {
		if strings.HasSuffix(a, ".css") {
			fmt.Fprintf(&b, `<link rel="stylesheet" href="%s">`, a)
		} else {
			fmt.Fprintf(&b, `<script src="%s"></script>`, a)
		}
	}
	writeHTML(w, b.String())
}

func whoami(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, clientIP(r))
}

func staticAsset(delay time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if delay > 0 {
			time.Sleep(delay)
		}
		name := strings.TrimPrefix(r.URL.Path, "/static/")
		w.Header().Set("Cache-Control", "public, max-age=3600") // browsers cache these
		if strings.HasSuffix(name, ".css") {
			w.Header().Set("Content-Type", "text/css")
		} else {
			w.Header().Set("Content-Type", "application/javascript")
		}
		fmt.Fprintf(w, "/* %s */\n", name)
		_, _ = w.Write([]byte(strings.Repeat("x", 2000))) // ~2 KB asset
	}
}

func writeHTML(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, "<!doctype html><html><body>%s</body></html>", body)
}
