// Package cli builds HowlInstinct's composable command tree.
//
// The tree is exported as constructors rather than executed here, so that the
// commands can be mounted under a future umbrella binary and, just as
// importantly, so that tests can build the tree repeatedly and capture its
// output. Commands write through the cobra command's own output streams for
// the same reason; nothing in this package writes to os.Stdout directly.
package cli

import (
	"errors"
	"fmt"

	"github.com/howlcipher/howlinstinct/pkg/instinct"
)

// Process exit codes.
//
// These are part of the CLI's contract with scripts, so they are constants
// with a test pinning them rather than literals scattered through the code.
//
// The codes describe how the tool failed, never what a decision concluded. A
// noul answering "no" is a successful decision and exits 0: encoding an
// answer in the exit status would make a confident "no" indistinguishable
// from a crashed provider, which is exactly the confusion this tool exists to
// prevent.
const (
	ExitOK = 0

	// ExitBadInput is a malformed or out-of-bounds request.
	ExitBadInput = 2

	// ExitConfig is a configuration or credential problem.
	ExitConfig = 3

	// ExitProviderUnavailable means the provider could not be reached or
	// refused to serve.
	ExitProviderUnavailable = 4

	// ExitProviderResponseInvalid means the provider answered, but the
	// answer failed validation. Distinct from unavailability because it is
	// an integrity problem rather than an operational one.
	ExitProviderResponseInvalid = 5

	// ExitTimeout means the deadline elapsed.
	ExitTimeout = 6

	// ExitEvalGate means an evaluation ran successfully and did not meet a
	// threshold the caller asked it to enforce.
	ExitEvalGate = 7
)

// ExitError carries a process exit code out to main.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }

func (e *ExitError) Unwrap() error { return e.Err }

// exitCodeFor maps a classified error onto a process exit code.
//
// An unclassified error deliberately becomes a generic failure rather than
// being guessed at: inventing a specific code for an error nobody classified
// would tell a script something we do not actually know.
func exitCodeFor(err error) int {
	kind, ok := instinct.KindOf(err)
	if !ok {
		return 1
	}
	switch kind {
	case instinct.KindInvalidInput:
		return ExitBadInput
	case instinct.KindConfiguration:
		return ExitConfig
	case instinct.KindProviderUnavailable:
		return ExitProviderUnavailable
	case instinct.KindProviderResponseInvalid:
		return ExitProviderResponseInvalid
	case instinct.KindTimeout:
		return ExitTimeout
	default:
		return 1
	}
}

// asExit wraps err with the exit code its classification implies.
func asExit(err error) error {
	if err == nil {
		return nil
	}
	var already *ExitError
	if errors.As(err, &already) {
		return err
	}
	return &ExitError{Code: exitCodeFor(err), Err: err}
}

// exitf builds an ExitError with an explicit code.
func exitf(code int, format string, args ...any) error {
	return &ExitError{Code: code, Err: fmt.Errorf(format, args...)}
}
