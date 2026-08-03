package collector

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jorgeccarhuasaroni/containersize/internal/storage"
)

func TestObserveCPUAndMemorySemantics(t *testing.T) {
	t.Parallel()

	timestamp := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.FixedZone("offset", -4*60*60))
	cache := uint64(40)
	limit := uint64(256)
	tests := []struct {
		name        string
		stats       RuntimeStats
		wantCores   float64
		wantPercent *float64
		wantWorking int64
		wantCache   *int64
	}{
		{
			name:        "first sample has unknown CPU delta",
			stats:       RuntimeStats{Timestamp: timestamp, CPU: CPUCounters{TotalUsage: 100, SystemUsage: 1_000, OnlineCPUs: 2}, Memory: Memory{UsageBytes: 100}},
			wantWorking: 100,
		},
		{
			name:        "missing system counter has unknown CPU delta",
			stats:       RuntimeStats{Timestamp: timestamp, CPU: CPUCounters{TotalUsage: 200, OnlineCPUs: 2}, PreviousCPU: &CPUCounters{TotalUsage: 100, SystemUsage: 1_000}, Memory: Memory{UsageBytes: 100}},
			wantWorking: 100,
		},
		{
			name:        "counter reset has unknown CPU delta",
			stats:       RuntimeStats{Timestamp: timestamp, CPU: CPUCounters{TotalUsage: 50, SystemUsage: 1_100, OnlineCPUs: 2}, PreviousCPU: &CPUCounters{TotalUsage: 100, SystemUsage: 1_000}, Memory: Memory{UsageBytes: 100}},
			wantWorking: 100,
		},
		{
			name:        "zero system delta has unknown CPU delta",
			stats:       RuntimeStats{Timestamp: timestamp, CPU: CPUCounters{TotalUsage: 200, SystemUsage: 1_000, OnlineCPUs: 2}, PreviousCPU: &CPUCounters{TotalUsage: 100, SystemUsage: 1_000}, Memory: Memory{UsageBytes: 100}},
			wantWorking: 100,
		},
		{
			name:        "valid CPU and Docker cache working set",
			stats:       RuntimeStats{Timestamp: timestamp, CPU: CPUCounters{TotalUsage: 250, SystemUsage: 1_200, OnlineCPUs: 2}, PreviousCPU: &CPUCounters{TotalUsage: 100, SystemUsage: 1_000}, Memory: Memory{UsageBytes: 100, CacheBytes: &cache, LimitBytes: &limit}},
			wantCores:   1.5,
			wantPercent: pointer(150.0),
			wantWorking: 60,
			wantCache:   pointer(int64(40)),
		},
		{
			name:        "cache larger than usage clamps working set",
			stats:       RuntimeStats{Timestamp: timestamp, Memory: Memory{UsageBytes: 10, CacheBytes: &cache}},
			wantWorking: 0,
			wantCache:   pointer(int64(40)),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := Observe(test.stats)
			if err != nil {
				t.Fatalf("Observe() error = %v", err)
			}
			if got.Timestamp != timestamp.UTC() || got.CPUUsageCores != test.wantCores || got.MemoryWorkingSet != test.wantWorking || !reflect.DeepEqual(got.CPUPercentHost, test.wantPercent) || !reflect.DeepEqual(got.MemoryCache, test.wantCache) {
				t.Errorf("Observe() = %#v", got)
			}
		})
	}
}

func TestObserveRejectsOutOfRangeMemory(t *testing.T) {
	t.Parallel()

	_, err := Observe(RuntimeStats{Timestamp: time.Now(), Memory: Memory{UsageBytes: uint64(math.MaxInt64) + 1}})
	if err == nil || !strings.Contains(err.Error(), "SQLite INTEGER range") {
		t.Fatalf("Observe() error = %v, want SQLite range error", err)
	}
}

func TestCollectorPersistsSamplesAndEvents(t *testing.T) {
	t.Parallel()

	stats := RuntimeStats{Timestamp: time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC), Memory: Memory{UsageBytes: 64}}
	store := &fakeStore{}
	collector := New(fakeStatsSource{stats: stats}, store)
	observation, err := collector.CollectSample(t.Context(), 7, "container-id")
	if err != nil {
		t.Fatalf("CollectSample() error = %v", err)
	}
	if observation.MemoryUsage != 64 || len(store.samples) != 1 || store.samples[0].SessionID != 7 || store.samples[0].ActivityState != "unknown" {
		t.Errorf("stored sample = %#v", store.samples)
	}

	instanceID := int64(8)
	exitCode := int64(137)
	if err := collector.PersistEvents(t.Context(), []EventObservation{{
		WorkloadID:          3,
		ContainerInstanceID: &instanceID,
		Event:               LifecycleEvent{Timestamp: stats.Timestamp, Type: EventRename, Name: "api-v2", ExitCode: &exitCode},
	}}); err != nil {
		t.Fatalf("PersistEvents() error = %v", err)
	}
	if len(store.events) != 1 || store.events[0].MetadataJSON == nil || *store.events[0].MetadataJSON != `{"name":"api-v2"}` || store.events[0].ExitCode == nil || *store.events[0].ExitCode != exitCode {
		t.Errorf("stored events = %#v", store.events)
	}
	var metadata map[string]string
	if err := json.Unmarshal([]byte(*store.events[0].MetadataJSON), &metadata); err != nil || metadata["name"] != "api-v2" {
		t.Errorf("event metadata = %q, decode error = %v", *store.events[0].MetadataJSON, err)
	}
}

func TestPersistEventsRejectsUnsupportedType(t *testing.T) {
	t.Parallel()

	store := &fakeStore{}
	err := New(fakeStatsSource{}, store).PersistEvents(t.Context(), []EventObservation{{
		Event: LifecycleEvent{Timestamp: time.Now(), Type: "pause"},
	}})
	if err == nil || len(store.events) != 0 {
		t.Fatalf("PersistEvents() error = %v, events = %#v", err, store.events)
	}
}

type fakeStatsSource struct {
	stats RuntimeStats
	err   error
}

func (source fakeStatsSource) Stats(context.Context, string) (RuntimeStats, error) {
	return source.stats, source.err
}

type fakeStore struct {
	samples []storage.MetricSample
	events  []storage.ContainerEvent
}

func (store *fakeStore) WriteMetricSamples(_ context.Context, samples []storage.MetricSample) error {
	store.samples = append(store.samples, samples...)
	return nil
}

func (store *fakeStore) WriteContainerEvents(_ context.Context, events []storage.ContainerEvent) error {
	store.events = append(store.events, events...)
	return nil
}

func pointer[T any](value T) *T {
	return &value
}
