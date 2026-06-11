// Secret-at-rest handling for the config store.
//
// The settings file lives under the user's config dir at 0600, but the API key
// it carries is still plaintext on disk — a release blocker (see §3.3 of the
// plan). This file encrypts ONLY the apiKey field before it is written and
// decrypts it on load, leaving the rest of the JSON human-editable.
//
// Scheme: AES-256-GCM. The 32-byte key lives in the OS credential store (macOS
// Keychain / Windows Credential Manager / Linux Secret Service via D-Bus),
// generated on first use. On disk the field is "enc:v1:<base64(nonce|cipher)>".
// A value without that prefix is treated as legacy plaintext and transparently
// re-encrypted on the next save — so old settings files keep working and silently
// upgrade.
//
// Graceful degradation: when the OS keyring is unavailable (a headless Linux box
// with no D-Bus, CI), we fall back to storing the apiKey in plaintext and log one
// warning to stderr. The app never crashes or blocks over key storage; the
// loopback+token transport is the primary protection, disk encryption is
// defense-in-depth.
package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/zalando/go-keyring"
)

const (
	// keyringService namespaces our secrets in the OS credential store.
	keyringService = "ai-fox"
	// keyringKeyUser is the credential entry holding the AES master key.
	keyringKeyUser = "settings-encryption-key"
	// encPrefix marks a ciphertext field on disk. v1 == AES-256-GCM, base64 of
	// nonce||ciphertext after the prefix.
	encPrefix = "enc:v1:"
	// aesKeyLen is the AES-256 key length in bytes.
	aesKeyLen = 32
)

// keyringProvider abstracts the OS credential store so tests can inject a fake
// (and so an unavailable real keyring degrades instead of crashing). The shape
// mirrors the subset of github.com/zalando/go-keyring we use.
type keyringProvider interface {
	Get(service, user string) (string, error)
	Set(service, user, password string) error
}

// osKeyring is the production keyringProvider backed by go-keyring.
type osKeyring struct{}

func (osKeyring) Get(service, user string) (string, error) {
	return keyring.Get(service, user)
}

func (osKeyring) Set(service, user, password string) error {
	return keyring.Set(service, user, password)
}

// errKeyringUnavailable means we could neither read an existing key nor store a
// freshly generated one. Callers degrade to plaintext.
var errKeyringUnavailable = errors.New("config: OS keyring unavailable")

// secretCipher encrypts/decrypts the apiKey field. A nil/disabled cipher (no
// usable keyring) passes values through unchanged so the rest of config works.
type secretCipher struct {
	aead cipher.AEAD // nil when encryption is disabled (keyring unavailable)
}

// newSecretCipher loads (or creates) the master key from kr and returns a cipher
// ready to encrypt. When kr is unavailable it returns a disabled cipher and a
// non-nil error so the caller can log the degradation once.
func newSecretCipher(kr keyringProvider) (*secretCipher, error) {
	key, err := loadOrCreateKey(kr)
	if err != nil {
		return &secretCipher{}, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return &secretCipher{}, fmt.Errorf("config: new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return &secretCipher{}, fmt.Errorf("config: new gcm: %w", err)
	}
	return &secretCipher{aead: aead}, nil
}

// enabled reports whether the cipher can actually encrypt (keyring was usable).
func (c *secretCipher) enabled() bool { return c != nil && c.aead != nil }

// encrypt returns the "enc:v1:..." form of plain. An empty input stays empty (no
// point storing an envelope for no secret), and a disabled cipher returns plain
// unchanged so the field falls back to plaintext on disk.
func (c *secretCipher) encrypt(plain string) (string, error) {
	if plain == "" || !c.enabled() {
		return plain, nil
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("config: nonce: %w", err)
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plain), nil)
	return encPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// decrypt reverses encrypt. A value without the enc prefix is legacy plaintext
// and returned verbatim (backward compatibility). A prefixed-but-corrupt value
// returns an error so the caller can drop just the apiKey rather than the whole
// settings file.
func (c *secretCipher) decrypt(stored string) (string, error) {
	if !strings.HasPrefix(stored, encPrefix) {
		return stored, nil // legacy plaintext or empty
	}
	if !c.enabled() {
		return "", errors.New("config: field is encrypted but no keyring key is available")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, encPrefix))
	if err != nil {
		return "", fmt.Errorf("config: decode secret: %w", err)
	}
	ns := c.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("config: secret ciphertext too short")
	}
	nonce, ct := raw[:ns], raw[ns:]
	plain, err := c.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("config: decrypt secret: %w", err)
	}
	return string(plain), nil
}

// loadOrCreateKey fetches the 32-byte master key from the keyring, generating
// and storing a fresh one on first run. Any keyring failure surfaces as
// errKeyringUnavailable so the caller degrades to plaintext.
func loadOrCreateKey(kr keyringProvider) ([]byte, error) {
	if kr == nil {
		return nil, errKeyringUnavailable
	}
	stored, err := kr.Get(keyringService, keyringKeyUser)
	if err == nil && stored != "" {
		key, derr := base64.StdEncoding.DecodeString(stored)
		if derr == nil && len(key) == aesKeyLen {
			return key, nil
		}
		// A malformed stored key is unusable; fall through to regenerate it so a
		// corrupted entry self-heals instead of permanently breaking encryption.
	}
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		// Get failed for a reason other than "not set yet" (no D-Bus, locked
		// keychain, unsupported platform): treat as unavailable.
		return nil, fmt.Errorf("%w: %v", errKeyringUnavailable, err)
	}
	key := make([]byte, aesKeyLen)
	if _, gerr := io.ReadFull(rand.Reader, key); gerr != nil {
		return nil, fmt.Errorf("config: generate key: %w", gerr)
	}
	if serr := kr.Set(keyringService, keyringKeyUser, base64.StdEncoding.EncodeToString(key)); serr != nil {
		return nil, fmt.Errorf("%w: %v", errKeyringUnavailable, serr)
	}
	return key, nil
}

// warnDegraded logs the one-time plaintext-fallback warning to stderr. Kept in a
// function so the message is consistent across the load path and tests can rely
// on the behavior (no crash) rather than the exact text.
func warnDegraded(reason error) {
	log.Printf("ai-fox: API key encryption disabled, storing settings key in plaintext (%v)", reason)
}
