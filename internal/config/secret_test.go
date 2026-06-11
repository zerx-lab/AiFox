package config

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

// fakeKeyring is an in-memory keyringProvider for tests. failGet/failSet make it
// simulate an unavailable OS credential store so the plaintext-degradation path
// can be exercised deterministically (the real keyring's availability depends on
// the host, which would make the test flaky).
type fakeKeyring struct {
	store   map[string]string
	failGet bool
	failSet bool
}

func newFakeKeyring() *fakeKeyring {
	return &fakeKeyring{store: map[string]string{}}
}

func (f *fakeKeyring) Get(service, user string) (string, error) {
	if f.failGet {
		return "", errors.New("fake keyring: get unavailable")
	}
	v, ok := f.store[service+"\x00"+user]
	if !ok {
		// Mirror go-keyring's sentinel so loadOrCreateKey treats this as
		// "not set yet" and generates a fresh key rather than degrading.
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func (f *fakeKeyring) Set(service, user, password string) error {
	if f.failSet {
		return errors.New("fake keyring: set unavailable")
	}
	f.store[service+"\x00"+user] = password
	return nil
}

func readRawKey(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings file: %v", err)
	}
	var on map[string]any
	if err := json.Unmarshal(raw, &on); err != nil {
		t.Fatalf("unmarshal settings file: %v", err)
	}
	v, _ := on["upstreamApiKey"].(string)
	return v
}

// TestEncryptRoundTrip: with a working keyring, the key is stored as ciphertext
// on disk and decrypts back to the original plaintext on reopen.
func TestEncryptRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	kr := newFakeKeyring()

	s, err := openWithKeyring(path, kr)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Set(Settings{UpstreamAPIKey: "sk-super-secret", AuthPreset: PresetOpenAI}); err != nil {
		t.Fatalf("set: %v", err)
	}

	onDisk := readRawKey(t, path)
	if !strings.HasPrefix(onDisk, encPrefix) {
		t.Fatalf("on-disk key should be ciphertext, got %q", onDisk)
	}
	if strings.Contains(onDisk, "sk-super-secret") {
		t.Fatalf("plaintext key leaked to disk: %q", onDisk)
	}

	reopened, err := openWithKeyring(path, kr)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := reopened.Get().UpstreamAPIKey; got != "sk-super-secret" {
		t.Fatalf("decrypted key = %q, want sk-super-secret", got)
	}
}

// TestEmptyKeyNotEnveloped: an empty API key stays empty on disk (no point
// sealing nothing), and round-trips as empty.
func TestEmptyKeyNotEnveloped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	kr := newFakeKeyring()

	s, err := openWithKeyring(path, kr)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Set(Settings{UpstreamAPIKey: ""}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if onDisk := readRawKey(t, path); onDisk != "" {
		t.Fatalf("empty key should stay empty on disk, got %q", onDisk)
	}
}

// TestKeyringUnavailableDegradesToPlaintext: when the keyring can't store a
// freshly generated key, Open does not fail; the key is written in plaintext.
func TestKeyringUnavailableDegradesToPlaintext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	kr := &fakeKeyring{store: map[string]string{}, failGet: true, failSet: true}

	s, err := openWithKeyring(path, kr)
	if err != nil {
		t.Fatalf("open must not fail when keyring is unavailable: %v", err)
	}
	if s.cipher.enabled() {
		t.Fatalf("cipher should be disabled when keyring is unavailable")
	}
	if err := s.Set(Settings{UpstreamAPIKey: "sk-plain"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if onDisk := readRawKey(t, path); onDisk != "sk-plain" {
		t.Fatalf("degraded mode should store plaintext, got %q", onDisk)
	}

	// Reopen with the same (still-unavailable) keyring: plaintext reads back.
	reopened, err := openWithKeyring(path, kr)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := reopened.Get().UpstreamAPIKey; got != "sk-plain" {
		t.Fatalf("plaintext key = %q, want sk-plain", got)
	}
}

// TestLegacyPlaintextUpgradesOnSave: a settings file written before encryption
// (plaintext key, no enc prefix) loads transparently and is re-sealed as
// ciphertext the first time it is saved again.
func TestLegacyPlaintextUpgradesOnSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// Hand-write a legacy plaintext settings file.
	legacy := `{"upstreamApiKey":"sk-legacy","authPreset":"anthropic","proxyPort":8787}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("seed legacy file: %v", err)
	}

	kr := newFakeKeyring()
	s, err := openWithKeyring(path, kr)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Loaded plaintext is visible verbatim.
	if got := s.Get().UpstreamAPIKey; got != "sk-legacy" {
		t.Fatalf("legacy key not loaded: %q", got)
	}
	// On-disk is still plaintext until we save.
	if readRawKey(t, path) != "sk-legacy" {
		t.Fatalf("expected disk to remain plaintext before save")
	}

	// Any save upgrades it to ciphertext.
	if err := s.Set(s.Get()); err != nil {
		t.Fatalf("set: %v", err)
	}
	if onDisk := readRawKey(t, path); !strings.HasPrefix(onDisk, encPrefix) {
		t.Fatalf("legacy key should be upgraded to ciphertext, got %q", onDisk)
	}

	reopened, err := openWithKeyring(path, kr)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := reopened.Get().UpstreamAPIKey; got != "sk-legacy" {
		t.Fatalf("upgraded key = %q, want sk-legacy", got)
	}
}

// TestCorruptCiphertextClearsKeyOnly: a prefixed-but-undecryptable key drops
// only the apiKey field; the rest of the settings survive (no whole-file loss).
func TestCorruptCiphertextClearsKeyOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	kr := newFakeKeyring()

	// First write a valid, encrypted settings file.
	s, err := openWithKeyring(path, kr)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Set(Settings{
		UpstreamAPIKey:  "sk-victim",
		UpstreamBaseURL: "https://api.openai.com/v1",
		AuthPreset:      PresetOpenAI,
		Theme:           ThemeDark,
	}); err != nil {
		t.Fatalf("set: %v", err)
	}

	// Corrupt the ciphertext: keep the prefix, mangle the payload so AES-GCM
	// authentication fails on Open.
	corrupt := encPrefix + base64.StdEncoding.EncodeToString([]byte("not a real sealed envelope"))
	raw, _ := os.ReadFile(path)
	var on map[string]any
	if err := json.Unmarshal(raw, &on); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	on["upstreamApiKey"] = corrupt
	out, _ := json.Marshal(on)
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	reopened, err := openWithKeyring(path, kr)
	if err != nil {
		t.Fatalf("open with corrupt key must not fail: %v", err)
	}
	got := reopened.Get()
	if got.UpstreamAPIKey != "" {
		t.Fatalf("corrupt key should be cleared, got %q", got.UpstreamAPIKey)
	}
	// The rest of the settings must survive.
	if got.UpstreamBaseURL != "https://api.openai.com/v1" {
		t.Fatalf("base URL should survive corrupt key: %q", got.UpstreamBaseURL)
	}
	if got.AuthPreset != PresetOpenAI {
		t.Fatalf("preset should survive corrupt key: %q", got.AuthPreset)
	}
	if got.Theme != ThemeDark {
		t.Fatalf("theme should survive corrupt key: %q", got.Theme)
	}
}

// TestEncryptedFieldButNoKeyringClearsKey: a file holding ciphertext opened on a
// box with no keyring can't decrypt; the key is cleared, not exploded.
func TestEncryptedFieldButNoKeyringClearsKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// Encrypt with a working keyring first.
	good := newFakeKeyring()
	s, err := openWithKeyring(path, good)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Set(Settings{UpstreamAPIKey: "sk-locked", UpstreamBaseURL: "https://x"}); err != nil {
		t.Fatalf("set: %v", err)
	}

	// Reopen with an unavailable keyring: decryption impossible.
	dead := &fakeKeyring{store: map[string]string{}, failGet: true, failSet: true}
	reopened, err := openWithKeyring(path, dead)
	if err != nil {
		t.Fatalf("open must not fail: %v", err)
	}
	if got := reopened.Get().UpstreamAPIKey; got != "" {
		t.Fatalf("undecryptable key should be cleared, got %q", got)
	}
	if got := reopened.Get().UpstreamBaseURL; got != "https://x" {
		t.Fatalf("base URL should survive: %q", got)
	}
}
