package config

// Shared QUIC config (units in MB for windows)
type QuicConfig struct {
	MaxIncomingStreams             int64 `json:"max_incoming_streams"`
	InitialStreamReceiveWindow     int   `json:"initial_stream_receive_window"`     // MB
	MaxStreamReceiveWindow         int   `json:"max_stream_receive_window"`         // MB
	InitialConnectionReceiveWindow int   `json:"initial_connection_receive_window"` // MB
	MaxConnectionReceiveWindow     int   `json:"max_connection_receive_window"`     // MB
	EnableDatagrams                bool  `json:"enable_datagrams"`
	KeepAlivePeriod                int   `json:"keep_alive_period"`
	Allow0RTT                      bool  `json:"allow_0rtt"`
	DisablePathMTUDiscovery        bool  `json:"disable_path_mtu_discovery"`
}

// TLSConfig configures only the QUIC transport encryption between the client
// (phone) and server.
type TLSConfig struct {
	CertFile           string `json:"cert_file"`
	KeyFile            string `json:"key_file"`
	CertValidityDays   int    `json:"cert_validity_days"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify"`
}

// Logging configuration
type LogConfig struct {
	Level   string `json:"level"`
	Colored bool   `json:"colored"`
	LogFile string `json:"log_file"`
}
