package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
)

type ClientConfig struct {
	Client ClientSettings `json:"client"`
}

type ClientSettings struct {
	ServerAddress   string            `json:"server_address"`
	Password        string            `json:"password"`
	AutoReconnect   bool              `json:"auto_reconnect"`
	Quic            QuicConfig        `json:"quic"`
	TLS             TLSConfig         `json:"tls"`
	Logging         LogConfig         `json:"logging"`
	Performance     PerformanceConfig `json:"performance"`
	Limits          ClientLimits      `json:"limits"`
	KeepAlivePeriod int               `json:"keep_alive_period"`
	MaxIdleTimeout  int               `json:"max_idle_timeout"`
	ClientID        string            `json:"client_id"`
	Mode            string            `json:"mode"`         // "reverse" (Rathole/FRP mode) or "exit" (mobile proxy mode)
	LocalTarget     string            `json:"local_target"` // Default local target for reverse mode (e.g. 127.0.0.1:8080)
	Tunnels         map[int]string    `json:"tunnels"`      // Port-to-local mappings (e.g. 8080: "127.0.0.1:3000")
	DNSServers      []string          `json:"dns_servers"`
}

type ClientLimits struct {
	PerHostConcurrency int `json:"per_host_concurrency"`
	QueueWaitSeconds   int `json:"queue_wait_seconds"`
}

type PerformanceConfig struct {
	BufferSize        int `json:"buffer_size"`
	ConnectionTimeout int `json:"connection_timeout"`
	TargetDialTimeout int `json:"target_dial_timeout"`
}

// Validate checks if the configuration is valid
func (c *ClientConfig) Validate() error {
	if c.Client.ServerAddress == "" {
		return fmt.Errorf("server_address cannot be empty")
	}
	if c.Client.Password == "" {
		return fmt.Errorf("password cannot be empty")
	}
	if c.Client.MaxIdleTimeout <= 0 {
		return fmt.Errorf("max_idle_timeout must be positive")
	}
	if c.Client.KeepAlivePeriod <= 0 {
		return fmt.Errorf("keep_alive_period must be positive")
	}
	if c.Client.Performance.BufferSize <= 0 {
		return fmt.Errorf("buffer_size must be positive")
	}
	if c.Client.Performance.ConnectionTimeout <= 0 {
		return fmt.Errorf("connection_timeout must be positive")
	}
	if c.Client.Performance.TargetDialTimeout <= 0 {
		return fmt.Errorf("target_dial_timeout must be positive")
	}
	return nil
}

// getDefaultConfig returns a ClientConfig with sensible defaults
func getDefaultConfig() *ClientConfig {
	return &ClientConfig{
		Client: ClientSettings{
			ServerAddress:   "127.0.0.1:4433",
			Password:        "change-this-password",
			Mode:            "reverse",
			LocalTarget:     "127.0.0.1:8080",
			Tunnels:         make(map[int]string),
			AutoReconnect:   true,
			MaxIdleTimeout:  600,
			KeepAlivePeriod: 25,
			DNSServers:      []string{"1.1.1.1:53", "8.8.8.8:53"},

			Quic: QuicConfig{
				MaxIncomingStreams:             2048,
				InitialStreamReceiveWindow:     4,
				MaxStreamReceiveWindow:         16,
				InitialConnectionReceiveWindow: 8,
				MaxConnectionReceiveWindow:     32,
				EnableDatagrams:                true,
				KeepAlivePeriod:                25,
			},

			TLS: TLSConfig{
				InsecureSkipVerify: true,
			},
			Logging: LogConfig{
				Level:   "info",
				Colored: true,
			},
			Performance: PerformanceConfig{
				BufferSize:        64,
				ConnectionTimeout: 10,
				TargetDialTimeout: 10,
			},
			Limits: ClientLimits{
				PerHostConcurrency: 3,
				QueueWaitSeconds:   300,
			},
		},
	}
}

// LoadClientConfig loads config from file path OR content string (supports INI/CONF and JSON)
func LoadClientConfig(configPathOrContent string) (*ClientConfig, error) {
	cfg := getDefaultConfig()

	isContent := strings.Contains(configPathOrContent, "\n") ||
		strings.Contains(configPathOrContent, "=") ||
		strings.Contains(configPathOrContent, "{")

	var data []byte
	if isContent {
		data = []byte(configPathOrContent)
	} else {
		var err error
		data, err = os.ReadFile(configPathOrContent)
		if err != nil {
			log.Printf("⚠ Client config %s not found - using defaults", configPathOrContent)
			return cfg, nil
		}
		log.Printf("✓ Client config loaded from file: %s", configPathOrContent)
	}

	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "{") {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("failed to parse client JSON config: %w", err)
		}
	} else {
		if err := parseClientINI(data, cfg); err != nil {
			return nil, fmt.Errorf("failed to parse client INI config: %w", err)
		}
	}

	return cfg, nil
}

func parseClientINI(data []byte, cfg *ClientConfig) error {
	ini := parseINI(data)

	cfg.Client.ServerAddress = ini.GetString("client", "server_address", cfg.Client.ServerAddress)
	cfg.Client.Password = ini.GetString("client", "password", cfg.Client.Password)
	cfg.Client.ClientID = ini.GetString("client", "client_id", cfg.Client.ClientID)
	cfg.Client.Mode = ini.GetString("client", "mode", cfg.Client.Mode)
	cfg.Client.LocalTarget = ini.GetString("client", "local_target", cfg.Client.LocalTarget)
	cfg.Client.AutoReconnect = ini.GetBool("client", "auto_reconnect", cfg.Client.AutoReconnect)
	cfg.Client.KeepAlivePeriod = ini.GetInt("client", "keep_alive_period", cfg.Client.KeepAlivePeriod)
	cfg.Client.MaxIdleTimeout = ini.GetInt("client", "max_idle_timeout", cfg.Client.MaxIdleTimeout)
	cfg.Client.DNSServers = ini.GetStringSlice("client", "dns_servers", cfg.Client.DNSServers)

	// QUIC
	cfg.Client.Quic.MaxIncomingStreams = ini.GetInt64("client.quic", "max_incoming_streams", cfg.Client.Quic.MaxIncomingStreams)
	cfg.Client.Quic.InitialStreamReceiveWindow = ini.GetInt("client.quic", "initial_stream_receive_window", cfg.Client.Quic.InitialStreamReceiveWindow)
	cfg.Client.Quic.MaxStreamReceiveWindow = ini.GetInt("client.quic", "max_stream_receive_window", cfg.Client.Quic.MaxStreamReceiveWindow)
	cfg.Client.Quic.InitialConnectionReceiveWindow = ini.GetInt("client.quic", "initial_connection_receive_window", cfg.Client.Quic.InitialConnectionReceiveWindow)
	cfg.Client.Quic.MaxConnectionReceiveWindow = ini.GetInt("client.quic", "max_connection_receive_window", cfg.Client.Quic.MaxConnectionReceiveWindow)
	cfg.Client.Quic.EnableDatagrams = ini.GetBool("client.quic", "enable_datagrams", cfg.Client.Quic.EnableDatagrams)
	cfg.Client.Quic.Allow0RTT = ini.GetBool("client.quic", "allow_0rtt", cfg.Client.Quic.Allow0RTT)

	// TLS
	cfg.Client.TLS.InsecureSkipVerify = ini.GetBool("client.tls", "insecure_skip_verify", cfg.Client.TLS.InsecureSkipVerify)

	// Limits
	cfg.Client.Limits.PerHostConcurrency = ini.GetInt("client.limits", "per_host_concurrency", cfg.Client.Limits.PerHostConcurrency)
	cfg.Client.Limits.QueueWaitSeconds = ini.GetInt("client.limits", "queue_wait_seconds", cfg.Client.Limits.QueueWaitSeconds)

	// Performance
	cfg.Client.Performance.BufferSize = ini.GetInt("client.performance", "buffer_size", cfg.Client.Performance.BufferSize)
	cfg.Client.Performance.ConnectionTimeout = ini.GetInt("client.performance", "connection_timeout", cfg.Client.Performance.ConnectionTimeout)
	cfg.Client.Performance.TargetDialTimeout = ini.GetInt("client.performance", "target_dial_timeout", cfg.Client.Performance.TargetDialTimeout)

	// Port-specific tunnels (e.g. [client.tunnels] 8080 = "127.0.0.1:3000")
	if cfg.Client.Tunnels == nil {
		cfg.Client.Tunnels = make(map[int]string)
	}
	for secName, secData := range ini.Sections {
		if secName == "client.tunnels" || secName == "tunnels" {
			for portStr, target := range secData {
				if port, err := strconv.Atoi(portStr); err == nil {
					cfg.Client.Tunnels[port] = target
				}
			}
		}
	}

	return nil
}
