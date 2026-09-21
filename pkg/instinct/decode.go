package instinct

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// DecodeRequest reads a JSON request, rejecting malformed, oversized, and
// ambiguous documents before any of it reaches a provider.
//
// "Ambiguous" is the interesting one. encoding/json silently keeps the last
// value when an object repeats a key, so a document containing two questions
// with the same identifier would decode to one question with no error at all,
// and the caller would get back fewer answers than it asked for with nothing
// to indicate why. The specification requires duplicate identifiers to be
// rejected deterministically, so this decoder walks the token stream and
// fails on any repeated key anywhere in the document.
func DecodeRequest(r io.Reader, lim Limits) (Request, error) {
	var req Request

	// Bound the document before parsing it. The state limit alone is not a
	// bound on the document, since questions, options, and levels all add to
	// it, so allow the state limit plus a fixed allowance for structure.
	maxDoc := int64(lim.MaxStateBytes) + 256*1024
	raw, err := io.ReadAll(io.LimitReader(r, maxDoc+1))
	if err != nil {
		return req, Wrap(KindInvalidInput, "decode", err, "reading request")
	}
	if int64(len(raw)) > maxDoc {
		return req, Errorf(KindInvalidInput, "decode",
			"request document exceeds %d bytes", maxDoc)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return req, Errorf(KindInvalidInput, "decode", "request is empty")
	}

	if err := checkDuplicateKeys(raw); err != nil {
		return req, err
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	// An unknown field is far more often a typo in a field that matters
	// (retain_state, escalation) than a harmless extra, and silently dropping
	// it would mean silently ignoring the caller's intent.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, Wrap(KindInvalidInput, "decode", err, "parsing request")
	}
	if dec.More() {
		return req, Errorf(KindInvalidInput, "decode",
			"request contains trailing content after the JSON document")
	}
	if err := req.Validate(lim); err != nil {
		return req, err
	}
	return req, nil
}

// checkDuplicateKeys walks a JSON document and reports the first object key
// that appears twice within the same object, identified by its path.
func checkDuplicateKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	tok, err := dec.Token()
	if err != nil {
		return Wrap(KindInvalidInput, "decode", err, "parsing request")
	}
	return walkValue(dec, tok, nil)
}

// walkValue consumes the value whose opening token is tok.
func walkValue(dec *json.Decoder, tok json.Token, path []string) error {
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil // scalar, already consumed
	}
	switch delim {
	case '{':
		return walkObject(dec, path)
	case '[':
		return walkArray(dec, path)
	default:
		return Errorf(KindInvalidInput, "decode",
			"unexpected %q at %s", delim, pathString(path))
	}
}

func walkObject(dec *json.Decoder, path []string) error {
	seen := make(map[string]bool)
	for {
		tok, err := dec.Token()
		if err != nil {
			return Wrap(KindInvalidInput, "decode", err, "parsing request")
		}
		if d, ok := tok.(json.Delim); ok && d == '}' {
			return nil
		}
		key, ok := tok.(string)
		if !ok {
			return Errorf(KindInvalidInput, "decode",
				"expected an object key at %s", pathString(path))
		}
		if seen[key] {
			return Errorf(KindInvalidInput, "decode",
				"duplicate key %q at %s", key, pathString(path))
		}
		seen[key] = true

		val, err := dec.Token()
		if err != nil {
			return Wrap(KindInvalidInput, "decode", err, "parsing request")
		}
		if err := walkValue(dec, val, append(path, key)); err != nil {
			return err
		}
	}
}

func walkArray(dec *json.Decoder, path []string) error {
	for i := 0; ; i++ {
		tok, err := dec.Token()
		if err != nil {
			return Wrap(KindInvalidInput, "decode", err, "parsing request")
		}
		if d, ok := tok.(json.Delim); ok && d == ']' {
			return nil
		}
		if err := walkValue(dec, tok, append(path, fmt.Sprintf("[%d]", i))); err != nil {
			return err
		}
	}
}

func pathString(path []string) string {
	if len(path) == 0 {
		return "the document root"
	}
	return strings.Join(path, ".")
}
