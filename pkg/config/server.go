package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
)

type ServerConfig struct {
	Server ServerSettings `json:"server"`
}

type ServerSettings struct {
	QuicAddress     string                      `json:"quic_address"`
	Password        string                      `json:"password"`
	Quic            QuicConfig                  `json:"quic"`
	TLS             TLSConfig                   `json:"tls"`
	Logging         LogConfig                   `json:"logging"`
	Limits          LimitConfig                 `json:"limits"`
	KeepAlivePeriod int                         `json:"keep_alive_period"`
	MaxIdleTimeout  int                         `json:"max_idle_timeout"`
	Clients         map[string]ClientPortConfig `json:"clients"`
}

type ClientPortConfig struct {
	Enabled bool   `json:"enabled"`
	Ports   []int  `json:"ports"`
	Name    string `json:"name"`
}

type LimitConfig struct {
	MaxConcurrentUsers int `json:"max_concurrent_users"`
	UserBufferSize     int `json:"user_buffer_size"`
	ConnectionTimeout  int `json:"connection_timeout"`
	QueueWaitSeconds   int `json:"queue_wait_seconds"`
}

// LoadServerConfig loads server configuration from INI/CONF or JSON file
func LoadServerConfig(configFile string) (*ServerConfig, error) {
	cfg := &ServerConfig{
		Server: ServerSettings{
			QuicAddress:     "0.0.0.0:4433",
			Password:        "change-this-password",
			KeepAlivePeriod: 25,
			MaxIdleTimeout:  600,
			Quic: QuicConfig{
				MaxIncomingStreams:             10000,
				InitialStreamReceiveWindow:     2,
				MaxStreamReceiveWindow:         6,
				InitialConnectionReceiveWindow: 4,
				MaxConnectionReceiveWindow:     15,
				EnableDatagrams:                true,
				KeepAlivePeriod:                25,
			},
			TLS: TLSConfig{
				CertValidityDays: 365,
			},
			Logging: LogConfig{
				Level:   "info",
				Colored: true,
			},
			Limits: LimitConfig{
				MaxConcurrentUsers: 1000,
				UserBufferSize:     32,
				ConnectionTimeout:  10,
				QueueWaitSeconds:   300,
			},
			Clients: make(map[string]ClientPortConfig),
		},
	}

	if data, err := os.ReadFile(configFile); err == nil {
		trimmed := strings.TrimSpace(string(data))
		if strings.HasPrefix(trimmed, "{") {
			if err := json.Unmarshal(data, cfg); err != nil {
				return nil, fmt.Errorf("failed to parse server JSON config: %w", err)
			}
		} else {
			if err := parseServerINI(data, cfg); err != nil {
				return nil, fmt.Errorf("failed to parse server INI config: %w", err)
			}
		}
		log.Printf("✓ Server config loaded: %s", configFile)
	} else {
		log.Printf("⚠  Server config %s not found - using defaults", configFile)
	}

	return cfg, nil
}

func parseServerINI(data []byte, cfg *ServerConfig) error {
	ini := parseINI(data)

	// Server settings (can be under [server] section or global)
	cfg.Server.QuicAddress = ini.GetString("server", "quic_address", cfg.Server.QuicAddress)
	cfg.Server.Password = ini.GetString("server", "password", cfg.Server.Password)
	cfg.Server.KeepAlivePeriod = ini.GetInt("server", "keep_alive_period", cfg.Server.KeepAlivePeriod)
	cfg.Server.MaxIdleTimeout = ini.GetInt("server", "max_idle_timeout", cfg.Server.MaxIdleTimeout)

	// QUIC settings
	cfg.Server.Quic.MaxIncomingStreams = ini.GetInt64("server.quic", "max_incoming_streams", cfg.Server.Quic.MaxIncomingStreams)
	cfg.Server.Quic.InitialStreamReceiveWindow = ini.GetInt("server.quic", "initial_stream_receive_window", cfg.Server.Quic.InitialStreamReceiveWindow)
	cfg.Server.Quic.MaxStreamReceiveWindow = ini.GetInt("server.quic", "max_stream_receive_window", cfg.Server.Quic.MaxStreamReceiveWindow)
	cfg.Server.Quic.InitialConnectionReceiveWindow = ini.GetInt("server.quic", "initial_connection_receive_window", cfg.Server.Quic.InitialConnectionReceiveWindow)
	cfg.Server.Quic.MaxConnectionReceiveWindow = ini.GetInt("server.quic", "max_connection_receive_window", cfg.Server.Quic.MaxConnectionReceiveWindow)
	cfg.Server.Quic.EnableDatagrams = ini.GetBool("server.quic", "enable_datagrams", cfg.Server.Quic.EnableDatagrams)
	cfg.Server.Quic.Allow0RTT = ini.GetBool("server.quic", "allow_0rtt", cfg.Server.Quic.Allow0RTT)

	// TLS settings
	cfg.Server.TLS.CertFile = ini.GetString("server.tls", "cert_file", cfg.Server.TLS.CertFile)
	cfg.Server.TLS.KeyFile = ini.GetString("server.tls", "key_file", cfg.Server.TLS.KeyFile)
	cfg.Server.TLS.CertValidityDays = ini.GetInt("server.tls", "cert_validity_days", cfg.Server.TLS.CertValidityDays)

	// Logging settings
	cfg.Server.Logging.Level = ini.GetString("server.logging", "level", cfg.Server.Logging.Level)
	cfg.Server.Logging.Colored = ini.GetBool("server.logging", "colored", cfg.Server.Logging.Colored)

	// Limits
	cfg.Server.Limits.MaxConcurrentUsers = ini.GetInt("server.limits", "max_concurrent_users", cfg.Server.Limits.MaxConcurrentUsers)
	cfg.Server.Limits.UserBufferSize = ini.GetInt("server.limits", "user_buffer_size", cfg.Server.Limits.UserBufferSize)
	cfg.Server.Limits.ConnectionTimeout = ini.GetInt("server.limits", "connection_timeout", cfg.Server.Limits.ConnectionTimeout)
	cfg.Server.Limits.QueueWaitSeconds = ini.GetInt("server.limits", "queue_wait_seconds", cfg.Server.Limits.QueueWaitSeconds)

	// Client definitions
	if cfg.Server.Clients == nil {
		cfg.Server.Clients = make(map[string]ClientPortConfig)
	}

	for secName, secData := range ini.Sections {
		normName := secName
		if strings.HasPrefix(normName, "server.clients.") {
			normName = strings.TrimPrefix(normName, "server.clients.")
		} else if strings.HasPrefix(normName, "clients.") {
			normName = strings.TrimPrefix(normName, "clients.")
		} else if normName == "server" || normName == "server.quic" || normName == "server.tls" || normName == "server.logging" || normName == "server.limits" || normName == "quic" || normName == "tls" || normName == "logging" || normName == "limits" {
			continue
		}

		clientCfg := ClientPortConfig{
			Enabled: true,
			Ports:   []int{},
		}
		if v, ok := secData["enabled"]; ok {
			v = strings.ToLower(v)
			clientCfg.Enabled = (v == "true" || v == "1" || v == "yes" || v == "on")
		}
		if v, ok := secData["name"]; ok {
			clientCfg.Name = v
		}
		if _, ok := secData["ports"]; ok {
			clientCfg.Ports = ini.GetIntSlice(secName, "ports", nil)
		} else if _, ok := secData["port"]; ok {
			clientCfg.Ports = ini.GetIntSlice(secName, "port", nil)
		}

		if len(clientCfg.Ports) > 0 {
			cfg.Server.Clients[normName] = clientCfg
		}
	}

	return nil
}

// Validate checks server configuration
func (c *ServerConfig) Validate() error {
	if c.Server.QuicAddress == "" {
		return fmt.Errorf("quic_address cannot be empty")
	}
	if c.Server.Password == "" {
		return fmt.Errorf("password cannot be empty")
	}
	if c.Server.MaxIdleTimeout <= 0 {
		return fmt.Errorf("max_idle_timeout must be positive")
	}
	if c.Server.KeepAlivePeriod <= 0 {
		return fmt.Errorf("keep_alive_period must be positive")
	}

	// Validate port assignments
	usedPorts := make(map[int]string)
	for clientID, clientCfg := range c.Server.Clients {
		if !clientCfg.Enabled {
			continue
		}
		for _, port := range clientCfg.Ports {
			if port < 1 || port > 65535 {
				return fmt.Errorf("invalid port %d for client %s", port, clientID)
			}
			if existingClient, exists := usedPorts[port]; exists {
				return fmt.Errorf("port %d assigned to both %s and %s", port, existingClient, clientID)
			}
			usedPorts[port] = clientID
		}
	}

	return nil
}

// GetClientPorts returns port assignments for a specific client ID
func (c *ServerConfig) GetClientPorts(clientID string) []int {
	if clientCfg, exists := c.Server.Clients[clientID]; exists && clientCfg.Enabled {
		return clientCfg.Ports
	}
	return nil
}

// GetAllClientPorts returns all port assignments
func (c *ServerConfig) GetAllClientPorts() map[string][]int {
	result := make(map[string][]int)
	for clientID, clientCfg := range c.Server.Clients {
		if clientCfg.Enabled {
			result[clientID] = clientCfg.Ports
		}
	}
	return result
}
