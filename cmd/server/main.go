package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"portila/pkg/config"
	"portila/pkg/server"
)

func main() {
	configPath := flag.String("c", "server.conf", "Path to server configuration file (.conf or .json)")
	quicAddr := flag.String("quic", "", "Override QUIC listen address (e.g. 0.0.0.0:4433)")
	password := flag.String("pass", "", "Override auth password")
	flag.Parse()

	targetConfig := *configPath
	if targetConfig == "server.conf" {
		if _, err := os.Stat("server.conf"); os.IsNotExist(err) {
			if _, err := os.Stat("config.json"); err == nil {
				targetConfig = "config.json"
			}
		}
	}

	// Load configuration
	cfg, err := config.LoadServerConfig(targetConfig)
	if err != nil {
		log.Fatalf("Error loading config: %v", err)
	}

	if *quicAddr != "" {
		cfg.Server.QuicAddress = *quicAddr
	}
	if *password != "" {
		cfg.Server.Password = *password
	}

	tunnelServer := server.NewTunnelServer(cfg)

	// Graceful shutdown handlers
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// Start server in background
	go func() {
		if err := tunnelServer.Start(); err != nil {
			log.Fatalf("Server start error: %v", err)
		}
	}()

	// Wait for shutdown signal
	<-sigCh
	_ = tunnelServer.Stop()
	log.Println("Server stopped gracefully")
}
