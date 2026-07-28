package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

// GenerateAPIKey creates a new raw API key string together with its display
// prefix and the SHA-256 hash that should be persisted. The raw key is only
// returned here, at creation time; afterwards only the hash is known to the
// server.
func GenerateAPIKey() (raw, prefix, hash string) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	raw = "km_" + hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(raw))
	hash = hex.EncodeToString(sum[:])
	prefix = raw[:8]
	return raw, prefix, hash
}

// HashAPIKey computes the SHA-256 hex hash of a raw API key (used to look up
// a key presented via the X-API-Key header).
func HashAPIKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
