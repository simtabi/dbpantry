// Command dbpantry is the toolkit's binary. It only wires the process to the
// command tree in internal/cli.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/simtabi/dbpantry/src/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Execute(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Environ)
	stop()
	os.Exit(code)
}
