package service

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2Params holds the cost parameters baked into each hash. Defaults follow
// OWASP's recommended minimum for argon2id.
type argon2Params struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
	saltLength  uint32
	keyLength   uint32
}

var defaultArgon2 = argon2Params{
	memory:      64 * 1024, // 64 MiB
	iterations:  1,
	parallelism: 4,
	saltLength:  16,
	keyLength:   32,
}

// HashPassword returns an encoded argon2id hash in the standard PHC string
// format: $argon2id$v=19$m=...,t=...,p=...$<salt>$<hash>. Exported so user
// creation (invites) and tests can produce stored hashes.
func HashPassword(password string) (string, error) {
	return hashPassword(password)
}

// hashPassword returns an encoded argon2id hash in the standard PHC string
// format: $argon2id$v=19$m=...,t=...,p=...$<salt>$<hash>.
func hashPassword(password string) (string, error) {
	p := defaultArgon2
	salt := make([]byte, p.saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("service.auth: read salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.iterations, p.memory, p.parallelism, p.keyLength)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.memory, p.iterations, p.parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// verifyPassword compares a plaintext password against an encoded argon2id hash
// in constant time. Returns false (not an error) on a malformed hash.
func verifyPassword(password, encoded string) bool {
	p, salt, key, err := decodeHash(encoded)
	if err != nil {
		return false
	}
	comparison := argon2.IDKey([]byte(password), salt, p.iterations, p.memory, p.parallelism, p.keyLength)
	return subtle.ConstantTimeCompare(key, comparison) == 1
}

func decodeHash(encoded string) (argon2Params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return argon2Params{}, nil, nil, errors.New("invalid hash format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return argon2Params{}, nil, nil, fmt.Errorf("invalid hash version: %w", err)
	}
	if version != argon2.Version {
		return argon2Params{}, nil, nil, errors.New("incompatible argon2 version")
	}
	var p argon2Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.iterations, &p.parallelism); err != nil {
		return argon2Params{}, nil, nil, fmt.Errorf("invalid hash params: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return argon2Params{}, nil, nil, fmt.Errorf("invalid salt: %w", err)
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return argon2Params{}, nil, nil, fmt.Errorf("invalid key: %w", err)
	}
	p.saltLength = uint32(len(salt))
	p.keyLength = uint32(len(key))
	return p, salt, key, nil
}
