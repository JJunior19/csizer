package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/jorgeccarhuasaroni/containersize/internal/dockerclient"
	"github.com/jorgeccarhuasaroni/containersize/internal/version"
)

const description = `ContainerSize observes container workloads and recommends resource settings.
Recommendations are evidence-based estimates, not load-test guarantees.`

type rootOptions struct {
	newDockerClient DockerClientFactory
}

// Option configures an external dependency used by a csizer command.
type Option func(*rootOptions)

// WithDockerClientFactory replaces lazy Docker client construction.
func WithDockerClientFactory(factory DockerClientFactory) Option {
	return func(options *rootOptions) {
		options.newDockerClient = factory
	}
}

// NewRoot constructs the csizer command with caller-controlled output streams.
func NewRoot(info version.BuildInfo, stdout, stderr io.Writer, opts ...Option) *cobra.Command {
	options := rootOptions{
		newDockerClient: func() (DockerClient, error) {
			return dockerclient.New("ContainerSize/" + info.Version)
		},
	}
	for _, option := range opts {
		option(&options)
	}

	root := &cobra.Command{
		Use:           "csizer",
		Short:         "Evidence-based container resource recommendations",
		Long:          description,
		Args:          cobra.NoArgs,
		Version:       info.String(),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetVersionTemplate("{{.Name}} {{.Version}}\n")

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print build version information",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(command.OutOrStdout(), "%s %s\n", root.Name(), info.String())
			return err
		},
	})
	root.AddCommand(newDockerCommand(options.newDockerClient))

	return root
}
