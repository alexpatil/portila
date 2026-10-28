package client

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
	"portila/pkg/config"
	"portila/pkg/protocol"
)

// TunnelClient manages an active QUIC connection to the Portila server.
type TunnelClient struct {
	config *config.ClientConfig
	logger *log.Logger

	mu   sync.RWMutex
	conn *quic.Conn

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	dialer       *net.Dialer
	lastActivity atomic.Int64

	hosts     *hostLimiter
	queueWait time.Duration

	isConnected atomic.Bool
}

// NewTunnelClient builds a client instance with configured dialers and limits.
func NewTunnelClient(cfg *config.ClientConfig) *TunnelClient {
	ctx, cancel := context.WithCancel(context.Background())

	queueWait := time.Duration(cfg.Client.Limits.QueueWaitSeconds) * time.Second
	if queueWait <= 0 {
		queueWait = 300 * time.Second
	}

	c := &TunnelClient{
		config: cfg,
		logger: log.New(os.Stdout, "[CLIENT] ", log.LstdFlags),
		ctx:    ctx,
		cancel: cancel,
		dialer: &net.Dialer{
			Timeout:       10 * time.Second,
			KeepAlive:     30 * time.Second,
			FallbackDelay: 200 * time.Millisecond,
			Resolver:      newDNSResolver(cfg.Client.DNSServers),
		},
		hosts:     newHostLimiter(cfg.Client.Limits.PerHostConcurrency),
		queueWait: queueWait,
	}
	c.touchActivity()
	return c
}

// Start begins the client connection loop.
func (c *TunnelClient) Start() error {
	c.logger.Println("Starting QUIC client...")

	if c.config.Client.AutoReconnect {
		return c.connectLoop()
	}
	return c.connectOnce()
}

// Stop terminates the client and waits for active streams to close.
func (c *TunnelClient) Stop() error {
	c.logger.Println("Stopping client...")
	c.cancel()

	c.mu.Lock()
	if c.conn != nil {
		c.conn.CloseWithError(0, "client stopped")
		c.conn = nil
	}
	c.mu.Unlock()

	c.isConnected.Store(false)
	c.wg.Wait()
	c.logger.Println("Client stopped cleanly")
	return nil
}

func (c *TunnelClient) connectLoop() error {
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		select {
		case <-c.ctx.Done():
			return nil
		default:
		}

		err := c.connectOnce()
		if err != nil {
			c.logger.Printf("Connection error: %v", err)
		}

		select {
		case <-c.ctx.Done():
			return nil
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (c *TunnelClient) connectOnce() error {
	cfg := c.config.Client

	tlsConf := &tls.Config{
		InsecureSkipVerify: cfg.TLS.InsecureSkipVerify,
		NextProtos:         []string{protocol.ProtocolNextProto},
	}

	quicCfg := protocol.DefaultQuicConfig(cfg.Quic, cfg.MaxIdleTimeout, cfg.KeepAlivePeriod)

	conn, err := quic.DialAddrEarly(c.ctx, cfg.ServerAddress, tlsConf, quicCfg)
	if err != nil {
		return err
	}

	if err := c.authenticate(conn); err != nil {
		conn.CloseWithError(1, "auth failed")
		return err
	}

	c.mu.Lock()
	oldConn := c.conn
	c.conn = conn
	c.mu.Unlock()

	if oldConn != nil && oldConn != conn {
		_ = oldConn.CloseWithError(0, "replaced by new connection")
	}

	c.isConnected.Store(true)
	c.touchActivity()
	c.logger.Println("QUIC connection active")

	c.wg.Add(1)
	go c.keepAliveLoop(conn)

	c.handleStreams(conn)
	return nil
}

func (c *TunnelClient) keepAliveLoop(conn *quic.Conn) {
	defer c.wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-conn.Context().Done():
			return
		case <-ticker.C:
			last := c.lastActivity.Load()
			if last > 0 && time.Since(time.Unix(last, 0)) < 10*time.Second {
				continue
			}

			ctx, cancel := context.WithTimeout(c.ctx, 2*time.Second)
			stream, err := conn.OpenStreamSync(ctx)
			cancel()
			if err != nil {
				continue
			}

			if err := protocol.WriteMessage(stream, 0, protocol.MsgPing, 0, nil); err != nil {
				_ = stream.Close()
				continue
			}

			_ = stream.Close()
		}
	}
}

func (c *TunnelClient) authenticate(conn *quic.Conn) error {
	stream, err := conn.OpenStreamSync(c.ctx)
	if err != nil {
		return err
	}
	defer stream.Close()

	pwd := sha256.Sum256([]byte(c.config.Client.Password))
	clientID := c.config.Client.ClientID

	authData := make([]byte, 0, 32+len(clientID))
	authData = append(authData, pwd[:]...)
	authData = append(authData, []byte(clientID)...)

	if err := protocol.WriteMessage(stream, 0, protocol.MsgAuth, 0, authData); err != nil {
		return err
	}

	msg, err := protocol.ReadMessage(stream)
	if err != nil {
		return err
	}

	if msg.Type != protocol.MsgAuthResp || len(msg.Data) == 0 || msg.Data[0] != protocol.ErrOK {
		return fmt.Errorf("auth rejected")
	}

	c.logger.Printf("Authenticated as: %s", clientID)
	return nil
}

func (c *TunnelClient) touchActivity() {
	c.lastActivity.Store(time.Now().Unix())
}
