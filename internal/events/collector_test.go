package events

import (
	"context"
	"encoding/json"
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
	accepted := make(chan Event, 1)
	c.OnAccepted = func(e Event) {
		select {
		case accepted <- e:
		default:
		}
	}
	if err := c.Start(ctx); err != nil {
		t.Fatalf("start collector: %v", err)
	}

	payloadBytes, err := json.Marshal(Event{
		Assistant: "claude",
		State:     StateWaitingInput,
		Target:    "s:0.1",
		TS:        time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	payload := payloadBytes
	if err := sendDatagram(socketPath, payload); err != nil {
		t.Fatalf("send datagram: %v", err)
	}

	select {
	case <-accepted:
		if got := len(store.Snapshot(time.Now().UTC())); got != 1 {
			t.Fatalf("expected 1 stored event after accept callback, got %d", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("collector did not accept event within timeout")
	}
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
