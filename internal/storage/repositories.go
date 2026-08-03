package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// EnsureWorkload creates a workload or refreshes its mutable identity metadata.
// Existing tracking state is never changed by an identity refresh.
func (store *Store) EnsureWorkload(ctx context.Context, workload Workload) (Workload, error) {
	workload.WorkloadKey = strings.TrimSpace(workload.WorkloadKey)
	if workload.WorkloadKey == "" {
		return Workload{}, errors.New("ensure workload: workload key is required")
	}
	displayName := strings.TrimSpace(workload.DisplayName)
	if displayName == "" {
		displayName = workload.WorkloadKey
	}
	now := formatTimestamp(time.Now())

	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return Workload{}, fmt.Errorf("ensure workload: begin transaction: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	const upsert = `
INSERT INTO workloads (
    workload_key, display_name, compose_project, compose_service,
    image_repository, created_at, updated_at
) VALUES (?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?)
ON CONFLICT(workload_key) DO UPDATE SET
    display_name = COALESCE(NULLIF(?, ''), workloads.display_name),
    compose_project = COALESCE(NULLIF(?, ''), workloads.compose_project),
    compose_service = COALESCE(NULLIF(?, ''), workloads.compose_service),
    image_repository = COALESCE(NULLIF(?, ''), workloads.image_repository),
    updated_at = ?`
	if _, err := transaction.ExecContext(
		ctx,
		upsert,
		workload.WorkloadKey,
		displayName,
		strings.TrimSpace(workload.ComposeProject),
		strings.TrimSpace(workload.ComposeService),
		strings.TrimSpace(workload.ImageRepository),
		now,
		now,
		strings.TrimSpace(workload.DisplayName),
		strings.TrimSpace(workload.ComposeProject),
		strings.TrimSpace(workload.ComposeService),
		strings.TrimSpace(workload.ImageRepository),
		now,
	); err != nil {
		return Workload{}, fmt.Errorf("ensure workload: upsert: %w", err)
	}

	var workloadID int64
	if err := transaction.QueryRowContext(
		ctx,
		`SELECT id FROM workloads WHERE workload_key = ?`,
		workload.WorkloadKey,
	).Scan(&workloadID); err != nil {
		return Workload{}, fmt.Errorf("ensure workload: read ID: %w", err)
	}
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO workload_settings(workload_id) VALUES (?)
         ON CONFLICT(workload_id) DO NOTHING`,
		workloadID,
	); err != nil {
		return Workload{}, fmt.Errorf("ensure workload: create settings: %w", err)
	}

	result, err := queryWorkload(ctx, transaction, workload.WorkloadKey)
	if err != nil {
		return Workload{}, fmt.Errorf("ensure workload: read result: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return Workload{}, fmt.Errorf("ensure workload: commit: %w", err)
	}
	return result, nil
}

// WorkloadByKey returns a workload by its stable key.
func (store *Store) WorkloadByKey(ctx context.Context, workloadKey string) (Workload, error) {
	workload, err := queryWorkload(ctx, store.db, workloadKey)
	if err != nil {
		return Workload{}, fmt.Errorf("get workload %q: %w", workloadKey, err)
	}
	return workload, nil
}

// SettingsForWorkload returns one workload's persisted defaults.
func (store *Store) SettingsForWorkload(ctx context.Context, workloadID int64) (WorkloadSettings, error) {
	var (
		settings        WorkloadSettings
		defaultProvider sql.NullString
	)
	err := store.db.QueryRowContext(
		ctx,
		`SELECT workload_id, active_sample_interval_seconds, idle_sample_interval_seconds,
                raw_retention_days, startup_window_seconds, default_provider, default_profile
         FROM workload_settings WHERE workload_id = ?`,
		workloadID,
	).Scan(
		&settings.WorkloadID,
		&settings.ActiveSampleIntervalSeconds,
		&settings.IdleSampleIntervalSeconds,
		&settings.RawRetentionDays,
		&settings.StartupWindowSeconds,
		&defaultProvider,
		&settings.DefaultProfile,
	)
	if err != nil {
		return WorkloadSettings{}, fmt.Errorf("get workload settings: %w", err)
	}
	settings.DefaultProvider = stringPointer(defaultProvider)
	return settings, nil
}

// RegisterContainerInstance creates or refreshes a physical container record.
func (store *Store) RegisterContainerInstance(ctx context.Context, instance ContainerInstance) (ContainerInstance, error) {
	instance.ContainerID = strings.TrimSpace(instance.ContainerID)
	if instance.WorkloadID <= 0 {
		return ContainerInstance{}, errors.New("register container instance: workload ID is required")
	}
	if instance.ContainerID == "" {
		return ContainerInstance{}, errors.New("register container instance: container ID is required")
	}
	createdAt := instance.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	if err := validateRequiredTimestamp("created_at", createdAt); err != nil {
		return ContainerInstance{}, fmt.Errorf("register container instance: %w", err)
	}
	if err := validateOptionalTimestamp("started_at", instance.StartedAt); err != nil {
		return ContainerInstance{}, fmt.Errorf("register container instance: %w", err)
	}
	if err := validateOptionalTimestamp("stopped_at", instance.StoppedAt); err != nil {
		return ContainerInstance{}, fmt.Errorf("register container instance: %w", err)
	}
	if instance.StartedAt != nil && instance.StoppedAt != nil && instance.StoppedAt.Before(*instance.StartedAt) {
		return ContainerInstance{}, errors.New("register container instance: stopped_at cannot precede started_at")
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return ContainerInstance{}, fmt.Errorf("register container instance: begin transaction: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	const upsert = `
INSERT INTO container_instances (
    workload_id, container_id, container_name, image_name, image_digest,
    architecture, operating_system, docker_host_id, started_at, stopped_at,
    exit_code, oom_killed, created_at
) VALUES (?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?, ?)
ON CONFLICT(container_id) DO UPDATE SET
    container_name = COALESCE(NULLIF(excluded.container_name, ''), container_instances.container_name),
    image_name = COALESCE(NULLIF(excluded.image_name, ''), container_instances.image_name),
    image_digest = COALESCE(excluded.image_digest, container_instances.image_digest),
    architecture = COALESCE(excluded.architecture, container_instances.architecture),
    operating_system = COALESCE(excluded.operating_system, container_instances.operating_system),
    docker_host_id = COALESCE(excluded.docker_host_id, container_instances.docker_host_id),
    started_at = COALESCE(excluded.started_at, container_instances.started_at),
    stopped_at = COALESCE(excluded.stopped_at, container_instances.stopped_at),
    exit_code = COALESCE(excluded.exit_code, container_instances.exit_code),
    oom_killed = CASE WHEN excluded.oom_killed = 1 THEN 1 ELSE container_instances.oom_killed END
WHERE container_instances.workload_id = excluded.workload_id
  AND (
      COALESCE(excluded.started_at, container_instances.started_at) IS NULL
      OR COALESCE(excluded.stopped_at, container_instances.stopped_at) IS NULL
      OR COALESCE(excluded.stopped_at, container_instances.stopped_at) >=
         COALESCE(excluded.started_at, container_instances.started_at)
  )`
	result, err := transaction.ExecContext(
		ctx,
		upsert,
		instance.WorkloadID,
		instance.ContainerID,
		strings.TrimSpace(instance.ContainerName),
		strings.TrimSpace(instance.ImageName),
		strings.TrimSpace(instance.ImageDigest),
		strings.TrimSpace(instance.Architecture),
		strings.TrimSpace(instance.OperatingSystem),
		strings.TrimSpace(instance.DockerHostID),
		nullableTimestamp(instance.StartedAt),
		nullableTimestamp(instance.StoppedAt),
		instance.ExitCode,
		instance.OOMKilled,
		formatTimestamp(createdAt),
	)
	if err != nil {
		return ContainerInstance{}, fmt.Errorf("register container instance: upsert: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return ContainerInstance{}, fmt.Errorf("register container instance: rows affected: %w", err)
	}
	if affected == 0 {
		var existingWorkloadID int64
		var existingStartedAt, existingStoppedAt sql.NullString
		if err := transaction.QueryRowContext(
			ctx,
			`SELECT workload_id, started_at, stopped_at
             FROM container_instances WHERE container_id = ?`,
			instance.ContainerID,
		).Scan(&existingWorkloadID, &existingStartedAt, &existingStoppedAt); err != nil {
			return ContainerInstance{}, fmt.Errorf(
				"register container instance %q: read rejected existing row: %w",
				instance.ContainerID,
				err,
			)
		}
		if existingWorkloadID != instance.WorkloadID {
			return ContainerInstance{}, fmt.Errorf(
				"register container instance %q: %w",
				instance.ContainerID,
				ErrContainerWorkloadConflict,
			)
		}
		return ContainerInstance{}, fmt.Errorf(
			"register container instance %q with stored range %q..%q: %w",
			instance.ContainerID,
			existingStartedAt.String,
			existingStoppedAt.String,
			ErrContainerTimestampRangeConflict,
		)
	}
	if err := transaction.Commit(); err != nil {
		return ContainerInstance{}, fmt.Errorf("register container instance: commit: %w", err)
	}
	return store.ContainerInstanceByContainerID(ctx, instance.ContainerID)
}

// ContainerInstanceByContainerID returns the persisted physical container.
func (store *Store) ContainerInstanceByContainerID(ctx context.Context, containerID string) (ContainerInstance, error) {
	var (
		instance                                      ContainerInstance
		imageDigest, architecture, operatingSystem    sql.NullString
		dockerHostID, startedAt, stoppedAt, createdAt sql.NullString
		exitCode                                      sql.NullInt64
	)
	err := store.db.QueryRowContext(
		ctx,
		`SELECT id, workload_id, container_id, container_name, image_name,
                image_digest, architecture, operating_system, docker_host_id,
                started_at, stopped_at, exit_code, oom_killed, created_at
         FROM container_instances WHERE container_id = ?`,
		containerID,
	).Scan(
		&instance.ID,
		&instance.WorkloadID,
		&instance.ContainerID,
		&instance.ContainerName,
		&instance.ImageName,
		&imageDigest,
		&architecture,
		&operatingSystem,
		&dockerHostID,
		&startedAt,
		&stoppedAt,
		&exitCode,
		&instance.OOMKilled,
		&createdAt,
	)
	if err != nil {
		return ContainerInstance{}, fmt.Errorf("get container instance %q: %w", containerID, err)
	}
	instance.ImageDigest = imageDigest.String
	instance.Architecture = architecture.String
	instance.OperatingSystem = operatingSystem.String
	instance.DockerHostID = dockerHostID.String
	instance.ExitCode = int64Pointer(exitCode)
	if instance.StartedAt, err = parseNullableTimestamp(startedAt); err != nil {
		return ContainerInstance{}, fmt.Errorf("get container instance %q: started_at: %w", containerID, err)
	}
	if instance.StoppedAt, err = parseNullableTimestamp(stoppedAt); err != nil {
		return ContainerInstance{}, fmt.Errorf("get container instance %q: stopped_at: %w", containerID, err)
	}
	if !createdAt.Valid {
		return ContainerInstance{}, fmt.Errorf("get container instance %q: created_at is NULL", containerID)
	}
	instance.CreatedAt, err = parseTimestamp(createdAt.String)
	if err != nil {
		return ContainerInstance{}, fmt.Errorf("get container instance %q: created_at: %w", containerID, err)
	}
	return instance, nil
}

// CreateTrackingSession starts a persisted observation window.
func (store *Store) CreateTrackingSession(ctx context.Context, session TrackingSession) (TrackingSession, error) {
	if session.WorkloadID <= 0 {
		return TrackingSession{}, errors.New("create tracking session: workload ID is required")
	}
	if session.ContainerInstanceID != nil && *session.ContainerInstanceID <= 0 {
		return TrackingSession{}, errors.New("create tracking session: container instance ID must be positive")
	}
	if err := validateRequiredTimestamp("started_at", session.StartedAt); err != nil {
		return TrackingSession{}, fmt.Errorf("create tracking session: %w", err)
	}
	if err := validateOptionalTimestamp("ended_at", session.EndedAt); err != nil {
		return TrackingSession{}, fmt.Errorf("create tracking session: %w", err)
	}
	if session.EndedAt != nil && session.EndedAt.Before(session.StartedAt) {
		return TrackingSession{}, errors.New("create tracking session: ended_at cannot precede started_at")
	}
	session.Status = strings.TrimSpace(session.Status)
	if session.Status == "" {
		return TrackingSession{}, errors.New("create tracking session: status is required")
	}
	session.CollectorVersion = strings.TrimSpace(session.CollectorVersion)

	result, err := store.db.ExecContext(
		ctx,
		`INSERT INTO tracking_sessions(
             workload_id, container_instance_id, started_at, ended_at,
             status, label, collector_version
		 )
		 SELECT ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, '')
		 WHERE ? IS NULL OR EXISTS (
		     SELECT 1 FROM container_instances
		     WHERE id = ? AND workload_id = ?
		 )`,
		session.WorkloadID,
		session.ContainerInstanceID,
		formatTimestamp(session.StartedAt),
		nullableTimestamp(session.EndedAt),
		session.Status,
		strings.TrimSpace(session.Label),
		session.CollectorVersion,
		session.ContainerInstanceID,
		session.ContainerInstanceID,
		session.WorkloadID,
	)
	if err != nil {
		return TrackingSession{}, fmt.Errorf("create tracking session: insert: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return TrackingSession{}, fmt.Errorf("create tracking session: rows affected: %w", err)
	}
	if affected == 0 {
		return TrackingSession{}, fmt.Errorf("create tracking session: %w", ErrContainerInstanceNotInWorkload)
	}
	session.ID, err = result.LastInsertId()
	if err != nil {
		return TrackingSession{}, fmt.Errorf("create tracking session: read ID: %w", err)
	}
	session.StartedAt = session.StartedAt.UTC()
	if session.EndedAt != nil {
		endedAt := session.EndedAt.UTC()
		session.EndedAt = &endedAt
	}
	return session, nil
}

// WriteMetricSamples writes a validated batch atomically with one prepared statement.
func (store *Store) WriteMetricSamples(ctx context.Context, samples []MetricSample) (err error) {
	if len(samples) == 0 {
		return nil
	}
	for index, sample := range samples {
		if err := validateMetricSample(sample); err != nil {
			return fmt.Errorf("write metric samples: sample %d: %w", index, err)
		}
	}

	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("write metric samples: begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = transaction.Rollback()
		}
	}()

	statement, err := transaction.PrepareContext(ctx, `
INSERT INTO metric_samples (
    session_id, timestamp, cpu_usage_cores, cpu_percent_host,
    memory_usage_bytes, memory_cache_bytes, memory_working_set_bytes,
    memory_limit_bytes, pids, network_rx_bytes, network_tx_bytes,
    block_read_bytes, block_write_bytes, activity_state
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("write metric samples: prepare insert: %w", err)
	}
	defer func() { _ = statement.Close() }()

	for index, sample := range samples {
		if _, err = statement.ExecContext(
			ctx,
			sample.SessionID,
			formatTimestamp(sample.Timestamp),
			sample.CPUUsageCores,
			sample.CPUPercentHost,
			sample.MemoryUsageBytes,
			sample.MemoryCacheBytes,
			sample.MemoryWorkingSetBytes,
			sample.MemoryLimitBytes,
			sample.PIDs,
			sample.NetworkRxBytes,
			sample.NetworkTxBytes,
			sample.BlockReadBytes,
			sample.BlockWriteBytes,
			strings.TrimSpace(sample.ActivityState),
		); err != nil {
			return fmt.Errorf("write metric samples: insert sample %d: %w", index, err)
		}
	}
	if err = statement.Close(); err != nil {
		return fmt.Errorf("write metric samples: close statement: %w", err)
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("write metric samples: commit: %w", err)
	}
	return nil
}

// WriteContainerEvents writes a validated lifecycle-event batch atomically.
func (store *Store) WriteContainerEvents(ctx context.Context, events []ContainerEvent) (err error) {
	if len(events) == 0 {
		return nil
	}
	for index, event := range events {
		if err := validateContainerEvent(event); err != nil {
			return fmt.Errorf("write container events: event %d: %w", index, err)
		}
	}

	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("write container events: begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = transaction.Rollback()
		}
	}()

	statement, err := transaction.PrepareContext(ctx, `
INSERT INTO container_events (
    workload_id, container_instance_id, timestamp, event_type, exit_code, metadata_json
) SELECT ?, ?, ?, ?, ?, ?
WHERE ? IS NULL OR EXISTS (
    SELECT 1 FROM container_instances WHERE id = ? AND workload_id = ?
)`)
	if err != nil {
		return fmt.Errorf("write container events: prepare insert: %w", err)
	}
	defer func() { _ = statement.Close() }()

	for index, event := range events {
		result, execErr := statement.ExecContext(
			ctx,
			event.WorkloadID,
			event.ContainerInstanceID,
			formatTimestamp(event.Timestamp),
			strings.TrimSpace(event.EventType),
			event.ExitCode,
			event.MetadataJSON,
			event.ContainerInstanceID,
			event.ContainerInstanceID,
			event.WorkloadID,
		)
		if execErr != nil {
			return fmt.Errorf("write container events: insert event %d: %w", index, execErr)
		}
		affected, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return fmt.Errorf("write container events: rows affected for event %d: %w", index, rowsErr)
		}
		if affected == 0 {
			return fmt.Errorf("write container events: event %d: %w", index, ErrContainerEventInstanceNotInWorkload)
		}
	}
	if err = statement.Close(); err != nil {
		return fmt.Errorf("write container events: close statement: %w", err)
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("write container events: commit: %w", err)
	}
	return nil
}

// MetricSamplesForSession returns samples in observation order.
func (store *Store) MetricSamplesForSession(ctx context.Context, sessionID int64) ([]MetricSample, error) {
	rows, err := store.db.QueryContext(ctx, `
SELECT id, session_id, timestamp, cpu_usage_cores, cpu_percent_host,
       memory_usage_bytes, memory_cache_bytes, memory_working_set_bytes,
       memory_limit_bytes, pids, network_rx_bytes, network_tx_bytes,
       block_read_bytes, block_write_bytes, activity_state
FROM metric_samples
WHERE session_id = ?
ORDER BY timestamp, id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list metric samples: %w", err)
	}
	defer func() { _ = rows.Close() }()

	samples := make([]MetricSample, 0)
	for rows.Next() {
		var (
			sample                                      MetricSample
			timestamp                                   string
			cpuPercentHost                              sql.NullFloat64
			memoryCache, memoryLimit, pids              sql.NullInt64
			networkRx, networkTx, blockRead, blockWrite sql.NullInt64
		)
		if err := rows.Scan(
			&sample.ID,
			&sample.SessionID,
			&timestamp,
			&sample.CPUUsageCores,
			&cpuPercentHost,
			&sample.MemoryUsageBytes,
			&memoryCache,
			&sample.MemoryWorkingSetBytes,
			&memoryLimit,
			&pids,
			&networkRx,
			&networkTx,
			&blockRead,
			&blockWrite,
			&sample.ActivityState,
		); err != nil {
			return nil, fmt.Errorf("scan metric sample: %w", err)
		}
		sample.Timestamp, err = parseTimestamp(timestamp)
		if err != nil {
			return nil, fmt.Errorf("scan metric sample timestamp: %w", err)
		}
		sample.CPUPercentHost = float64Pointer(cpuPercentHost)
		sample.MemoryCacheBytes = int64Pointer(memoryCache)
		sample.MemoryLimitBytes = int64Pointer(memoryLimit)
		sample.PIDs = int64Pointer(pids)
		sample.NetworkRxBytes = int64Pointer(networkRx)
		sample.NetworkTxBytes = int64Pointer(networkTx)
		sample.BlockReadBytes = int64Pointer(blockRead)
		sample.BlockWriteBytes = int64Pointer(blockWrite)
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate metric samples: %w", err)
	}
	return samples, nil
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func queryWorkload(ctx context.Context, queryer queryRower, workloadKey string) (Workload, error) {
	var (
		workload                       Workload
		composeProject, composeService sql.NullString
		imageRepository                sql.NullString
		createdAt, updatedAt           string
	)
	err := queryer.QueryRowContext(
		ctx,
		`SELECT id, workload_key, display_name, compose_project, compose_service,
                image_repository, tracking_enabled, created_at, updated_at
         FROM workloads WHERE workload_key = ?`,
		workloadKey,
	).Scan(
		&workload.ID,
		&workload.WorkloadKey,
		&workload.DisplayName,
		&composeProject,
		&composeService,
		&imageRepository,
		&workload.TrackingEnabled,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return Workload{}, err
	}
	workload.ComposeProject = composeProject.String
	workload.ComposeService = composeService.String
	workload.ImageRepository = imageRepository.String
	workload.CreatedAt, err = parseTimestamp(createdAt)
	if err != nil {
		return Workload{}, fmt.Errorf("created_at: %w", err)
	}
	workload.UpdatedAt, err = parseTimestamp(updatedAt)
	if err != nil {
		return Workload{}, fmt.Errorf("updated_at: %w", err)
	}
	return workload, nil
}

func validateMetricSample(sample MetricSample) error {
	if sample.SessionID <= 0 {
		return errors.New("session ID is required")
	}
	if err := validateRequiredTimestamp("timestamp", sample.Timestamp); err != nil {
		return err
	}
	if !nonnegativeFinite(sample.CPUUsageCores) {
		return errors.New("CPU usage cores must be finite and nonnegative")
	}
	if sample.MemoryUsageBytes < 0 {
		return errors.New("memory usage bytes must be nonnegative")
	}
	if sample.MemoryWorkingSetBytes < 0 {
		return errors.New("memory working set bytes must be nonnegative")
	}
	if strings.TrimSpace(sample.ActivityState) == "" {
		return errors.New("activity state is required")
	}
	if sample.CPUPercentHost != nil && !nonnegativeFinite(*sample.CPUPercentHost) {
		return errors.New("host CPU percent must be finite and nonnegative")
	}
	optionalMetrics := []struct {
		name  string
		value *int64
	}{
		{name: "memory cache bytes", value: sample.MemoryCacheBytes},
		{name: "memory limit bytes", value: sample.MemoryLimitBytes},
		{name: "PIDs", value: sample.PIDs},
		{name: "network receive bytes", value: sample.NetworkRxBytes},
		{name: "network transmit bytes", value: sample.NetworkTxBytes},
		{name: "block read bytes", value: sample.BlockReadBytes},
		{name: "block write bytes", value: sample.BlockWriteBytes},
	}
	for _, metric := range optionalMetrics {
		if metric.value != nil && *metric.value < 0 {
			return fmt.Errorf("%s must be nonnegative", metric.name)
		}
	}
	return nil
}

func validateContainerEvent(event ContainerEvent) error {
	if event.WorkloadID <= 0 {
		return errors.New("workload ID is required")
	}
	if event.ContainerInstanceID != nil && *event.ContainerInstanceID <= 0 {
		return errors.New("container instance ID must be positive")
	}
	if err := validateRequiredTimestamp("timestamp", event.Timestamp); err != nil {
		return err
	}
	if strings.TrimSpace(event.EventType) == "" {
		return errors.New("event type is required")
	}
	if event.MetadataJSON != nil && !json.Valid([]byte(*event.MetadataJSON)) {
		return errors.New("metadata JSON must be valid")
	}
	return nil
}

func nonnegativeFinite(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func validateRequiredTimestamp(name string, value time.Time) error {
	if value.IsZero() {
		return fmt.Errorf("%s is required", name)
	}
	return validateRepresentableTimestamp(name, value)
}

func validateOptionalTimestamp(name string, value *time.Time) error {
	if value == nil {
		return nil
	}
	return validateRepresentableTimestamp(name, *value)
}

func validateRepresentableTimestamp(name string, value time.Time) error {
	utc := value.UTC()
	if utc.Year() < 0 || utc.Year() > 9999 {
		return fmt.Errorf("%s year must be between 0000 and 9999", name)
	}
	formatted := utc.Format(time.RFC3339Nano)
	parsed, err := time.Parse(time.RFC3339Nano, formatted)
	if err != nil || !parsed.Equal(utc) {
		return fmt.Errorf("%s must round-trip as UTC RFC3339Nano", name)
	}
	return nil
}

func formatTimestamp(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func nullableTimestamp(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTimestamp(*value)
}

func parseTimestamp(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func parseNullableTimestamp(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseTimestamp(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func int64Pointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}

func float64Pointer(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	result := value.Float64
	return &result
}

func stringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}
