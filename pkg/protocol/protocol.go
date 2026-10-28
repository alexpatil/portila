package protocol

import "time"

const (
	ProtocolNextProto = "portila-tunnel/1"

	ProtocolVersion = 1
	HeaderSize      = 12
	MaxMessageSize  = 10 * 1024 * 1024 // 10 MB

	// Message types. Framing is used only for the auth handshake and keepalive;
	// the data path uses the raw QUIC stream (see pkg/relay).
	MsgAuth     byte = 0x01
	MsgAuthResp byte = 0x02

	MsgPing byte = 0x20
	MsgPong byte = 0x21

	// Error codes for the AuthResp payload.
	ErrOK         byte = 0x00
	ErrAuthFailed byte = 0x01
)

type Message struct {
	Version uint8
	Type    uint8
	Flags   uint8
	_       uint8 // reserved

	ConnID uint32
	Data   []byte
}

// StreamRW abstracts stream reads, writes, and deadline controls
type StreamRW interface {
	Read(p []byte) (n int, err error)
	Write(p []byte) (n int, err error)
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
}
