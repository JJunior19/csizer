// Package collector turns normalized runtime counters into local observations.
package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jorgeccarhuasaroni/containersize/internal/storage"
)

// DefaultSampleInterval is the active sampling baseline. Scheduling belongs to
// a later daemon phase.
const DefaultSampleInterval = 2 * time.Second

// StatsSource supplies normalized runtime counters for one container.
type StatsSource interface {
	Stats(context.Context, string) (RuntimeStats, error)
}

// Store persists collector observations in atomic batches.
type Store interface {
	WriteMetricSamples(context.Context, []storage.MetricSample) error
	WriteContainerEvents(context.Context, []storage.ContainerEvent) error
}

// Collector captures one-shot observations. It intentionally owns no background
// lifecycle, ticker, or activity classification.
type Collector struct {
	stats StatsSource
	store Store
}

// RuntimeStats is the collector-owned representation of one runtime sample.
type RuntimeStats struct {
	Timestamp   time.Time
	CPU         CPUCounters
	PreviousCPU *CPUCounters
	Memory      Memory
	PIDs        *uint64
	NetworkRx   *uint64
	NetworkTx   *uint64
	BlockRead   *uint64
	BlockWrite  *uint64
}

// CPUCounters are cumulative runtime counters used to calculate CPU cores.
type CPUCounters struct {
	TotalUsage  uint64
	SystemUsage uint64
	OnlineCPUs  uint32
}

// Memory retains Docker's raw cgroup usage and its reclaimable-cache counter.
type Memory struct {
	UsageBytes uint64
	CacheBytes *uint64
	LimitBytes *uint64
}

// MetricObservation is a normalized resource observation before persistence.
type MetricObservation struct {
	Timestamp        time.Time
	CPUUsageCores    float64
	CPUPercentHost   *float64
	MemoryUsage      int64
	MemoryCache      *int64
	MemoryWorkingSet int64
	MemoryLimit      *int64
	PIDs             *int64
	NetworkRx        *int64
	NetworkTx        *int64
	BlockRead        *int64
	BlockWrite       *int64
}

// EventType identifies a Docker lifecycle event relevant to collection.
type EventType string

const (
	EventStart   EventType = "start"
	EventStop    EventType = "stop"
	EventDie     EventType = "die"
	EventDestroy EventType = "destroy"
	EventOOM     EventType = "oom"
	EventRename  EventType = "rename"
)

// LifecycleEvent is a normalized Docker lifecycle observation.
type LifecycleEvent struct {
	ContainerID string
	Timestamp   time.Time
	Type        EventType
	ExitCode    *int64
	Name        string
}

// EventObservation associates a lifecycle event with already-resolved storage IDs.
type EventObservation struct {
	WorkloadID          int64
	ContainerInstanceID *int64
	Event               LifecycleEvent
}

// New creates a collector at the runtime and persistence seams.
func New(stats StatsSource, store Store) *Collector {
	return &Collector{stats: stats, store: store}
}

// CollectSample reads and persists one sample with an unknown activity state.
// Activity classification is intentionally deferred to a later phase.
func (collector *Collector) CollectSample(ctx context.Context, sessionID int64, containerID string) (MetricObservation, error) {
	if collector == nil || collector.stats == nil {
		return MetricObservation{}, errors.New("collect sample: stats source is nil")
	}
	if collector.store == nil {
		return MetricObservation{}, errors.New("collect sample: store is nil")
	}
	stats, err := collector.stats.Stats(ctx, containerID)
	if err != nil {
		return MetricObservation{}, fmt.Errorf("collect sample: read stats: %w", err)
	}
	observation, err := Observe(stats)
	if err != nil {
		return MetricObservation{}, fmt.Errorf("collect sample: normalize stats: %w", err)
	}
	if err := collector.store.WriteMetricSamples(ctx, []storage.MetricSample{observation.metricSample(sessionID)}); err != nil {
		return MetricObservation{}, fmt.Errorf("collect sample: persist sample: %w", err)
	}
	return observation, nil
}

// PersistEvents stores normalized lifecycle observations as one atomic batch.
func (collector *Collector) PersistEvents(ctx context.Context, observations []EventObservation) error {
	if collector == nil || collector.store == nil {
		return errors.New("persist events: store is nil")
	}
	events := make([]storage.ContainerEvent, 0, len(observations))
	for index, observation := range observations {
		event, err := observation.storageEvent()
		if err != nil {
			return fmt.Errorf("persist events: event %d: %w", index, err)
		}
		events = append(events, event)
	}
	if err := collector.store.WriteContainerEvents(ctx, events); err != nil {
		return fmt.Errorf("persist events: write batch: %w", err)
	}
	return nil
}

// Observe calculates CPU deltas and Docker memory semantics without I/O.
func Observe(stats RuntimeStats) (MetricObservation, error) {
	if stats.Timestamp.IsZero() {
		return MetricObservation{}, errors.New("sample timestamp is required")
	}
	usage, err := int64Value(stats.Memory.UsageBytes)
	if err != nil {
		return MetricObservation{}, fmt.Errorf("memory usage: %w", err)
	}
	cache, err := optionalInt64Value(stats.Memory.CacheBytes)
	if err != nil {
		return MetricObservation{}, fmt.Errorf("memory cache: %w", err)
	}
	limit, err := optionalInt64Value(stats.Memory.LimitBytes)
	if err != nil {
		return MetricObservation{}, fmt.Errorf("memory limit: %w", err)
	}
	pids, err := optionalInt64Value(stats.PIDs)
	if err != nil {
		return MetricObservation{}, fmt.Errorf("PIDs: %w", err)
	}
	networkRx, err := optionalInt64Value(stats.NetworkRx)
	if err != nil {
		return MetricObservation{}, fmt.Errorf("network receive: %w", err)
	}
	networkTx, err := optionalInt64Value(stats.NetworkTx)
	if err != nil {
		return MetricObservation{}, fmt.Errorf("network transmit: %w", err)
	}
	blockRead, err := optionalInt64Value(stats.BlockRead)
	if err != nil {
		return MetricObservation{}, fmt.Errorf("block read: %w", err)
	}
	blockWrite, err := optionalInt64Value(stats.BlockWrite)
	if err != nil {
		return MetricObservation{}, fmt.Errorf("block write: %w", err)
	}

	workingSet := usage
	if cache != nil && *cache < usage {
		workingSet -= *cache
	} else if cache != nil {
		workingSet = 0
	}
	cores, percent := cpuUsage(stats.CPU, stats.PreviousCPU)
	return MetricObservation{
		Timestamp:        stats.Timestamp.UTC(),
		CPUUsageCores:    cores,
		CPUPercentHost:   percent,
		MemoryUsage:      usage,
		MemoryCache:      cache,
		MemoryWorkingSet: workingSet,
		MemoryLimit:      limit,
		PIDs:             pids,
		NetworkRx:        networkRx,
		NetworkTx:        networkTx,
		BlockRead:        blockRead,
		BlockWrite:       blockWrite,
	}, nil
}

func (observation MetricObservation) metricSample(sessionID int64) storage.MetricSample {
	return storage.MetricSample{
		SessionID:             sessionID,
		Timestamp:             observation.Timestamp,
		CPUUsageCores:         observation.CPUUsageCores,
		CPUPercentHost:        observation.CPUPercentHost,
		MemoryUsageBytes:      observation.MemoryUsage,
		MemoryCacheBytes:      observation.MemoryCache,
		MemoryWorkingSetBytes: observation.MemoryWorkingSet,
		MemoryLimitBytes:      observation.MemoryLimit,
		PIDs:                  observation.PIDs,
		NetworkRxBytes:        observation.NetworkRx,
		NetworkTxBytes:        observation.NetworkTx,
		BlockReadBytes:        observation.BlockRead,
		BlockWriteBytes:       observation.BlockWrite,
		ActivityState:         "unknown",
	}
}

func (observation EventObservation) storageEvent() (storage.ContainerEvent, error) {
	if !validEventType(observation.Event.Type) {
		return storage.ContainerEvent{}, fmt.Errorf("unsupported event type %q", observation.Event.Type)
	}
	if observation.Event.Timestamp.IsZero() {
		return storage.ContainerEvent{}, errors.New("event timestamp is required")
	}
	var metadata *string
	if observation.Event.Name != "" {
		encoded, err := json.Marshal(struct {
			Name string `json:"name"`
		}{Name: observation.Event.Name})
		if err != nil {
			return storage.ContainerEvent{}, fmt.Errorf("encode event metadata: %w", err)
		}
		value := string(encoded)
		metadata = &value
	}
	return storage.ContainerEvent{
		WorkloadID:          observation.WorkloadID,
		ContainerInstanceID: observation.ContainerInstanceID,
		Timestamp:           observation.Event.Timestamp,
		EventType:           string(observation.Event.Type),
		ExitCode:            observation.Event.ExitCode,
		MetadataJSON:        metadata,
	}, nil
}

func cpuUsage(current CPUCounters, previous *CPUCounters) (float64, *float64) {
	if previous == nil || current.SystemUsage == 0 || previous.SystemUsage == 0 || current.OnlineCPUs == 0 ||
		current.TotalUsage < previous.TotalUsage || current.SystemUsage < previous.SystemUsage {
		return 0, nil
	}
	systemDelta := current.SystemUsage - previous.SystemUsage
	if systemDelta == 0 {
		return 0, nil
	}
	cores := float64(current.TotalUsage-previous.TotalUsage) / float64(systemDelta) * float64(current.OnlineCPUs)
	if math.IsNaN(cores) || math.IsInf(cores, 0) || cores < 0 {
		return 0, nil
	}
	percent := cores * 100
	return cores, &percent
}

func validEventType(eventType EventType) bool {
	switch eventType {
	case EventStart, EventStop, EventDie, EventDestroy, EventOOM, EventRename:
		return true
	default:
		return false
	}
}

func int64Value(value uint64) (int64, error) {
	if value > math.MaxInt64 {
		return 0, errors.New("value exceeds SQLite INTEGER range")
	}
	return int64(value), nil
}

func optionalInt64Value(value *uint64) (*int64, error) {
	if value == nil {
		return nil, nil
	}
	converted, err := int64Value(*value)
	if err != nil {
		return nil, err
	}
	return &converted, nil
}
