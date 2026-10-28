package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
	"portila/pkg/protocol"
)

// ClientSession represents an active authenticated client and its assigned public listeners.
type ClientSession struct {
	clientID      string
	conn          *quic.Conn
	assignedPorts []int
	listeners     []net.Listener

	activeStreams int32
	totalStreams  uint32
	queued        atomic.Int32

	mu         sync.RWMutex
	ctx        context.Context
	cancel     context.CancelFunc
	logger     *log.Logger
	lastActive atomic.Int64
}

// authenticateConnection reads the auth handshake from the first QUIC stream.
func (s *TunnelServer) authenticateConnection(conn *quic.Conn) (string, error) {
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

	if len(msg.Data) < 32 {
		_ = protocol.WriteMessage(stream, 0, protocol.MsgAuthResp, 0, []byte{protocol.ErrAuthFailed})
		return "", fmt.Errorf("auth data too short")
	}

	passwordHash := sha256.Sum256([]byte(s.config.Server.Password))
	receivedHash := msg.Data[:32]

	// Constant-time compare to mitigate timing attacks.
	if subtle.ConstantTimeCompare(receivedHash, passwordHash[:]) != 1 {
		time.Sleep(300 * time.Millisecond) // Throttling brute-force attempts
		_ = protocol.WriteMessage(stream, 0, protocol.MsgAuthResp, 0, []byte{protocol.ErrAuthFailed})
		return "", fmt.Errorf("invalid password")
	}

	var clientID string
	if len(msg.Data) > 32 {
		clientID = string(msg.Data[32:])
		s.logger.Printf("Client identified as: %s", clientID)
	} else {
		clientID = fmt.Sprintf("client_%s", conn.RemoteAddr().String())
		s.logger.Printf("No client ID provided, generated: %s", clientID)
	}

	if s.config.GetClientPorts(clientID) == nil {
		s.logger.Printf("Client ID '%s' not in config, assigning defaults", clientID)
	}

	if err := protocol.WriteMessage(stream, 0, protocol.MsgAuthResp, 0, []byte{protocol.ErrOK}); err != nil {
		return "", fmt.Errorf("failed to send auth response: %w", err)
	}

	return clientID, nil
}

// createOrReplaceSession instantiates a new session and tears down any existing one for clientID.
func (s *TunnelServer) createOrReplaceSession(clientID string, conn *quic.Conn) *ClientSession {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()

	if existing, exists := s.sessions[clientID]; exists {
		s.logger.Printf("Replacing existing session for client: %s", clientID)
		s.closeSessionLocked(existing)
	}

	assignedPorts := s.config.GetClientPorts(clientID)
	if len(assignedPorts) == 0 {
		assignedPorts = []int{8080, 8081}
	}

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

	s.portRoutingMu.Lock()
	for _, port := range assignedPorts {
		s.portRouting[port] = clientID
	}
	s.portRoutingMu.Unlock()

	s.wg.Add(1)
	go s.startClientListeners(session)

	return session
}

// handleClientControl processes control messages such as heartbeats.
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

	if msg.Type == protocol.MsgPing {
		session.lastActive.Store(time.Now().Unix())
		_ = protocol.WriteMessage(stream, 0, protocol.MsgPong, 0, nil)
	}
}

// removeSession closes and cleans up the session if it matches the current active one.
func (s *TunnelServer) removeSession(session *ClientSession) {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()

	if current, exists := s.sessions[session.clientID]; !exists || current != session {
		return
	}

	s.closeSessionLocked(session)
	delete(s.sessions, session.clientID)

	s.portRoutingMu.Lock()
	for _, port := range session.assignedPorts {
		delete(s.portRouting, port)
	}
	s.portRoutingMu.Unlock()
}

func (s *TunnelServer) closeSessionLocked(session *ClientSession) {
	session.cancel()

	session.mu.Lock()
	for _, ln := range session.listeners {
		_ = ln.Close()
	}
	session.listeners = nil
	session.mu.Unlock()

	if session.conn != nil {
		session.conn.CloseWithError(0, "session closed")
		session.conn = nil
	}
	session.logger.Printf("Session closed")
}
