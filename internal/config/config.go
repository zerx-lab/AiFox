// Package config holds user-editable settings (upstream provider info, UI
// language, theme, proxy enable flag, auth header preset) and persists them
// to a JSON file under the OS user-config dir.
//
// TODO: The current implementation stores the API key in plaintext JSON.
// Before shipping a release, swap the persistence layer for the OS keyring
// (Keychain / DPAPI / libsecret). This is called out explicitly so a code
// review catches a release attempt that still leaks the key.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// Language enumerates the UI locales we ship with. Adding a new locale here
// requires a matching dictionary in electron/src/renderer/i18n/locales.
type Language string

const (
	LanguageEN   Language = "en"
	LanguageZHCN Language = "zh-CN"
)

// Theme is the renderer color scheme. Empty means "follow the OS".
type Theme string

const (
	ThemeSystem Theme = ""
	ThemeDark   Theme = "dark"
	ThemeLight  Theme = "light"
)

// AuthPreset picks the upstream auth header shape. Built-ins cover the
// providers we ship with; "custom" hands control over to CustomHeaders.
type AuthPreset string

const (
	PresetAnthropic       AuthPreset = "anthropic"
	PresetOpenAI          AuthPreset = "openai"
	PresetOpenAIResponses AuthPreset = "openai-responses"
	PresetCustom          AuthPreset = "custom"
)

// HeaderKV is one user-defined header injected on every forwarded request.
// Used when AuthPreset == PresetCustom.
type HeaderKV struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Settings is the persisted shape of user configuration. Field names follow
// JSON convention so the file is human-editable in a pinch.
type Settings struct {
	// UpstreamBaseURL is the AI provider endpoint the proxy forwards to,
	// for example "https://api.anthropic.com" or "https://api.openai.com".
	UpstreamBaseURL string `json:"upstreamBaseUrl"`
	// UpstreamAPIKey is the secret injected into the headers a preset
	// requires. Ignored when AuthPreset == PresetCustom (the custom header
	// list supplies its own values).
	UpstreamAPIKey string `json:"upstreamApiKey"`
	// AuthPreset picks how the key is forwarded. See AuthPreset constants.
	AuthPreset AuthPreset `json:"authPreset"`
	// CustomHeaders is the list of headers injected on every forwarded
	// request when AuthPreset == PresetCustom. Each one overwrites whatever
	// the client supplied with the same name.
	CustomHeaders []HeaderKV `json:"customHeaders"`
	// ProxyEnabled toggles the reverse proxy on/off without restarting the
	// app. Defaults to true.
	ProxyEnabled bool `json:"proxyEnabled"`
	// Language is the active UI locale. Empty means "follow OS".
	Language Language `json:"language"`
	// Theme is the active color scheme. Empty means "follow OS".
	Theme Theme `json:"theme"`
}

// Store wraps a Settings value with mutex protection and on-disk persistence.
type Store struct {
	mu       sync.RWMutex
	settings Settings
	path     string
}

// Open loads settings from `path` (creating the parent dir if missing). A
// non-existent file produces an empty Settings, not an error.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("config path is required")
	}
	s := &Store{path: path}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// DefaultPath returns the OS-conventional config file location for ai-fox.
// It does NOT create the file, only computes the path. Falls back to the
// current directory on platforms where os.UserConfigDir fails.
func DefaultPath() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir = "."
	}
	return filepath.Join(dir, "ai-fox", "settings.json")
}

// Get returns a snapshot of the current settings. The returned value is a
// copy; mutating it does not affect the store.
func (s *Store) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

// Set replaces the entire settings value and writes it to disk. Any disk
// error is returned, but the in-memory value is updated regardless so the
// running app stays usable.
func (s *Store) Set(next Settings) error {
	s.mu.Lock()
	s.settings = normalize(next)
	snapshot := s.settings
	path := s.path
	s.mu.Unlock()
	return write(path, snapshot)
}

func (s *Store) load() error {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.settings = defaults()
		return nil
	}
	if err != nil {
		return err
	}
	var loaded Settings
	if err := json.Unmarshal(raw, &loaded); err != nil {
		// Don't fail boot on a corrupt config file — empty it in memory and
		// let the user fix or overwrite it via the settings dialog.
		s.settings = defaults()
		return nil
	}
	// json.Unmarshal leaves bool fields false. We can't distinguish "user
	// disabled the proxy" from "field absent" in stored JSON, but the file
	// is always written by Set() which normalizes, so on-disk JSON always
	// has the field present. A missing field can only happen on a hand-
	// edited or pre-existing file — treat that as "default enabled".
	if len(raw) > 0 && !hasProxyEnabledKey(raw) {
		loaded.ProxyEnabled = true
	}
	s.settings = normalize(loaded)
	return nil
}

func defaults() Settings {
	return Settings{
		AuthPreset:   PresetAnthropic,
		ProxyEnabled: true,
	}
}

func normalize(s Settings) Settings {
	switch s.Language {
	case LanguageEN, LanguageZHCN:
	default:
		s.Language = ""
	}
	switch s.Theme {
	case ThemeDark, ThemeLight:
	default:
		s.Theme = ""
	}
	switch s.AuthPreset {
	case PresetAnthropic, PresetOpenAI, PresetOpenAIResponses, PresetCustom:
	default:
		s.AuthPreset = PresetAnthropic
	}
	if s.CustomHeaders == nil {
		s.CustomHeaders = []HeaderKV{}
	}
	return s
}

func hasProxyEnabledKey(raw []byte) bool {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	_, ok := probe["proxyEnabled"]
	return ok
}

func write(path string, s Settings) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	// 0600: only the current user should be able to read the API key.
	return os.WriteFile(path, raw, 0o600)
}
