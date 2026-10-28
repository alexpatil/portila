package mobile

import (
	"sync"
	"testing"
	"time"
)

type testCallback struct {
	mu           sync.Mutex
	logs         []string
	connected    bool
	disconnected bool
	errors       []string
}

func (t *testCallback) OnConnected() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.connected = true
}

func (t *testCallback) OnDisconnected(reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.disconnected = true
}

func (t *testCallback) OnError(err string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.errors = append(t.errors, err)
}

func (t *testCallback) OnLog(msg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.logs = append(t.logs, msg)
}

func TestMobileVersion(t *testing.T) {
	if v := Version(); v != "1.0.0" {
		t.Errorf("expected 1.0.0, got %s", v)
	}
}

func TestMobileStartStop(t *testing.T) {
	cb := &testCallback{}
	conf := `
[client]
server_address = 127.0.0.1:54321
password = testpass
client_id = mobile_test
`
	if err := StartClient(conf, cb); err != nil {
		t.Fatalf("StartClient failed: %v", err)
	}

	if !IsRunning() {
		t.Errorf("expected IsRunning to be true")
	}

	// Double start should return error
	if err := StartClient(conf, cb); err == nil {
		t.Errorf("expected error when starting already running client, got nil")
	}

	time.Sleep(100 * time.Millisecond)

	if err := StopClient(); err != nil {
		t.Fatalf("StopClient failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if IsRunning() {
		t.Errorf("expected IsRunning to be false after stop")
	}
}
