package test

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTunnelSmoke(t *testing.T) {
	proxyAddr, target, _, stop := setupTunnel(t, 0, 1, 0)
	defer stop()

	lat, err := proxyRequest(proxyAddr, target)
	if err != nil {
		t.Fatalf("smoke request failed: %v", err)
	}
	t.Logf("smoke request ok in %v", lat)
}

func TestTunnelHalfClose(t *testing.T) {
	proxyAddr, target, _, stop := setupTunnel(t, 0, 1, 0)
	defer stop()

	conn, err := net.DialTimeout("tcp", proxyAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil || !strings.Contains(status, "200") {
		t.Fatalf("CONNECT rejected: status=%q err=%v", status, err)
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil || line == "\r\n" || line == "\n" {
			break
		}
	}

	if _, err := fmt.Fprintf(conn, "GET /big HTTP/1.1\r\nHost: %s\r\n\r\n", target); err != nil {
		t.Fatalf("write GET: %v", err)
	}

	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.CloseWrite()
	}

	body, err := io.ReadAll(br)
	if err != nil {
		t.Fatalf("read after CloseWrite: %v", err)
	}
	if len(body) < bigSize {
		t.Fatalf("expected at least %d bytes, got %d", bigSize, len(body))
	}
}

func TestTunnelEcho(t *testing.T) {
	payload := bytes.Repeat([]byte("0123456789abcdef"), 64*1024)

	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			io.WriteString(w, "OK-PORTILA")
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(body)
	}))
	defer echo.Close()

	proxyAddr, _, _, stop := setupTunnel(t, 0, 1, 0)
	defer stop()

	target := echo.Listener.Addr().String()
	waitReady(t, proxyAddr, target)

	conn, err := net.DialTimeout("tcp", proxyAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil || !strings.Contains(status, "200") {
		t.Fatalf("CONNECT rejected: %v", status)
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil || line == "\r\n" || line == "\n" {
			break
		}
	}

	req, err := http.NewRequest(http.MethodPost, "http://"+target+"/echo", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Connection", "close")
	if err := req.Write(conn); err != nil {
		t.Fatalf("write POST: %v", err)
	}

	resp, err := http.ReadResponse(br, req)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close()

	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read echo body: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("echo mismatch: sent %d bytes, got %d bytes", len(payload), len(got))
	}
}

func TestTunnelConcurrency(t *testing.T) {
	const (
		numRequests = 30
		concurrency = 8
		originDelay = 10 * time.Millisecond
	)

	proxyAddr, target, _, stop := setupTunnel(t, originDelay, concurrency, 0)
	defer stop()

	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)
	errCh := make(chan error, numRequests)
	durations := make([]time.Duration, numRequests)

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			dur, err := proxyRequest(proxyAddr, target)
			durations[idx] = dur
			if err != nil {
				errCh <- fmt.Errorf("req %d failed: %w", idx, err)
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("request error: %v", err)
	}

	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	t.Logf("completed %d requests: min=%v p50=%v p95=%v max=%v",
		numRequests,
		durations[0],
		durations[numRequests*50/100],
		durations[numRequests*95/100],
		durations[numRequests-1],
	)
}

func TestPerHostCap(t *testing.T) {
	const (
		perHostCap  = 3
		numRequests = 20
		originDelay = 50 * time.Millisecond
	)

	proxyAddr, target, stats, stop := setupTunnel(t, originDelay, 10, perHostCap)
	defer stop()

	var wg sync.WaitGroup
	errCh := make(chan error, numRequests)

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			if _, err := proxyRequest(proxyAddr, target); err != nil {
				errCh <- fmt.Errorf("req %d: %w", idx, err)
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("error: %v", err)
	}

	peak := stats.maxConc.Load()
	if peak > perHostCap {
		t.Fatalf("origin peak concurrency was %d, expected at most %d", peak, perHostCap)
	}
	t.Logf("RESULT per-host: n=%d cap=%d ok=%d fail=0 origin_peak=%d",
		numRequests, perHostCap, stats.total.Load(), peak)
}
