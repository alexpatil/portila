// Package mobile provides Gomobile-compatible bindings for Android and iOS apps.
// It allows embedding the Portila client directly inside a native mobile app without Termux.
package mobile

import (
	"fmt"
	"sync"

	"portila/pkg/client"
	"portila/pkg/config"
)

// Callback defines the interface for native mobile apps (Kotlin/Java/Swift)
// to receive events from the running Portila tunnel.
type Callback interface {
	OnConnected()
	OnDisconnected(reason string)
	OnError(err string)
	OnLog(message string)
}

var (
	clientMu     sync.Mutex
	activeClient *client.TunnelClient
)

// Version returns the current Portila version
func Version() string {
	return "1.0.0"
}

// StartClient starts the Portila client in a background goroutine.
// configText can be either a .conf (INI format) string or a JSON string.
func StartClient(configText string, cb Callback) error {
	clientMu.Lock()
	defer clientMu.Unlock()

	if activeClient != nil {
		return fmt.Errorf("portila client is already running")
	}

	cfg, err := config.LoadClientConfig(configText)
	if err != nil {
		if cb != nil {
			cb.OnError(err.Error())
		}
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	c := client.NewTunnelClient(cfg)
	activeClient = c

	go func() {
		if cb != nil {
			cb.OnLog("Starting Portila tunnel...")
		}
		if err := c.Start(); err != nil {
			if cb != nil {
				cb.OnError(err.Error())
			}
		}
		clientMu.Lock()
		activeClient = nil
		clientMu.Unlock()

		if cb != nil {
			cb.OnDisconnected("stopped")
		}
	}()

	return nil
}

// StopClient cleanly stops the active Portila client
func StopClient() error {
	clientMu.Lock()
	c := activeClient
	clientMu.Unlock()

	if c == nil {
		return nil
	}

	return c.Stop()
}

// IsRunning returns whether the client instance is active
func IsRunning() bool {
	clientMu.Lock()
	defer clientMu.Unlock()
	return activeClient != nil
}
