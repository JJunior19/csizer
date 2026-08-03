package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jorgeccarhuasaroni/containersize/internal/dockerclient"
	"github.com/jorgeccarhuasaroni/containersize/internal/identity"
	"github.com/jorgeccarhuasaroni/containersize/internal/version"
)

type fakeDockerClient struct {
	containers []dockerclient.Container
	listErr    error
	pingErr    error
	closeErr   error
	closed     bool
	context    context.Context
}

func (fake *fakeDockerClient) Ping(ctx context.Context) error {
	fake.context = ctx
	return fake.pingErr
}

func (fake *fakeDockerClient) List(ctx context.Context) ([]dockerclient.Container, error) {
	fake.context = ctx
	return fake.containers, fake.listErr
}

func (fake *fakeDockerClient) Close() error {
	fake.closed = true
	return fake.closeErr
}

func TestDockerList(t *testing.T) {
	t.Parallel()

	dockerError := errors.New("daemon unavailable")
	tests := []struct {
		name         string
		client       *fakeDockerClient
		wantOutput   string
		wantError    string
		contextValue string
	}{
		{
			name: "prints deterministic table",
			client: &fakeDockerClient{containers: []dockerclient.Container{
				{
					ID:     "worker-id",
					Name:   "demo-worker-1",
					Image:  "registry.example.com/demo/worker:2",
					State:  "running",
					Status: "Up 5 minutes",
					Labels: map[string]string{identity.LabelComposeProject: "demo", identity.LabelComposeService: "worker"},
				},
				{
					ID:     "api-id",
					Name:   "demo-api-1",
					Image:  "registry.example.com/demo/api:1",
					State:  "exited",
					Status: "Exited (0) 1 minute ago",
					Labels: map[string]string{identity.LabelComposeProject: "demo", identity.LabelComposeService: "api"},
				},
			}},
			wantOutput: "CONTAINER      SERVICE  PROJECT  IMAGE                               STATUS\n" +
				"demo-api-1     api      demo     registry.example.com/demo/api:1     exited\n" +
				"demo-worker-1  worker   demo     registry.example.com/demo/worker:2  running\n",
			contextValue: "request-context",
		},
		{
			name: "prints fallback identity without Compose metadata",
			client: &fakeDockerClient{containers: []dockerclient.Container{
				{Name: "/standalone", Image: "redis:7", Status: "Exited (0) 2 hours ago"},
			}},
			wantOutput: "CONTAINER   SERVICE  PROJECT  IMAGE    STATUS\n" +
				"standalone  -        -        redis:7  Exited (0) 2 hours ago\n",
		},
		{
			name:      "propagates Docker errors",
			client:    &fakeDockerClient{listErr: dockerError},
			wantError: "list Docker containers: daemon unavailable",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			command := NewRoot(
				version.BuildInfo{Version: "test"},
				&stdout,
				&stderr,
				WithDockerClientFactory(func() (DockerClient, error) { return test.client, nil }),
			)
			ctx := context.WithValue(context.Background(), contextKey{}, test.contextValue)
			command.SetContext(ctx)
			command.SetArgs([]string{"docker", "list"})

			err := command.Execute()
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("Execute() error = %v, want error containing %q", err, test.wantError)
				}
			} else if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if got := stdout.String(); got != test.wantOutput {
				t.Errorf("stdout = %q, want %q", got, test.wantOutput)
			}
			if got := stderr.String(); got != "" {
				t.Errorf("stderr = %q, want empty", got)
			}
			if !test.client.closed {
				t.Error("Docker client was not closed")
			}
			if test.client.context != ctx {
				t.Error("List() did not receive command.Context()")
			}
		})
	}
}

type contextKey struct{}

func TestDockerClientConstructionIsLazy(t *testing.T) {
	t.Parallel()

	constructed := false
	command := NewRoot(
		version.BuildInfo{Version: "test"},
		&bytes.Buffer{},
		&bytes.Buffer{},
		WithDockerClientFactory(func() (DockerClient, error) {
			constructed = true
			return &fakeDockerClient{}, nil
		}),
	)
	command.SetArgs([]string{"version"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if constructed {
		t.Error("Docker client was constructed for a non-Docker command")
	}
}
