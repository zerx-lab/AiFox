package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zerx-lab/ai-fox/internal/config"
	"github.com/zerx-lab/ai-fox/internal/store"
)

func TestApplyOverridesToBody(t *testing.T) {
	model := "claude-new"
	temp := 0.5
	maxTok := 256

	t.Run("valid JSON gets patched", func(t *testing.T) {
		out, err := applyOverridesToBody(`{"model":"old","messages":[]}`, ReplayOverrides{
			Model:     &model,
			MaxTokens: &maxTok,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("output not valid JSON: %v", err)
		}
		if got["model"] != "claude-new" {
			t.Fatalf("model not overridden: %v", got["model"])
		}
		if got["max_tokens"].(float64) != 256 {
			t.Fatalf("max_tokens not overridden: %v", got["max_tokens"])
		}
		if _, ok := got["messages"]; !ok {
			t.Fatalf("existing keys must be preserved")
		}
	})

	t.Run("empty body synthesizes object", func(t *testing.T) {
		out, err := applyOverridesToBody("", ReplayOverrides{Temperature: &temp})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("output not valid JSON: %v", err)
		}
		if got["temperature"].(float64) != 0.5 {
			t.Fatalf("temperature not set on synthesized body: %v", got)
		}
	})

	t.Run("invalid JSON errors", func(t *testing.T) {
		if _, err := applyOverridesToBody(`not json`, ReplayOverrides{Model: &model}); err == nil {
			t.Fatalf("expected error for invalid JSON body")
		}
	})
}

// TestReplayHeaderFiltering verifies the shared forward-header path on the
// replay route: the captured auth header is replaced by the current preset, and
// a Connection-listed hop-by-hop header is stripped.
func TestReplayHeaderFiltering(t *testing.T) {
	type captured struct {
		apiKey     string
		xFoo       string
		connection string
		body       string
	}
	got := make(chan captured, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- captured{
			apiKey:     r.Header.Get("x-api-key"),
			xFoo:       r.Header.Get("X-Foo"),
			connection: r.Header.Get("Connection"),
			body:       string(b),
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	cfg := newConfig(t, config.Settings{
		UpstreamBaseURL: upstream.URL,
		UpstreamAPIKey:  "sk-current",
		AuthPreset:      config.PresetAnthropic,
	})
	st := store.New(4)
	ctrl, err := NewController(0, cfg, st)
	if err != nil {
		t.Fatalf("controller new: %v", err)
	}
	t.Cleanup(ctrl.Close)
	if err := ctrl.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Seed an original entry with a stale captured key and a hop-by-hop header
	// named by Connection.
	original := &store.Entry{
		ID:        st.NextID(),
		StartedAt: time.Now(),
		Method:    "POST",
		URL:       "/v1/messages",
		RequestHeaders: map[string]string{
			"X-Api-Key":  "sk-STALE",
			"X-Foo":      "drop-me",
			"Connection": "X-Foo",
			"X-Keep":     "kept",
		},
		RequestBody: `{"model":"m","messages":[]}`,
	}
	st.Add(original)

	if _, err := ctrl.Replay(context.Background(), original.ID, ReplayOverrides{}); err != nil {
		t.Fatalf("replay: %v", err)
	}

	select {
	case c := <-got:
		if c.apiKey != "sk-current" {
			t.Fatalf("replay must inject current preset key, got %q", c.apiKey)
		}
		if c.xFoo != "" {
			t.Fatalf("Connection-listed hop-by-hop header X-Foo must be stripped, got %q", c.xFoo)
		}
		if c.connection != "" {
			t.Fatalf("Connection header itself must be stripped, got %q", c.connection)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("upstream never received the replay")
	}
}
