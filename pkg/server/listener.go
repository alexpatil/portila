package server

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"portila/pkg/relay"
)

// defaultStreamQueueWait is used when the config does not set a queue wait.
const defaultStreamQueueWait = 300 * time.Second

// startClientListeners opens public TCP listening ports assigned to this client session.
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
			session.logger.Printf("Failed to bind port %s: %v", addr, err)
			continue
		}

		session.mu.Lock()
		session.listeners = append(session.listeners, ln)
		session.mu.Unlock()

		session.logger.Printf("🎯 Port %d → %s tunnel (transparent TCP proxy)", port, session.clientID)

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

		s.wg.Add(1)
		go s.forwardTCPToClient(session, conn, port)
	}
}

// forwardTCPToClient tunnels an incoming TCP connection through an available QUIC stream.
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

	// 5-second deadline for the initial byte protects against idle scanners and Slowloris.
	_ = tcpConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	br := bufio.NewReader(tcpConn)
	if _, err := br.Peek(1); err != nil {
		return // Connection closed or timed out before transmitting payload
	}
	_ = tcpConn.SetReadDeadline(time.Time{})

	// Queue admission limiter.
	if maxQueue := s.config.Server.Limits.MaxConcurrentUsers; maxQueue > 0 {
		if depth := session.queued.Add(1); depth > int32(maxQueue) {
			session.queued.Add(-1)
			session.logger.Printf("Queue full (%d), dropping request from port %d", maxQueue, sourcePort)
			return
		}
		defer session.queued.Add(-1)
	}

	queueWait := time.Duration(s.config.Server.Limits.QueueWaitSeconds) * time.Second
	if queueWait <= 0 {
		queueWait = defaultStreamQueueWait
	}

	ctx, cancel := context.WithTimeout(session.ctx, queueWait)
	defer cancel()

	stream, err := quicConn.OpenStreamSync(ctx)
	if err != nil {
		session.logger.Printf("OpenStream timeout (waited up to %v): %v", queueWait, err)
		return
	}

	connID := atomic.AddUint32(&session.totalStreams, 1)
	atomic.AddInt32(&session.activeStreams, 1)
	defer atomic.AddInt32(&session.activeStreams, -1)

	// Send 4-byte sourcePort prefix so client routes to the mapped local service.
	var portHeader [4]byte
	binary.BigEndian.PutUint32(portHeader[:], uint32(sourcePort))
	if _, err := stream.Write(portHeader[:]); err != nil {
		stream.CancelWrite(0)
		stream.CancelRead(0)
		return
	}

	session.lastActive.Store(time.Now().Unix())

	// Bidirectional half-close pipeline.
	relay.Pipe(session.ctx, relay.TCPBuffered(tcpConn, br), relay.Stream(stream, nil))

	session.lastActive.Store(time.Now().Unix())
	session.logger.Printf("🔻 Stream #%d closed", connID)
}
