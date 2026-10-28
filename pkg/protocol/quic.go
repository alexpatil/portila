package protocol

import (
	"time"

	"github.com/quic-go/quic-go"
	"portila/pkg/config"
)

// DefaultQuicConfig builds a tuned quic.Config optimized for mobile (4G/5G) and high throughput.
func DefaultQuicConfig(cfg config.QuicConfig, maxIdleSec, keepAliveSec int) *quic.Config {
	maxStreams := cfg.MaxIncomingStreams
	if maxStreams <= 0 {
		maxStreams = 10000
	}

	initStreamWin := uint64(cfg.InitialStreamReceiveWindow) * 1024 * 1024
	if initStreamWin == 0 {
		initStreamWin = 2 * 1024 * 1024 // 2 MB: fast 4G/5G ramp-up without BDP stall
	}

	maxStreamWin := uint64(cfg.MaxStreamReceiveWindow) * 1024 * 1024
	if maxStreamWin == 0 {
		maxStreamWin = 8 * 1024 * 1024 // 8 MB: handles bursty 5G throughput
	}

	initConnWin := uint64(cfg.InitialConnectionReceiveWindow) * 1024 * 1024
	if initConnWin == 0 {
		initConnWin = 4 * 1024 * 1024 // 4 MB: connection-level flow control
	}

	maxConnWin := uint64(cfg.MaxConnectionReceiveWindow) * 1024 * 1024
	if maxConnWin == 0 {
		maxConnWin = 16 * 1024 * 1024 // 16 MB: safe ceiling for mobile RAM
	}

	idleTimeout := time.Duration(maxIdleSec) * time.Second
	if idleTimeout <= 0 {
		idleTimeout = 600 * time.Second
	}

	keepAlive := time.Duration(keepAliveSec) * time.Second
	if keepAlive <= 0 {
		keepAlive = 25 * time.Second // Keeps cellular carrier CGNAT UDP mappings alive
	}

	return &quic.Config{
		MaxIdleTimeout:                 idleTimeout,
		KeepAlivePeriod:                keepAlive,
		MaxIncomingStreams:             maxStreams,
		InitialStreamReceiveWindow:     initStreamWin,
		MaxStreamReceiveWindow:         maxStreamWin,
		InitialConnectionReceiveWindow: initConnWin,
		MaxConnectionReceiveWindow:     maxConnWin,
		EnableDatagrams:                cfg.EnableDatagrams,
		Allow0RTT:                      cfg.Allow0RTT,
		DisablePathMTUDiscovery:        cfg.DisablePathMTUDiscovery, // PMTU active for dynamic 4G/5G MTU
	}
}
