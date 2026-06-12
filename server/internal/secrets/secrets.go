// Package secrets provides AES-256-GCM encryption-at-rest for credential
// values stored in SQLite. Encrypted values carry an "enc:v1:" prefix;
// values without it are treated as legacy plaintext and pass through reads
// unchanged, so existing databases keep working and migrate in place.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

const (
	prefix = "enc:v1:"
	// EnvKey holds a base64-encoded 32-byte key; when unset a key file is
	// generated next to the database instead.
	EnvKey  = "CANTINARR_ENCRYPTION_KEY"
	keySize = 32
)

// Cipher encrypts and decrypts stored secret values.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher creates a Cipher from a 32-byte key.
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("encryption key must be %d bytes, got %d", keySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// LoadKey resolves the encryption key: the CANTINARR_ENCRYPTION_KEY env var
// (base64, 32 bytes) wins; otherwise a key is generated once and persisted to
// keyFile with 0600 permissions.
func LoadKey(keyFile string) ([]byte, error) {
	if env := os.Getenv(EnvKey); env != "" {
		key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(env))
		if err != nil {
			return nil, fmt.Errorf("%s is not valid base64: %w", EnvKey, err)
		}
		if len(key) != keySize {
			return nil, fmt.Errorf("%s must decode to %d bytes, got %d", EnvKey, keySize, len(key))
		}
		return key, nil
	}

	data, err := os.ReadFile(keyFile)
	if err == nil {
		key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
		if err != nil {
			return nil, fmt.Errorf("key file %s is corrupt: %w", keyFile, err)
		}
		if len(key) != keySize {
			return nil, fmt.Errorf("key file %s must hold %d bytes, got %d", keyFile, keySize, len(key))
		}
		return key, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		// A key file that exists but can't be read must never be silently
		// replaced — regenerating here would permanently orphan every
		// secret encrypted with the old key.
		return nil, fmt.Errorf("read key file %s: %w", keyFile, err)
	}

	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(key) + "\n"
	// O_EXCL: if a concurrent process won the race, defer to its key.
	f, err := os.OpenFile(keyFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return LoadKey(keyFile)
	}
	if err != nil {
		return nil, fmt.Errorf("create key file: %w", err)
	}
	if _, err := f.WriteString(encoded); err != nil {
		f.Close()
		return nil, fmt.Errorf("persist key file: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return nil, fmt.Errorf("sync key file: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close key file: %w", err)
	}
	return key, nil
}

// Encrypt seals plain and returns the prefixed, base64-encoded ciphertext.
// Empty strings stay empty so optional secrets round-trip cleanly.
func (c *Cipher) Encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plain), nil)
	return prefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt opens a stored value. Values without the encryption prefix are
// legacy plaintext and are returned unchanged; prefixed values that fail
// authentication return an error rather than leaking ciphertext to callers.
//
// Known edge: a legacy plaintext secret whose literal value happens to start
// with "enc:v1:" would be misclassified as ciphertext and fail to decrypt.
// Real API keys never look like that; secrets saved after this feature
// shipped are encrypted (and decryption restores the literal), so the
// ambiguity only exists for pre-migration rows.
func (c *Cipher) Decrypt(stored string) (string, error) {
	if !IsEncrypted(stored) {
		return stored, nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, prefix))
	if err != nil {
		return "", fmt.Errorf("decode secret: %w", err)
	}
	if len(raw) < c.aead.NonceSize() {
		return "", fmt.Errorf("secret too short")
	}
	nonce, ct := raw[:c.aead.NonceSize()], raw[c.aead.NonceSize():]
	plain, err := c.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt secret: %w", err)
	}
	return string(plain), nil
}

// IsEncrypted reports whether a stored value carries the encryption prefix.
func IsEncrypted(s string) bool {
	return strings.HasPrefix(s, prefix)
}
