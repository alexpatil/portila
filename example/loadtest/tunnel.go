package main

import (
	"fmt"
	"net"
	"os"

	"portila/pkg/client"
	"portila/pkg/config"
	"portila/pkg/server"
)

const (
	tunnelClientID = "loadtest"
	tunnelPassword = "loadtest-pass"
)

// bootTunnel starts a meghlock server + client in-process and returns the proxy
// address (the server's CONNECT port). The tunnel's internal loggers are
// silenced so only the simulation report is printed.
func bootTunnel(perHost int, maxStreams int64) (proxyAddr string, stop func()) {
	quicAddr := fmt.Sprintf("127.0.0.1:%d", freeUDPPort())
	tcpPort := freeTCPPort()

	// The server/client capture os.Stdout when their loggers are built, so
	// redirect it to the null device for the duration of construction.
	realStdout := os.Stdout
	if devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0); err == nil {
		os.Stdout = devnull
		defer func() { os.Stdout = realStdout }()
	}

	srv := server.NewTunnelServer(&config.ServerConfig{Server: config.ServerSettings{
		QuicAddress:     quicAddr,
		Password:        tunnelPassword,
		KeepAlivePeriod: 25,
		MaxIdleTimeout:  600,
		TLS:             config.TLSConfig{CertValidityDays: 365},
		Quic:            config.QuicConfig{KeepAlivePeriod: 25},
		Limits:          config.LimitConfig{MaxConcurrentUsers: 1000, QueueWaitSeconds: 60},
		Clients: map[string]config.ClientPortConfig{
			tunnelClientID: {Enabled: true, Ports: []int{tcpPort}, Name: "loadtest"},
		},
	}})
	go srv.Start()

	cli := client.NewTunnelClient(&config.ClientConfig{Client: config.ClientSettings{
		ServerAddress:   quicAddr,
		Password:        tunnelPassword,
		ClientID:        tunnelClientID,
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
		Limits: config.ClientLimits{PerHostConcurrency: perHost, QueueWaitSeconds: 60},
	}})
	go cli.Start()

	return fmt.Sprintf("127.0.0.1:%d", tcpPort), func() {
		_ = cli.Stop()
		_ = srv.Stop()
	}
}

func freeTCPPort() int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func freeUDPPort() int {
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}
