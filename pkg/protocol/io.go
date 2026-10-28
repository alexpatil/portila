package protocol

import (
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

// ---- low-level read/write ----

func WriteMessage(stream StreamRW, connID uint32, msgType, flags byte, data []byte) error {
	if len(data) > MaxMessageSize {
		return fmt.Errorf("message too large: %d bytes", len(data))
	}

	header := make([]byte, HeaderSize)
	header[0] = ProtocolVersion
	header[1] = msgType
	header[2] = flags
	header[3] = 0 // reserved
	binary.BigEndian.PutUint32(header[4:8], connID)
	binary.BigEndian.PutUint32(header[8:12], uint32(len(data)))

	// optional: deadlines to avoid stuck writes
	_ = stream.SetWriteDeadline(time.Now().Add(30 * time.Second))
	defer stream.SetWriteDeadline(time.Time{})

	if _, err := stream.Write(header); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	if len(data) > 0 {
		if _, err := stream.Write(data); err != nil {
			return fmt.Errorf("write body: %w", err)
		}
	}
	return nil
}

func ReadMessage(stream StreamRW) (*Message, error) {
	header := make([]byte, HeaderSize)

	_ = stream.SetReadDeadline(time.Now().Add(60 * time.Second))
	defer stream.SetReadDeadline(time.Time{})

	if _, err := io.ReadFull(stream, header); err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	version := header[0]
	if version != ProtocolVersion {
		return nil, fmt.Errorf("unsupported protocol version: %d", version)
	}

	msgType := header[1]
	flags := header[2]
	// header[3] reserved
	connID := binary.BigEndian.Uint32(header[4:8])
	length := binary.BigEndian.Uint32(header[8:12])

	if length > MaxMessageSize {
		return nil, fmt.Errorf("message too large: %d bytes", length)
	}

	var data []byte
	if length > 0 {
		data = make([]byte, length)
		if _, err := io.ReadFull(stream, data); err != nil {
			return nil, fmt.Errorf("read body: %w", err)
		}
	}

	return &Message{
		Version: version,
		Type:    msgType,
		Flags:   flags,
		ConnID:  connID,
		Data:    data,
	}, nil
}

func ReadMessageWithDeadline(stream StreamRW, deadline time.Time) (*Message, error) {
	_ = stream.SetReadDeadline(deadline)
	defer stream.SetReadDeadline(time.Time{})
	return ReadMessage(stream)
}
