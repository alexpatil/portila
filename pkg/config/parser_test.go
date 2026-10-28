package config

import (
	"testing"
)

func TestParseServerINI(t *testing.T) {
	content := []byte(`
# Portila Server Config
[server]
quic_address = 0.0.0.0:5555
password = testsecret123

[client_test_device]
enabled = true
ports = 8080, 8081
name = "My Test Phone"

[server.limits]
max_concurrent_users = 500
queue_wait_seconds = 120
`)

	cfg := &ServerConfig{}
	if err := parseServerINI(content, cfg); err != nil {
		t.Fatalf("parseServerINI failed: %v", err)
	}

	if cfg.Server.QuicAddress != "0.0.0.0:5555" {
		t.Errorf("expected 0.0.0.0:5555, got %s", cfg.Server.QuicAddress)
	}
	if cfg.Server.Password != "testsecret123" {
		t.Errorf("expected testsecret123, got %s", cfg.Server.Password)
	}
	if cfg.Server.Limits.MaxConcurrentUsers != 500 {
		t.Errorf("expected 500, got %d", cfg.Server.Limits.MaxConcurrentUsers)
	}
	if cfg.Server.Limits.QueueWaitSeconds != 120 {
		t.Errorf("expected 120, got %d", cfg.Server.Limits.QueueWaitSeconds)
	}

	client, exists := cfg.Server.Clients["client_test_device"]
	if !exists {
		t.Fatalf("client_test_device not found in clients map")
	}
	if !client.Enabled {
		t.Errorf("expected client to be enabled")
	}
	if client.Name != "My Test Phone" {
		t.Errorf("expected My Test Phone, got %s", client.Name)
	}
	if len(client.Ports) != 2 || client.Ports[0] != 8080 || client.Ports[1] != 8081 {
		t.Errorf("unexpected ports: %v", client.Ports)
	}
}

func TestParseClientINI(t *testing.T) {
	content := []byte(`
# Portila Client Config
[client]
server_address = vps.example.com:4433
password = secretpass
client_id = client_test_device
auto_reconnect = true
dns_servers = 1.1.1.1:53, 9.9.9.9:53

[client.limits]
per_host_concurrency = 5
queue_wait_seconds = 200
`)

	cfg := &ClientConfig{}
	if err := parseClientINI(content, cfg); err != nil {
		t.Fatalf("parseClientINI failed: %v", err)
	}

	if cfg.Client.ServerAddress != "vps.example.com:4433" {
		t.Errorf("expected vps.example.com:4433, got %s", cfg.Client.ServerAddress)
	}
	if cfg.Client.Password != "secretpass" {
		t.Errorf("expected secretpass, got %s", cfg.Client.Password)
	}
	if cfg.Client.ClientID != "client_test_device" {
		t.Errorf("expected client_test_device, got %s", cfg.Client.ClientID)
	}
	if !cfg.Client.AutoReconnect {
		t.Errorf("expected AutoReconnect to be true")
	}
	if len(cfg.Client.DNSServers) != 2 || cfg.Client.DNSServers[1] != "9.9.9.9:53" {
		t.Errorf("unexpected DNS servers: %v", cfg.Client.DNSServers)
	}
	if cfg.Client.Limits.PerHostConcurrency != 5 {
		t.Errorf("expected 5, got %d", cfg.Client.Limits.PerHostConcurrency)
	}
}
