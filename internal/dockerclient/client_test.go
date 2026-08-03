package dockerclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	containertypes "github.com/moby/moby/api/types/container"
	eventtypes "github.com/moby/moby/api/types/events"
	mobyclient "github.com/moby/moby/client"

	"github.com/jorgeccarhuasaroni/containersize/internal/collector"
)

func TestPing(t *testing.T) {
	t.Parallel()

	type contextKey struct{}
	ctx := t.Context()
	ctx = context.WithValue(ctx, contextKey{}, "ping-context")
	httpClient := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.Context().Value(contextKey{}) != "ping-context" {
			t.Error("Ping() did not propagate context")
		}
		if request.URL.Path != "/_ping" {
			t.Errorf("request path = %q, want /_ping", request.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("OK")),
		}, nil
	})}
	apiClient, err := mobyclient.New(
		mobyclient.WithHost("http://docker.test"),
		mobyclient.WithHTTPClient(httpClient),
	)
	if err != nil {
		t.Fatalf("mobyclient.New() error = %v", err)
	}
	docker := &Client{apiClient: apiClient}
	if err := docker.Ping(ctx); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestPrimaryName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		names []string
		want  string
	}{
		{name: "sorts aliases", names: []string{"/worker", "/api"}, want: "api"},
		{name: "removes empty names", names: []string{"", "/"}, want: ""},
		{name: "trims Docker slash", names: []string{" /backend "}, want: "backend"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := primaryName(test.names); got != test.want {
				t.Errorf("primaryName() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestRuntimeStatsMapsDockerCounters(t *testing.T) {
	t.Parallel()

	read := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	response := containertypes.StatsResponse{
		Read:    read,
		PreRead: read.Add(-time.Second),
		CPUStats: containertypes.CPUStats{
			CPUUsage:    containertypes.CPUUsage{TotalUsage: 200},
			SystemUsage: 2_000,
			OnlineCPUs:  2,
		},
		PreCPUStats: containertypes.CPUStats{
			CPUUsage:    containertypes.CPUUsage{TotalUsage: 100},
			SystemUsage: 1_000,
		},
		MemoryStats: containertypes.MemoryStats{
			Usage: 512,
			Limit: 1_024,
			Stats: map[string]uint64{
				"inactive_file":       128,
				"total_inactive_file": 256,
			},
		},
		PidsStats: containertypes.PidsStats{Current: 3},
		Networks: map[string]containertypes.NetworkStats{
			"eth0": {RxBytes: 7, TxBytes: 11},
			"eth1": {RxBytes: 13, TxBytes: 17},
		},
		BlkioStats: containertypes.BlkioStats{IoServiceBytesRecursive: []containertypes.BlkioStatEntry{
			{Op: "Read", Value: 19}, {Op: "Write", Value: 23},
		}},
	}

	got := runtimeStats(response)
	if got.Timestamp != read || got.CPU != (collector.CPUCounters{TotalUsage: 200, SystemUsage: 2_000, OnlineCPUs: 2}) || got.PreviousCPU == nil || *got.PreviousCPU != (collector.CPUCounters{TotalUsage: 100, SystemUsage: 1_000}) {
		t.Errorf("runtimeStats() CPU = %#v", got)
	}
	if !reflect.DeepEqual(got.Memory.CacheBytes, uint64Pointer(128)) || !reflect.DeepEqual(got.Memory.LimitBytes, uint64Pointer(1_024)) || !reflect.DeepEqual(got.PIDs, uint64Pointer(3)) || !reflect.DeepEqual(got.NetworkRx, uint64Pointer(20)) || !reflect.DeepEqual(got.NetworkTx, uint64Pointer(28)) || !reflect.DeepEqual(got.BlockRead, uint64Pointer(19)) || !reflect.DeepEqual(got.BlockWrite, uint64Pointer(23)) {
		t.Errorf("runtimeStats() counters = %#v", got)
	}
}

func TestStatsDecodesDockerResponse(t *testing.T) {
	t.Parallel()

	read := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	payload, err := json.Marshal(containertypes.StatsResponse{
		Read: read,
		MemoryStats: containertypes.MemoryStats{
			Usage: 512,
		},
	})
	if err != nil {
		t.Fatalf("marshal stats response error = %v", err)
	}
	httpClient := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/_ping" {
			header := make(http.Header)
			header.Set("API-Version", "1.55")
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     header,
				Body:       io.NopCloser(strings.NewReader("OK")),
			}, nil
		}
		if request.URL.Path != "/v1.55/containers/container-id/stats" {
			t.Errorf("request path = %q, want stats endpoint", request.URL.Path)
		}
		if request.URL.Query().Get("stream") != "false" || request.URL.Query().Get("one-shot") != "" {
			t.Errorf("stats query = %q, want stream=false with prior sample", request.URL.RawQuery)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(string(payload))),
		}, nil
	})}
	apiClient, err := mobyclient.New(
		mobyclient.WithHost("http://docker.test"),
		mobyclient.WithHTTPClient(httpClient),
	)
	if err != nil {
		t.Fatalf("mobyclient.New() error = %v", err)
	}
	stats, err := (&Client{apiClient: apiClient}).Stats(t.Context(), "container-id")
	if err != nil {
		t.Fatalf("Stats() error = %v", err)
	}
	if stats.Timestamp != read || stats.Memory.UsageBytes != 512 {
		t.Errorf("Stats() = %#v", stats)
	}
}

func TestNormalizeEvent(t *testing.T) {
	t.Parallel()

	timestamp := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		message eventtypes.Message
		want    collector.LifecycleEvent
		found   bool
	}{
		{
			name:    "die includes exit code",
			message: eventtypes.Message{Action: "die", Actor: eventtypes.Actor{ID: "container-id", Attributes: map[string]string{"exitCode": "137"}}, TimeNano: timestamp.UnixNano()},
			want:    collector.LifecycleEvent{ContainerID: "container-id", Timestamp: timestamp, Type: collector.EventDie, ExitCode: int64Pointer(137)},
			found:   true,
		},
		{
			name:    "rename includes name",
			message: eventtypes.Message{Action: "rename", Actor: eventtypes.Actor{ID: "container-id", Attributes: map[string]string{"name": "api-v2"}}, Time: timestamp.Unix()},
			want:    collector.LifecycleEvent{ContainerID: "container-id", Timestamp: timestamp, Type: collector.EventRename, Name: "api-v2"},
			found:   true,
		},
		{
			name:    "untracked action ignored",
			message: eventtypes.Message{Action: "pause", Actor: eventtypes.Actor{ID: "container-id"}, Time: timestamp.Unix()},
		},
		{
			name:    "missing timestamp ignored",
			message: eventtypes.Message{Action: "start", Actor: eventtypes.Actor{ID: "container-id"}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, found := normalizeEvent(test.message)
			if found != test.found || !reflect.DeepEqual(got, test.want) {
				t.Errorf("normalizeEvent() = (%#v, %v), want (%#v, %v)", got, found, test.want, test.found)
			}
		})
	}
}

func uint64Pointer(value uint64) *uint64 {
	return &value
}

func int64Pointer(value int64) *int64 {
	return &value
}
