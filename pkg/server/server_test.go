package server

import (
	"testing"

	"portila/pkg/config"
)

func TestServerPasswordValidation(t *testing.T) {
	cfg := &config.ServerConfig{
		Server: config.ServerSettings{
			Password: "change-this-password",
		},
	}
	srv := NewTunnelServer(cfg)
	if err := srv.validatePassword(); err == nil {
		t.Errorf("expected insecure password error, got nil")
	}

	cfg.Server.Password = "strong-random-password-123"
	if err := srv.validatePassword(); err != nil {
		t.Errorf("expected valid password, got error: %v", err)
	}
}

func TestServerLoadTLSConfig(t *testing.T) {
	cfg := &config.ServerConfig{
		Server: config.ServerSettings{
			TLS: config.TLSConfig{
				CertValidityDays: 30,
			},
		},
	}
	srv := NewTunnelServer(cfg)
	tlsConf, err := srv.loadTLSConfig()
	if err != nil {
		t.Fatalf("loadTLSConfig failed: %v", err)
	}

	if len(tlsConf.Certificates) == 0 {
		t.Fatalf("expected at least 1 certificate loaded")
	}
}

func TestServerPortLookup(t *testing.T) {
	cfg := &config.ServerConfig{
		Server: config.ServerSettings{
			Clients: map[string]config.ClientPortConfig{
				"phone1": {
					Enabled: true,
					Ports:   []int{9001, 9002},
					Name:    "Phone 1",
				},
			},
		},
	}

	ports := cfg.GetClientPorts("phone1")
	if len(ports) != 2 || ports[0] != 9001 {
		t.Errorf("unexpected ports for phone1: %v", ports)
	}

	if cfg.GetClientPorts("non_existent") != nil {
		t.Errorf("expected nil for non-existent client")
	}
}
