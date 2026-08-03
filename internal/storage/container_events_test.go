package storage

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteContainerEventsPersistsBatch(t *testing.T) {
	t.Parallel()

	store := openTestStore(t, filepath.Join(t.TempDir(), "event batch", "database.db"))
	workload, instance, _ := createSampleParents(t, store)
	instanceID := instance.ID
	exitCode := int64(137)
	metadata := `{"name":"api-v2"}`
	timestamp := time.Date(2026, time.August, 4, 12, 0, 0, 123, time.FixedZone("offset", -4*60*60))
	events := []ContainerEvent{
		{WorkloadID: workload.ID, ContainerInstanceID: &instanceID, Timestamp: timestamp, EventType: "die", ExitCode: &exitCode},
		{WorkloadID: workload.ID, Timestamp: timestamp.Add(time.Second), EventType: "rename", MetadataJSON: &metadata},
	}
	if err := store.WriteContainerEvents(t.Context(), events); err != nil {
		t.Fatalf("WriteContainerEvents() error = %v", err)
	}
	var (
		count          int
		storedTime     string
		storedExit     int64
		storedMetadata string
	)
	if err := store.db.QueryRowContext(t.Context(), `
SELECT timestamp, exit_code FROM container_events WHERE event_type = 'die'`).Scan(&storedTime, &storedExit); err != nil {
		t.Fatalf("query die event error = %v", err)
	}
	if storedTime != timestamp.UTC().Format(time.RFC3339Nano) || storedExit != exitCode {
		t.Errorf("stored die event = (%q, %d)", storedTime, storedExit)
	}
	if err := store.db.QueryRowContext(t.Context(), `
SELECT metadata_json FROM container_events WHERE event_type = 'rename'`).Scan(&storedMetadata); err != nil {
		t.Fatalf("query rename event error = %v", err)
	}
	if storedMetadata != metadata {
		t.Errorf("stored metadata = %v, want %q", storedMetadata, metadata)
	}
	if err := store.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM container_events`).Scan(&count); err != nil {
		t.Fatalf("count container events error = %v", err)
	}
	if count != 2 {
		t.Errorf("event count = %d, want 2", count)
	}
}

func TestWriteContainerEventsRejectsInvalidBatchAtomically(t *testing.T) {
	t.Parallel()

	store := openTestStore(t, filepath.Join(t.TempDir(), "event rollback", "database.db"))
	workload, instance, _ := createSampleParents(t, store)
	instanceID := instance.ID
	now := time.Now()
	invalidMetadata := "not-json"
	err := store.WriteContainerEvents(t.Context(), []ContainerEvent{
		{WorkloadID: workload.ID, ContainerInstanceID: &instanceID, Timestamp: now, EventType: "start"},
		{WorkloadID: workload.ID, Timestamp: now, EventType: "rename", MetadataJSON: &invalidMetadata},
	})
	if err == nil || !strings.Contains(err.Error(), "metadata JSON") {
		t.Fatalf("WriteContainerEvents() error = %v, want metadata validation error", err)
	}
	if got := rowCount(t, store, "container_events"); got != 0 {
		t.Errorf("event count = %d, want 0 after rejected batch", got)
	}

	other, err := store.EnsureWorkload(t.Context(), Workload{WorkloadKey: "events:other"})
	if err != nil {
		t.Fatalf("EnsureWorkload() error = %v", err)
	}
	err = store.WriteContainerEvents(t.Context(), []ContainerEvent{{
		WorkloadID:          other.ID,
		ContainerInstanceID: &instanceID,
		Timestamp:           now,
		EventType:           "start",
	}})
	if !errors.Is(err, ErrContainerEventInstanceNotInWorkload) {
		t.Fatalf("WriteContainerEvents() error = %v, want ErrContainerEventInstanceNotInWorkload", err)
	}
	if got := rowCount(t, store, "container_events"); got != 0 {
		t.Errorf("event count = %d, want 0 after rejected ownership", got)
	}
}
