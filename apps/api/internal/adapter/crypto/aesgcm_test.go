package crypto

import (
	"bytes"
	"testing"
)

func mustSealer(t *testing.T, key []byte) *AESGCM {
	t.Helper()
	s, err := NewAESGCM(key)
	if err != nil {
		t.Fatalf("NewAESGCM: %v", err)
	}
	return s
}

func fixedKey() []byte {
	k := make([]byte, KeySize)
	for i := range k {
		k[i] = byte(i + 1)
	}
	return k
}

func TestSealOpenRoundTrip(t *testing.T) {
	s := mustSealer(t, fixedKey())
	plaintext := []byte("s3cr3t-p4ssw0rd-\x00\xff-binary")

	blob, err := s.Seal(plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains(blob, plaintext) {
		t.Fatal("ciphertext must not contain the plaintext")
	}
	if len(blob) != NonceSize+len(plaintext)+s.aead.Overhead() {
		t.Fatalf("unexpected blob length %d", len(blob))
	}

	got, err := s.Open(blob)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round trip mismatch: %q != %q", got, plaintext)
	}
}

func TestSealIsNonDeterministic(t *testing.T) {
	s := mustSealer(t, fixedKey())
	a, _ := s.Seal([]byte("same"))
	b, _ := s.Seal([]byte("same"))
	if bytes.Equal(a, b) {
		t.Fatal("two seals of the same plaintext must differ (random nonce)")
	}
}

func TestOpenRejectsTamperedCiphertext(t *testing.T) {
	s := mustSealer(t, fixedKey())
	blob, _ := s.Seal([]byte("data"))
	blob[len(blob)-1] ^= 0x01
	if _, err := s.Open(blob); err == nil {
		t.Fatal("expected auth failure on tampered ciphertext")
	}
}

func TestOpenRejectsShortCiphertext(t *testing.T) {
	s := mustSealer(t, fixedKey())
	if _, err := s.Open([]byte("short")); err != ErrCiphertext {
		t.Fatalf("want ErrCiphertext, got %v", err)
	}
}

func TestWrongKeyFails(t *testing.T) {
	s1 := mustSealer(t, fixedKey())
	k2 := fixedKey()
	k2[0] ^= 0xff
	s2 := mustSealer(t, k2)
	blob, _ := s1.Seal([]byte("data"))
	if _, err := s2.Open(blob); err == nil {
		t.Fatal("decryption with a different key must fail")
	}
}

func TestKeySizeValidation(t *testing.T) {
	if _, err := NewAESGCM(make([]byte, 16)); err != ErrKeySize {
		t.Fatalf("want ErrKeySize, got %v", err)
	}
	if _, err := NewAESGCMFromBase64("not-base64!!"); err == nil {
		t.Fatal("expected base64 decode error")
	}
}

func TestGenerateKey(t *testing.T) {
	k, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if len(k) != KeySize {
		t.Fatalf("want %d bytes, got %d", KeySize, len(k))
	}
	if _, err := NewAESGCM(k); err != nil {
		t.Fatalf("generated key must be usable: %v", err)
	}
}
