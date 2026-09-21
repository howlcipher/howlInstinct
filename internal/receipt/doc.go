// Package receipt builds durable, versioned records of decisions.
//
// A decision receipt is what survives a decision. It is designed to be stored
// by a caller, attached to an audit trail, and read back long after the state
// that produced it is gone, so it favours stable hashes and metadata over raw
// content.
//
// Two properties are load bearing. First, receipts are deterministic: the same
// decision inputs always produce the same hashes, independent of Go map
// ordering, field ordering, or the machine that produced them. Second,
// receipts are credential-free and, by default, content-free: the state that
// was judged is recorded as a hash, never as text, unless the caller
// explicitly opts in.
package receipt
