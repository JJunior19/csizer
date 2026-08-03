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
		t.Skipf("Docker client unavailable: %v", err)
	}
	t.Cleanup(func() {
		if err := docker.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := docker.List(ctx); err != nil {
		t.Skipf("Docker daemon unavailable: %v", err)
	}
}
