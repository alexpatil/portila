package test

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"portila/pkg/client"
	"portila/pkg/config"
	"portila/pkg/server"
)

func silenceLogs() func() {
	old := log.Writer()
	log.SetOutput(io.Discard)
	return func() { log.SetOutput(old) }
}

// BenchmarkTunnelThroughput measures raw throughput over the reverse QUIC tunnel.
func BenchmarkTunnelThroughput(b *testing.B) {
	defer silenceLogs()()
	const chunkSize = 1024 * 1024 // 1 MB
	data := make([]byte, chunkSize)
	for i := range data {
		data[i] = byte(i % 253)
	}

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", chunkSize))
		w.Write(data)
	}))
	defer origin.Close()

	quicAddr := fmt.Sprintf("127.0.0.1:%d", freeUDPPortBench(b))
	vpsPort := freeTCPPortBench(b)

	srv := server.NewTunnelServer(&config.ServerConfig{Server: config.ServerSettings{
		QuicAddress:     quicAddr,
		Password:        testPassword,
		KeepAlivePeriod: 25,
		MaxIdleTimeout:  600,
		TLS:             config.TLSConfig{CertValidityDays: 365},
		Limits:          config.LimitConfig{MaxConcurrentUsers: 1000, QueueWaitSeconds: 10},
		Clients: map[string]config.ClientPortConfig{
			"bench_client": {Enabled: true, Ports: []int{vpsPort}, Name: "bench"},
		},
	}})
	go srv.Start()
	defer srv.Stop()

	cli := client.NewTunnelClient(&config.ClientConfig{Client: config.ClientSettings{
		ServerAddress:   quicAddr,
		Password:        testPassword,
		ClientID:        "bench_client",
		Mode:            "reverse",
		LocalTarget:     origin.Listener.Addr().String(),
		KeepAlivePeriod: 25,
		MaxIdleTimeout:  600,
		TLS:             config.TLSConfig{InsecureSkipVerify: true},
	}})
	go cli.Start()
	defer cli.Stop()

	time.Sleep(1 * time.Second)

	vpsAddr := fmt.Sprintf("127.0.0.1:%d", vpsPort)
	buf := make([]byte, 32*1024)

	b.SetBytes(chunkSize)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		conn, err := net.DialTimeout("tcp", vpsAddr, 2*time.Second)
		if err != nil {
			b.Fatalf("dial: %v", err)
		}
		if _, err := fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"); err != nil {
			conn.Close()
			b.Fatalf("write: %v", err)
		}
		for {
			n, err := conn.Read(buf)
			if n == 0 || err != nil {
				break
			}
		}
		conn.Close()
	}
}

// BenchmarkTunnelLatency measures request-response round-trip latency.
func BenchmarkTunnelLatency(b *testing.B) {
	defer silenceLogs()()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "OK")
	}))
	defer origin.Close()

	quicAddr := fmt.Sprintf("127.0.0.1:%d", freeUDPPortBench(b))
	vpsPort := freeTCPPortBench(b)

	srv := server.NewTunnelServer(&config.ServerConfig{Server: config.ServerSettings{
		QuicAddress:     quicAddr,
		Password:        testPassword,
		KeepAlivePeriod: 25,
		MaxIdleTimeout:  600,
		TLS:             config.TLSConfig{CertValidityDays: 365},
		Limits:          config.LimitConfig{MaxConcurrentUsers: 1000, QueueWaitSeconds: 10},
		Clients: map[string]config.ClientPortConfig{
			"lat_client": {Enabled: true, Ports: []int{vpsPort}, Name: "latency"},
		},
	}})
	go srv.Start()
	defer srv.Stop()

	cli := client.NewTunnelClient(&config.ClientConfig{Client: config.ClientSettings{
		ServerAddress:   quicAddr,
		Password:        testPassword,
		ClientID:        "lat_client",
		Mode:            "reverse",
		LocalTarget:     origin.Listener.Addr().String(),
		KeepAlivePeriod: 25,
		MaxIdleTimeout:  600,
		TLS:             config.TLSConfig{InsecureSkipVerify: true},
	}})
	go cli.Start()
	defer cli.Stop()

	time.Sleep(1 * time.Second)

	vpsAddr := fmt.Sprintf("127.0.0.1:%d", vpsPort)
	buf := make([]byte, 1024)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		conn, err := net.DialTimeout("tcp", vpsAddr, 2*time.Second)
		if err != nil {
			b.Fatalf("dial: %v", err)
		}
		fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
		for {
			n, err := conn.Read(buf)
			if n == 0 || err != nil {
				break
			}
		}
		conn.Close()
	}
}

// BenchmarkTunnelConcurrentLoad measures multi-client concurrent throughput.
func BenchmarkTunnelConcurrentLoad(b *testing.B) {
	defer silenceLogs()()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "PONG")
	}))
	defer origin.Close()

	quicAddr := fmt.Sprintf("127.0.0.1:%d", freeUDPPortBench(b))
	vpsPort := freeTCPPortBench(b)

	srv := server.NewTunnelServer(&config.ServerConfig{Server: config.ServerSettings{
		QuicAddress:     quicAddr,
		Password:        testPassword,
		KeepAlivePeriod: 25,
		MaxIdleTimeout:  600,
		TLS:             config.TLSConfig{CertValidityDays: 365},
		Limits:          config.LimitConfig{MaxConcurrentUsers: 5000, QueueWaitSeconds: 10},
		Clients: map[string]config.ClientPortConfig{
			"conc_client": {Enabled: true, Ports: []int{vpsPort}, Name: "conc"},
		},
	}})
	go srv.Start()
	defer srv.Stop()

	cli := client.NewTunnelClient(&config.ClientConfig{Client: config.ClientSettings{
		ServerAddress:   quicAddr,
		Password:        testPassword,
		ClientID:        "conc_client",
		Mode:            "reverse",
		LocalTarget:     origin.Listener.Addr().String(),
		KeepAlivePeriod: 25,
		MaxIdleTimeout:  600,
		TLS:             config.TLSConfig{InsecureSkipVerify: true},
	}})
	go cli.Start()
	defer cli.Stop()

	time.Sleep(1 * time.Second)

	vpsAddr := fmt.Sprintf("127.0.0.1:%d", vpsPort)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		buf := make([]byte, 512)
		for pb.Next() {
			conn, err := net.DialTimeout("tcp", vpsAddr, 2*time.Second)
			if err != nil {
				continue
			}
			fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
			for {
				n, err := conn.Read(buf)
				if n == 0 || err != nil {
					break
				}
			}
			conn.Close()
		}
	})
}

func freeTCPPortBench(b *testing.B) int {
	b.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatalf("free tcp port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func freeUDPPortBench(b *testing.B) int {
	b.Helper()
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		b.Fatalf("free udp port: %v", err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}
