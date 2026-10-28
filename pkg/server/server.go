package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"portila/pkg/config"
	"portila/pkg/protocol"
	tlsutil "portila/pkg/tls"
)

// TunnelServer coordinates incoming QUIC tunnels and exposes public proxy ports.
type TunnelServer struct {
	config *config.ServerConfig
	logger *log.Logger

	quicListener *quic.Listener

	sessionMu sync.RWMutex
	sessions  map[string]*ClientSession

	portRoutingMu sync.RWMutex
	portRouting   map[int]string

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewTunnelServer constructs a server instance with initialized maps and context.
func NewTunnelServer(cfg *config.ServerConfig) *TunnelServer {
	ctx, cancel := context.WithCancel(context.Background())
	return &TunnelServer{
		config:      cfg,
		logger:      log.New(os.Stdout, "[SERVER] ", log.LstdFlags),
		sessions:    make(map[string]*ClientSession),
		portRouting: make(map[int]string),
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Start boots the QUIC listener and begins accepting tunnel clients.
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

	quicListener, err := quic.ListenAddr(cfg.QuicAddress, tlsConf, quicCfg)
	if err != nil {
		return fmt.Errorf("failed to start QUIC listener: %w", err)
	}
	s.quicListener = quicListener

	s.logger.Printf("🔐 QUIC listener: %s", cfg.QuicAddress)
	s.logger.Printf("📡 Multi-client transparent TCP proxy enabled")

	s.wg.Add(1)
	go s.acceptClients()

	<-s.ctx.Done()
	return s.Stop()
}

// Stop shuts down the QUIC listener, closes all active client sessions, and waits for routines.
func (s *TunnelServer) Stop() error {
	s.logger.Println("Stopping server...")
	s.cancel()

	if s.quicListener != nil {
		_ = s.quicListener.Close()
	}

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

	clientID, err := s.authenticateConnection(conn)
	if err != nil {
		s.logger.Printf("❌ Authentication failed: %s - %v", remoteAddr, err)
		conn.CloseWithError(1, "auth failed")
		return
	}

	s.logger.Printf("✓ Client authenticated: %s (ID: %s)", remoteAddr, clientID)
	session := s.createOrReplaceSession(clientID, conn)
	s.logger.Printf("✅ Client session active: %s", clientID)

	s.wg.Add(1)
	go s.handleClientControl(session)

	<-conn.Context().Done()
	s.logger.Printf("📱 Client disconnected: %s", clientID)

	s.removeSession(session)
}
