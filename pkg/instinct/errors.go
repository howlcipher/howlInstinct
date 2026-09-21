package instinct

import (
	"errors"
	"fmt"
)

// ErrorKind classifies a failure by what the caller should understand about
// it, not by which function produced it. The CLI maps these onto distinct
// process exit codes so that a script can tell a bad request from an
// unreachable provider without parsing text.
type ErrorKind string

const (
	// KindInvalidInput is a caller mistake: a malformed or out-of-bounds
	// request that was rejected before any provider was contacted.
	KindInvalidInput ErrorKind = "invalid_input"

	// KindConfiguration is an operator mistake: missing endpoint, unknown
	// provider, unreadable config, absent credential environment variable.
	KindConfiguration ErrorKind = "configuration"

	// KindProviderUnavailable means the provider could not be reached or
	// refused to serve: transport failure, auth rejection, rate limiting,
	// overload.
	KindProviderUnavailable ErrorKind = "provider_unavailable"

	// KindProviderResponseInvalid means the provider answered, but the answer
	// did not survive validation. This is kept distinct from unavailability
	// on purpose: an unreachable provider is an operational problem, whereas
	// a provider returning malformed or mismatched answers is a correctness
	// or integrity problem and deserves a different alarm.
	KindProviderResponseInvalid ErrorKind = "provider_response_invalid"

	// KindTimeout means the deadline elapsed.
	KindTimeout ErrorKind = "timeout"
)

// Error is HowlInstinct's classified error.
type Error struct {
	Kind ErrorKind
	Op   string
	Msg  string
	Err  error
}

func (e *Error) Error() string {
	switch {
	case e.Op != "" && e.Err != nil:
		return fmt.Sprintf("%s: %s: %v", e.Op, e.Msg, e.Err)
	case e.Op != "":
		return fmt.Sprintf("%s: %s", e.Op, e.Msg)
	case e.Err != nil:
		return fmt.Sprintf("%s: %v", e.Msg, e.Err)
	default:
		return e.Msg
	}
}

// Unwrap exposes the underlying cause to errors.Is and errors.As.
func (e *Error) Unwrap() error { return e.Err }

// Errorf builds a classified error.
func Errorf(kind ErrorKind, op, format string, args ...any) *Error {
	return &Error{Kind: kind, Op: op, Msg: fmt.Sprintf(format, args...)}
}

// Wrap builds a classified error around an existing cause.
func Wrap(kind ErrorKind, op string, err error, format string, args ...any) *Error {
	return &Error{Kind: kind, Op: op, Msg: fmt.Sprintf(format, args...), Err: err}
}

// KindOf reports the classification of err, and whether err was classified at
// all. An unclassified error is not guessed at: callers decide what to do
// with an error whose kind is unknown rather than having one invented here.
func KindOf(err error) (ErrorKind, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind, true
	}
	return "", false
}
