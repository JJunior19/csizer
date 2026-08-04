package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/jorgeccarhuasaroni/containersize/internal/cli"
	"github.com/jorgeccarhuasaroni/containersize/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	command := cli.NewRoot(version.Current(), stdout, stderr)
	ctx, stop := commandContext(context.Background())
	defer stop()
	command.SetContext(ctx)
	command.SetArgs(args)

	if err := command.Execute(); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}

	return 0
}

func commandContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}
