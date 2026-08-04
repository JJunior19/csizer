package cli

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/jorgeccarhuasaroni/containersize/internal/config"
	"github.com/jorgeccarhuasaroni/containersize/internal/version"
)

func TestDaemonCommandUsesResolvedDatabaseAndContext(t *testing.T) {
	t.Parallel()

	var gotContext context.Context
	var gotPath string
	command := NewRoot(
		version.BuildInfo{Version: "test"},
		&bytes.Buffer{},
		&bytes.Buffer{},
		WithPathResolver(func() (config.Paths, error) { return config.Paths{Database: "daemon.db"}, nil }),
		WithDaemonRunner(func(ctx context.Context, path string) error {
			gotContext = ctx
			gotPath = path
			return nil
		}),
	)
	ctx := context.WithValue(t.Context(), contextKey{}, "daemon-context")
	command.SetContext(ctx)
	command.SetArgs([]string{"daemon"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if gotContext != ctx || gotPath != "daemon.db" {
		t.Errorf("runner got context=%v path=%q, want command context and daemon.db", gotContext, gotPath)
	}
}

func TestDaemonCommandWrapsRunnerError(t *testing.T) {
	t.Parallel()

	runnerErr := errors.New("lease held")
	command := NewRoot(
		version.BuildInfo{Version: "test"},
		&bytes.Buffer{},
		&bytes.Buffer{},
		WithPathResolver(testPathResolver),
		WithDaemonRunner(func(context.Context, string) error { return runnerErr }),
	)
	command.SetArgs([]string{"daemon"})
	if err := command.Execute(); !errors.Is(err, runnerErr) {
		t.Fatalf("Execute() error = %v, want runner error", err)
	}
}
