package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"portila/pkg/client"
	"portila/pkg/config"
)

var version = "dev"

func main() {
	configPath := flag.String("c", "client.conf", "Path to client configuration file (.conf or .json)")
	serverAddr := flag.String("server", "", "Override server address (e.g. 1.2.3.4:4433)")
	password := flag.String("pass", "", "Override auth password")
	clientID := flag.String("id", "", "Override client ID")
	mode := flag.String("mode", "", "Tunnel mode: reverse (Rathole/FRP local proxy) or exit (mobile exit proxy)")
	target := flag.String("target", "", "Local target in reverse mode (e.g. 127.0.0.1:8080)")
	showVersion := flag.Bool("v", false, "Print version and exit")
	flag.BoolVar(showVersion, "version", false, "Print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("portila-client version %s\n", version)
		os.Exit(0)
	}

	targetConfig := *configPath
	if targetConfig == "client.conf" {
		if _, err := os.Stat("client.conf"); os.IsNotExist(err) {
			if _, err := os.Stat("config.json"); err == nil {
				targetConfig = "config.json"
			}
		}
	}

	log.Printf("Using config file: %s", targetConfig)

	// Load config
	cfg, err := config.LoadClientConfig(targetConfig)
	if err != nil {
		log.Fatalf("Error loading config: %v", err)
	}

	if *serverAddr != "" {
		cfg.Client.ServerAddress = *serverAddr
	}
	if *password != "" {
		cfg.Client.Password = *password
	}
	if *clientID != "" {
		cfg.Client.ClientID = *clientID
	}
	if *mode != "" {
		cfg.Client.Mode = *mode
	}
	if *target != "" {
		cfg.Client.LocalTarget = *target
	}

	tunnelClient := client.NewTunnelClient(cfg)

	// Graceful shutdown on CTRL+C / docker stop / SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := tunnelClient.Start(); err != nil {
			log.Fatalf("Client start error: %v", err)
		}
	}()

	// Block until signal received
	<-sigCh
	_ = tunnelClient.Stop()
}
