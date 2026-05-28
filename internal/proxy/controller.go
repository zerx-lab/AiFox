package proxy

import (
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"

	"github.com/zerx-lab/ai-fox/internal/config"
	"github.com/zerx-lab/ai-fox/internal/store"
)

// Controller wraps a Proxy with a runtime on/off switch. The listener stays
// bound either way so the address the user configured in their AI client
// remains valid even while the proxy is "stopped" — disabled requests just
// get a 503 instead of being forwarded.
//
// This avoids fighting the OS over port reuse on rapid stop/start cycles.
type Controller struct {
	proxy   *Proxy
	enabled atomic.Bool
}

// NewController binds the proxy listener on `port` (0 = OS picks). The
// returned controller is not yet enabled — call Start to begin forwarding.
func NewController(port int, cfg *config.Store, st *store.Store) (*Controller, error) {
	p, err := New(port, cfg, st)
	if err != nil {
		return nil, err
	}
	c := &Controller{proxy: p}
	// Wrap the proxy handler with the on/off gate.
	p.server.Handler = http.HandlerFunc(c.handle)
	go func() { _ = p.Serve() }()
	return c, nil
}

// Start enables forwarding. Subsequent requests are proxied upstream.
func (c *Controller) Start() error {
	c.enabled.Store(true)
	return nil
}

// Stop disables forwarding. The listener stays bound; new requests respond
// with 503.
func (c *Controller) Stop() {
	c.enabled.Store(false)
}

// Close shuts the listener down for good. Used on app exit.
func (c *Controller) Close() {
	c.proxy.Close()
}

// Enabled reports whether the proxy is currently forwarding.
func (c *Controller) Enabled() bool {
	return c.enabled.Load()
}

// Address returns the bound host:port. Always available, even while stopped.
func (c *Controller) Address() string {
	return c.proxy.Addr().String()
}

// Port returns the bound TCP port.
func (c *Controller) Port() int {
	return c.proxy.Addr().Port
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

// handle is the actual listener handler. When disabled, every request short-
// circuits with 503 + a friendly message so the AI client knows the proxy
// was stopped intentionally.
func (c *Controller) handle(w http.ResponseWriter, r *http.Request) {
	if !c.enabled.Load() {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprintln(w, "ai-fox: proxy is paused. Toggle it on in the app to resume forwarding.")
		return
	}
	c.proxy.ServeHTTP(w, r)
}

// ErrNotRunning is returned by callers that ask for live-proxy details while
// the proxy is stopped.
var ErrNotRunning = errors.New("proxy is not running")
