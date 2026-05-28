// Package proxy: controller.go wraps the bare Proxy with a runtime on/off
// gate and a configurable fixed port.
//
// Semantics:
//   - The user picks a fixed loopback port in settings; the controller binds
//     that exact port (no OS-picked random fallback).
//   - On startup the proxy is NOT bound. The user has to click "Connect" in
//     the UI to start forwarding.
//   - Toggling off frees the port; toggling on rebinds. Changing the port
//     while connected restarts the listener at the new port.
//
// Freeing the port on stop is the opposite of the previous "keep the
// listener alive and 503 inside the handler" trick. The new contract gives
// users a deterministic address that's either reachable (Connected) or
// completely silent (Disconnected) — closer to how Charles / mitmproxy
// behave and easier to reason about in `lsof`.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/zerx-lab/ai-fox/internal/config"
	"github.com/zerx-lab/ai-fox/internal/store"
)

// Controller owns the lifecycle of the reverse proxy. It does NOT bind a
// listener until Start is called.
type Controller struct {
	cfg   *config.Store
	store *store.Store

	mu       sync.Mutex
	port     int
	listener net.Listener
	server   *http.Server
	proxy    *Proxy
}

// NewController creates a stopped controller with the requested fixed port.
// port == 0 is honored as "let the OS pick a free port on Start", which is
// useful in tests; production callers pass the persisted setting value so
// the user always sees the same address.
func NewController(port int, cfg *config.Store, st *store.Store) (*Controller, error) {
	if cfg == nil || st == nil {
		return nil, errors.New("proxy: config and store are required")
	}
	return &Controller{cfg: cfg, store: st, port: port}, nil
}

// Start binds the listener on the configured port and begins serving. Calling
// Start while already enabled is a no-op.
func (c *Controller) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.listener != nil {
		return nil
	}
	return c.startLocked()
}

// Stop closes the listener and frees the port. Calling Stop while already
// stopped is a no-op.
func (c *Controller) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopLocked()
}

// Close shuts everything down for good. Used on app exit.
func (c *Controller) Close() {
	c.Stop()
}

// Enabled reports whether the listener is currently accepting connections.
func (c *Controller) Enabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.listener != nil
}

// Address returns the canonical loopback host:port the proxy uses. Stable
// across enable/disable cycles so the user can paste it into their AI client
// once and forget about it.
func (c *Controller) Address() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return fmt.Sprintf("127.0.0.1:%d", c.port)
}

// Port returns the configured fixed port.
func (c *Controller) Port() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.port
}

// SetPort changes the bound port. If the controller is currently running it
// restarts at the new port; otherwise it just updates the stored value so the
// next Start uses it.
func (c *Controller) SetPort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("proxy port out of range: %d", port)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.port == port {
		return nil
	}
	c.port = port
	if c.listener == nil {
		return nil
	}
	c.stopLocked()
	return c.startLocked()
}

// SetEnabled toggles the proxy to the desired state and returns whether the
// proxy is now running. Returns an error only if Start failed.
func (c *Controller) SetEnabled(want bool) (bool, error) {
	if want {
		if err := c.Start(); err != nil {
			return false, err
		}
		return true, nil
	}
	c.Stop()
	return false, nil
}

// ErrNotRunning is returned by callers that ask for live-proxy details while
// the proxy is stopped.
var ErrNotRunning = errors.New("proxy is not running")

func (c *Controller) startLocked() error {
	addr := fmt.Sprintf("127.0.0.1:%d", c.port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("bind %s: %w", addr, err)
	}
	p, err := newWithListener(ln, c.cfg, c.store)
	if err != nil {
		_ = ln.Close()
		return err
	}
	c.listener = ln
	c.server = p.server
	c.proxy = p
	// When the caller passed port 0 the OS picked a real port at bind time;
	// surface it so Address() / Port() stay accurate while running.
	if c.port == 0 {
		if tcp, ok := ln.Addr().(*net.TCPAddr); ok {
			c.port = tcp.Port
		}
	}
	go func() { _ = p.Serve() }()
	return nil
}

func (c *Controller) stopLocked() {
	if c.listener == nil {
		return
	}
	srv := c.server
	c.listener = nil
	c.server = nil
	c.proxy = nil
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}
