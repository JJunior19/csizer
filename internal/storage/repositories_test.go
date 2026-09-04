package storage

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWorkloadAndContainerRepositories(t *testing.T) {
	t.Parallel()

	store := openTestStore(t, filepath.Join(t.TempDir(), "repository tests", "database.db"))
	ctx := t.Context()
	workload, err := store.EnsureWorkload(ctx, Workload{
		WorkloadKey:     "compose:demo/api",
		DisplayName:     "Demo API",
		ComposeProject:  "demo",
		ComposeService:  "api",
		ImageRepository: "example/api",
	})
	if err != nil {
		t.Fatalf("EnsureWorkload(create) error = %v", err)
	}
	if workload.ID <= 0 || !workload.TrackingEnabled {
		t.Fatalf("created workload = %#v, want ID and default tracking enabled", workload)
	}
	settings, err := store.SettingsForWorkload(ctx, workload.ID)
	if err != nil {
		t.Fatalf("SettingsForWorkload() error = %v", err)
	}
	if settings.ActiveSampleIntervalSeconds != 2 ||
		settings.IdleSampleIntervalSeconds != 10 ||
		settings.RawRetentionDays != 30 ||
		settings.StartupWindowSeconds != 20 ||
		settings.DefaultProvider != nil ||
		settings.DefaultProfile != "balanced" {
		t.Errorf("default settings = %#v", settings)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE workloads SET tracking_enabled = 0 WHERE id = ?`, workload.ID); err != nil {
		t.Fatalf("disable tracking error = %v", err)
	}

	updated, err := store.EnsureWorkload(ctx, Workload{
		WorkloadKey:     workload.WorkloadKey,
		DisplayName:     "Renamed API",
		ImageRepository: "example/api-v2",
	})
	if err != nil {
		t.Fatalf("EnsureWorkload(update) error = %v", err)
	}
	if updated.ID != workload.ID || updated.TrackingEnabled {
		t.Errorf("identity refresh changed ID or tracking state: %#v", updated)
	}
	if updated.DisplayName != "Renamed API" || updated.ImageRepository != "example/api-v2" {
		t.Errorf("updated metadata = %#v", updated)
	}
	if updated.ComposeProject != "demo" || updated.ComposeService != "api" {
		t.Errorf("empty update erased Compose metadata: %#v", updated)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM workload_settings WHERE workload_id = ?`, workload.ID); err != nil {
		t.Fatalf("delete workload settings error = %v", err)
	}
	if _, err := store.EnsureWorkload(ctx, Workload{WorkloadKey: workload.WorkloadKey}); err != nil {
		t.Fatalf("EnsureWorkload(recreate settings) error = %v", err)
	}
	if _, err := store.SettingsForWorkload(ctx, workload.ID); err != nil {
		t.Fatalf("settings were not created for existing workload: %v", err)
	}

	startedAt := time.Date(2026, time.August, 3, 12, 30, 0, 123, time.FixedZone("offset", -5*60*60))
	exitCode := int64(137)
	instance, err := store.RegisterContainerInstance(ctx, ContainerInstance{
		WorkloadID:      workload.ID,
		ContainerID:     "physical-container-id",
		ContainerName:   "demo-api-1",
		ImageName:       "example/api:v1",
		ImageDigest:     "sha256:first",
		Architecture:    "arm64",
		OperatingSystem: "linux",
		DockerHostID:    "host-a",
		StartedAt:       &startedAt,
	})
	if err != nil {
		t.Fatalf("RegisterContainerInstance(create) error = %v", err)
	}
	createdAt := instance.CreatedAt
	stoppedAt := startedAt.Add(time.Hour)
	instance, err = store.RegisterContainerInstance(ctx, ContainerInstance{
		WorkloadID:    workload.ID,
		ContainerID:   "physical-container-id",
		ContainerName: "demo-api-renamed",
		ImageName:     "example/api:v2",
		StoppedAt:     &stoppedAt,
		ExitCode:      &exitCode,
		OOMKilled:     true,
	})
	if err != nil {
		t.Fatalf("RegisterContainerInstance(update) error = %v", err)
	}
	if instance.ContainerName != "demo-api-renamed" || instance.ImageName != "example/api:v2" {
		t.Errorf("container mutable metadata = %#v", instance)
	}
	if instance.ImageDigest != "sha256:first" || instance.Architecture != "arm64" ||
		instance.OperatingSystem != "linux" || instance.DockerHostID != "host-a" ||
		instance.StartedAt == nil || instance.StoppedAt == nil || instance.ExitCode == nil ||
		!instance.OOMKilled || instance.CreatedAt != createdAt {
		t.Errorf("container optional or historical metadata was not preserved: %#v", instance)
	}

	other, err := store.EnsureWorkload(ctx, Workload{WorkloadKey: "standalone:other"})
	if err != nil {
		t.Fatalf("EnsureWorkload(other) error = %v", err)
	}
	_, err = store.RegisterContainerInstance(ctx, ContainerInstance{
		WorkloadID:  other.ID,
		ContainerID: instance.ContainerID,
	})
	if !errors.Is(err, ErrContainerWorkloadConflict) {
		t.Fatalf("different-workload registration error = %v, want ErrContainerWorkloadConflict", err)
	}
	persisted, err := store.ContainerInstanceByContainerID(ctx, instance.ContainerID)
	if err != nil {
		t.Fatalf("ContainerInstanceByContainerID() error = %v", err)
	}
	if persisted.WorkloadID != workload.ID {
		t.Errorf("container workload ID = %d, want original %d", persisted.WorkloadID, workload.ID)
	}
}

func TestRegisterContainerInstanceEffectiveTimestampRange(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, time.January, 2, 10, 0, 0, 123, time.UTC)
	earlier := base.Add(-time.Hour)
	later := base.Add(time.Hour)
	tests := []struct {
		name         string
		initialStart *time.Time
		initialStop  *time.Time
		updateStart  *time.Time
		updateStop   *time.Time
		wantConflict bool
		wantStart    *time.Time
		wantStop     *time.Time
	}{
		{
			name:         "stored start then earlier stop rejected",
			initialStart: &base,
			updateStop:   &earlier,
			wantConflict: true,
			wantStart:    &base,
		},
		{
			name:         "stored stop then later start rejected",
			initialStop:  &base,
			updateStart:  &later,
			wantConflict: true,
			wantStop:     &base,
		},
		{
			name:         "stored start then later stop accepted",
			initialStart: &base,
			updateStop:   &later,
			wantStart:    &base,
			wantStop:     &later,
		},
		{
			name:        "stored stop then earlier start accepted",
			initialStop: &base,
			updateStart: &earlier,
			wantStart:   &earlier,
			wantStop:    &base,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store := openTestStore(t, filepath.Join(t.TempDir(), "effective range", "database.db"))
			workload, err := store.EnsureWorkload(t.Context(), Workload{WorkloadKey: "range-test"})
			if err != nil {
				t.Fatalf("EnsureWorkload() error = %v", err)
			}
			containerID := strings.ReplaceAll(test.name, " ", "-")
			_, err = store.RegisterContainerInstance(t.Context(), ContainerInstance{
				WorkloadID:    workload.ID,
				ContainerID:   containerID,
				ContainerName: "original-name",
				ImageName:     "example/image:test",
				StartedAt:     test.initialStart,
				StoppedAt:     test.initialStop,
			})
			if err != nil {
				t.Fatalf("RegisterContainerInstance(initial) error = %v", err)
			}
			_, err = store.RegisterContainerInstance(t.Context(), ContainerInstance{
				WorkloadID:    workload.ID,
				ContainerID:   containerID,
				ContainerName: "updated-name",
				StartedAt:     test.updateStart,
				StoppedAt:     test.updateStop,
			})
			if test.wantConflict {
				if !errors.Is(err, ErrContainerTimestampRangeConflict) {
					t.Fatalf("RegisterContainerInstance(update) error = %v, want ErrContainerTimestampRangeConflict", err)
				}
			} else if err != nil {
				t.Fatalf("RegisterContainerInstance(update) error = %v", err)
			}

			persisted, err := store.ContainerInstanceByContainerID(t.Context(), containerID)
			if err != nil {
				t.Fatalf("ContainerInstanceByContainerID() error = %v", err)
			}
			if !equalTimePointers(persisted.StartedAt, test.wantStart) || !equalTimePointers(persisted.StoppedAt, test.wantStop) {
				t.Errorf("persisted range = %v..%v, want %v..%v", persisted.StartedAt, persisted.StoppedAt, test.wantStart, test.wantStop)
			}
			wantName := "updated-name"
			if test.wantConflict {
				wantName = "original-name"
			}
			if persisted.ContainerName != wantName {
				t.Errorf("container name = %q, want %q", persisted.ContainerName, wantName)
			}
		})
	}
}

func TestRegisterContainerInstanceConcurrentConflictsAreDeterministic(t *testing.T) {
	t.Parallel()

	store := openTestStore(t, filepath.Join(t.TempDir(), "concurrent range", "database.db"))
	workload, err := store.EnsureWorkload(t.Context(), Workload{WorkloadKey: "concurrent-range"})
	if err != nil {
		t.Fatalf("EnsureWorkload() error = %v", err)
	}
	startedAt := time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)
	if _, err := store.RegisterContainerInstance(t.Context(), ContainerInstance{
		WorkloadID:    workload.ID,
		ContainerID:   "concurrent-container",
		ContainerName: "original-name",
		StartedAt:     &startedAt,
	}); err != nil {
		t.Fatalf("RegisterContainerInstance(initial) error = %v", err)
	}
	earlierStop := startedAt.Add(-time.Second)
	const attempts = 6
	errorsChannel := make(chan error, attempts)
	var group sync.WaitGroup
	for index := range attempts {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := store.RegisterContainerInstance(t.Context(), ContainerInstance{
				WorkloadID:    workload.ID,
				ContainerID:   "concurrent-container",
				ContainerName: fmt.Sprintf("rejected-name-%d", index),
				StoppedAt:     &earlierStop,
			})
			errorsChannel <- err
		}()
	}
	group.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if !errors.Is(err, ErrContainerTimestampRangeConflict) {
			t.Errorf("concurrent update error = %v, want ErrContainerTimestampRangeConflict", err)
		}
	}
	if _, err := store.RegisterContainerInstance(t.Context(), ContainerInstance{
		WorkloadID:  workload.ID,
		ContainerID: "concurrent-container",
		StoppedAt:   &earlierStop,
	}); !errors.Is(err, ErrContainerTimestampRangeConflict) {
		t.Fatalf("repeated update error = %v, want ErrContainerTimestampRangeConflict", err)
	}
	persisted, err := store.ContainerInstanceByContainerID(t.Context(), "concurrent-container")
	if err != nil {
		t.Fatalf("ContainerInstanceByContainerID() error = %v", err)
	}
	if persisted.ContainerName != "original-name" || persisted.StoppedAt != nil || !equalTimePointers(persisted.StartedAt, &startedAt) {
		t.Errorf("persisted instance changed after rejected updates: %#v", persisted)
	}
}

func TestCreateTrackingSessionSupportsOptionalInstanceAndMetadata(t *testing.T) {
	t.Parallel()

	store := openTestStore(t, filepath.Join(t.TempDir(), "session tests", "database.db"))
	workload, instance, _ := createSampleParents(t, store)
	startedAt := time.Date(2026, time.August, 3, 8, 0, 0, 456, time.FixedZone("offset", 3*60*60))
	instanceID := instance.ID
	session, err := store.CreateTrackingSession(t.Context(), TrackingSession{
		WorkloadID:          workload.ID,
		ContainerInstanceID: &instanceID,
		StartedAt:           startedAt,
		Status:              "running",
		Label:               "representative window",
		CollectorVersion:    "1.2.3",
	})
	if err != nil {
		t.Fatalf("CreateTrackingSession(instance) error = %v", err)
	}
	if session.ID <= 0 || session.StartedAt != startedAt.UTC() {
		t.Errorf("session = %#v", session)
	}
	withoutInstance, err := store.CreateTrackingSession(t.Context(), TrackingSession{
		WorkloadID: workload.ID,
		StartedAt:  startedAt.Add(time.Minute),
		Status:     "running",
	})
	if err != nil {
		t.Fatalf("CreateTrackingSession(nil instance) error = %v", err)
	}
	if withoutInstance.ContainerInstanceID != nil {
		t.Errorf("container instance ID = %v, want nil", withoutInstance.ContainerInstanceID)
	}
	if withoutInstance.CollectorVersion != "" {
		t.Errorf("collector version = %q, want empty", withoutInstance.CollectorVersion)
	}
	var collectorVersionIsNull bool
	if err := store.db.QueryRowContext(
		t.Context(),
		`SELECT collector_version IS NULL FROM tracking_sessions WHERE id = ?`,
		withoutInstance.ID,
	).Scan(&collectorVersionIsNull); err != nil {
		t.Fatalf("query collector version nullability error = %v", err)
	}
	if !collectorVersionIsNull {
		t.Error("collector_version was not stored as SQL NULL")
	}
}

func TestCreateTrackingSessionRequiresInstanceInWorkload(t *testing.T) {
	t.Parallel()

	store := openTestStore(t, filepath.Join(t.TempDir(), "session workload tests", "database.db"))
	firstWorkload, firstInstance, _ := createSampleParents(t, store)
	secondWorkload, err := store.EnsureWorkload(t.Context(), Workload{WorkloadKey: "compose:other/api"})
	if err != nil {
		t.Fatalf("EnsureWorkload(second) error = %v", err)
	}
	secondInstance, err := store.RegisterContainerInstance(t.Context(), ContainerInstance{
		WorkloadID:    secondWorkload.ID,
		ContainerID:   "other-container-id",
		ContainerName: "other-api-1",
		ImageName:     "example/other:test",
	})
	if err != nil {
		t.Fatalf("RegisterContainerInstance(second) error = %v", err)
	}

	baseCount := rowCount(t, store, "tracking_sessions")
	tests := []struct {
		name       string
		workloadID int64
		instanceID int64
		wantError  bool
	}{
		{name: "same workload succeeds", workloadID: firstWorkload.ID, instanceID: firstInstance.ID},
		{name: "different workload rejected", workloadID: firstWorkload.ID, instanceID: secondInstance.ID, wantError: true},
		{name: "missing instance rejected", workloadID: firstWorkload.ID, instanceID: 999999, wantError: true},
	}
	inserted := 0
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			instanceID := test.instanceID
			_, err := store.CreateTrackingSession(t.Context(), TrackingSession{
				WorkloadID:          test.workloadID,
				ContainerInstanceID: &instanceID,
				StartedAt:           time.Now(),
				Status:              "running",
			})
			if test.wantError {
				if !errors.Is(err, ErrContainerInstanceNotInWorkload) {
					t.Fatalf("CreateTrackingSession() error = %v, want ErrContainerInstanceNotInWorkload", err)
				}
				if got := rowCount(t, store, "tracking_sessions"); got != baseCount+inserted {
					t.Errorf("tracking session count = %d, want %d after rejected insert", got, baseCount+inserted)
				}
				return
			}
			if err != nil {
				t.Fatalf("CreateTrackingSession() error = %v", err)
			}
			inserted++
		})
	}
}

func TestTimestampValidationBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		value     time.Time
		wantError string
	}{
		{name: "year zero", value: time.Date(0, time.January, 1, 0, 0, 0, 0, time.UTC)},
		{name: "year 9999", value: time.Date(9999, time.December, 31, 23, 59, 59, 999999999, time.UTC)},
		{name: "negative year", value: time.Date(-1, time.December, 31, 0, 0, 0, 0, time.UTC), wantError: "between 0000 and 9999"},
		{name: "year 10000", value: time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC), wantError: "between 0000 and 9999"},
		{name: "zero value", value: time.Time{}, wantError: "is required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateRequiredTimestamp("timestamp", test.value)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("validateRequiredTimestamp() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("validateRequiredTimestamp() error = %v, want containing %q", err, test.wantError)
			}
		})
	}
}

func TestInvalidContainerTimestampsDoNotWrite(t *testing.T) {
	t.Parallel()

	validStart := time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC)
	beforeStart := validStart.Add(-time.Second)
	outOfRange := time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		instance  ContainerInstance
		wantError string
	}{
		{name: "stopped before started", instance: ContainerInstance{StartedAt: &validStart, StoppedAt: &beforeStart}, wantError: "stopped_at cannot precede started_at"},
		{name: "started year out of range", instance: ContainerInstance{StartedAt: &outOfRange}, wantError: "started_at year"},
		{name: "created year out of range", instance: ContainerInstance{CreatedAt: outOfRange}, wantError: "created_at year"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store := openTestStore(t, filepath.Join(t.TempDir(), "invalid container time", "database.db"))
			workload, err := store.EnsureWorkload(t.Context(), Workload{WorkloadKey: "time-test"})
			if err != nil {
				t.Fatalf("EnsureWorkload() error = %v", err)
			}
			instance := test.instance
			instance.WorkloadID = workload.ID
			instance.ContainerID = "invalid-time-container"
			_, err = store.RegisterContainerInstance(t.Context(), instance)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("RegisterContainerInstance() error = %v, want containing %q", err, test.wantError)
			}
			if got := rowCount(t, store, "container_instances"); got != 0 {
				t.Errorf("container instance count = %d, want 0", got)
			}
		})
	}
}

func TestInvalidTrackingTimestampsDoNotWrite(t *testing.T) {
	t.Parallel()

	validStart := time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC)
	beforeStart := validStart.Add(-time.Second)
	outOfRange := time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		startedAt time.Time
		endedAt   *time.Time
		wantError string
	}{
		{name: "ended before started", startedAt: validStart, endedAt: &beforeStart, wantError: "ended_at cannot precede started_at"},
		{name: "started year out of range", startedAt: outOfRange, wantError: "started_at year"},
		{name: "ended year out of range", startedAt: validStart, endedAt: &outOfRange, wantError: "ended_at year"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store := openTestStore(t, filepath.Join(t.TempDir(), "invalid session time", "database.db"))
			workload, err := store.EnsureWorkload(t.Context(), Workload{WorkloadKey: "time-test"})
			if err != nil {
				t.Fatalf("EnsureWorkload() error = %v", err)
			}
			_, err = store.CreateTrackingSession(t.Context(), TrackingSession{
				WorkloadID: workload.ID,
				StartedAt:  test.startedAt,
				EndedAt:    test.endedAt,
				Status:     "running",
			})
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("CreateTrackingSession() error = %v, want containing %q", err, test.wantError)
			}
			if got := rowCount(t, store, "tracking_sessions"); got != 0 {
				t.Errorf("tracking session count = %d, want 0", got)
			}
		})
	}
}

func TestMetricSampleBatchBehavior(t *testing.T) {
	t.Parallel()

	store := openTestStore(t, filepath.Join(t.TempDir(), "batch tests", "database.db"))
	_, _, session := createSampleParents(t, store)
	cpuPercentHost := 55.5
	memoryCache := int64(100_000)
	memoryLimit := int64(1_000_000)
	pids := int64(7)
	networkRx := int64(123)
	firstTime := time.Date(2026, time.August, 3, 10, 15, 30, 123456789, time.FixedZone("offset", 2*60*60))
	samples := []MetricSample{
		{
			SessionID:             session.ID,
			Timestamp:             firstTime,
			CPUUsageCores:         1.25,
			CPUPercentHost:        &cpuPercentHost,
			MemoryUsageBytes:      500_000,
			MemoryCacheBytes:      &memoryCache,
			MemoryWorkingSetBytes: 400_000,
			MemoryLimitBytes:      &memoryLimit,
			PIDs:                  &pids,
			NetworkRxBytes:        &networkRx,
			ActivityState:         "bursting",
		},
		{
			SessionID:             session.ID,
			Timestamp:             firstTime.Add(time.Second),
			CPUUsageCores:         0,
			MemoryUsageBytes:      300_000,
			MemoryWorkingSetBytes: 250_000,
			ActivityState:         "idle",
		},
	}
	if err := store.WriteMetricSamples(t.Context(), samples); err != nil {
		t.Fatalf("WriteMetricSamples() error = %v", err)
	}
	got, err := store.MetricSamplesForSession(t.Context(), session.ID)
	if err != nil {
		t.Fatalf("MetricSamplesForSession() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("sample count = %d, want 2", len(got))
	}
	if got[0].Timestamp != firstTime.UTC() || got[0].CPUPercentHost == nil ||
		got[0].MemoryCacheBytes == nil || got[0].MemoryLimitBytes == nil ||
		got[0].PIDs == nil || got[0].NetworkRxBytes == nil {
		t.Errorf("first sample = %#v", got[0])
	}
	if got[1].CPUPercentHost != nil || got[1].MemoryCacheBytes != nil ||
		got[1].MemoryLimitBytes != nil || got[1].PIDs != nil || got[1].NetworkRxBytes != nil {
		t.Errorf("NULL metrics decoded as values: %#v", got[1])
	}
	var storedTimestamp string
	if err := store.db.QueryRowContext(
		t.Context(),
		`SELECT timestamp FROM metric_samples WHERE id = ?`,
		got[0].ID,
	).Scan(&storedTimestamp); err != nil {
		t.Fatalf("query stored timestamp error = %v", err)
	}
	if storedTimestamp != firstTime.UTC().Format(time.RFC3339Nano) {
		t.Errorf("stored timestamp = %q, want %q", storedTimestamp, firstTime.UTC().Format(time.RFC3339Nano))
	}
	if err := store.WriteMetricSamples(t.Context(), nil); err != nil {
		t.Fatalf("WriteMetricSamples(empty) error = %v", err)
	}
	assertSampleCount(t, store, 2)
}

func TestMetricSamplesForWorkloadRespectsBoundaries(t *testing.T) {
	t.Parallel()

	store := openTestStore(t, filepath.Join(t.TempDir(), "workload samples", "database.db"))
	workload, _, session := createSampleParents(t, store)
	other, _, otherSession := createSampleParentsForWorkload(t, store, "compose:other/api", "other-container")
	base := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	if err := store.WriteMetricSamples(t.Context(), []MetricSample{
		{SessionID: session.ID, Timestamp: base, CPUUsageCores: 1, MemoryUsageBytes: 100, MemoryWorkingSetBytes: 80, ActivityState: "unknown"},
		{SessionID: session.ID, Timestamp: base.Add(time.Second), CPUUsageCores: 2, MemoryUsageBytes: 200, MemoryWorkingSetBytes: 160, ActivityState: "unknown"},
		{SessionID: session.ID, Timestamp: base.Add(2 * time.Second), CPUUsageCores: 3, MemoryUsageBytes: 300, MemoryWorkingSetBytes: 240, ActivityState: "unknown"},
		{SessionID: otherSession.ID, Timestamp: base.Add(time.Second), CPUUsageCores: 4, MemoryUsageBytes: 400, MemoryWorkingSetBytes: 320, ActivityState: "unknown"},
	}); err != nil {
		t.Fatalf("WriteMetricSamples() error = %v", err)
	}

	samples, err := store.MetricSamplesForWorkload(t.Context(), workload.ID, base.Add(time.Second), base.Add(2*time.Second))
	if err != nil {
		t.Fatalf("MetricSamplesForWorkload() error = %v", err)
	}
	if len(samples) != 2 || samples[0].Timestamp != base.Add(time.Second) || samples[1].Timestamp != base.Add(2*time.Second) {
		t.Fatalf("MetricSamplesForWorkload() = %#v", samples)
	}
	if samples[0].SessionID == otherSession.ID || other.ID == workload.ID {
		t.Errorf("returned samples are not scoped to workload %d: %#v", workload.ID, samples)
	}
	if _, err := store.MetricSamplesForWorkload(t.Context(), workload.ID, base.Add(time.Second), base); err == nil || !strings.Contains(err.Error(), "to cannot precede from") {
		t.Errorf("reverse boundaries error = %v", err)
	}
}

func TestMetricSamplesForWorkloadHandlesMixedFractionalTimestampPrecision(t *testing.T) {
	t.Parallel()

	store := openTestStore(t, filepath.Join(t.TempDir(), "fractional samples", "database.db"))
	workload, _, session := createSampleParents(t, store)
	base := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	if err := store.WriteMetricSamples(t.Context(), []MetricSample{
		{SessionID: session.ID, Timestamp: base.Add(500 * time.Millisecond), CPUUsageCores: 1, MemoryUsageBytes: 100, MemoryWorkingSetBytes: 80, ActivityState: "unknown"},
		{SessionID: session.ID, Timestamp: base, CPUUsageCores: 2, MemoryUsageBytes: 200, MemoryWorkingSetBytes: 160, ActivityState: "unknown"},
	}); err != nil {
		t.Fatalf("WriteMetricSamples() error = %v", err)
	}

	samples, err := store.MetricSamplesForWorkload(t.Context(), workload.ID, base, base)
	if err != nil {
		t.Fatalf("MetricSamplesForWorkload() error = %v", err)
	}
	if len(samples) != 1 || !samples[0].Timestamp.Equal(base) {
		t.Fatalf("MetricSamplesForWorkload() = %#v, want only the zero-fraction boundary", samples)
	}
}

func TestListWorkloadsAndTrackingState(t *testing.T) {
	t.Parallel()

	store := openTestStore(t, filepath.Join(t.TempDir(), "workload list", "database.db"))
	first, err := store.EnsureWorkload(t.Context(), Workload{WorkloadKey: "compose:demo/worker", DisplayName: "Worker"})
	if err != nil {
		t.Fatalf("EnsureWorkload(first) error = %v", err)
	}
	second, err := store.EnsureWorkload(t.Context(), Workload{WorkloadKey: "compose:demo/api", DisplayName: "API"})
	if err != nil {
		t.Fatalf("EnsureWorkload(second) error = %v", err)
	}
	updated, err := store.SetTrackingEnabled(t.Context(), first.ID, false)
	if err != nil {
		t.Fatalf("SetTrackingEnabled() error = %v", err)
	}
	if updated.TrackingEnabled {
		t.Errorf("updated tracking state = enabled, want disabled")
	}
	workloads, err := store.ListWorkloads(t.Context())
	if err != nil {
		t.Fatalf("ListWorkloads() error = %v", err)
	}
	if len(workloads) != 2 || workloads[0].ID != second.ID || workloads[1].ID != first.ID || workloads[0].DisplayName != "API" || workloads[1].TrackingEnabled {
		t.Errorf("ListWorkloads() = %#v", workloads)
	}
}

func TestMetricSampleBoundsForWorkload(t *testing.T) {
	t.Parallel()

	store := openTestStore(t, filepath.Join(t.TempDir(), "sample bounds", "database.db"))
	workload, _, session := createSampleParents(t, store)
	base := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	if _, _, err := store.MetricSampleBoundsForWorkload(t.Context(), workload.ID); !errors.Is(err, ErrNoMetricSamples) {
		t.Fatalf("MetricSampleBoundsForWorkload(empty) error = %v, want ErrNoMetricSamples", err)
	}
	if err := store.WriteMetricSamples(t.Context(), []MetricSample{
		{SessionID: session.ID, Timestamp: base.Add(time.Second), CPUUsageCores: 1, MemoryUsageBytes: 100, MemoryWorkingSetBytes: 80, ActivityState: "unknown"},
		{SessionID: session.ID, Timestamp: base, CPUUsageCores: 2, MemoryUsageBytes: 200, MemoryWorkingSetBytes: 160, ActivityState: "unknown"},
	}); err != nil {
		t.Fatalf("WriteMetricSamples() error = %v", err)
	}
	from, to, err := store.MetricSampleBoundsForWorkload(t.Context(), workload.ID)
	if err != nil || !from.Equal(base) || !to.Equal(base.Add(time.Second)) {
		t.Errorf("MetricSampleBoundsForWorkload() = %v, %v, %v", from, to, err)
	}
}

func TestMetricSampleBatchRollsBackOnValidationAndDatabaseErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		configure func(*testing.T, *Store)
		second    func(MetricSample) MetricSample
		wantError string
	}{
		{
			name: "validation error",
			second: func(sample MetricSample) MetricSample {
				sample.MemoryWorkingSetBytes = -1
				return sample
			},
			wantError: "memory working set bytes must be nonnegative",
		},
		{
			name: "timestamp year out of range",
			second: func(sample MetricSample) MetricSample {
				sample.Timestamp = time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)
				return sample
			},
			wantError: "timestamp year must be between 0000 and 9999",
		},
		{
			name: "foreign key error",
			second: func(sample MetricSample) MetricSample {
				sample.SessionID = 999999
				return sample
			},
			wantError: "FOREIGN KEY constraint failed",
		},
		{
			name: "database trigger error",
			configure: func(t *testing.T, store *Store) {
				t.Helper()
				_, err := store.db.ExecContext(t.Context(), `
CREATE TRIGGER reject_test_sample
BEFORE INSERT ON metric_samples
WHEN NEW.cpu_usage_cores = 99
BEGIN
    SELECT RAISE(ABORT, 'test database rejection');
END`)
				if err != nil {
					t.Fatalf("create trigger error = %v", err)
				}
			},
			second: func(sample MetricSample) MetricSample {
				sample.CPUUsageCores = 99
				return sample
			},
			wantError: "test database rejection",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store := openTestStore(t, filepath.Join(t.TempDir(), "rollback test", "database.db"))
			_, _, session := createSampleParents(t, store)
			if test.configure != nil {
				test.configure(t, store)
			}
			base := MetricSample{
				SessionID:             session.ID,
				Timestamp:             time.Now(),
				CPUUsageCores:         1,
				MemoryUsageBytes:      1,
				MemoryWorkingSetBytes: 1,
				ActivityState:         "active",
			}
			second := test.second(base)
			second.Timestamp = second.Timestamp.Add(time.Second)
			err := store.WriteMetricSamples(t.Context(), []MetricSample{base, second})
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("WriteMetricSamples() error = %v, want containing %q", err, test.wantError)
			}
			assertSampleCount(t, store, 0)
		})
	}
}

func TestMetricSampleValidation(t *testing.T) {
	t.Parallel()

	valid := MetricSample{
		SessionID:             1,
		Timestamp:             time.Now(),
		CPUUsageCores:         1,
		MemoryUsageBytes:      1,
		MemoryWorkingSetBytes: 1,
		ActivityState:         "unknown",
	}
	negativeInt := int64(-1)
	negativeFloat := -1.0
	tests := []struct {
		name      string
		mutate    func(*MetricSample)
		wantError string
	}{
		{name: "missing session", mutate: func(sample *MetricSample) { sample.SessionID = 0 }, wantError: "session ID"},
		{name: "missing timestamp", mutate: func(sample *MetricSample) { sample.Timestamp = time.Time{} }, wantError: "timestamp"},
		{name: "negative CPU", mutate: func(sample *MetricSample) { sample.CPUUsageCores = -1 }, wantError: "CPU usage cores"},
		{name: "negative host CPU", mutate: func(sample *MetricSample) { sample.CPUPercentHost = &negativeFloat }, wantError: "host CPU percent"},
		{name: "negative memory", mutate: func(sample *MetricSample) { sample.MemoryUsageBytes = -1 }, wantError: "memory usage bytes"},
		{name: "negative working set", mutate: func(sample *MetricSample) { sample.MemoryWorkingSetBytes = -1 }, wantError: "memory working set bytes"},
		{name: "negative optional metric", mutate: func(sample *MetricSample) { sample.PIDs = &negativeInt }, wantError: "PIDs"},
		{name: "empty activity", mutate: func(sample *MetricSample) { sample.ActivityState = "  " }, wantError: "activity state"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sample := valid
			test.mutate(&sample)
			if err := validateMetricSample(sample); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("validateMetricSample() error = %v, want containing %q", err, test.wantError)
			}
		})
	}
}

func TestDaemonLeaseCoordinatesOwnersAndExpires(t *testing.T) {
	t.Parallel()

	store := openTestStore(t, filepath.Join(t.TempDir(), "daemon-lease.db"))
	now := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	acquired, err := store.AcquireDaemonLease(t.Context(), "first", now, now.Add(time.Minute))
	if err != nil || !acquired {
		t.Fatalf("AcquireDaemonLease(first) = %v, %v; want true, nil", acquired, err)
	}
	acquired, err = store.AcquireDaemonLease(t.Context(), "second", now.Add(time.Second), now.Add(2*time.Minute))
	if err != nil || acquired {
		t.Fatalf("AcquireDaemonLease(second) = %v, %v; want false, nil", acquired, err)
	}
	acquired, err = store.AcquireDaemonLease(t.Context(), "second", now.Add(time.Minute), now.Add(2*time.Minute))
	if err != nil || !acquired {
		t.Fatalf("AcquireDaemonLease(expired second) = %v, %v; want true, nil", acquired, err)
	}
	if err := store.ReleaseDaemonLease(t.Context(), "first"); err != nil {
		t.Fatalf("ReleaseDaemonLease(first) error = %v", err)
	}
	acquired, err = store.AcquireDaemonLease(t.Context(), "third", now.Add(time.Minute), now.Add(3*time.Minute))
	if err != nil || acquired {
		t.Fatalf("AcquireDaemonLease(third) = %v, %v; want false, nil", acquired, err)
	}
}

func TestActiveTrackingSessionsIncludesDisabledWorkloadsAndExcludesEndedSessions(t *testing.T) {
	t.Parallel()

	store := openTestStore(t, filepath.Join(t.TempDir(), "active-sessions.db"))
	workload, instance, session := createSampleParents(t, store)
	if _, err := store.db.ExecContext(t.Context(), `UPDATE workloads SET tracking_enabled = 0 WHERE id = ?`, workload.ID); err != nil {
		t.Fatalf("disable workload error = %v", err)
	}
	sessions, err := store.ActiveTrackingSessions(t.Context())
	if err != nil {
		t.Fatalf("ActiveTrackingSessions() error = %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != session.ID || sessions[0].ContainerID != instance.ContainerID || sessions[0].TrackingEnabled {
		t.Fatalf("ActiveTrackingSessions() = %#v, want session %d for %q", sessions, session.ID, instance.ContainerID)
	}
	stopped, err := store.EndTrackingSession(t.Context(), session.ID, time.Now(), "stopped")
	if err != nil || !stopped {
		t.Fatalf("EndTrackingSession() = %v, %v; want true, nil", stopped, err)
	}
	sessions, err = store.ActiveTrackingSessions(t.Context())
	if err != nil {
		t.Fatalf("ActiveTrackingSessions() after stop error = %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("ActiveTrackingSessions() after stop = %#v, want none", sessions)
	}
}

func createSampleParents(t *testing.T, store *Store) (Workload, ContainerInstance, TrackingSession) {
	t.Helper()
	return createSampleParentsForWorkload(t, store, "compose:test/api", "container-id")
}

func createSampleParentsForWorkload(t *testing.T, store *Store, workloadKey, containerID string) (Workload, ContainerInstance, TrackingSession) {
	t.Helper()
	workload, err := store.EnsureWorkload(t.Context(), Workload{WorkloadKey: workloadKey})
	if err != nil {
		t.Fatalf("EnsureWorkload() error = %v", err)
	}
	instance, err := store.RegisterContainerInstance(t.Context(), ContainerInstance{
		WorkloadID:    workload.ID,
		ContainerID:   containerID,
		ContainerName: "test-api-1",
		ImageName:     "example/api:test",
	})
	if err != nil {
		t.Fatalf("RegisterContainerInstance() error = %v", err)
	}
	instanceID := instance.ID
	session, err := store.CreateTrackingSession(t.Context(), TrackingSession{
		WorkloadID:          workload.ID,
		ContainerInstanceID: &instanceID,
		StartedAt:           time.Now(),
		Status:              "running",
		CollectorVersion:    "test",
	})
	if err != nil {
		t.Fatalf("CreateTrackingSession() error = %v", err)
	}
	return workload, instance, session
}

func assertSampleCount(t *testing.T, store *Store, want int) {
	t.Helper()
	var got int
	if err := store.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM metric_samples`).Scan(&got); err != nil {
		t.Fatalf("query sample count error = %v", err)
	}
	if got != want {
		t.Errorf("sample count = %d, want %d", got, want)
	}
}

func rowCount(t *testing.T, store *Store, table string) int {
	t.Helper()
	var count int
	if err := store.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		t.Fatalf("query %s count error = %v", table, err)
	}
	return count
}

func equalTimePointers(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}
