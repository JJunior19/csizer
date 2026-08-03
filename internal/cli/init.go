package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

func newInitCommand(
	resolvePaths PathResolver,
	openStorage StorageOpener,
	newDockerClient DockerClientFactory,
) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize local storage and check Docker access",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runInit(command, resolvePaths, openStorage, newDockerClient)
		},
	}
}

func runInit(
	command *cobra.Command,
	resolvePaths PathResolver,
	openStorage StorageOpener,
	newDockerClient DockerClientFactory,
) (err error) {
	if resolvePaths == nil {
		return fmt.Errorf("resolve paths: resolver is nil")
	}
	paths, err := resolvePaths()
	if err != nil {
		return fmt.Errorf("resolve paths: %w", err)
	}
	if openStorage == nil {
		return fmt.Errorf("initialize storage: opener is nil")
	}
	store, err := openStorage(command.Context(), paths.Database)
	if err != nil {
		return fmt.Errorf("initialize storage: %w", err)
	}
	if store == nil {
		return fmt.Errorf("initialize storage: opener returned nil")
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close storage: %w", closeErr))
		}
	}()

	if _, err = fmt.Fprintf(command.OutOrStdout(), "Database: %s\n", store.Path()); err != nil {
		return fmt.Errorf("write database status: %w", err)
	}
	if newDockerClient == nil {
		return fmt.Errorf("create Docker client: factory is nil")
	}
	docker, err := newDockerClient()
	if err != nil {
		return fmt.Errorf("create Docker client: %w", err)
	}
	if docker == nil {
		return fmt.Errorf("create Docker client: factory returned nil")
	}
	defer func() {
		if closeErr := docker.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close Docker client: %w", closeErr))
		}
	}()

	if err := docker.Ping(command.Context()); err != nil {
		return fmt.Errorf("check Docker access: %w", err)
	}
	if _, err = fmt.Fprintln(command.OutOrStdout(), "Docker: accessible"); err != nil {
		return fmt.Errorf("write Docker status: %w", err)
	}
	return nil
}
