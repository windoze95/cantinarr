package secrets

import (
	"bytes"
	"strings"
	"testing"
)

func testCipher(t *testing.T) *Cipher {
	t.Helper()
	c, err := NewCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return c
}

func TestRoundTrip(t *testing.T) {
	c := testCipher(t)
	for _, plain := range []string{"hunter2", "a much longer secret value with spaces", "ключ"} {
		enc, err := c.Encrypt(plain)
		if err != nil {
			t.Fatalf("Encrypt(%q): %v", plain, err)
		}
		if !IsEncrypted(enc) {
			t.Fatalf("Encrypt(%q) missing prefix: %q", plain, enc)
		}
		got, err := c.Decrypt(enc)
		if err != nil {
			t.Fatalf("Decrypt: %v", err)
		}
		if got != plain {
			t.Fatalf("round trip: got %q want %q", got, plain)
		}
	}
}

func TestEmptyStaysEmpty(t *testing.T) {
	c := testCipher(t)
	enc, err := c.Encrypt("")
	if err != nil || enc != "" {
		t.Fatalf("Encrypt(\"\") = %q, %v; want empty, nil", enc, err)
	}
}

func TestPlaintextPassthrough(t *testing.T) {
	c := testCipher(t)
	got, err := c.Decrypt("legacy-plaintext-api-key")
	if err != nil {
		t.Fatalf("Decrypt plaintext: %v", err)
	}
	if got != "legacy-plaintext-api-key" {
		t.Fatalf("plaintext passthrough mangled: %q", got)
	}
}

func TestTamperDetection(t *testing.T) {
	c := testCipher(t)
	enc, _ := c.Encrypt("secret")
	tampered := enc[:len(enc)-2] + "AA"
	if tampered == enc {
		tampered = enc[:len(enc)-2] + "BB"
	}
	if _, err := c.Decrypt(tampered); err == nil {
		t.Fatal("tampered ciphertext decrypted without error")
	}
}

func TestWrongKeyFails(t *testing.T) {
	c := testCipher(t)
	other, _ := NewCipher(bytes.Repeat([]byte{0x07}, 32))
	enc, _ := c.Encrypt("secret")
	if _, err := other.Decrypt(enc); err == nil {
		t.Fatal("decrypt with wrong key succeeded")
	}
}

func TestIsEncrypted(t *testing.T) {
	if IsEncrypted("plain") || !IsEncrypted("enc:v1:abc") {
		t.Fatal("IsEncrypted misclassifies")
	}
}

func TestNonceUniqueness(t *testing.T) {
	c := testCipher(t)
	a, _ := c.Encrypt("same")
	b, _ := c.Encrypt("same")
	if a == b {
		t.Fatal("two encryptions of the same value produced identical ciphertext")
	}
	if !strings.HasPrefix(a, "enc:v1:") {
		t.Fatalf("unexpected format: %q", a)
	}
}
