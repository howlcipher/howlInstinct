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

// main is deliberately the only place in the program that exits.
//
// Everything below returns errors, which keeps the command tree testable: a
// test can build and run any command repeatedly and inspect what came back,
// which is impossible once a library starts calling os.Exit.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := cli.NewRootCommand().ExecuteContext(ctx)
	if err == nil {
		return
	}

	fmt.Fprintln(os.Stderr, "howlinstinct:", err)

	var exitErr *cli.ExitError
	if errors.As(err, &exitErr) {
		os.Exit(exitErr.Code)
	}
	os.Exit(1)
}
