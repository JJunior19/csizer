package cli

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/jorgeccarhuasaroni/containersize/internal/config"
	"github.com/jorgeccarhuasaroni/containersize/internal/version"
)

type fakeStorage struct {
	path     string
	closed   bool
	closeErr error
}

func (fake *fakeStorage) Path() string {
	return fake.path
}

func (fake *fakeStorage) Close() error {
	fake.closed = true
	return fake.closeErr
}

func TestInitSuccessUsesActualStorePath(t *testing.T) {
	t.Parallel()

	resolvedPath := filepath.Join("relative path", "container size.db")
	actualPath := filepath.Join(t.TempDir(), "absolute path with spaces", "container size.db")
	store := &fakeStorage{path: actualPath}
	docker := &fakeDockerClient{}
	var openContext context.Context
	var openedPath string
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command := NewRoot(
		version.BuildInfo{Version: "test"},
		&stdout,
		&stderr,
		WithPathResolver(func() (config.Paths, error) {
			return config.Paths{Database: resolvedPath}, nil
		}),
		WithStorageOpener(func(ctx context.Context, path string) (Storage, error) {
			openContext = ctx
			openedPath = path
			return store, nil
		}),
		WithDockerClientFactory(func() (DockerClient, error) { return docker, nil }),
	)
	ctx := context.WithValue(context.Background(), contextKey{}, "init-context")
	command.SetContext(ctx)
	command.SetArgs([]string{"init"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	wantOutput := "Database: " + actualPath + "\nDocker: accessible\n"
	if stdout.String() != wantOutput {
		t.Errorf("stdout = %q, want %q", stdout.String(), wantOutput)
	}
	if stderr.String() != "" {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
	if openedPath != resolvedPath {
		t.Errorf("storage input path = %q, want resolver path %q", openedPath, resolvedPath)
	}
	if openContext != ctx || docker.context != ctx {
		t.Error("storage opener and Docker Ping must receive command.Context()")
	}
	if !store.closed || !docker.closed {
		t.Errorf("closed state: storage=%v Docker=%v, want both true", store.closed, docker.closed)
	}
}

func TestInitPrintsActualDatabaseBeforeDockerFailure(t *testing.T) {
	t.Parallel()

	store := &fakeStorage{path: "/absolute/database with spaces.db"}
	docker := &fakeDockerClient{pingErr: errors.New("daemon unavailable")}
	var stdout bytes.Buffer
	command := newTestInitCommand(&stdout, store, docker)

	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "check Docker access: daemon unavailable") {
		t.Fatalf("Execute() error = %v, want Docker access error", err)
	}
	if stdout.String() != "Database: /absolute/database with spaces.db\n" {
		t.Errorf("stdout = %q, want actual database status only", stdout.String())
	}
	if !store.closed || !docker.closed {
		t.Errorf("closed state: storage=%v Docker=%v, want both true", store.closed, docker.closed)
	}
}

func TestInitDependencyErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		resolver      PathResolver
		opener        StorageOpener
		dockerFactory DockerClientFactory
		wantError     string
		wantOutput    string
	}{
		{
			name: "resolver error",
			resolver: func() (config.Paths, error) {
				return config.Paths{}, errors.New("home unavailable")
			},
			wantError: "resolve paths: home unavailable",
		},
		{
			name:     "storage error",
			resolver: testPathResolver,
			opener: func(context.Context, string) (Storage, error) {
				return nil, errors.New("disk unavailable")
			},
			wantError: "initialize storage: disk unavailable",
		},
		{
			name:     "Docker factory error",
			resolver: testPathResolver,
			opener: func(context.Context, string) (Storage, error) {
				return &fakeStorage{path: "/absolute/test-container-size.db"}, nil
			},
			dockerFactory: func() (DockerClient, error) {
				return nil, errors.New("bad Docker environment")
			},
			wantError:  "create Docker client: bad Docker environment",
			wantOutput: "Database: /absolute/test-container-size.db\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			command := NewRoot(
				version.BuildInfo{Version: "test"},
				&stdout,
				&bytes.Buffer{},
				WithPathResolver(test.resolver),
				WithStorageOpener(test.opener),
				WithDockerClientFactory(test.dockerFactory),
			)
			command.SetArgs([]string{"init"})
			err := command.Execute()
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Execute() error = %v, want containing %q", err, test.wantError)
			}
			if stdout.String() != test.wantOutput {
				t.Errorf("stdout = %q, want %q", stdout.String(), test.wantOutput)
			}
		})
	}
}

func TestInitJoinsCleanupErrorsDeterministically(t *testing.T) {
	t.Parallel()

	pingErr := errors.New("ping failed")
	dockerCloseErr := errors.New("Docker close failed")
	storageCloseErr := errors.New("storage close failed")
	tests := []struct {
		name      string
		pingErr   error
		wantOrder []string
	}{
		{
			name:      "both close failures",
			wantOrder: []string{"close Docker client", "close storage"},
		},
		{
			name:      "primary and both close failures",
			pingErr:   pingErr,
			wantOrder: []string{"check Docker access", "close Docker client", "close storage"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store := &fakeStorage{path: "/absolute/database.db", closeErr: storageCloseErr}
			docker := &fakeDockerClient{pingErr: test.pingErr, closeErr: dockerCloseErr}
			command := newTestInitCommand(&bytes.Buffer{}, store, docker)
			err := command.Execute()
			if err == nil {
				t.Fatal("Execute() error = nil")
			}
			if !errors.Is(err, dockerCloseErr) || !errors.Is(err, storageCloseErr) {
				t.Fatalf("Execute() error = %v, want both cleanup causes", err)
			}
			if test.pingErr != nil && !errors.Is(err, pingErr) {
				t.Fatalf("Execute() error = %v, want primary cause", err)
			}
			lastIndex := -1
			for _, fragment := range test.wantOrder {
				index := strings.Index(err.Error(), fragment)
				if index <= lastIndex {
					t.Errorf("error order = %q, want %q after previous fragment", err, fragment)
				}
				lastIndex = index
			}
			if !store.closed || !docker.closed {
				t.Error("dependencies were not both closed")
			}
		})
	}
}

func TestInitJoinsPrimaryFactoryAndStorageCloseErrors(t *testing.T) {
	t.Parallel()

	factoryErr := errors.New("factory failed")
	storageCloseErr := errors.New("storage close failed")
	store := &fakeStorage{path: "/absolute/database.db", closeErr: storageCloseErr}
	command := NewRoot(
		version.BuildInfo{Version: "test"},
		&bytes.Buffer{},
		&bytes.Buffer{},
		WithPathResolver(testPathResolver),
		WithStorageOpener(func(context.Context, string) (Storage, error) { return store, nil }),
		WithDockerClientFactory(func() (DockerClient, error) { return nil, factoryErr }),
	)
	command.SetArgs([]string{"init"})
	err := command.Execute()
	if !errors.Is(err, factoryErr) || !errors.Is(err, storageCloseErr) {
		t.Fatalf("Execute() error = %v, want factory and storage close causes", err)
	}
}

func newTestInitCommand(stdout *bytes.Buffer, store *fakeStorage, docker *fakeDockerClient) *cobra.Command {
	command := NewRoot(
		version.BuildInfo{Version: "test"},
		stdout,
		&bytes.Buffer{},
		WithPathResolver(testPathResolver),
		WithStorageOpener(func(context.Context, string) (Storage, error) { return store, nil }),
		WithDockerClientFactory(func() (DockerClient, error) { return docker, nil }),
	)
	command.SetArgs([]string{"init"})
	return command
}

func testPathResolver() (config.Paths, error) {
	return config.Paths{Database: "relative/test-container-size.db"}, nil
}
