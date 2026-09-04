package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/jorgeccarhuasaroni/containersize/internal/config"
	"github.com/jorgeccarhuasaroni/containersize/internal/dockerclient"
	"github.com/jorgeccarhuasaroni/containersize/internal/identity"
	"github.com/jorgeccarhuasaroni/containersize/internal/storage"
	"github.com/jorgeccarhuasaroni/containersize/internal/version"
)

type fakeWorkloadStore struct {
	path      string
	workloads []storage.Workload
	samples   []storage.MetricSample
	from      time.Time
	to        time.Time
	ensured   storage.Workload
	closed    bool
	closeErr  error
}

func (fake *fakeWorkloadStore) Path() string { return fake.path }

func (fake *fakeWorkloadStore) Close() error {
	fake.closed = true
	return fake.closeErr
}

func (fake *fakeWorkloadStore) EnsureWorkload(_ context.Context, workload storage.Workload) (storage.Workload, error) {
	workload.ID = 1
	workload.TrackingEnabled = true
	fake.ensured = workload
	return workload, nil
}

func (fake *fakeWorkloadStore) ListWorkloads(context.Context) ([]storage.Workload, error) {
	return append([]storage.Workload(nil), fake.workloads...), nil
}

func (fake *fakeWorkloadStore) SetTrackingEnabled(_ context.Context, workloadID int64, enabled bool) (storage.Workload, error) {
	for index, workload := range fake.workloads {
		if workload.ID == workloadID {
			workload.TrackingEnabled = enabled
			fake.workloads[index] = workload
			return workload, nil
		}
	}
	return storage.Workload{}, nil
}

func (fake *fakeWorkloadStore) MetricSampleBoundsForWorkload(context.Context, int64) (time.Time, time.Time, error) {
	return fake.from, fake.to, nil
}

func (fake *fakeWorkloadStore) MetricSamplesForWorkload(context.Context, int64, time.Time, time.Time) ([]storage.MetricSample, error) {
	return append([]storage.MetricSample(nil), fake.samples...), nil
}

func TestTrackRegistersComposeService(t *testing.T) {
	t.Parallel()

	store := &fakeWorkloadStore{path: "test.db"}
	docker := &fakeDockerClient{containers: []dockerclient.Container{{
		ID:     "api-id",
		Name:   "demo-api-1",
		Image:  "example/api:1",
		Labels: map[string]string{identity.LabelComposeProject: "demo", identity.LabelComposeService: "api"},
	}}}
	stdout := &bytes.Buffer{}
	command := newWorkloadTestCommand(stdout, store, docker)
	command.SetArgs([]string{"track", "api", "--json"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if store.ensured.WorkloadKey != "demo/api" || store.ensured.ComposeService != "api" || !strings.Contains(stdout.String(), `"version":"v1"`) || !strings.Contains(stdout.String(), `"workload_key":"demo/api"`) {
		t.Errorf("track result: workload=%#v output=%q", store.ensured, stdout.String())
	}
	if !store.closed || !docker.closed {
		t.Error("track did not close storage and Docker client")
	}
}

func TestListInspectAndRecommendEmitStableJSON(t *testing.T) {
	t.Parallel()

	store := sampleWorkloadStore()
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{name: "list", args: []string{"list", "--json"}, want: []string{`"version":"v1"`, `"workloads"`, `"workload_key":"demo/api"`}},
		{name: "inspect", args: []string{"inspect", "api", "--json"}, want: []string{`"analysis"`, `"input_sample_ids"`, `"memory_working_set"`}},
		{name: "recommend", args: []string{"recommend", "demo/api", "--json"}, want: []string{`"recommendation"`, `"ecs_fargate"`, `"cpu_units":1024`}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stdout := &bytes.Buffer{}
			command := newWorkloadTestCommand(stdout, store, &fakeDockerClient{})
			command.SetArgs(test.args)
			if err := command.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			for _, fragment := range test.want {
				if !strings.Contains(stdout.String(), fragment) {
					t.Errorf("stdout = %q, want containing %q", stdout.String(), fragment)
				}
			}
		})
	}
}

func newWorkloadTestCommand(stdout *bytes.Buffer, store *fakeWorkloadStore, docker *fakeDockerClient) *cobra.Command {
	command := NewRoot(
		version.BuildInfo{Version: "test"},
		stdout,
		&bytes.Buffer{},
		WithPathResolver(func() (config.Paths, error) { return config.Paths{Database: "test.db"}, nil }),
		WithWorkloadStorageOpener(func(context.Context, string) (WorkloadStore, error) { return store, nil }),
		WithDockerClientFactory(func() (DockerClient, error) { return docker, nil }),
	)
	return command
}

func sampleWorkloadStore() *fakeWorkloadStore {
	base := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	samples := make([]storage.MetricSample, 30)
	for index := range samples {
		samples[index] = storage.MetricSample{
			ID:                    int64(index + 1),
			SessionID:             1,
			Timestamp:             base.Add(time.Duration(index) * time.Second),
			CPUUsageCores:         .5,
			MemoryUsageBytes:      100 * 1024 * 1024,
			MemoryWorkingSetBytes: 100 * 1024 * 1024,
			ActivityState:         "unknown",
		}
	}
	return &fakeWorkloadStore{
		path:      "test.db",
		workloads: []storage.Workload{{ID: 1, WorkloadKey: "demo/api", DisplayName: "api", ComposeService: "api", TrackingEnabled: true}},
		samples:   samples,
		from:      samples[0].Timestamp,
		to:        samples[len(samples)-1].Timestamp,
	}
}
