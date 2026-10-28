package protocol

import (
	"bytes"
	"testing"
	"time"
)

type mockStream struct {
	buf bytes.Buffer
}

func (m *mockStream) Read(p []byte) (n int, err error) {
	return m.buf.Read(p)
}

func (m *mockStream) Write(p []byte) (n int, err error) {
	return m.buf.Write(p)
}

func (m *mockStream) SetReadDeadline(t time.Time) error {
	return nil
}

func (m *mockStream) SetWriteDeadline(t time.Time) error {
	return nil
}

func TestWriteAndReadMessage(t *testing.T) {
	stream := &mockStream{}
	data := []byte("hello portila")
	connID := uint32(12345)

	if err := WriteMessage(stream, connID, MsgAuth, 0, data); err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}

	msg, err := ReadMessage(stream)
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}

	if msg.Version != ProtocolVersion {
		t.Errorf("expected version %d, got %d", ProtocolVersion, msg.Version)
	}
	if msg.Type != MsgAuth {
		t.Errorf("expected type %d, got %d", MsgAuth, msg.Type)
	}
	if msg.ConnID != connID {
		t.Errorf("expected connID %d, got %d", connID, msg.ConnID)
	}
	if string(msg.Data) != string(data) {
		t.Errorf("expected data %s, got %s", data, msg.Data)
	}
}

func TestMessageTooLarge(t *testing.T) {
	stream := &mockStream{}
	hugeData := make([]byte, MaxMessageSize+1)

	err := WriteMessage(stream, 1, MsgPing, 0, hugeData)
	if err == nil {
		t.Fatalf("expected error for oversized message, got nil")
	}
}

func TestReadWithDeadline(t *testing.T) {
	stream := &mockStream{}
	if err := WriteMessage(stream, 1, MsgPing, 0, nil); err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}

	msg, err := ReadMessageWithDeadline(stream, time.Now().Add(5*time.Second))
	if err != nil {
		t.Fatalf("ReadMessageWithDeadline failed: %v", err)
	}
	if msg.Type != MsgPing {
		t.Errorf("expected MsgPing, got %d", msg.Type)
	}
}
