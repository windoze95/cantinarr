package secrets

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	projectdb "github.com/windoze95/cantinarr-server/internal/db"
)

// SEC-002: authenticated canary failures must refuse the key without rewriting the stored bytes.
func TestVerifyKeyIdentityRejectsTamperAndWrongKeyWithoutMutation(t *testing.T) {
	tests := []struct {
		name        string
		storedValue func(t *testing.T, encrypted []byte, cipher *Cipher) []byte
		verifyKey   byte
	}{
		{
			name: "tampered ciphertext",
			storedValue: func(t *testing.T, encrypted []byte, cipher *Cipher) []byte {
				t.Helper()
				encoded := strings.TrimPrefix(string(encrypted), prefix)
				raw, err := base64.StdEncoding.DecodeString(encoded)
				if err != nil {
					t.Fatalf("decode canary: %v", err)
				}
				raw[cipher.aead.NonceSize()] ^= 0x01
				return []byte(prefix + base64.StdEncoding.EncodeToString(raw))
			},
			verifyKey: 0x42,
		},
		{
			name: "tampered authentication tag",
			storedValue: func(t *testing.T, encrypted []byte, _ *Cipher) []byte {
				t.Helper()
				encoded := strings.TrimPrefix(string(encrypted), prefix)
				raw, err := base64.StdEncoding.DecodeString(encoded)
				if err != nil {
					t.Fatalf("decode canary: %v", err)
				}
				raw[len(raw)-1] ^= 0x01
				return []byte(prefix + base64.StdEncoding.EncodeToString(raw))
			},
			verifyKey: 0x42,
		},
		{
			name: "wrong key",
			storedValue: func(_ *testing.T, encrypted []byte, _ *Cipher) []byte {
				return encrypted
			},
			verifyKey: 0x07,
		},
		{
			name: "authenticated wrong canary value",
			storedValue: func(t *testing.T, _ []byte, cipher *Cipher) []byte {
				t.Helper()
				encrypted, err := cipher.Encrypt("synthetic-wrong-canary")
				if err != nil {
					t.Fatalf("encrypt wrong canary: %v", err)
				}
				return []byte(encrypted)
			},
			verifyKey: 0x42,
		},
		{
			name: "plaintext canary replacement",
			storedValue: func(_ *testing.T, _ []byte, _ *Cipher) []byte {
				return []byte(canaryValue)
			},
			verifyKey: 0x42,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database, err := projectdb.Open(":memory:")
			if err != nil {
				t.Fatalf("open database: %v", err)
			}
			t.Cleanup(func() { _ = database.Close() })

			initialCipher, err := NewCipher(bytes.Repeat([]byte{0x42}, 32))
			if err != nil {
				t.Fatalf("create initial cipher: %v", err)
			}
			if err := VerifyKeyIdentity(database, initialCipher); err != nil {
				t.Fatalf("seed encrypted canary: %v", err)
			}
			if err := VerifyKeyIdentity(database, initialCipher); err != nil {
				t.Fatalf("reverify valid encrypted canary: %v", err)
			}

			var encrypted []byte
			if err := database.QueryRow(
				"SELECT CAST(value AS BLOB) FROM settings WHERE key = ?", canaryKey,
			).Scan(&encrypted); err != nil {
				t.Fatalf("read encrypted canary: %v", err)
			}
			before := tt.storedValue(t, append([]byte(nil), encrypted...), initialCipher)
			if _, err := database.Exec(
				"UPDATE settings SET value = ? WHERE key = ?", string(before), canaryKey,
			); err != nil {
				t.Fatalf("replace canary: %v", err)
			}

			verificationCipher, err := NewCipher(bytes.Repeat([]byte{tt.verifyKey}, 32))
			if err != nil {
				t.Fatalf("create verification cipher: %v", err)
			}
			if err := VerifyKeyIdentity(database, verificationCipher); err == nil {
				t.Fatal("VerifyKeyIdentity accepted an unauthenticated canary")
			}

			var after []byte
			if err := database.QueryRow(
				"SELECT CAST(value AS BLOB) FROM settings WHERE key = ?", canaryKey,
			).Scan(&after); err != nil {
				t.Fatalf("read rejected canary: %v", err)
			}
			if !bytes.Equal(after, before) {
				t.Fatalf("rejected canary was mutated: before=%q after=%q", before, after)
			}
		})
	}
}
