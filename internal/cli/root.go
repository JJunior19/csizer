package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/jorgeccarhuasaroni/containersize/internal/config"
	"github.com/jorgeccarhuasaroni/containersize/internal/dockerclient"
	"github.com/jorgeccarhuasaroni/containersize/internal/storage"
	"github.com/jorgeccarhuasaroni/containersize/internal/version"
)

const description = `ContainerSize observes container workloads and recommends resource settings.
Recommendations are evidence-based estimates, not load-test guarantees.`

type rootOptions struct {
	newDockerClient DockerClientFactory
	resolvePaths    PathResolver
	openStorage     StorageOpener
	runDaemon       DaemonRunner
}

// PathResolver resolves persistent paths when a command needs them.
type PathResolver func() (config.Paths, error)

// Storage is the lifecycle capability consumed by the init command.
type Storage interface {
	Path() string
	Close() error
}

// StorageOpener creates or migrates local storage when init runs.
type StorageOpener func(context.Context, string) (Storage, error)

// Option configures an external dependency used by a csizer command.
type Option func(*rootOptions)

// WithDockerClientFactory replaces lazy Docker client construction.
func WithDockerClientFactory(factory DockerClientFactory) Option {
	return func(options *rootOptions) {
		options.newDockerClient = factory
	}
}

// WithPathResolver replaces persistent path resolution.
func WithPathResolver(resolver PathResolver) Option {
	return func(options *rootOptions) {
		options.resolvePaths = resolver
	}
}

// WithStorageOpener replaces storage construction and migration.
func WithStorageOpener(opener StorageOpener) Option {
	return func(options *rootOptions) {
		options.openStorage = opener
	}
}

// WithDaemonRunner replaces the long-running collection process.
func WithDaemonRunner(runner DaemonRunner) Option {
	return func(options *rootOptions) {
		options.runDaemon = runner
	}
}

// NewRoot constructs the csizer command with caller-controlled output streams.
func NewRoot(info version.BuildInfo, stdout, stderr io.Writer, opts ...Option) *cobra.Command {
	options := rootOptions{
		newDockerClient: func() (DockerClient, error) {
			return dockerclient.New("ContainerSize/" + info.Version)
		},
		resolvePaths: config.ResolvePaths,
		openStorage: func(ctx context.Context, path string) (Storage, error) {
			return storage.Open(ctx, path)
		},
		runDaemon: func(ctx context.Context, databasePath string) error {
			return startCollectionDaemon(ctx, databasePath, info.Version)
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
	root.AddCommand(newInitCommand(options.resolvePaths, options.openStorage, options.newDockerClient))
	root.AddCommand(newDockerCommand(options.newDockerClient))
	root.AddCommand(newDaemonCommand(options.resolvePaths, options.runDaemon))

	return root
}
