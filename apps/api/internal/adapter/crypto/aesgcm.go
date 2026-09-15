// Package crypto implements AES-256-GCM envelope encryption for credential
// material at rest (worker account passwords and proxy pool keys).
//
// Design (DEVELOPMENT_RULE §security, P1-07):
//   - 32-byte key (AES-256), supplied as base64 in CREDENTIAL_KEY.
//   - Random 12-byte nonce, PREPENDED to the ciphertext: [nonce||ciphertext||tag].
//   - Ciphertext is opaque bytea in Postgres and is never selected through the
//     API surface; decryption happens only inside the login/dispatch path.
//
// The Sealer interface keeps callers (services) free of crypto details and lets
// tests substitute a fake.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// NonceSize is the AES-GCM standard nonce length.
const NonceSize = 12

// KeySize is the AES-256 key length in bytes.
const KeySize = 32

var (
	// ErrKeySize is returned when a key is not exactly 32 bytes.
	ErrKeySize = errors.New("crypto: key must be 32 bytes (AES-256)")
	// ErrCiphertext is returned for malformed/too-short ciphertext.
	ErrCiphertext = errors.New("crypto: ciphertext too short")
)

// Sealer encrypts and decrypts credential blobs.
type Sealer interface {
	Seal(plaintext []byte) ([]byte, error)
	Open(ciphertext []byte) ([]byte, error)
}

// AESGCM is a Sealer backed by AES-256-GCM.
type AESGCM struct {
	aead cipher.AEAD
	rand io.Reader
}

// NewAESGCM builds a Sealer from a 32-byte key. Pass nil rand to use crypto/rand.
func NewAESGCM(key []byte) (*AESGCM, error) {
	if len(key) != KeySize {
		return nil, ErrKeySize
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: new gcm: %w", err)
	}
	return &AESGCM{aead: aead, rand: rand.Reader}, nil
}

// NewAESGCMFromBase64 decodes a base64 (std) key and builds a Sealer.
func NewAESGCMFromBase64(encoded string) (*AESGCM, error) {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("crypto: decode key: %w", err)
	}
	return NewAESGCM(key)
}

// Seal encrypts plaintext, returning [nonce||ciphertext||tag].
func (s *AESGCM) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(s.rand, nonce); err != nil {
		return nil, fmt.Errorf("crypto: nonce: %w", err)
	}
	// Prepend the nonce so the blob is self-describing.
	return s.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Open decrypts a blob produced by Seal.
func (s *AESGCM) Open(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < NonceSize+s.aead.Overhead() {
		return nil, ErrCiphertext
	}
	nonce, body := ciphertext[:NonceSize], ciphertext[NonceSize:]
	plaintext, err := s.aead.Open(nil, nonce, body, nil)
	if err != nil {
		return nil, fmt.Errorf("crypto: open: %w", err)
	}
	return plaintext, nil
}

// GenerateKey returns a fresh random 32-byte key (for bootstrapping CREDENTIAL_KEY).
func GenerateKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("crypto: generate key: %w", err)
	}
	return key, nil
}
