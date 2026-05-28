package config

import (
	"path/filepath"
	"testing"
)

func TestOpenMissingFileReturnsDefaults(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	got := s.Get()
	if got.UpstreamBaseURL != "" || got.UpstreamAPIKey != "" {
		t.Fatalf("expected zero credentials, got %+v", got)
	}
	if got.AuthPreset != PresetAnthropic {
		t.Fatalf("default AuthPreset should be anthropic, got %q", got.AuthPreset)
	}
	if got.ProxyEnabled {
		t.Fatalf("proxy should default to disabled (manual connect)")
	}
	if got.ProxyPort != DefaultProxyPort {
		t.Fatalf("proxy port should default to %d, got %d", DefaultProxyPort, got.ProxyPort)
	}
}

func TestSetPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "settings.json")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Set(Settings{
		UpstreamBaseURL: "https://api.openai.com/v1",
		UpstreamAPIKey:  "sk-secret",
		AuthPreset:      PresetOpenAI,
		ProxyEnabled:    false,
		Language:        LanguageZHCN,
		Theme:           ThemeLight,
		CustomHeaders: []HeaderKV{
			{Name: "X-Trace", Value: "abc"},
		},
	}); err != nil {
		t.Fatalf("set: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got := reopened.Get()
	if got.UpstreamBaseURL != "https://api.openai.com/v1" {
		t.Fatalf("base URL not persisted: %q", got.UpstreamBaseURL)
	}
	if got.UpstreamAPIKey != "sk-secret" {
		t.Fatalf("api key not persisted")
	}
	if got.AuthPreset != PresetOpenAI {
		t.Fatalf("preset not persisted: %q", got.AuthPreset)
	}
	if got.ProxyEnabled {
		t.Fatalf("proxyEnabled=false not persisted")
	}
	if got.Theme != ThemeLight {
		t.Fatalf("theme not persisted: %q", got.Theme)
	}
	if len(got.CustomHeaders) != 1 || got.CustomHeaders[0].Name != "X-Trace" {
		t.Fatalf("custom headers not persisted: %+v", got.CustomHeaders)
	}
}

func TestUnknownEnumsNormalized(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = s.Set(Settings{Language: "klingon", Theme: "neon", AuthPreset: "foo"})
	got := s.Get()
	if got.Language != "" {
		t.Fatalf("language should normalize: %q", got.Language)
	}
	if got.Theme != "" {
		t.Fatalf("theme should normalize: %q", got.Theme)
	}
	if got.AuthPreset != PresetAnthropic {
		t.Fatalf("preset should fall back to anthropic, got %q", got.AuthPreset)
	}
}
