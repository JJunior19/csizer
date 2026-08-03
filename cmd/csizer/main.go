package main

import (
	"fmt"
	"io"
	"os"

	"github.com/jorgeccarhuasaroni/containersize/internal/cli"
	"github.com/jorgeccarhuasaroni/containersize/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	command := cli.NewRoot(version.Current(), stdout, stderr)
	command.SetArgs(args)

	if err := command.Execute(); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}

	return 0
}
