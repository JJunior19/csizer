//go:build integration

package dockerclient

import (
	"context"
	"testing"
	"time"
)

func TestListIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Docker integration test disabled in short mode")
	}

	docker, err := New("ContainerSize/integration-test")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := docker.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	pingContext, cancelPing := context.WithTimeout(t.Context(), 5*time.Second)
	if err := docker.Ping(pingContext); err != nil {
		cancelPing()
		t.Skipf("Docker daemon unavailable: %v", err)
	}
	cancelPing()
	listContext, cancelList := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelList()
	if _, err := docker.List(listContext); err != nil {
		t.Fatalf("List() after successful Ping() error = %v", err)
	}
}

func TestStatsIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Docker integration test disabled in short mode")
	}

	docker, err := New("ContainerSize/integration-test")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = docker.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := docker.Ping(ctx); err != nil {
		t.Skipf("Docker daemon unavailable: %v", err)
	}
	containers, err := docker.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	for _, container := range containers {
		if container.State != "running" {
			continue
		}
		stats, err := docker.Stats(ctx, container.ID)
		if err != nil {
			t.Fatalf("Stats(%q) error = %v", container.ID, err)
		}
		if stats.Timestamp.IsZero() {
			t.Errorf("Stats(%q) returned a zero timestamp", container.ID)
		}
		return
	}
	t.Skip("Docker daemon has no running containers")
}
