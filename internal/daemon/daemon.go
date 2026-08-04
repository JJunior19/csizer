// Package daemon owns continuous container collection and recovery.
package daemon

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jorgeccarhuasaroni/containersize/internal/collector"
	"github.com/jorgeccarhuasaroni/containersize/internal/dockerclient"
	"github.com/jorgeccarhuasaroni/containersize/internal/identity"
	"github.com/jorgeccarhuasaroni/containersize/internal/storage"
)

const (
	defaultReconcileInterval = time.Minute
	defaultRetryInterval     = 5 * time.Second
	defaultLeaseDuration     = 30 * time.Second
)

var (
	// ErrAlreadyRunning means another daemon still holds the collection lease.
	ErrAlreadyRunning = errors.New("collection daemon is already running")
	// ErrLeaseLost means this process can no longer prove exclusive ownership.
	ErrLeaseLost = errors.New("collection daemon lease was lost")
)

// Store is the persistence boundary required by the collection daemon.
type Store interface {
	collector.Store
	EnsureWorkload(context.Context, storage.Workload) (storage.Workload, error)
	WorkloadByKey(context.Context, string) (storage.Workload, error)
	RegisterContainerInstance(context.Context, storage.ContainerInstance) (storage.ContainerInstance, error)
	ContainerInstanceByContainerID(context.Context, string) (storage.ContainerInstance, error)
	CreateTrackingSession(context.Context, storage.TrackingSession) (storage.TrackingSession, error)
	ActiveTrackingSessions(context.Context) ([]storage.ActiveTrackingSession, error)
	EndTrackingSession(context.Context, int64, time.Time, string) (bool, error)
	AcquireDaemonLease(context.Context, string, time.Time, time.Time) (bool, error)
	ReleaseDaemonLease(context.Context, string) error
}

// Docker is the normalized Docker boundary used by the daemon.
type Docker interface {
	collector.StatsSource
	List(context.Context) ([]dockerclient.Container, error)
	Events(context.Context) (<-chan collector.LifecycleEvent, <-chan error)
}

// Config controls the daemon cadence. Zero values use production defaults.
type Config struct {
	CollectorVersion  string
	SampleInterval    time.Duration
	ReconcileInterval time.Duration
	RetryInterval     time.Duration
	LeaseDuration     time.Duration
	OwnerID           string
	Now               func() time.Time
}

// Daemon runs collection independently of foreground CLI command lifetimes.
type Daemon struct {
	store             Store
	docker            Docker
	collector         *collector.Collector
	collectorVersion  string
	sampleInterval    time.Duration
	reconcileInterval time.Duration
	retryInterval     time.Duration
	leaseDuration     time.Duration
	ownerID           string
	now               func() time.Time
}

// New constructs a daemon with explicit runtime and persistence boundaries.
func New(store Store, docker Docker, config Config) (*Daemon, error) {
	if store == nil {
		return nil, errors.New("new daemon: store is nil")
	}
	if docker == nil {
		return nil, errors.New("new daemon: Docker client is nil")
	}
	if config.SampleInterval <= 0 {
		config.SampleInterval = collector.DefaultSampleInterval
	}
	if config.ReconcileInterval <= 0 {
		config.ReconcileInterval = defaultReconcileInterval
	}
	if config.RetryInterval <= 0 {
		config.RetryInterval = defaultRetryInterval
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = defaultLeaseDuration
	}
	if config.LeaseDuration <= config.RetryInterval {
		return nil, errors.New("new daemon: lease duration must exceed retry interval")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	ownerID := strings.TrimSpace(config.OwnerID)
	if ownerID == "" {
		var err error
		ownerID, err = newOwnerID()
		if err != nil {
			return nil, err
		}
	}
	return &Daemon{
		store:             store,
		docker:            docker,
		collector:         collector.New(docker, store),
		collectorVersion:  strings.TrimSpace(config.CollectorVersion),
		sampleInterval:    config.SampleInterval,
		reconcileInterval: config.ReconcileInterval,
		retryInterval:     config.RetryInterval,
		leaseDuration:     config.LeaseDuration,
		ownerID:           ownerID,
		now:               config.Now,
	}, nil
}

// Run keeps collecting until ctx is canceled. Docker failures are retried;
// storage lease loss stops this daemon before a duplicate collector can run.
func (daemon *Daemon) Run(ctx context.Context) (err error) {
	if daemon == nil {
		return errors.New("run daemon: daemon is nil")
	}
	if err := daemon.acquire(ctx); err != nil {
		return err
	}
	collectionContext, cancelCollection := context.WithCancel(ctx)
	defer cancelCollection()
	leaseErrors := daemon.maintainLease(ctx, cancelCollection)
	defer func() {
		releaseContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if releaseErr := daemon.store.ReleaseDaemonLease(releaseContext, daemon.ownerID); releaseErr != nil {
			err = errors.Join(err, fmt.Errorf("release daemon lease: %w", releaseErr))
		}
	}()

	sessions := daemon.reconcile(collectionContext, true)
	daemon.sample(collectionContext, sessions)

	sampleTicker := time.NewTicker(daemon.sampleInterval)
	defer sampleTicker.Stop()
	reconcileTicker := time.NewTicker(daemon.reconcileInterval)
	defer reconcileTicker.Stop()
	events, eventErrors := daemon.docker.Events(collectionContext)
	var retry <-chan time.Time
	for {
		select {
		case err := <-leaseErrors:
			return err
		case <-collectionContext.Done():
			select {
			case err := <-leaseErrors:
				return err
			default:
			}
			return nil
		case <-sampleTicker.C:
			daemon.sample(collectionContext, sessions)
		case <-reconcileTicker.C:
			sessions = daemon.reconcile(collectionContext, true)
		case event, ok := <-events:
			if !ok {
				events = nil
				retry = daemon.retry(retry)
				continue
			}
			sessions = daemon.handleEvent(collectionContext, event, sessions)
		case _, ok := <-eventErrors:
			if !ok {
				eventErrors = nil
			} else {
				events = nil
				eventErrors = nil
			}
			retry = daemon.retry(retry)
		case <-retry:
			sessions = daemon.reconcile(collectionContext, true)
			events, eventErrors = daemon.docker.Events(collectionContext)
			retry = nil
		}
	}
}

func (daemon *Daemon) maintainLease(ctx context.Context, cancelCollection context.CancelFunc) <-chan error {
	errorsChannel := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(daemon.leaseDuration / 2)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := daemon.renew(ctx); err != nil {
					errorsChannel <- err
					cancelCollection()
					return
				}
			}
		}
	}()
	return errorsChannel
}

func (daemon *Daemon) acquire(ctx context.Context) error {
	now := daemon.now().UTC()
	acquired, err := daemon.store.AcquireDaemonLease(ctx, daemon.ownerID, now, now.Add(daemon.leaseDuration))
	if err != nil {
		return fmt.Errorf("acquire daemon lease: %w", err)
	}
	if !acquired {
		return ErrAlreadyRunning
	}
	return nil
}

func (daemon *Daemon) renew(ctx context.Context) error {
	now := daemon.now().UTC()
	acquired, err := daemon.store.AcquireDaemonLease(ctx, daemon.ownerID, now, now.Add(daemon.leaseDuration))
	if err != nil {
		return fmt.Errorf("renew daemon lease: %w", err)
	}
	if !acquired {
		return ErrLeaseLost
	}
	return nil
}

func (daemon *Daemon) retry(existing <-chan time.Time) <-chan time.Time {
	if existing != nil {
		return existing
	}
	return time.After(daemon.retryInterval)
}

func (daemon *Daemon) sample(ctx context.Context, sessions map[string]storage.ActiveTrackingSession) {
	for containerID, session := range sessions {
		// A failed sample is retried on the next cadence after Docker recovers.
		_, _ = daemon.collector.CollectSample(ctx, session.ID, containerID)
	}
}

func (daemon *Daemon) handleEvent(
	ctx context.Context,
	event collector.LifecycleEvent,
	sessions map[string]storage.ActiveTrackingSession,
) map[string]storage.ActiveTrackingSession {
	refreshed := daemon.reconcile(ctx, false)
	instance, err := daemon.store.ContainerInstanceByContainerID(ctx, event.ContainerID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return refreshed
		}
		return sessions
	}
	instanceID := instance.ID
	_ = daemon.collector.PersistEvents(ctx, []collector.EventObservation{{
		WorkloadID:          instance.WorkloadID,
		ContainerInstanceID: &instanceID,
		Event:               event,
	}})
	return refreshed
}

func (daemon *Daemon) reconcile(ctx context.Context, recordRecoveredStops bool) map[string]storage.ActiveTrackingSession {
	sessions := daemon.activeSessions(ctx)
	for containerID, session := range sessions {
		if !session.TrackingEnabled {
			daemon.stopSession(ctx, session, false)
			delete(sessions, containerID)
		}
	}
	containers, err := daemon.docker.List(ctx)
	if err != nil {
		return sessions
	}

	for _, container := range containers {
		resolved, resolveErr := identity.Resolve(identity.Container{
			Name:   container.Name,
			Image:  container.Image,
			Labels: container.Labels,
		}, "")
		if resolveErr != nil {
			continue
		}
		// Only existing workloads are tracked; discovery alone must not opt in a container.
		workload, workloadErr := daemon.store.WorkloadByKey(ctx, resolved.WorkloadKey)
		if workloadErr != nil {
			continue
		}
		workload, workloadErr = daemon.store.EnsureWorkload(ctx, storage.Workload{
			WorkloadKey:     resolved.WorkloadKey,
			DisplayName:     resolved.DisplayName,
			ComposeProject:  resolved.ComposeProject,
			ComposeService:  resolved.ComposeService,
			ImageRepository: resolved.ImageRepository,
		})
		if workloadErr != nil {
			continue
		}
		instance, instanceErr := daemon.store.RegisterContainerInstance(ctx, storage.ContainerInstance{
			WorkloadID:    workload.ID,
			ContainerID:   container.ID,
			ContainerName: container.Name,
			ImageName:     container.Image,
		})
		if instanceErr != nil {
			continue
		}
		active, found := sessions[container.ID]
		if workload.TrackingEnabled && isRunning(container.State) {
			if !found {
				instanceID := instance.ID
				created, createErr := daemon.store.CreateTrackingSession(ctx, storage.TrackingSession{
					WorkloadID:          workload.ID,
					ContainerInstanceID: &instanceID,
					StartedAt:           daemon.now().UTC(),
					Status:              "running",
					CollectorVersion:    daemon.collectorVersion,
				})
				if createErr != nil {
					continue
				}
				sessions[container.ID] = storage.ActiveTrackingSession{TrackingSession: created, ContainerID: container.ID}
			}
			continue
		}
		if found {
			daemon.stopSession(ctx, active, recordRecoveredStops)
			delete(sessions, container.ID)
		}
	}

	known := make(map[string]struct{}, len(containers))
	for _, container := range containers {
		known[container.ID] = struct{}{}
	}
	for containerID, session := range sessions {
		if _, found := known[containerID]; !found {
			daemon.stopSession(ctx, session, recordRecoveredStops)
			delete(sessions, containerID)
		}
	}
	return sessions
}

func (daemon *Daemon) activeSessions(ctx context.Context) map[string]storage.ActiveTrackingSession {
	items, err := daemon.store.ActiveTrackingSessions(ctx)
	if err != nil {
		return make(map[string]storage.ActiveTrackingSession)
	}
	sessions := make(map[string]storage.ActiveTrackingSession, len(items))
	for _, session := range items {
		sessions[session.ContainerID] = session
	}
	return sessions
}

func (daemon *Daemon) stopSession(ctx context.Context, session storage.ActiveTrackingSession, recordEvent bool) {
	stopped, err := daemon.store.EndTrackingSession(ctx, session.ID, daemon.now().UTC(), "stopped")
	if err != nil || !stopped || !recordEvent || session.ContainerInstanceID == nil {
		return
	}
	_ = daemon.collector.PersistEvents(ctx, []collector.EventObservation{{
		WorkloadID:          session.WorkloadID,
		ContainerInstanceID: session.ContainerInstanceID,
		Event: collector.LifecycleEvent{
			ContainerID: session.ContainerID,
			Timestamp:   daemon.now().UTC(),
			Type:        collector.EventStop,
		},
	}})
}

func isRunning(state string) bool {
	return strings.EqualFold(strings.TrimSpace(state), "running")
}

func newOwnerID() (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", fmt.Errorf("new daemon: generate owner ID: %w", err)
	}
	return hex.EncodeToString(token[:]), nil
}
