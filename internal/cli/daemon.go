package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jorgeccarhuasaroni/containersize/internal/daemon"
	"github.com/jorgeccarhuasaroni/containersize/internal/dockerclient"
	"github.com/jorgeccarhuasaroni/containersize/internal/storage"
)

// DaemonRunner starts the independent collection process for one database.
type DaemonRunner func(context.Context, string) error

func newDaemonCommand(resolvePaths PathResolver, run DaemonRunner) *cobra.Command {
	return &cobra.Command{
		Use:   "daemon",
		Short: "Run continuous local container collection",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runDaemon(command, resolvePaths, run)
		},
	}
}

func runDaemon(command *cobra.Command, resolvePaths PathResolver, run DaemonRunner) error {
	if resolvePaths == nil {
		return errors.New("resolve paths: resolver is nil")
	}
	paths, err := resolvePaths()
	if err != nil {
		return fmt.Errorf("resolve paths: %w", err)
	}
	if run == nil {
		return errors.New("start daemon: runner is nil")
	}
	if err := run(command.Context(), paths.Database); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	return nil
}

func startCollectionDaemon(ctx context.Context, databasePath, collectorVersion string) (err error) {
	store, err := storage.Open(ctx, databasePath)
	if err != nil {
		return fmt.Errorf("open storage: %w", err)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close storage: %w", closeErr))
		}
	}()

	docker, err := dockerclient.New("ContainerSize/" + collectorVersion)
	if err != nil {
		return fmt.Errorf("create Docker client: %w", err)
	}
	defer func() {
		if closeErr := docker.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close Docker client: %w", closeErr))
		}
	}()

	service, err := daemon.New(store, docker, daemon.Config{CollectorVersion: collectorVersion})
	if err != nil {
		return err
	}
	return service.Run(ctx)
}
