package events

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCollector_StartBindsSocket(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := NewStore(5 * time.Minute)
	socketPath := shortSocketPath(t)
	c := NewCollector(store, socketPath)

	if err := c.Start(ctx); err != nil {
		t.Fatalf("start collector: %v", err)
	}

	if _, err := os.Stat(socketPath); err != nil {
		t.Fatalf("expected socket at %s: %v", socketPath, err)
	}
}

func TestCollector_AcceptsValidEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := NewStore(5 * time.Minute)
	socketPath := shortSocketPath(t)
	c := NewCollector(store, socketPath)
	if err := c.Start(ctx); err != nil {
		t.Fatalf("start collector: %v", err)
	}

	payload := []byte(`{"assistant":"claude","state":"waiting_input","target":"s:0.1","ts":"2026-02-27T12:00:00Z"}`)
	if err := sendDatagram(socketPath, payload); err != nil {
		t.Fatalf("send datagram: %v", err)
	}

	waitFor(t, 1*time.Second, func() bool {
		return len(store.Snapshot(time.Now().UTC())) == 1
	})
}

func TestCollector_IgnoresMalformedEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := NewStore(5 * time.Minute)
	socketPath := shortSocketPath(t)
	c := NewCollector(store, socketPath)
	if err := c.Start(ctx); err != nil {
		t.Fatalf("start collector: %v", err)
	}

	if err := sendDatagram(socketPath, []byte(`not-json`)); err != nil {
		t.Fatalf("send datagram: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	if got := len(store.Snapshot(time.Now().UTC())); got != 0 {
		t.Fatalf("expected 0 events for malformed payload, got %d", got)
	}
}

func TestCollector_RejectsOversizedPayload(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := NewStore(5 * time.Minute)
	socketPath := shortSocketPath(t)
	c := NewCollector(store, socketPath)
	c.MaxPayloadBytes = 64
	if err := c.Start(ctx); err != nil {
		t.Fatalf("start collector: %v", err)
	}

	big := make([]byte, 128)
	for i := range big {
		big[i] = 'a'
	}
	if err := sendDatagram(socketPath, big); err != nil {
		t.Fatalf("send datagram: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	if got := len(store.Snapshot(time.Now().UTC())); got != 0 {
		t.Fatalf("expected 0 events for oversized payload, got %d", got)
	}
}

func sendDatagram(socketPath string, payload []byte) error {
	addr, err := net.ResolveUnixAddr("unixgram", socketPath)
	if err != nil {
		return err
	}
	conn, err := net.DialUnix("unixgram", nil, addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Write(payload)
	return err
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

func shortSocketPath(t *testing.T) string {
	t.Helper()
	base := filepath.Join(os.TempDir(), "pp-events")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatalf("mkdir temp base: %v", err)
	}
	p := filepath.Join(base, fmt.Sprintf("%d-%d.sock", time.Now().UnixNano(), os.Getpid()))
	t.Cleanup(func() {
		_ = os.Remove(p)
	})
	return p
}
