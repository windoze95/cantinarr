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
	"fmt"
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

	if data, err := os.ReadFile(keyFile); err == nil {
		key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
		if err != nil {
			return nil, fmt.Errorf("key file %s is corrupt: %w", keyFile, err)
		}
		if len(key) != keySize {
			return nil, fmt.Errorf("key file %s must hold %d bytes, got %d", keyFile, keySize, len(key))
		}
		return key, nil
	}

	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(key) + "\n"
	if err := os.WriteFile(keyFile, []byte(encoded), 0o600); err != nil {
		return nil, fmt.Errorf("persist key file: %w", err)
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
