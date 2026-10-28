package server

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"portila/pkg/config"
	"portila/pkg/protocol"
	"portila/pkg/relay"
	tlsutil "portila/pkg/tls"
	"github.com/quic-go/quic-go"
)

// defaultStreamQueueWait is used when the config does not set a queue wait.
const defaultStreamQueueWait = 300 * time.Second

type TunnelServer struct {
	config *config.ServerConfig
	logger *log.Logger

	quicListener *quic.Listener

	// Multi-client session management
	sessionMu sync.RWMutex
	sessions  map[string]*ClientSession // clientID -> session

	// Port-to-client routing map
	portRoutingMu sync.RWMutex
	portRouting   map[int]string // port -> clientID

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// ClientSession represents an authenticated Android client with its tunnel and port listeners
type ClientSession struct {
	clientID      string
	conn          *quic.Conn
	assignedPorts []int
	listeners     []net.Listener

	activeStreams int32
	totalStreams  uint32
	queued        atomic.Int32 // connections waiting for / using a tunnel stream slot

	mu         sync.RWMutex
	ctx        context.Context
	cancel     context.CancelFunc
	logger     *log.Logger
	lastActive atomic.Int64
}

func NewTunnelServer(cfg *config.ServerConfig) *TunnelServer {
	ctx, cancel := context.WithCancel(context.Background())
	logger := log.New(os.Stdout, "[SERVER] ", log.LstdFlags)

	return &TunnelServer{
		config:      cfg,
		logger:      logger,
		sessions:    make(map[string]*ClientSession),
		portRouting: make(map[int]string),
		ctx:         ctx,
		cancel:      cancel,
	}
}

func (s *TunnelServer) Start() error {
	cfg := s.config.Server

	if err := s.validatePassword(); err != nil {
		s.logger.Printf("WARNING: %v", err)
	}

	tlsConf, err := s.loadTLSConfig()
	if err != nil {
		return fmt.Errorf("failed to load TLS config: %w", err)
	}

	quicCfg := &quic.Config{
		MaxIdleTimeout:  time.Duration(cfg.MaxIdleTimeout) * time.Second,
		KeepAlivePeriod: time.Duration(cfg.KeepAlivePeriod) * time.Second,
		EnableDatagrams: false,
	}

	// QUIC listener (UDP)
	quicListener, err := quic.ListenAddr(cfg.QuicAddress, tlsConf, quicCfg)
	if err != nil {
		return fmt.Errorf("failed to start QUIC listener: %w", err)
	}
	s.quicListener = quicListener

	s.logger.Printf("🔐 QUIC listener: %s", cfg.QuicAddress)
	s.logger.Printf("📡 Multi-client transparent TCP proxy enabled")

	// Start QUIC accept loop
	s.wg.Add(1)
	go s.acceptClients()

	// Block until context cancelled
	<-s.ctx.Done()
	return s.Stop()
}

func (s *TunnelServer) Stop() error {
	s.logger.Println("Stopping server...")
	s.cancel()

	if s.quicListener != nil {
		_ = s.quicListener.Close()
	}

	// Close all client sessions
	s.sessionMu.Lock()
	for _, session := range s.sessions {
		s.closeSessionLocked(session)
	}
	s.sessionMu.Unlock()

	s.wg.Wait()
	s.logger.Println("Server stopped")
	return nil
}

func (s *TunnelServer) loadTLSConfig() (*tls.Config, error) {
	cfg := s.config.Server.TLS
	var cert tls.Certificate
	var err error

	if cfg.CertFile != "" && cfg.KeyFile != "" {
		s.logger.Printf("Loading TLS cert: %s", cfg.CertFile)
		cert, err = tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	} else {
		s.logger.Println("Generating self-signed certificate...")
		cert, err = tlsutil.GenerateSelfSignedCert(cfg.CertValidityDays)
	}
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{protocol.ProtocolNextProto},
		MinVersion:   tls.VersionTLS13,
	}, nil
}

func (s *TunnelServer) validatePassword() error {
	password := s.config.Server.Password
	if password == "change-this-password" || password == "" {
		s.logger.Println("═══════════════════════════════════")
		s.logger.Println("  ⚠️  WARNING: Using default/empty password!")
		s.logger.Println("  Please set a secure password in config")
		s.logger.Println("═══════════════════════════════════")
		return fmt.Errorf("insecure password")
	}
	return nil
}

func (s *TunnelServer) acceptClients() {
	defer s.wg.Done()

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		conn, err := s.quicListener.Accept(context.Background())
		if err != nil {
			if s.ctx.Err() != nil {
				return
			}
			s.logger.Printf("Accept error: %v", err)
			time.Sleep(time.Second)
			continue
		}

		go s.handleNewClient(conn)
	}
}

func (s *TunnelServer) handleNewClient(conn *quic.Conn) {
	remoteAddr := conn.RemoteAddr()
	s.logger.Printf("📱 New connection: %s", remoteAddr)

	// Authenticate and get client ID
	clientID, err := s.authenticateConnection(conn)
	if err != nil {
		s.logger.Printf("❌ Authentication failed: %s - %v", remoteAddr, err)
		conn.CloseWithError(1, "auth failed")
		return
	}

	s.logger.Printf("✓ Client authenticated: %s (ID: %s)", remoteAddr, clientID)

	// Create or replace client session
	session := s.createOrReplaceSession(clientID, conn)
	s.logger.Printf("✅ Client session active: %s", clientID)

	// Handle control messages from this client
	s.wg.Add(1)
	go s.handleClientControl(session)

	// Wait for connection to close
	<-conn.Context().Done()
	s.logger.Printf("📱 Client disconnected: %s", clientID)

	// Clean up session. Pass the session we created (not just the clientID) so a
	// stale connection's teardown can't remove a NEWER session that has already
	// replaced ours under the same clientID — which would tear down the live
	// tunnel and reset its streams (the browser/client then sees "CONNECT parse
	// failed: read request line: EOF"), forcing an endless reconnect storm.
	s.removeSession(session)
}

func (s *TunnelServer) authenticateConnection(conn *quic.Conn) (string, error) {
	// First stream used for AUTH
	stream, err := conn.AcceptStream(context.Background())
	if err != nil {
		return "", fmt.Errorf("failed to accept auth stream: %w", err)
	}
	defer stream.Close()

	msg, err := protocol.ReadMessageWithDeadline(stream, time.Now().Add(5*time.Second))
	if err != nil {
		return "", fmt.Errorf("failed to read auth message: %w", err)
	}

	if msg.Type != protocol.MsgAuth {
		_ = protocol.WriteMessage(stream, 0, protocol.MsgAuthResp, 0, []byte{protocol.ErrAuthFailed})
		return "", fmt.Errorf("expected MsgAuth, got type %d", msg.Type)
	}

	// Auth payload format: [32 bytes password hash][remaining bytes client ID]
	if len(msg.Data) < 32 {
		_ = protocol.WriteMessage(stream, 0, protocol.MsgAuthResp, 0, []byte{protocol.ErrAuthFailed})
		return "", fmt.Errorf("auth data too short")
	}

	passwordHash := sha256.Sum256([]byte(s.config.Server.Password))
	receivedHash := msg.Data[:32]

	// Constant-time compare to avoid leaking the password hash via timing.
	if subtle.ConstantTimeCompare(receivedHash, passwordHash[:]) != 1 {
		time.Sleep(300 * time.Millisecond) // rate-limit brute-force / timing attacks
		_ = protocol.WriteMessage(stream, 0, protocol.MsgAuthResp, 0, []byte{protocol.ErrAuthFailed})
		return "", fmt.Errorf("invalid password")
	}

	// Extract client ID from remaining bytes (if provided)
	var clientID string
	if len(msg.Data) > 32 {
		clientID = string(msg.Data[32:])
		s.logger.Printf("Client identified as: %s", clientID)
	} else {
		// Fallback: generate from remote address
		clientID = fmt.Sprintf("client_%s", conn.RemoteAddr().String())
		s.logger.Printf("⚠️  No client ID provided, using: %s", clientID)
	}

	// Validate client ID exists in config
	if s.config.GetClientPorts(clientID) == nil {
		s.logger.Printf("⚠️  Client ID '%s' not found in config", clientID)
		// Allow connection but it will get default ports
	}

	// Send auth success
	if err := protocol.WriteMessage(stream, 0, protocol.MsgAuthResp, 0, []byte{protocol.ErrOK}); err != nil {
		return "", fmt.Errorf("failed to send auth response: %w", err)
	}

	return clientID, nil
}

func (s *TunnelServer) createOrReplaceSession(clientID string, conn *quic.Conn) *ClientSession {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()

	// Close existing session if present
	if existingSession, exists := s.sessions[clientID]; exists {
		s.logger.Printf("Replacing existing session: %s", clientID)
		s.closeSessionLocked(existingSession)
	}

	// Get ports from config
	assignedPorts := s.config.GetClientPorts(clientID)
	if len(assignedPorts) == 0 {
		s.logger.Printf("⚠️  No ports configured for client: %s - using default [8080, 8081]", clientID)
		assignedPorts = []int{8080, 8081}
	}

	// Create new session
	ctx, cancel := context.WithCancel(s.ctx)
	session := &ClientSession{
		clientID:      clientID,
		conn:          conn,
		assignedPorts: assignedPorts,
		listeners:     make([]net.Listener, 0),
		ctx:           ctx,
		cancel:        cancel,
		logger:        log.New(os.Stdout, fmt.Sprintf("[%s] ", clientID), log.LstdFlags),
	}
	session.lastActive.Store(time.Now().Unix())

	s.sessions[clientID] = session

	// Update port routing map
	s.portRoutingMu.Lock()
	for _, port := range assignedPorts {
		s.portRouting[port] = clientID
	}
	s.portRoutingMu.Unlock()

	// Start TCP listeners for this client's assigned ports
	s.wg.Add(1)
	go s.startClientListeners(session)

	return session
}

func (s *TunnelServer) startClientListeners(session *ClientSession) {
	defer s.wg.Done()

	for _, port := range session.assignedPorts {
		addr := fmt.Sprintf("0.0.0.0:%d", port)
		var ln net.Listener
		var err error
		for attempt := 0; attempt < 5; attempt++ {
			ln, err = net.Listen("tcp", addr)
			if err == nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if err != nil {
			session.logger.Printf("Failed to start listener on %s: %v", addr, err)
			continue
		}

		session.mu.Lock()
		session.listeners = append(session.listeners, ln)
		session.mu.Unlock()

		session.logger.Printf("🎯 Port %d → %s tunnel (transparent TCP proxy)", port, session.clientID)

		// Accept connections on this port
		s.wg.Add(1)
		go s.acceptPortConnections(session, ln, port)
	}
}

func (s *TunnelServer) acceptPortConnections(session *ClientSession, ln net.Listener, port int) {
	defer s.wg.Done()
	defer ln.Close()

	for {
		select {
		case <-session.ctx.Done():
			return
		case <-s.ctx.Done():
			return
		default:
		}

		conn, err := ln.Accept()
		if err != nil {
			if session.ctx.Err() != nil || s.ctx.Err() != nil {
				return
			}
			session.logger.Printf("Accept error on port %d: %v", port, err)
			continue
		}

		// Route this connection through the client's QUIC tunnel
		s.wg.Add(1)
		go s.forwardTCPToClient(session, conn, port)
	}
}

// forwardTCPToClient - Transparent TCP forwarding (no protocol parsing)
func (s *TunnelServer) forwardTCPToClient(session *ClientSession, tcpConn net.Conn, sourcePort int) {
	defer s.wg.Done()
	defer tcpConn.Close()

	session.mu.RLock()
	quicConn := session.conn
	session.mu.RUnlock()

	if quicConn == nil {
		session.logger.Printf("No active QUIC connection for routing")
		return
	}

	// Wait for the incoming connection's first byte with a 5-second deadline.
	// This protects against Slowloris attacks and idle port scanners that connect
	// and never send bytes, while dropping empty preconnect sockets safely.
	_ = tcpConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	br := bufio.NewReader(tcpConn)
	if _, err := br.Peek(1); err != nil {
		return // connection timed out or closed without sending data
	}
	_ = tcpConn.SetReadDeadline(time.Time{}) // clear deadline once active data arrives

	// Admission control: bound how many connections may queue for this client.
	// MaxConcurrentUsers caps the queue so a slow phone can't make the server
	// hold an unbounded number of waiting browser connections.
	if maxQueue := s.config.Server.Limits.MaxConcurrentUsers; maxQueue > 0 {
		if depth := session.queued.Add(1); depth > int32(maxQueue) {
			session.queued.Add(-1)
			session.logger.Printf("⛔ Queue full (%d) - dropping connection from port %d", maxQueue, sourcePort)
			return
		}
		defer session.queued.Add(-1)
	}

	// Open a stream over the tunnel. With the client advertising
	// MaxIncomingStreams=1, OpenStreamSync BLOCKS here (back-pressure) until the
	// client has finished the previous request and returned a stream credit.
	// This is what serializes the system end-to-end: one request at a time,
	// the rest waiting in line up to streamQueueWait.
	queueWait := time.Duration(s.config.Server.Limits.QueueWaitSeconds) * time.Second
	if queueWait <= 0 {
		queueWait = defaultStreamQueueWait
	}

	ctx, cancel := context.WithTimeout(session.ctx, queueWait)
	defer cancel()

	stream, err := quicConn.OpenStreamSync(ctx)
	if err != nil {
		session.logger.Printf("Failed to open stream (waited up to %v): %v", queueWait, err)
		return
	}
	// relay.Pipe closes the stream gracefully on completion; do not reset it
	// afterwards or in-flight bytes can be truncated.

	// Generate unique connection ID
	connID := atomic.AddUint32(&session.totalStreams, 1)
	atomic.AddInt32(&session.activeStreams, 1)
	defer atomic.AddInt32(&session.activeStreams, -1)

	// Send 4-byte sourcePort header so the client knows which VPS port was connected
	var portHeader [4]byte
	binary.BigEndian.PutUint32(portHeader[:], uint32(sourcePort))
	if _, err := stream.Write(portHeader[:]); err != nil {
		stream.CancelWrite(0)
		stream.CancelRead(0)
		return
	}

	session.lastActive.Store(time.Now().Unix())

	// Raw bidirectional copy with half-close.
	relay.Pipe(session.ctx, relay.TCPBuffered(tcpConn, br), relay.Stream(stream, nil))

	session.lastActive.Store(time.Now().Unix())
	session.logger.Printf("🔻 Stream #%d closed", connID)
}

func (s *TunnelServer) handleClientControl(session *ClientSession) {
	defer s.wg.Done()

	for {
		select {
		case <-session.ctx.Done():
			return
		case <-s.ctx.Done():
			return
		default:
		}

		stream, err := session.conn.AcceptStream(context.Background())
		if err != nil {
			if session.ctx.Err() != nil || s.ctx.Err() != nil {
				return
			}
			session.logger.Printf("Control stream accept error: %v", err)
			return
		}

		s.wg.Add(1)
		go s.handleControlStream(session, stream)
	}
}

func (s *TunnelServer) handleControlStream(session *ClientSession, stream *quic.Stream) {
	defer s.wg.Done()
	defer func() {
		stream.CancelRead(0)
		stream.CancelWrite(0)
		stream.Close()
	}()

	msg, err := protocol.ReadMessageWithDeadline(stream, time.Now().Add(5*time.Second))
	if err != nil {
		return
	}

	switch msg.Type {
	case protocol.MsgPing:
		session.lastActive.Store(time.Now().Unix())
		_ = protocol.WriteMessage(stream, 0, protocol.MsgPong, 0, nil)
	default:
		session.logger.Printf("Unexpected control message type: %d", msg.Type)
	}
}

func (s *TunnelServer) removeSession(session *ClientSession) {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()

	// Only remove if this exact session is still the current one. If it has
	// already been replaced (a reconnect under the same clientID), the
	// replacement now owns the map entry, the listeners and the port routing —
	// leave them intact. createOrReplaceSession already closed our old session.
	if current, exists := s.sessions[session.clientID]; !exists || current != session {
		return
	}

	s.closeSessionLocked(session)
	delete(s.sessions, session.clientID)

	// Clean up port routing
	s.portRoutingMu.Lock()
	for _, port := range session.assignedPorts {
		delete(s.portRouting, port)
	}
	s.portRoutingMu.Unlock()
}

func (s *TunnelServer) closeSessionLocked(session *ClientSession) {
	session.cancel()

	// Close all TCP listeners
	session.mu.Lock()
	for _, ln := range session.listeners {
		_ = ln.Close()
	}
	session.listeners = nil
	session.mu.Unlock()

	// Close QUIC connection
	if session.conn != nil {
		session.conn.CloseWithError(0, "session closed")
		session.conn = nil
	}

	session.logger.Printf("Session closed")
}
