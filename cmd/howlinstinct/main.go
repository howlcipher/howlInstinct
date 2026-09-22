// Command howlinstinct asks bounded semantic questions and reports typed
// judgments with explicit uncertainty.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/howlcipher/howlinstinct/pkg/cli"
)

// main is deliberately the only place in the program that exits, and it does
// nothing but translate a return value into an exit code.
//
// The work happens in run, which returns rather than exiting, so that its
// deferred signal cleanup actually runs. os.Exit does not unwind defers, so
// calling it from inside the function holding the defer would silently skip
// the cleanup.
func main() {
	os.Exit(run())
}

// run executes the command tree and reports the process exit code.
//
// Keeping everything below main error-returning rather than exiting is what
// makes the command tree testable: a test can build and run any command
// repeatedly and inspect the result, which is impossible once a library
// starts calling os.Exit.
func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := cli.NewRootCommand().ExecuteContext(ctx)
	if err == nil {
		return cli.ExitOK
	}

	fmt.Fprintln(os.Stderr, "howlinstinct:", err)

	var exitErr *cli.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code
	}
	return 1
}
