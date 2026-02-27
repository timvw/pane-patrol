package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
)

const defaultMaxPayloadBytes = 8 * 1024

type Collector struct {
	store *Store
	path  string

	MaxPayloadBytes int

	mu     sync.Mutex
	conn   *net.UnixConn
	closed bool
}

func NewCollector(store *Store, socketPath string) *Collector {
	return &Collector{
		store:           store,
		path:            socketPath,
		MaxPayloadBytes: defaultMaxPayloadBytes,
	}
}

func (c *Collector) SocketPath() string {
	return c.path
}

func (c *Collector) Start(ctx context.Context) error {
	if c.store == nil {
		return fmt.Errorf("store is required")
	}
	if c.path == "" {
		return fmt.Errorf("socket path is required")
	}
	if c.MaxPayloadBytes <= 0 {
		c.MaxPayloadBytes = defaultMaxPayloadBytes
	}

	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}
	if err := os.Chmod(filepath.Dir(c.path), 0o700); err != nil {
		return fmt.Errorf("chmod socket dir: %w", err)
	}
	if err := os.Remove(c.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale socket: %w", err)
	}

	addr, err := net.ResolveUnixAddr("unixgram", c.path)
	if err != nil {
		return fmt.Errorf("resolve unix addr: %w", err)
	}
	conn, err := net.ListenUnixgram("unixgram", addr)
	if err != nil {
		return fmt.Errorf("listen unixgram: %w", err)
	}
	if err := os.Chmod(c.path, 0o600); err != nil {
		_ = conn.Close()
		return fmt.Errorf("chmod socket: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.closed = false
	c.mu.Unlock()

	go func() {
		<-ctx.Done()
		c.close()
	}()

	go c.readLoop()

	return nil
}

func (c *Collector) readLoop() {
	buf := make([]byte, c.MaxPayloadBytes)
	for {
		c.mu.Lock()
		conn := c.conn
		c.mu.Unlock()
		if conn == nil {
			return
		}

		n, _, err := conn.ReadFromUnix(buf)
		if err != nil {
			if c.isClosed() {
				return
			}
			continue
		}

		if n <= 0 || n >= c.MaxPayloadBytes {
			continue
		}

		var e Event
		if err := json.Unmarshal(buf[:n], &e); err != nil {
			continue
		}
		if err := e.Validate(); err != nil {
			continue
		}
		c.store.Upsert(e)
	}
}

func (c *Collector) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *Collector) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
}
