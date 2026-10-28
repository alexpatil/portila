// Package relay copies bytes bidirectionally between two endpoints with correct
// half-close semantics: when one direction reaches EOF it is closed for writing
// while the other direction keeps flowing, so a slower direction is never
// truncated. It supports a QUIC stream and a net.Conn as endpoints.
package relay

import (
	"context"
	"io"
	"net"
	"sync"

	"github.com/quic-go/quic-go"
)

// Conn is one endpoint of a relay.
//
//	CloseWrite signals "I have no more data to send" (a half close).
//	Close fully tears the endpoint down, unblocking any pending Read/Write.
type Conn interface {
	io.Reader
	io.Writer
	CloseWrite() error
	Close() error
}

// Pipe copies between x and y until BOTH directions reach EOF (honoring
// half-close) or ctx is cancelled. It blocks until both copies finish.
//
// On normal completion each side is only half-closed (CloseWrite), so buffered
// data is allowed to flush. A full Close (which may reset/truncate a QUIC
// stream's pending bytes) happens ONLY on cancellation, to unblock the copies.
func Pipe(ctx context.Context, x, y Conn) {
	closed := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = x.Close()
			_ = y.Close()
		case <-closed:
		}
	}()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(y, x)
		_ = y.CloseWrite() // x is done sending; half-close y's write side
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(x, y)
		_ = x.CloseWrite()
	}()
	wg.Wait()
	close(closed) // both directions drained gracefully; leave half-closes intact
}

// Stream adapts a QUIC stream. Pass r to read from a buffer that already holds
// bytes drained off the stream (e.g. a *bufio.Reader); pass nil to read the
// stream directly.
func Stream(s *quic.Stream, r io.Reader) Conn {
	if r == nil {
		r = s
	}
	return streamConn{r: r, s: s}
}

type streamConn struct {
	r io.Reader
	s *quic.Stream
}

func (c streamConn) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c streamConn) Write(p []byte) (int, error) { return c.s.Write(p) }
func (c streamConn) CloseWrite() error           { return c.s.Close() } // sends FIN on the write half
func (c streamConn) Close() error {
	c.s.CancelRead(0)
	c.s.CancelWrite(0)
	return nil
}

// TCP adapts a net.Conn (typically a *net.TCPConn).
func TCP(c net.Conn) Conn { return tcpConn{c: c, r: c} }

// TCPBuffered is like TCP but reads through r (e.g. a *bufio.Reader) so bytes
// already drained off the connection — such as a first byte peeked to detect an
// empty/probe connection — are not lost. Writes and closes still go to c.
func TCPBuffered(c net.Conn, r io.Reader) Conn { return tcpConn{c: c, r: r} }

type tcpConn struct {
	c net.Conn
	r io.Reader
}

func (t tcpConn) Read(p []byte) (int, error)  { return t.r.Read(p) }
func (t tcpConn) Write(p []byte) (int, error) { return t.c.Write(p) }
func (t tcpConn) CloseWrite() error {
	if cw, ok := t.c.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return t.c.Close()
}
func (t tcpConn) Close() error { return t.c.Close() }
