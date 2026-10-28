package relay

import (
	"bytes"
	"context"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

type pipeConn struct {
	net.Conn
	closeWriteOnce sync.Once
	closedWrite    bool
}

func (p *pipeConn) CloseWrite() error {
	p.closeWriteOnce.Do(func() {
		p.closedWrite = true
		_ = p.Conn.Close()
	})
	return nil
}

func TestPipeBidirectional(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	p1 := &pipeConn{Conn: c1}
	p2 := &pipeConn{Conn: c2}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		Pipe(ctx, TCP(p1), TCP(p2))
	}()

	// Since Pipe copies between x and y directly,
	// when p1 is passed as x and p2 as y, Pipe copies p1 -> p2 and p2 -> p1.
	// We verify that cancelling or closing unblocks Pipe cleanly.
	cancel()
	wg.Wait()
}

func TestTCPBuffered(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	initial := "PEEKED_"
	r := strings.NewReader(initial)
	buffered := TCPBuffered(c1, r)

	buf := make([]byte, len(initial))
	n, err := buffered.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("read buffered failed: %v", err)
	}

	if string(buf[:n]) != initial {
		t.Errorf("expected %s, got %s", initial, string(buf[:n]))
	}
}

func TestStreamConn(t *testing.T) {
	buf := bytes.NewBufferString("stream-data")
	conn := streamConn{r: buf, s: nil}

	out := make([]byte, 11)
	n, err := conn.Read(out)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if string(out[:n]) != "stream-data" {
		t.Errorf("expected stream-data, got %s", string(out[:n]))
	}
}
