package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/jorgeccarhuasaroni/containersize/internal/dockerclient"
	"github.com/jorgeccarhuasaroni/containersize/internal/identity"
)

// DockerClient is the discovery capability consumed by the CLI.
type DockerClient interface {
	List(context.Context) ([]dockerclient.Container, error)
	Ping(context.Context) error
	Close() error
}

// DockerClientFactory constructs a Docker client when a Docker command runs.
type DockerClientFactory func() (DockerClient, error)

func newDockerCommand(factory DockerClientFactory) *cobra.Command {
	dockerCommand := &cobra.Command{
		Use:   "docker",
		Short: "Discover local Docker containers",
		Args:  cobra.NoArgs,
	}
	dockerCommand.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List Docker containers and resolved workloads",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runDockerList(command, factory)
		},
	})

	return dockerCommand
}

func runDockerList(command *cobra.Command, factory DockerClientFactory) (err error) {
	if factory == nil {
		return fmt.Errorf("create Docker client: factory is nil")
	}
	client, err := factory()
	if err != nil {
		return fmt.Errorf("create Docker client: %w", err)
	}
	if client == nil {
		return fmt.Errorf("create Docker client: factory returned nil")
	}
	defer func() {
		if closeErr := client.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("close Docker client: %w", closeErr)
		}
	}()

	containers, err := client.List(command.Context())
	if err != nil {
		return fmt.Errorf("list Docker containers: %w", err)
	}
	containers = append([]dockerclient.Container(nil), containers...)
	sort.Slice(containers, func(i, j int) bool {
		left := strings.ToLower(containers[i].Name)
		right := strings.ToLower(containers[j].Name)
		if left == right {
			return containers[i].ID < containers[j].ID
		}
		return left < right
	})

	writer := tabwriter.NewWriter(command.OutOrStdout(), 0, 0, 2, ' ', 0)
	if _, err = fmt.Fprintln(writer, "CONTAINER\tSERVICE\tPROJECT\tIMAGE\tSTATUS"); err != nil {
		return fmt.Errorf("write Docker container list: %w", err)
	}
	for _, container := range containers {
		resolved, resolveErr := identity.Resolve(identity.Container{
			Name:   container.Name,
			Image:  container.Image,
			Labels: container.Labels,
		}, "")
		if resolveErr != nil {
			return fmt.Errorf("resolve container %q: %w", displayContainerName(container), resolveErr)
		}

		service := resolved.ComposeService
		if service == "" {
			service = "-"
		}
		project := resolved.ComposeProject
		if project == "" {
			project = "-"
		}
		if _, err = fmt.Fprintf(
			writer,
			"%s\t%s\t%s\t%s\t%s\n",
			displayContainerName(container),
			service,
			project,
			container.Image,
			containerStatus(container),
		); err != nil {
			return fmt.Errorf("write Docker container list: %w", err)
		}
	}
	if err = writer.Flush(); err != nil {
		return fmt.Errorf("write Docker container list: %w", err)
	}

	return nil
}

func displayContainerName(container dockerclient.Container) string {
	name := strings.TrimPrefix(strings.TrimSpace(container.Name), "/")
	if name != "" {
		return name
	}
	if len(container.ID) > 12 {
		return container.ID[:12]
	}
	if container.ID != "" {
		return container.ID
	}
	return "-"
}

func containerStatus(container dockerclient.Container) string {
	if state := strings.TrimSpace(container.State); state != "" {
		return strings.ToLower(state)
	}
	if status := strings.TrimSpace(container.Status); status != "" {
		return status
	}
	return "-"
}
