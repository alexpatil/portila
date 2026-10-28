package test

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"portila/pkg/client"
	"portila/pkg/config"
	"portila/pkg/server"
)

func TestReverseTunnel(t *testing.T) {
	localOrigin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "HELLO-FROM-LOCAL-SERVICE")
	}))
	defer localOrigin.Close()

	localAddr := localOrigin.Listener.Addr().String()

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

	time.Sleep(1 * time.Second)

	conn, err := net.DialTimeout("tcp", vpsListenAddr, 3*time.Second)
	if err != nil {
		t.Fatalf("Failed to dial VPS port %s: %v", vpsListenAddr, err)
	}
	defer conn.Close()

	if _, err := fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatalf("Failed to send HTTP request: %v", err)
	}

	resp, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("Failed to read HTTP response from VPS port: %v", err)
	}

	if !strings.Contains(string(resp), "HELLO-FROM-LOCAL-SERVICE") {
		t.Fatalf("Unexpected response through reverse tunnel: %s", string(resp))
	}
	t.Logf("✓ Reverse tunnel successfully served: HELLO-FROM-LOCAL-SERVICE")
}

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

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", vpsPort), 2*time.Second)
	if err != nil {
		t.Fatalf("Failed to connect to VPS port %d: %v", vpsPort, err)
	}
	defer conn.Close()

	start := time.Now()
	buf := make([]byte, 1)
	_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	_, err = conn.Read(buf)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("Expected connection drop/EOF for idle probe, but read succeeded")
	}

	if elapsed < 4500*time.Millisecond || elapsed > 7500*time.Millisecond {
		t.Fatalf("Expected ~5s timeout, took: %v", elapsed)
	}
	t.Logf("✓ Idle slowloris probe safely dropped in %v", elapsed)
}

func TestMultiPortServiceRouting(t *testing.T) {
	svcAlpha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "SERVICE-ALPHA-CONTENT")
	}))
	defer svcAlpha.Close()

	svcBeta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "SERVICE-BETA-CONTENT")
	}))
	defer svcBeta.Close()

	quicAddr := fmt.Sprintf("127.0.0.1:%d", freeUDPPort(t))
	portA := freeTCPPort(t)
	portB := freeTCPPort(t)

	srv := server.NewTunnelServer(&config.ServerConfig{Server: config.ServerSettings{
		QuicAddress:     quicAddr,
		Password:        testPassword,
		KeepAlivePeriod: 25,
		MaxIdleTimeout:  600,
		TLS:             config.TLSConfig{CertValidityDays: 365},
		Limits:          config.LimitConfig{MaxConcurrentUsers: 100, QueueWaitSeconds: 10},
		Clients: map[string]config.ClientPortConfig{
			"multi_client": {Enabled: true, Ports: []int{portA, portB}, Name: "multi_service"},
		},
	}})
	go srv.Start()
	defer srv.Stop()

	cli := client.NewTunnelClient(&config.ClientConfig{Client: config.ClientSettings{
		ServerAddress: quicAddr,
		Password:      testPassword,
		ClientID:      "multi_client",
		Mode:          "reverse",
		Tunnels: map[int]string{
			portA: svcAlpha.Listener.Addr().String(),
			portB: svcBeta.Listener.Addr().String(),
		},
		KeepAlivePeriod: 25,
		MaxIdleTimeout:  600,
		TLS:             config.TLSConfig{InsecureSkipVerify: true},
	}})
	go cli.Start()
	defer cli.Stop()

	time.Sleep(1 * time.Second)

	// Verify Service Alpha
	connA, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", portA), 2*time.Second)
	if err != nil {
		t.Fatalf("Failed to dial portA %d: %v", portA, err)
	}
	fmt.Fprintf(connA, "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
	bodyA, _ := io.ReadAll(connA)
	connA.Close()
	if !strings.Contains(string(bodyA), "SERVICE-ALPHA-CONTENT") {
		t.Fatalf("Port A response mismatch: %s", string(bodyA))
	}

	// Verify Service Beta
	connB, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", portB), 2*time.Second)
	if err != nil {
		t.Fatalf("Failed to dial portB %d: %v", portB, err)
	}
	fmt.Fprintf(connB, "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
	bodyB, _ := io.ReadAll(connB)
	connB.Close()
	if !strings.Contains(string(bodyB), "SERVICE-BETA-CONTENT") {
		t.Fatalf("Port B response mismatch: %s", string(bodyB))
	}

	t.Logf("✓ Multi-port routing verified: port %d -> Alpha, port %d -> Beta", portA, portB)
}

func TestReversePayloadIntegrity(t *testing.T) {
	const payloadSize = 5 * 1024 * 1024
	testData := make([]byte, payloadSize)
	for i := range testData {
		testData[i] = byte(i % 251)
	}
	expectedHash := sha256.Sum256(testData)

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", payloadSize))
		w.Write(testData)
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
		Limits:          config.LimitConfig{MaxConcurrentUsers: 10, QueueWaitSeconds: 10},
		Clients: map[string]config.ClientPortConfig{
			"hash_client": {Enabled: true, Ports: []int{vpsPort}, Name: "hash_test"},
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

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", vpsPort), 3*time.Second)
	if err != nil {
		t.Fatalf("Dial VPS: %v", err)
	}
	defer conn.Close()

	fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")

	resp, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("Read response: %v", err)
	}

	headerEnd := strings.Index(string(resp), "\r\n\r\n")
	if headerEnd == -1 {
		t.Fatalf("Could not locate HTTP body start")
	}
	body := resp[headerEnd+4:]

	if len(body) != payloadSize {
		t.Fatalf("Payload length mismatch: got %d, expected %d", len(body), payloadSize)
	}

	actualHash := sha256.Sum256(body)
	if actualHash != expectedHash {
		t.Fatalf("SHA-256 integrity mismatch!")
	}
	t.Logf("✓ 5MB data transferred through reverse tunnel with 100%% SHA-256 integrity match")
}
