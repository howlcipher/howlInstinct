package receipt

import (
	"crypto/rand"
	"encoding/hex"
)

// NewID returns a fresh random decision identifier.
//
// Identifiers are random rather than derived from the decision's content, and
// deliberately so: two identical questions asked about identical state at
// different times are two different decisions, and collapsing them into one
// identifier would make an audit trail lie about how many judgments were made.
// Because the identifier is therefore not reproducible, it is excluded from
// every hash in the receipt.
func NewID() string {
	var b [16]byte
	// crypto/rand.Read is documented never to return an error on any
	// supported platform; it panics internally on entropy failure.
	_, _ = rand.Read(b[:])
	return "dec_" + hex.EncodeToString(b[:])
}
