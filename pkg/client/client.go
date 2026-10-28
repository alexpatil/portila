package client

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"portila/pkg/config"
	"portila/pkg/protocol"
	"portila/pkg/relay"
	"github.com/quic-go/quic-go"
)

type TunnelClient struct {
	config *config.ClientConfig
	logger *log.Logger

	mu   sync.RWMutex
	conn *quic.Conn

	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	lastActivity atomic.Int64
	dialer       *net.Dialer

	// Per-host pacing to protect the shared exit IP from rate-limit blocks.
	hosts     *hostLimiter
	queueWait time.Duration

	// Connection state tracking
	isConnected atomic.Bool
}

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
			// Resolve target hostnames via an explicit DNS server. A client cross-
			// compiled with CGO_ENABLED=0 (the common GOOS=android build) uses Go's
			// pure resolver, which on Android finds no /etc/resolv.conf, falls back to
			// 127.0.0.1:53, and fails every lookup ("dial tcp: lookup ...: connection
			// refused"). Querying a known public resolver directly avoids that.
			Resolver: newDNSResolver(cfg.Client.DNSServers),
		},
		hosts:     newHostLimiter(cfg.Client.Limits.PerHostConcurrency),
		queueWait: queueWait,
	}
	c.touchActivity()
	return c
}

// newDNSResolver builds a resolver. On standard operating systems (Linux, Windows, macOS),
// it returns net.DefaultResolver by default so that the operating system's native DNS configuration
// (systemd-resolved, VPNs, local /etc/hosts) is respected. If explicit upstream servers are
// configured, or when running on Android (where /etc/resolv.conf is absent), it constructs
// a custom net.Resolver with PreferGo.
func newDNSResolver(servers []string) *net.Resolver {
	if len(servers) == 0 {
		if runtime.GOOS == "android" {
			servers = []string{"1.1.1.1:53", "8.8.8.8:53"}
		} else {
			return net.DefaultResolver
		}
	}

	normalized := make([]string, 0, len(servers))
	for _, s := range servers {
		if _, _, err := net.SplitHostPort(s); err != nil {
			s = net.JoinHostPort(s, "53") // bare IP -> add default DNS port
		}
		normalized = append(normalized, s)
	}

	d := &net.Dialer{Timeout: 5 * time.Second}
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var lastErr error
			for _, srv := range normalized {
				conn, err := d.DialContext(ctx, network, srv)
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
	}
}

func (c *TunnelClient) touchActivity() {
	c.lastActivity.Store(time.Now().Unix())
}

func (c *TunnelClient) Start() error {
	c.logger.Println("Starting QUIC client...")

	c.wg.Add(1)
	go c.reconnectLoop()

	<-c.ctx.Done()
	return c.Stop()
}

func (c *TunnelClient) Stop() error {
	c.logger.Println("Stopping client...")
	c.cancel()
	c.isConnected.Store(false)

	c.mu.Lock()
	if c.conn != nil {
		c.conn.CloseWithError(0, "client stopped")
		c.conn = nil
	}
	c.mu.Unlock()

	c.wg.Wait()
	c.logger.Println("Client stopped cleanly")
	return nil
}

// Reconnect backoff bounds. Backoff grows after failed attempts and resets to
// the initial value after a healthy connection, so transient drops recover fast
// while a down server is retried gently (battery-friendly on mobile).
const (
	initialReconnectBackoff = 1 * time.Second
	maxReconnectBackoff     = 60 * time.Second
)

func (c *TunnelClient) reconnectLoop() {
	defer c.wg.Done()

	backoff := initialReconnectBackoff
	for {
		if c.ctx.Err() != nil {
			return
		}

		if err := c.connectOnce(); err != nil {
			c.logger.Printf("Connect error: %v", err)
			c.isConnected.Store(false)
		} else {
			// connectOnce only returns nil after a connection that was healthy
			// and then dropped; treat that as success and retry quickly.
			backoff = initialReconnectBackoff
		}

		select {
		case <-c.ctx.Done():
			return
		case <-time.After(backoff):
		}

		if backoff *= 2; backoff > maxReconnectBackoff {
			backoff = maxReconnectBackoff
		}
	}
}

func (c *TunnelClient) connectOnce() error {
	cfg := c.config.Client

	tlsConf := &tls.Config{
		InsecureSkipVerify:     cfg.TLS.InsecureSkipVerify,
		NextProtos:             []string{protocol.ProtocolNextProto},
		SessionTicketsDisabled: false,
		ClientSessionCache:     tls.NewLRUClientSessionCache(32),
	}

	quicCfg := &quic.Config{
		KeepAlivePeriod:                time.Duration(cfg.KeepAlivePeriod) * time.Second,
		MaxIdleTimeout:                 time.Duration(cfg.MaxIdleTimeout) * time.Second,
		MaxIncomingStreams:             int64(cfg.Quic.MaxIncomingStreams),
		InitialStreamReceiveWindow:     uint64(cfg.Quic.InitialStreamReceiveWindow) * 1024 * 1024,
		MaxStreamReceiveWindow:         uint64(cfg.Quic.MaxStreamReceiveWindow) * 1024 * 1024,
		InitialConnectionReceiveWindow: uint64(cfg.Quic.InitialConnectionReceiveWindow) * 1024 * 1024,
		MaxConnectionReceiveWindow:     uint64(cfg.Quic.MaxConnectionReceiveWindow) * 1024 * 1024,
		EnableDatagrams:                cfg.Quic.EnableDatagrams,
		Allow0RTT:                      cfg.Quic.Allow0RTT,
		DisablePathMTUDiscovery:        cfg.Quic.DisablePathMTUDiscovery,
	}

	conn, err := quic.DialAddrEarly(c.ctx, cfg.ServerAddress, tlsConf, quicCfg)
	if err != nil {
		return err
	}

	if err := c.authenticate(conn); err != nil {
		conn.CloseWithError(1, "auth failed")
		return err
	}

	// Replace connection with proper locking
	c.mu.Lock()
	oldConn := c.conn
	c.conn = conn
	c.mu.Unlock()

	// Close old connection after lock is released
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
				c.logger.Printf("keepAlive: OpenStream failed: %v", err)
				continue
			}

			if err := protocol.WriteMessage(stream, 0, protocol.MsgPing, 0, nil); err != nil {
				c.logger.Printf("keepAlive: write ping failed: %v", err)
				_ = stream.Close()
				continue
			}

			c.touchActivity()
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

	// Build auth payload: [32 bytes password hash][client ID string]
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

	if msg.Type != protocol.MsgAuthResp ||
		len(msg.Data) == 0 ||
		msg.Data[0] != protocol.ErrOK {
		return fmt.Errorf("auth rejected")
	}

	c.logger.Printf("Authenticated as: %s", clientID)
	return nil
}

func (c *TunnelClient) handleStreams(conn *quic.Conn) {
	for {
		stream, err := conn.AcceptStream(c.ctx)
		if err != nil {
			c.isConnected.Store(false)
			return
		}

		c.wg.Add(1)
		go c.handleStream(stream)
	}
}

func (c *TunnelClient) handleStream(stream *quic.Stream) {
	defer c.wg.Done()
	c.touchActivity()

	// Read 4-byte sourcePort header
	var portHeader [4]byte
	if _, err := io.ReadFull(stream, portHeader[:]); err != nil {
		stream.CancelRead(0)
		stream.CancelWrite(0)
		return
	}
	sourcePort := int(binary.BigEndian.Uint32(portHeader[:]))

	mode := strings.ToLower(c.config.Client.Mode)
	if mode == "exit" || mode == "forward" || mode == "proxy" {
		c.handleExitStream(stream)
	} else {
		c.handleReverseStream(stream, sourcePort)
	}
}

// handleReverseStream forwards traffic directly to a local service (Rathole / FRP mode)
func (c *TunnelClient) handleReverseStream(stream *quic.Stream, sourcePort int) {
	start := time.Now()

	// Check if this specific VPS port has a designated local mapping
	target := c.config.Client.LocalTarget
	if specificTarget, ok := c.config.Client.Tunnels[sourcePort]; ok && specificTarget != "" {
		target = specificTarget
	}
	if target == "" {
		c.logger.Printf("Reverse tunnel: no local target configured for port %d", sourcePort)
		stream.CancelRead(0)
		stream.CancelWrite(0)
		return
	}

	dst, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		c.logger.Printf("Reverse tunnel: dial local target %s (port %d) failed: %v", target, sourcePort, err)
		stream.CancelRead(0)
		stream.CancelWrite(0)
		return
	}
	defer dst.Close()

	c.logger.Printf("🔄 Reverse tunnel: port %d → local %s | %v", sourcePort, target, time.Since(start))
	relay.Pipe(c.ctx, relay.Stream(stream, nil), relay.TCP(dst))
	c.logger.Printf("🔻 Stream closed (%s)", target)
}

// handleExitStream tunnels HTTP CONNECT out over mobile/local internet (Exit Node mode)
func (c *TunnelClient) handleExitStream(stream *quic.Stream) {
	start := time.Now()

	// The browser's HTTP CONNECT request is the first thing on the raw stream.
	br := bufio.NewReader(stream)
	target, err := readHTTPConnect(br)
	if err != nil {
		c.logger.Printf("CONNECT parse failed: %v", err)
		stream.CancelRead(0)
		stream.CancelWrite(0)
		return
	}
	c.logger.Printf("CONNECT → %s", target)

	// Per-host pacing: wait for a free slot for this site so a burst of users on
	// the same page doesn't trip the site's per-IP rate limit. Extra connections
	// queue here (the browser sees a delay, not a failure) up to queueWait.
	host := target
	if h, _, splitErr := net.SplitHostPort(target); splitErr == nil {
		host = h
	}
	acqCtx, cancel := context.WithTimeout(c.ctx, c.queueWait)
	release, err := c.hosts.acquire(acqCtx, host)
	cancel()
	if err != nil {
		c.logger.Printf("queue wait exceeded for %s: %v", host, err)
		_, _ = stream.Write([]byte("HTTP/1.1 504 Gateway Timeout\r\n\r\n"))
		_ = stream.Close()
		stream.CancelRead(0)
		return
	}
	defer release()

	// Dial the target over mobile data.
	dst, err := c.dialer.DialContext(c.ctx, "tcp", target)
	if err != nil {
		c.logger.Printf("Dial failed: %v (%s)", err, target)
		_, _ = stream.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		_ = stream.Close() // flush the 502, then stop reading
		stream.CancelRead(0)
		return
	}
	defer dst.Close()
	c.logger.Printf("TCP dial success → %s | %v", target, time.Since(start))

	if _, err := stream.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		stream.CancelRead(0)
		stream.CancelWrite(0)
		return
	}

	// Raw bidirectional copy with half-close. br is used as the stream's read
	// side so any bytes buffered past the CONNECT request are not lost. Pipe
	// closes both sides gracefully, so we must NOT reset the stream afterwards.
	relay.Pipe(c.ctx, relay.Stream(stream, br), relay.TCP(dst))
	c.logger.Printf("🔻 Stream closed (%s)", target)
}

// readHTTPConnect reads and parses a single "CONNECT host:port HTTP/1.1" request

func readHTTPConnect(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read request line: %w", err)
	}

	parts := strings.Fields(line)
	if len(parts) < 3 || parts[0] != "CONNECT" {
		return "", fmt.Errorf("expected CONNECT, got %q", strings.TrimSpace(line))
	}
	target := parts[1]

	for { // consume headers up to the blank line
		h, err := r.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("read headers: %w", err)
		}
		if h == "\r\n" || h == "\n" {
			break
		}
	}

	if target == "" {
		return "", fmt.Errorf("empty CONNECT target")
	}
	return target, nil
}
