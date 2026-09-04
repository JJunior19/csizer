package storage

import (
	"errors"
	"time"
)

var (
	// ErrContainerWorkloadConflict means a physical container ID was already
	// registered to a different logical workload.
	ErrContainerWorkloadConflict = errors.New("container belongs to a different workload")
	// ErrContainerInstanceNotInWorkload means a requested container instance
	// does not exist under the tracking session's workload.
	ErrContainerInstanceNotInWorkload = errors.New("container instance does not belong to workload")
	// ErrContainerEventInstanceNotInWorkload means an event was associated with
	// a container instance outside its workload.
	ErrContainerEventInstanceNotInWorkload = errors.New("container event instance does not belong to workload")
	// ErrContainerTimestampRangeConflict means an incremental update would make
	// the effective stored stop timestamp precede the start timestamp.
	ErrContainerTimestampRangeConflict = errors.New("container timestamp range conflicts with stored values")
	// ErrNoMetricSamples means a workload has no persisted observations yet.
	ErrNoMetricSamples = errors.New("workload has no metric samples")
)

// Workload is a stable logical deployable unit.
type Workload struct {
	ID              int64     `json:"id"`
	WorkloadKey     string    `json:"workload_key"`
	DisplayName     string    `json:"display_name"`
	ComposeProject  string    `json:"compose_project,omitempty"`
	ComposeService  string    `json:"compose_service,omitempty"`
	ImageRepository string    `json:"image_repository,omitempty"`
	TrackingEnabled bool      `json:"tracking_enabled"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// WorkloadSettings contains persisted sampling and recommendation defaults.
type WorkloadSettings struct {
	WorkloadID                  int64
	ActiveSampleIntervalSeconds int
	IdleSampleIntervalSeconds   int
	RawRetentionDays            int
	StartupWindowSeconds        int
	DefaultProvider             *string
	DefaultProfile              string
}

// ContainerInstance identifies one physical container over its lifetime.
type ContainerInstance struct {
	ID              int64
	WorkloadID      int64
	ContainerID     string
	ContainerName   string
	ImageName       string
	ImageDigest     string
	Architecture    string
	OperatingSystem string
	DockerHostID    string
	StartedAt       *time.Time
	StoppedAt       *time.Time
	ExitCode        *int64
	OOMKilled       bool
	CreatedAt       time.Time
}

// TrackingSession identifies one observation window for a workload.
type TrackingSession struct {
	ID                  int64
	WorkloadID          int64
	ContainerInstanceID *int64
	StartedAt           time.Time
	EndedAt             *time.Time
	Status              string
	Label               string
	CollectorVersion    string
}

// ActiveTrackingSession is a running session joined with its current tracking state.
type ActiveTrackingSession struct {
	TrackingSession
	ContainerID     string
	TrackingEnabled bool
}

// MetricSample is one resource observation in a tracking session.
type MetricSample struct {
	ID                    int64
	SessionID             int64
	Timestamp             time.Time
	CPUUsageCores         float64
	CPUPercentHost        *float64
	MemoryUsageBytes      int64
	MemoryCacheBytes      *int64
	MemoryWorkingSetBytes int64
	MemoryLimitBytes      *int64
	PIDs                  *int64
	NetworkRxBytes        *int64
	NetworkTxBytes        *int64
	BlockReadBytes        *int64
	BlockWriteBytes       *int64
	ActivityState         string
}

// ContainerEvent is one normalized lifecycle observation for a container.
type ContainerEvent struct {
	ID                  int64
	WorkloadID          int64
	ContainerInstanceID *int64
	Timestamp           time.Time
	EventType           string
	ExitCode            *int64
	MetadataJSON        *string
}
