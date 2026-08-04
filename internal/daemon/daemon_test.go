package daemon

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jorgeccarhuasaroni/containersize/internal/collector"
	"github.com/jorgeccarhuasaroni/containersize/internal/dockerclient"
	"github.com/jorgeccarhuasaroni/containersize/internal/storage"
)

func TestRunResumesPersistedSessionAndCollects(t *testing.T) {
	t.Parallel()

	store, session := openStoreWithRunningSession(t)
	ctx, cancel := context.WithCancel(t.Context())
	docker := &fakeDocker{
		containers: []dockerclient.Container{{ID: "container-id", Name: "api", Image: "example/api:test", State: "running"}},
		stats:      collector.RuntimeStats{Timestamp: time.Now(), Memory: collector.Memory{UsageBytes: 64}},
		events:     []eventStream{{}},
		onEvents: func(calls int) {
			if calls == 1 {
				cancel()
			}
		},
	}
	service := newTestDaemon(t, store, docker, "restart-owner")

	if err := service.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	samples, err := store.MetricSamplesForSession(t.Context(), session.ID)
	if err != nil {
		t.Fatalf("MetricSamplesForSession() error = %v", err)
	}
	if len(samples) != 1 || samples[0].MemoryUsageBytes != 64 {
		t.Errorf("samples = %#v, want one recovered-session sample", samples)
	}
	sessions, err := store.ActiveTrackingSessions(t.Context())
	if err != nil {
		t.Fatalf("ActiveTrackingSessions() error = %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != session.ID {
		t.Errorf("active sessions = %#v, want original session %d", sessions, session.ID)
	}
}

func TestRunRejectsExistingDaemonLease(t *testing.T) {
	t.Parallel()

	store, _ := openStoreWithRunningSession(t)
	now := time.Now().UTC()
	acquired, err := store.AcquireDaemonLease(t.Context(), "other-owner", now, now.Add(time.Minute))
	if err != nil || !acquired {
		t.Fatalf("AcquireDaemonLease() = %v, %v; want true, nil", acquired, err)
	}
	docker := &fakeDocker{}
	service := newTestDaemon(t, store, docker, "new-owner")

	err = service.Run(t.Context())
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("Run() error = %v, want ErrAlreadyRunning", err)
	}
	if docker.listCalls() != 0 {
		t.Errorf("Docker List calls = %d, want 0", docker.listCalls())
	}
}

func TestRunReconnectsAfterEventStreamFailure(t *testing.T) {
	t.Parallel()

	store, _ := openStoreWithRunningSession(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	docker := &fakeDocker{
		containers: []dockerclient.Container{{ID: "container-id", Name: "api", Image: "example/api:test", State: "running"}},
		events: []eventStream{
			closedEventStream(errors.New("Docker connection lost")),
			{},
		},
		onEvents: func(calls int) {
			if calls == 2 {
				cancel()
			}
		},
	}
	service := newTestDaemon(t, store, docker, "reconnect-owner")

	if err := service.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if calls := docker.eventCalls(); calls < 2 {
		t.Errorf("Docker Events calls = %d, want at least 2 after reconnect", calls)
	}
}

func TestRunRetriesSampleAfterDockerStatsFailure(t *testing.T) {
	t.Parallel()

	store, session := openStoreWithRunningSession(t)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	docker := &fakeDocker{
		containers:  []dockerclient.Container{{ID: "container-id", Name: "api", Image: "example/api:test", State: "running"}},
		stats:       collector.RuntimeStats{Timestamp: time.Now(), Memory: collector.Memory{UsageBytes: 64}},
		statsErrors: []error{errors.New("Docker unavailable")},
	}
	service, err := New(store, docker, Config{
		OwnerID:           "stats-retry-owner",
		SampleInterval:    time.Millisecond,
		ReconcileInterval: time.Hour,
		RetryInterval:     time.Millisecond,
		LeaseDuration:     20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := service.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	samples, err := store.MetricSamplesForSession(t.Context(), session.ID)
	if err != nil {
		t.Fatalf("MetricSamplesForSession() error = %v", err)
	}
	if len(samples) == 0 {
		t.Error("MetricSamplesForSession() has no samples after a transient Docker failure")
	}
}

func TestRunMaintainsLeaseWhileStatsBlocks(t *testing.T) {
	t.Parallel()

	store, _ := openStoreWithRunningSession(t)
	blocked := make(chan struct{})
	started := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	docker := &fakeDocker{
		containers:   []dockerclient.Container{{ID: "container-id", Name: "api", Image: "example/api:test", State: "running"}},
		stats:        collector.RuntimeStats{Timestamp: time.Now(), Memory: collector.Memory{UsageBytes: 64}},
		statsBlocked: blocked,
		statsStarted: started,
	}
	service, err := New(store, docker, Config{
		OwnerID:           "blocked-owner",
		SampleInterval:    time.Hour,
		ReconcileInterval: time.Hour,
		RetryInterval:     time.Millisecond,
		LeaseDuration:     40 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Stats() did not block")
	}
	time.Sleep(3 * service.leaseDuration)
	now := time.Now().UTC()
	acquired, err := store.AcquireDaemonLease(t.Context(), "second-owner", now, now.Add(time.Minute))
	if err != nil || acquired {
		t.Fatalf("AcquireDaemonLease(second owner) = %v, %v; want false, nil", acquired, err)
	}
	cancel()
	close(blocked)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not exit after blocked Stats() returned")
	}
}

func TestReconcileClosesMissingContainerSession(t *testing.T) {
	t.Parallel()

	store, _ := openStoreWithRunningSession(t)
	service := newTestDaemon(t, store, &fakeDocker{}, "reconcile-owner")
	sessions := service.reconcile(t.Context(), true)
	if len(sessions) != 0 {
		t.Fatalf("reconcile sessions = %#v, want none", sessions)
	}
	active, err := store.ActiveTrackingSessions(t.Context())
	if err != nil {
		t.Fatalf("ActiveTrackingSessions() error = %v", err)
	}
	if len(active) != 0 {
		t.Errorf("active sessions = %#v, want none", active)
	}
}

func TestReconcileDoesNotTrackUnknownContainer(t *testing.T) {
	t.Parallel()

	store, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "daemon.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := newTestDaemon(t, store, &fakeDocker{
		containers: []dockerclient.Container{{ID: "unknown", Name: "unknown", Image: "example/unknown:test", State: "running"}},
	}, "unknown-owner")
	if sessions := service.reconcile(t.Context(), true); len(sessions) != 0 {
		t.Fatalf("reconcile sessions = %#v, want none", sessions)
	}
	if _, err := store.WorkloadByKey(t.Context(), "unknown"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("WorkloadByKey() error = %v, want sql.ErrNoRows", err)
	}
}

func TestReconcileClosesDisabledSessionBeforeReenable(t *testing.T) {
	t.Parallel()

	instanceID := int64(1)
	store := &memoryStore{
		workload: storage.Workload{ID: 1, WorkloadKey: "api", TrackingEnabled: false},
		instance: storage.ContainerInstance{ID: instanceID, WorkloadID: 1, ContainerID: "container-id"},
		active: map[string]storage.ActiveTrackingSession{
			"container-id": {
				TrackingSession: storage.TrackingSession{ID: 1, WorkloadID: 1, ContainerInstanceID: &instanceID, Status: "running"},
				ContainerID:     "container-id",
				TrackingEnabled: false,
			},
		},
	}
	service, err := New(store, &fakeDocker{
		containers: []dockerclient.Container{{ID: "container-id", Name: "api", Image: "example/api:test", State: "running"}},
	}, Config{OwnerID: "disabled-owner", LeaseDuration: time.Second, RetryInterval: time.Millisecond})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if sessions := service.reconcile(t.Context(), true); len(sessions) != 0 {
		t.Fatalf("disabled reconcile sessions = %#v, want none", sessions)
	}
	if store.ended != 1 || len(store.created) != 0 {
		t.Fatalf("disabled reconciliation ended=%d created=%d, want 1 and 0", store.ended, len(store.created))
	}
	store.workload.TrackingEnabled = true
	sessions := service.reconcile(t.Context(), true)
	if len(sessions) != 1 || len(store.created) != 1 || store.created[0].ID == 1 {
		t.Errorf("re-enabled reconcile sessions=%#v created=%#v, want one new window", sessions, store.created)
	}
}

func openStoreWithRunningSession(t *testing.T) (*storage.Store, storage.TrackingSession) {
	t.Helper()
	store, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "daemon.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	workload, err := store.EnsureWorkload(t.Context(), storage.Workload{WorkloadKey: "api", DisplayName: "api"})
	if err != nil {
		t.Fatalf("EnsureWorkload() error = %v", err)
	}
	instance, err := store.RegisterContainerInstance(t.Context(), storage.ContainerInstance{
		WorkloadID: workload.ID, ContainerID: "container-id", ContainerName: "api", ImageName: "example/api:test",
	})
	if err != nil {
		t.Fatalf("RegisterContainerInstance() error = %v", err)
	}
	instanceID := instance.ID
	session, err := store.CreateTrackingSession(t.Context(), storage.TrackingSession{
		WorkloadID: workload.ID, ContainerInstanceID: &instanceID, StartedAt: time.Now(), Status: "running",
	})
	if err != nil {
		t.Fatalf("CreateTrackingSession() error = %v", err)
	}
	return store, session
}

func newTestDaemon(t *testing.T, store *storage.Store, docker *fakeDocker, ownerID string) *Daemon {
	t.Helper()
	service, err := New(store, docker, Config{
		OwnerID:           ownerID,
		SampleInterval:    time.Hour,
		ReconcileInterval: time.Hour,
		RetryInterval:     time.Millisecond,
		LeaseDuration:     20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return service
}

type eventStream struct {
	events <-chan collector.LifecycleEvent
	errors <-chan error
}

func closedEventStream(err error) eventStream {
	events := make(chan collector.LifecycleEvent)
	errorsChannel := make(chan error, 1)
	close(events)
	if err != nil {
		errorsChannel <- err
	}
	close(errorsChannel)
	return eventStream{events: events, errors: errorsChannel}
}

type fakeDocker struct {
	mu           sync.Mutex
	containers   []dockerclient.Container
	stats        collector.RuntimeStats
	statsErrors  []error
	statsBlocked <-chan struct{}
	statsStarted chan<- struct{}
	statsStart   sync.Once
	events       []eventStream
	onEvents     func(int)
	listCount    int
	statsCount   int
	eventCount   int
}

func (docker *fakeDocker) List(context.Context) ([]dockerclient.Container, error) {
	docker.mu.Lock()
	defer docker.mu.Unlock()
	docker.listCount++
	return append([]dockerclient.Container(nil), docker.containers...), nil
}

func (docker *fakeDocker) Stats(context.Context, string) (collector.RuntimeStats, error) {
	docker.mu.Lock()
	docker.statsCount++
	calls := docker.statsCount
	stats := docker.stats
	var err error
	if len(docker.statsErrors) >= calls {
		err = docker.statsErrors[calls-1]
	}
	docker.mu.Unlock()
	if docker.statsStarted != nil {
		docker.statsStart.Do(func() { close(docker.statsStarted) })
	}
	if docker.statsBlocked != nil {
		<-docker.statsBlocked
	}
	return stats, err
}

func (docker *fakeDocker) Events(context.Context) (<-chan collector.LifecycleEvent, <-chan error) {
	docker.mu.Lock()
	docker.eventCount++
	calls := docker.eventCount
	var stream eventStream
	if len(docker.events) >= calls {
		stream = docker.events[calls-1]
	}
	onEvents := docker.onEvents
	docker.mu.Unlock()
	if onEvents != nil {
		onEvents(calls)
	}
	return stream.events, stream.errors
}

func (docker *fakeDocker) listCalls() int {
	docker.mu.Lock()
	defer docker.mu.Unlock()
	return docker.listCount
}

func (docker *fakeDocker) eventCalls() int {
	docker.mu.Lock()
	defer docker.mu.Unlock()
	return docker.eventCount
}

type memoryStore struct {
	workload storage.Workload
	instance storage.ContainerInstance
	active   map[string]storage.ActiveTrackingSession
	created  []storage.TrackingSession
	ended    int
}

func (store *memoryStore) WriteMetricSamples(context.Context, []storage.MetricSample) error {
	return nil
}

func (store *memoryStore) WriteContainerEvents(context.Context, []storage.ContainerEvent) error {
	return nil
}

func (store *memoryStore) EnsureWorkload(context.Context, storage.Workload) (storage.Workload, error) {
	return store.workload, nil
}

func (store *memoryStore) WorkloadByKey(context.Context, string) (storage.Workload, error) {
	return store.workload, nil
}

func (store *memoryStore) RegisterContainerInstance(context.Context, storage.ContainerInstance) (storage.ContainerInstance, error) {
	return store.instance, nil
}

func (store *memoryStore) ContainerInstanceByContainerID(context.Context, string) (storage.ContainerInstance, error) {
	return store.instance, nil
}

func (store *memoryStore) CreateTrackingSession(_ context.Context, session storage.TrackingSession) (storage.TrackingSession, error) {
	session.ID = int64(len(store.created) + 2)
	store.created = append(store.created, session)
	store.active[store.instance.ContainerID] = storage.ActiveTrackingSession{
		TrackingSession: session,
		ContainerID:     store.instance.ContainerID,
		TrackingEnabled: true,
	}
	return session, nil
}

func (store *memoryStore) ActiveTrackingSessions(context.Context) ([]storage.ActiveTrackingSession, error) {
	sessions := make([]storage.ActiveTrackingSession, 0, len(store.active))
	for _, session := range store.active {
		sessions = append(sessions, session)
	}
	return sessions, nil
}

func (store *memoryStore) EndTrackingSession(_ context.Context, sessionID int64, _ time.Time, _ string) (bool, error) {
	for containerID, session := range store.active {
		if session.ID == sessionID {
			delete(store.active, containerID)
			store.ended++
			return true, nil
		}
	}
	return false, nil
}

func (store *memoryStore) AcquireDaemonLease(context.Context, string, time.Time, time.Time) (bool, error) {
	return true, nil
}

func (store *memoryStore) ReleaseDaemonLease(context.Context, string) error {
	return nil
}
