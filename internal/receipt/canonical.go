package receipt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// Canonicalize renders v as deterministic JSON bytes.
//
// Determinism here means byte-identical output for equal input, on any
// machine, in any process. Go's map iteration order is randomized, and
// encoding/json sorts map keys but preserves struct field order, so ordinary
// marshalling is stable for structs and unstable the moment a map of
// interface values is involved. Canonical form removes the question entirely:
// every object's keys are emitted in sorted order, with no insignificant
// whitespace.
//
// Numbers are emitted exactly as encoding/json produced them, which is the
// shortest representation that round-trips to the same float64. That is both
// deterministic and lossless, which a fixed-precision format would not be.
//
// Non-finite floats are rejected rather than encoded. NaN and the infinities
// have no JSON representation at all, and inventing one would make a hash
// that no other implementation could reproduce.
func Canonicalize(v any) ([]byte, error) {
	// Marshalling first normalizes structs, tags, and omitempty into plain
	// JSON values, and rejects NaN and Inf on the way through.
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("canonicalize: %w", err)
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	// UseNumber keeps each number as the literal text encoding/json chose,
	// so canonical form never re-formats and never loses precision.
	dec.UseNumber()

	var tree any
	if err := dec.Decode(&tree); err != nil {
		return nil, fmt.Errorf("canonicalize: %w", err)
	}

	var buf bytes.Buffer
	if err := writeCanonical(&buf, tree); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonical(buf *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case nil:
		buf.WriteString("null")
		return nil
	case bool:
		if t {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
		return nil
	case json.Number:
		buf.WriteString(t.String())
		return nil
	case string:
		// Delegate string escaping to encoding/json so that canonical form
		// matches what every other JSON reader expects.
		enc, err := json.Marshal(t)
		if err != nil {
			return fmt.Errorf("canonicalize string: %w", err)
		}
		buf.Write(enc)
		return nil
	case []any:
		buf.WriteByte('[')
		for i, item := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
		return nil
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			enc, err := json.Marshal(k)
			if err != nil {
				return fmt.Errorf("canonicalize key: %w", err)
			}
			buf.Write(enc)
			buf.WriteByte(':')
			if err := writeCanonical(buf, t[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
		return nil
	default:
		return fmt.Errorf("canonicalize: unsupported value of type %T", v)
	}
}
