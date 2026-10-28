package test

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"portila/pkg/client"
	"portila/pkg/config"
	"portila/pkg/server"
)

// TestReversePayloadIntegrity validates 100% SHA-256 byte integrity over a 5MB payload.
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

	conn := dialWithRetry(t, "tcp", fmt.Sprintf("127.0.0.1:%d", vpsPort), 10*time.Second)
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
