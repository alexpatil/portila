package test

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"portila/pkg/client"
	"portila/pkg/config"
	"portila/pkg/server"
)

const (
	testClientID = "test_client"
	testPassword = "test-pass"
	bigSize      = 4 << 20 // 4 MB response body for the half-close test
)

// originStats observes how many requests the origin handles at once.
type originStats struct {
	inFlight atomic.Int64
	maxConc  atomic.Int64
	total    atomic.Int64
}

func (o *originStats) enter() {
	c := o.inFlight.Add(1)
	o.total.Add(1)
	for {
		m := o.maxConc.Load()
		if c <= m || o.maxConc.CompareAndSwap(m, c) {
			return
		}
	}
}

func (o *originStats) leave() { o.inFlight.Add(-1) }

// setupTunnel starts an origin server, the meghlock server (VPS role) and the
// meghlock client (Termux role) fully in-process and returns the public proxy
// address plus the origin target address.
func setupTunnel(t *testing.T, delay time.Duration, maxStreams int64, perHost int) (proxyAddr, target string, stats *originStats, stop func()) {
	t.Helper()
	stats = &originStats{}

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stats.enter()
		defer stats.leave()
		if delay > 0 {
			time.Sleep(delay)
		}
		if r.URL.Path == "/big" {
			// Stream a large body to exercise half-close: the client stops
			// sending while this response is still flowing.
			_, _ = w.Write(make([]byte, bigSize))
			return
		}
		io.WriteString(w, "OK-MEGHLOCK")
	}))
	target = origin.Listener.Addr().String()

	quicAddr := fmt.Sprintf("127.0.0.1:%d", freeUDPPort(t))
	tcpPort := freeTCPPort(t)
	proxyAddr = fmt.Sprintf("127.0.0.1:%d", tcpPort)

	srv := server.NewTunnelServer(&config.ServerConfig{Server: config.ServerSettings{
		QuicAddress:     quicAddr,
		Password:        testPassword,
		KeepAlivePeriod: 25,
		MaxIdleTimeout:  600,
		TLS:             config.TLSConfig{CertValidityDays: 365},
		Quic:            config.QuicConfig{KeepAlivePeriod: 25},
		Limits:          config.LimitConfig{MaxConcurrentUsers: 1000, QueueWaitSeconds: 30},
		Clients: map[string]config.ClientPortConfig{
			testClientID: {Enabled: true, Ports: []int{tcpPort}, Name: "test"},
		},
	}})
	go srv.Start()

	cli := client.NewTunnelClient(&config.ClientConfig{Client: config.ClientSettings{
		ServerAddress:   quicAddr,
		Password:        testPassword,
		ClientID:        testClientID,
		Mode:            "exit",
		KeepAlivePeriod: 25,
		MaxIdleTimeout:  600,
		TLS:             config.TLSConfig{InsecureSkipVerify: true},
		Quic: config.QuicConfig{
			MaxIncomingStreams:             maxStreams,
			InitialStreamReceiveWindow:     4,
			MaxStreamReceiveWindow:         16,
			InitialConnectionReceiveWindow: 8,
			MaxConnectionReceiveWindow:     32,
		},
		Limits: config.ClientLimits{PerHostConcurrency: perHost, QueueWaitSeconds: 30},
	}})
	go cli.Start()

	waitReady(t, proxyAddr, target)

	stop = func() {
		_ = cli.Stop()
		_ = srv.Stop()
		origin.Close()
	}
	return proxyAddr, target, stats, stop
}

// proxyRequest performs an HTTP CONNECT to the proxy, then a plain GET through
// the tunnel — exactly how a browser configured with an HTTPS proxy behaves.
func proxyRequest(proxyAddr, target string) (time.Duration, error) {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", proxyAddr, 5*time.Second)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))

	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target); err != nil {
		return 0, err
	}

	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		return 0, fmt.Errorf("read CONNECT status: %w", err)
	}
	if !strings.Contains(status, "200") {
		return 0, fmt.Errorf("CONNECT rejected: %s", strings.TrimSpace(status))
	}
	for { // consume CONNECT response headers up to the blank line
		line, err := br.ReadString('\n')
		if err != nil {
			return 0, err
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}

	if _, err := fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", target); err != nil {
		return 0, err
	}
	body, err := io.ReadAll(br)
	if err != nil {
		return 0, err
	}
	if !strings.Contains(string(body), "OK-MEGHLOCK") {
		return 0, fmt.Errorf("unexpected body: %q", string(body))
	}
	return time.Since(start), nil
}

func TestTunnelSmoke(t *testing.T) {
	proxyAddr, target, _, stop := setupTunnel(t, 0, 1, 0)
	defer stop()

	lat, err := proxyRequest(proxyAddr, target)
	if err != nil {
		t.Fatalf("round trip failed: %v", err)
	}
	t.Logf("RESULT smoke: single request OK in %v", lat)
}

func TestTunnelConcurrency(t *testing.T) {
	const (
		n     = 50
		delay = 200 * time.Millisecond
	)
	proxyAddr, target, stats, stop := setupTunnel(t, delay, 2048, 0)
	defer stop()

	var wg sync.WaitGroup
	var ok, fail atomic.Int64
	lat := make([]time.Duration, n)

	wallStart := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			d, err := proxyRequest(proxyAddr, target)
			if err != nil {
				fail.Add(1)
				t.Logf("req %d failed: %v", idx, err)
				return
			}
			ok.Add(1)
			lat[idx] = d
		}(i)
	}
	wg.Wait()
	wall := time.Since(wallStart)

	var sum, max time.Duration
	for _, d := range lat {
		sum += d
		if d > max {
			max = d
		}
	}
	avg := time.Duration(0)
	if ok.Load() > 0 {
		avg = sum / time.Duration(ok.Load())
	}

	t.Logf("RESULT concurrency: n=%d ok=%d fail=%d", n, ok.Load(), fail.Load())
	t.Logf("RESULT timing: wall=%v avg=%v max=%v (per-request origin delay=%v)", wall, avg, max, delay)
	t.Logf("RESULT origin: peak_concurrent=%d total_served=%d", stats.maxConc.Load(), stats.total.Load())
	t.Logf("INTERPRET: serial would take ~%v; observed %v", time.Duration(n)*delay, wall)

	if ok.Load() != n {
		t.Fatalf("expected %d successful requests, got %d", n, ok.Load())
	}
}

// TestTunnelSerial proves the system serializes requests when the client
// advertises MaxIncomingStreams=1: the origin must never see more than one
// request at a time, and total wall time grows with N (one-by-one).
func TestTunnelSerial(t *testing.T) {
	const (
		n     = 12
		delay = 100 * time.Millisecond
	)
	proxyAddr, target, stats, stop := setupTunnel(t, delay, 1, 0)
	defer stop()

	var wg sync.WaitGroup
	var ok, fail atomic.Int64

	wallStart := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if _, err := proxyRequest(proxyAddr, target); err != nil {
				fail.Add(1)
				t.Logf("req %d failed: %v", idx, err)
				return
			}
			ok.Add(1)
		}(i)
	}
	wg.Wait()
	wall := time.Since(wallStart)

	t.Logf("RESULT serial: n=%d ok=%d fail=%d", n, ok.Load(), fail.Load())
	t.Logf("RESULT serial timing: wall=%v (serial baseline ~%v)", wall, time.Duration(n)*delay)
	t.Logf("RESULT serial origin: peak_concurrent=%d total_served=%d", stats.maxConc.Load(), stats.total.Load())

	if ok.Load() != n {
		t.Fatalf("expected %d successful requests, got %d", n, ok.Load())
	}
	if peak := stats.maxConc.Load(); peak != 1 {
		t.Fatalf("expected origin peak concurrency 1 (strict serial), got %d", peak)
	}
}

// TestTunnelHalfClose proves the half-close fix: the client finishes sending
// (TCP FIN on its write half) while a large response is still streaming back.
// The full response must arrive — under the old "first direction wins" teardown
// it was truncated.
func TestTunnelHalfClose(t *testing.T) {
	proxyAddr, target, _, stop := setupTunnel(t, 0, 1, 0)
	defer stop()

	conn, err := net.DialTimeout("tcp", proxyAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	br := bufio.NewReader(conn)
	if status, _ := br.ReadString('\n'); !strings.Contains(status, "200") {
		t.Fatalf("CONNECT rejected: %s", strings.TrimSpace(status))
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read CONNECT headers: %v", err)
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}

	fmt.Fprintf(conn, "GET /big HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", target)
	// Half-close our write side; the origin keeps streaming the large response.
	if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatalf("half-close: %v", err)
	}

	resp, err := http.ReadResponse(br, &http.Request{Method: "GET"})
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body (truncated?): %v", err)
	}
	t.Logf("RESULT half-close: received %d bytes (expected %d)", len(body), bigSize)
	if len(body) != bigSize {
		t.Fatalf("TRUNCATED: got %d bytes, want %d", len(body), bigSize)
	}
}

// TestLoadManyUsers simulates N users each making several sequential requests
// through one phone, under different concurrency limits. It reports latency
// percentiles so the serial-vs-bounded trade-off is concrete. Skipped in -short.
func TestLoadManyUsers(t *testing.T) {
	if testing.Short() {
		t.Skip("load test")
	}
	const (
		users       = 20
		reqsPerUser = 5
		delay       = 100 * time.Millisecond // simulated work at the exit per request
	)

	for _, tc := range []struct {
		name       string
		maxStreams int64
	}{
		{"serial_max1", 1},
		{"bounded_max32", 32},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proxyAddr, target, _, stop := setupTunnel(t, delay, tc.maxStreams, 0)
			defer stop()

			lats := make([]time.Duration, users*reqsPerUser)
			var fail atomic.Int64
			var wg sync.WaitGroup

			start := time.Now()
			for u := 0; u < users; u++ {
				wg.Add(1)
				go func(u int) {
					defer wg.Done()
					for r := 0; r < reqsPerUser; r++ {
						d, err := proxyRequest(proxyAddr, target)
						if err != nil {
							fail.Add(1)
							continue
						}
						lats[u*reqsPerUser+r] = d
					}
				}(u)
			}
			wg.Wait()
			wall := time.Since(start)

			ok := make([]time.Duration, 0, len(lats))
			for _, d := range lats {
				if d > 0 {
					ok = append(ok, d)
				}
			}
			sort.Slice(ok, func(i, j int) bool { return ok[i] < ok[j] })
			pct := func(p float64) time.Duration {
				if len(ok) == 0 {
					return 0
				}
				return ok[int(float64(len(ok)-1)*p)]
			}

			t.Logf("LOAD %s: %d users x %d req, work=%v/req", tc.name, users, reqsPerUser, delay)
			t.Logf("LOAD %s: wall=%v fail=%d p50=%v p99=%v max=%v",
				tc.name, wall.Round(time.Millisecond), fail.Load(),
				pct(0.50).Round(time.Millisecond), pct(0.99).Round(time.Millisecond), pct(1.0).Round(time.Millisecond))
		})
	}
}

// TestPerHostCap proves the per-site limiter: with a high global stream limit
// but per_host_concurrency=3, a burst of users on the SAME site is paced to at
// most 3 at the origin — preventing the per-IP rate-limit block — while every
// request still completes (queued, not dropped).
func TestPerHostCap(t *testing.T) {
	const (
		n       = 20
		hostCap = 3
		delay   = 100 * time.Millisecond
		maxStrm = 64 // global limit is NOT the bottleneck here; per-host is
	)
	proxyAddr, target, stats, stop := setupTunnel(t, delay, maxStrm, hostCap)
	defer stop()

	var wg sync.WaitGroup
	var ok, fail atomic.Int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := proxyRequest(proxyAddr, target); err != nil {
				fail.Add(1)
				return
			}
			ok.Add(1)
		}()
	}
	wg.Wait()

	t.Logf("RESULT per-host: n=%d cap=%d ok=%d fail=%d origin_peak=%d",
		n, hostCap, ok.Load(), fail.Load(), stats.maxConc.Load())

	if ok.Load() != n {
		t.Fatalf("expected all %d requests to complete (queued, not dropped), got %d", n, ok.Load())
	}
	if peak := stats.maxConc.Load(); peak > hostCap {
		t.Fatalf("per-host cap breached: origin saw %d concurrent, cap=%d", peak, hostCap)
	}
}

func waitReady(t *testing.T, proxyAddr, target string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := proxyRequest(proxyAddr, target); err == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("tunnel did not become ready within 20s")
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free tcp port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free udp port: %v", err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

// TestReverseTunnel exercises the Rathole / FRP style reverse proxy mode:
// Local Service (origin) -> Portila Client (Mode: reverse) -> VPS Server (Public Port) -> Public Client (direct TCP)
func TestReverseTunnel(t *testing.T) {
	// 1. Local backend service
	localOrigin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "HELLO-FROM-LOCAL-SERVICE")
	}))
	defer localOrigin.Close()

	localAddr := localOrigin.Listener.Addr().String()

	// 2. VPS Server
	quicAddr := fmt.Sprintf("127.0.0.1:%d", freeUDPPort(t))
	vpsPort := freeTCPPort(t)
	vpsListenAddr := fmt.Sprintf("127.0.0.1:%d", vpsPort)

	srv := server.NewTunnelServer(&config.ServerConfig{Server: config.ServerSettings{
		QuicAddress:     quicAddr,
		Password:        testPassword,
		KeepAlivePeriod: 25,
		MaxIdleTimeout:  600,
		TLS:             config.TLSConfig{CertValidityDays: 365},
		Limits:          config.LimitConfig{MaxConcurrentUsers: 100, QueueWaitSeconds: 10},
		Clients: map[string]config.ClientPortConfig{
			"reverse_client": {Enabled: true, Ports: []int{vpsPort}, Name: "local_dev"},
		},
	}})
	go srv.Start()
	defer srv.Stop()

	// 3. Portila Client in Reverse mode
	cli := client.NewTunnelClient(&config.ClientConfig{Client: config.ClientSettings{
		ServerAddress:   quicAddr,
		Password:        testPassword,
		ClientID:        "reverse_client",
		Mode:            "reverse",
		LocalTarget:     localAddr,
		KeepAlivePeriod: 25,
		MaxIdleTimeout:  600,
		TLS:             config.TLSConfig{InsecureSkipVerify: true},
	}})
	go cli.Start()
	defer cli.Stop()

	// 4. Wait for tunnel to be established
	time.Sleep(1 * time.Second)

	// 5. Connect directly to VPS public port without any proxy setting!
	clientHTTP := &http.Client{Timeout: 5 * time.Second}
	resp, err := clientHTTP.Get(fmt.Sprintf("http://%s/", vpsListenAddr))
	if err != nil {
		t.Fatalf("failed to connect to VPS port: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	if !strings.Contains(string(body), "HELLO-FROM-LOCAL-SERVICE") {
		t.Fatalf("unexpected body from local service: %s", string(body))
	}

	t.Logf("✓ Reverse tunnel successfully served: %s", string(body))
}

// Battle Test 1: Slowloris attack / Idle probe
// An attacker connects raw TCP to public VPS port and sends NO data.
// Server must disconnect within 5s deadline without hanging or blocking other connections.
func TestSlowlorisPortProbe(t *testing.T) {
	quicAddr := fmt.Sprintf("127.0.0.1:%d", freeUDPPort(t))
	vpsPort := freeTCPPort(t)

	srv := server.NewTunnelServer(&config.ServerConfig{Server: config.ServerSettings{
		QuicAddress:     quicAddr,
		Password:        testPassword,
		KeepAlivePeriod: 25,
		MaxIdleTimeout:  600,
		TLS:             config.TLSConfig{CertValidityDays: 365},
		Limits:          config.LimitConfig{MaxConcurrentUsers: 10, QueueWaitSeconds: 5},
		Clients: map[string]config.ClientPortConfig{
			"dummy_client": {Enabled: true, Ports: []int{vpsPort}, Name: "dummy"},
		},
	}})
	go srv.Start()
	defer srv.Stop()

	cli := client.NewTunnelClient(&config.ClientConfig{Client: config.ClientSettings{
		ServerAddress:   quicAddr,
		Password:        testPassword,
		ClientID:        "dummy_client",
		Mode:            "reverse",
		KeepAlivePeriod: 25,
		MaxIdleTimeout:  600,
		TLS:             config.TLSConfig{InsecureSkipVerify: true},
	}})
	go cli.Start()
	defer cli.Stop()

	time.Sleep(1 * time.Second)

	// Connect TCP to VPS port
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", vpsPort), 2*time.Second)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	// Send NO data. Server should close connection after 5s deadline.
	start := time.Now()
	buf := make([]byte, 1)
	_ = conn.SetReadDeadline(time.Now().Add(7 * time.Second))
	_, err = conn.Read(buf)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected EOF or error from server dropping idle connection, got nil")
	}

	if elapsed > 6500*time.Millisecond {
		t.Errorf("idle probe took too long to be dropped: %v", elapsed)
	}
	t.Logf("✓ Idle slowloris probe safely dropped in %v", elapsed)
}

// Battle Test 2: Multi-port service routing (FRP / Rathole style)
// Public Port A -> Local Service Alpha
// Public Port B -> Local Service Beta
func TestMultiPortServiceRouting(t *testing.T) {
	originAlpha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "RESPONSE-ALPHA")
	}))
	defer originAlpha.Close()

	originBeta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "RESPONSE-BETA")
	}))
	defer originBeta.Close()

	quicAddr := fmt.Sprintf("127.0.0.1:%d", freeUDPPort(t))
	portAlpha := freeTCPPort(t)
	portBeta := freeTCPPort(t)

	srv := server.NewTunnelServer(&config.ServerConfig{Server: config.ServerSettings{
		QuicAddress:     quicAddr,
		Password:        testPassword,
		KeepAlivePeriod: 25,
		MaxIdleTimeout:  600,
		TLS:             config.TLSConfig{CertValidityDays: 365},
		Limits:          config.LimitConfig{MaxConcurrentUsers: 100, QueueWaitSeconds: 10},
		Clients: map[string]config.ClientPortConfig{
			"multi_client": {Enabled: true, Ports: []int{portAlpha, portBeta}, Name: "multi"},
		},
	}})
	go srv.Start()
	defer srv.Stop()

	cli := client.NewTunnelClient(&config.ClientConfig{Client: config.ClientSettings{
		ServerAddress:   quicAddr,
		Password:        testPassword,
		ClientID:        "multi_client",
		Mode:            "reverse",
		KeepAlivePeriod: 25,
		MaxIdleTimeout:  600,
		TLS:             config.TLSConfig{InsecureSkipVerify: true},
		Tunnels: map[int]string{
			portAlpha: originAlpha.Listener.Addr().String(),
			portBeta:  originBeta.Listener.Addr().String(),
		},
	}})
	go cli.Start()
	defer cli.Stop()

	time.Sleep(1 * time.Second)

	httpCli := &http.Client{Timeout: 5 * time.Second}

	// Request Port Alpha
	respA, err := httpCli.Get(fmt.Sprintf("http://127.0.0.1:%d/", portAlpha))
	if err != nil {
		t.Fatalf("portAlpha get failed: %v", err)
	}
	bodyA, _ := io.ReadAll(respA.Body)
	respA.Body.Close()

	if string(bodyA) != "RESPONSE-ALPHA" {
		t.Errorf("expected RESPONSE-ALPHA, got %s", string(bodyA))
	}

	// Request Port Beta
	respB, err := httpCli.Get(fmt.Sprintf("http://127.0.0.1:%d/", portBeta))
	if err != nil {
		t.Fatalf("portBeta get failed: %v", err)
	}
	bodyB, _ := io.ReadAll(respB.Body)
	respB.Body.Close()

	if string(bodyB) != "RESPONSE-BETA" {
		t.Errorf("expected RESPONSE-BETA, got %s", string(bodyB))
	}

	t.Logf("✓ Multi-port routing verified: port %d -> Alpha, port %d -> Beta", portAlpha, portBeta)
}

// Battle Test 3: Payload Integrity (5MB transfer SHA-256 match)
func TestReversePayloadIntegrity(t *testing.T) {
	// Generate 5MB of deterministic test data
	size := 5 * 1024 * 1024
	testData := make([]byte, size)
	for i := range testData {
		testData[i] = byte(i % 251)
	}
	expectedHash := sha256.Sum256(testData)

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(testData)
	}))
	defer origin.Close()

	quicAddr := fmt.Sprintf("127.0.0.1:%d", freeUDPPort(t))
	vpsPort := freeTCPPort(t)

	srv := server.NewTunnelServer(&config.ServerConfig{Server: config.ServerSettings{
		QuicAddress:     quicAddr,
		Password:        testPassword,
		KeepAlivePeriod: 25,
		MaxIdleTimeout:  600,
		TLS:             config.TLSConfig{CertValidityDays: 365},
		Limits:          config.LimitConfig{MaxConcurrentUsers: 100, QueueWaitSeconds: 15},
		Clients: map[string]config.ClientPortConfig{
			"hash_client": {Enabled: true, Ports: []int{vpsPort}},
		},
	}})
	go srv.Start()
	defer srv.Stop()

	cli := client.NewTunnelClient(&config.ClientConfig{Client: config.ClientSettings{
		ServerAddress:   quicAddr,
		Password:        testPassword,
		ClientID:        "hash_client",
		Mode:            "reverse",
		LocalTarget:     origin.Listener.Addr().String(),
		KeepAlivePeriod: 25,
		MaxIdleTimeout:  600,
		TLS:             config.TLSConfig{InsecureSkipVerify: true},
	}})
	go cli.Start()
	defer cli.Stop()

	time.Sleep(1 * time.Second)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/data", vpsPort))
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	defer resp.Body.Close()

	received, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	actualHash := sha256.Sum256(received)
	if !bytes.Equal(expectedHash[:], actualHash[:]) {
		t.Fatalf("SHA-256 hash mismatch! Data corrupted across reverse tunnel")
	}

	t.Logf("✓ 5MB data transferred through reverse tunnel with 100%% SHA-256 integrity match")
}
