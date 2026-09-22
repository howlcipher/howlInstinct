package receipt

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// HashPrefix labels every digest with the algorithm that produced it, so that
// a stored receipt stays readable if the algorithm is ever changed.
const HashPrefix = "sha256:"

// HashString returns the labelled SHA-256 digest of s.
func HashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return HashPrefix + hex.EncodeToString(sum[:])
}

// HashCanonical returns the labelled SHA-256 digest of v's canonical form.
//
// Hashing the canonical bytes rather than the marshalled bytes is what makes
// the digest independent of map ordering: two requests that differ only in
// the order their questions happened to be inserted must hash identically,
// because they are the same request.
func HashCanonical(v any) (string, error) {
	canon, err := Canonicalize(v)
	if err != nil {
		return "", fmt.Errorf("hash: %w", err)
	}
	sum := sha256.Sum256(canon)
	return HashPrefix + hex.EncodeToString(sum[:]), nil
}
