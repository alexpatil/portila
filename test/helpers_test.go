package test

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
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
	bigSize      = 4 << 20 // 4 MB payload for half-close stream testing
)

// originStats monitors active concurrent requests at the test origin.
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

// setupTunnel initializes an in-process server, client, and mock origin HTTP target.
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
			_, _ = w.Write(make([]byte, bigSize))
			return
		}
		io.WriteString(w, "OK-PORTILA")
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

// proxyRequest executes an HTTP CONNECT handshake followed by a GET request.
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
	for {
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
	if !strings.Contains(string(body), "OK-PORTILA") {
		return 0, fmt.Errorf("unexpected body: %q", string(body))
	}
	return time.Since(start), nil
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

func dialWithRetry(t *testing.T, network, addr string, timeout time.Duration) net.Conn {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		conn, err := net.DialTimeout(network, addr, 200*time.Millisecond)
		if err == nil {
			return conn
		}
		if time.Now().After(deadline) {
			t.Fatalf("failed to dial %s within %v: %v", addr, timeout, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
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
