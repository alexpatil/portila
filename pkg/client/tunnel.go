package client

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/quic-go/quic-go"
	"portila/pkg/relay"
)

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

	// Read 4-byte sourcePort prefix sent by the server.
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

// handleReverseStream forwards traffic directly to a local service (Rathole / FRP mode).
func (c *TunnelClient) handleReverseStream(stream *quic.Stream, sourcePort int) {
	start := time.Now()

	// Look up port-specific target or fall back to primary local_target.
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

// handleExitStream acts as an HTTP CONNECT proxy exit node (Tailscale / Gateway mode).
func (c *TunnelClient) handleExitStream(stream *quic.Stream) {
	br := bufio.NewReader(stream)
	req, err := http.ReadRequest(br)
	if err != nil {
		c.logger.Printf("Exit node: read request failed: %v", err)
		stream.CancelRead(0)
		stream.CancelWrite(0)
		return
	}

	if req.Method != http.MethodConnect {
		c.logger.Printf("Exit node: unsupported HTTP method: %s", req.Method)
		resp := "HTTP/1.1 405 Method Not Allowed\r\nContent-Length: 0\r\n\r\n"
		_, _ = stream.Write([]byte(resp))
		stream.CancelRead(0)
		stream.CancelWrite(0)
		return
	}

	target := req.RequestURI
	if target == "" {
		target = req.Host
	}

	host, _, err := net.SplitHostPort(target)
	if err != nil {
		host = target
	}

	acquireCtx, cancel := context.WithTimeout(c.ctx, c.queueWait)
	defer cancel()

	release, err := c.hosts.acquire(acquireCtx, host)
	if err != nil {
		c.logger.Printf("Exit node: concurrency queue full for %s: %v", host, err)
		resp := "HTTP/1.1 504 Gateway Timeout\r\nContent-Length: 0\r\n\r\n"
		_, _ = stream.Write([]byte(resp))
		stream.CancelRead(0)
		stream.CancelWrite(0)
		return
	}
	defer release()

	start := time.Now()
	dst, err := c.dialer.DialContext(c.ctx, "tcp", target)
	if err != nil {
		c.logger.Printf("Exit node: dial %s failed (%v): %v", target, time.Since(start), err)
		resp := "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n"
		_, _ = stream.Write([]byte(resp))
		stream.CancelRead(0)
		stream.CancelWrite(0)
		return
	}
	defer dst.Close()

	if _, err := stream.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		stream.CancelRead(0)
		stream.CancelWrite(0)
		return
	}

	c.logger.Printf("TCP dial success → %s | %v", target, time.Since(start))
	relay.Pipe(c.ctx, relay.Stream(stream, br), relay.TCP(dst))
	c.logger.Printf("🔻 Stream closed (%s)", target)
}
