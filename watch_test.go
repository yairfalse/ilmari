package ilmari

import (
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestWatchReceivesEvents(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		var mu sync.Mutex
		events := make([]WatchEvent, 0)
		done := make(chan struct{})
		var doneOnce sync.Once

		// Start watching ConfigMaps
		stop, err := ctx.Watch("configmap", func(event WatchEvent) {
			mu.Lock()
			events = append(events, event)
			eventCount := len(events)
			mu.Unlock()
			if eventCount >= 2 {
				doneOnce.Do(func() { close(done) })
			}
		})
		if err != nil {
			t.Fatalf("Watch failed: %v", err)
		}
		defer stop()

		// Give watch time to establish before creating resources
		time.Sleep(100 * time.Millisecond)

		// Create a ConfigMap - should trigger ADDED event
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "watch-test"},
			Data:       map[string]string{"key": "value1"},
		}
		if err := ctx.Apply(cm); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		// Wait a bit to ensure first event is processed before update
		time.Sleep(100 * time.Millisecond)

		// Update it - should trigger MODIFIED event
		cm.Data["key"] = "value2"
		if err := ctx.Apply(cm); err != nil {
			t.Fatalf("Apply update failed: %v", err)
		}

		// Wait for events
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			mu.Lock()
			eventCount := len(events)
			mu.Unlock()
			t.Fatalf("timeout waiting for watch events, got %d", eventCount)
		}

		// Verify we received at least 2 events
		mu.Lock()
		eventsCopy := make([]WatchEvent, len(events))
		copy(eventsCopy, events)
		mu.Unlock()
		if len(eventsCopy) < 2 {
			t.Fatalf("expected at least 2 events, got %d", len(eventsCopy))
		}

		// Check that we have at least one ADDED event (first create)
		hasAdded := false
		for _, e := range eventsCopy {
			if e.Type == "ADDED" {
				hasAdded = true
				break
			}
		}
		if !hasAdded {
			t.Error("expected at least one ADDED event")
		}
	})
}

// TestWaitDeletedWaitsForRemoval verifies WaitDeleted blocks until resource is gone.
func TestWaitDeletedWaitsForRemoval(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a ConfigMap
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "delete-test"},
			Data:       map[string]string{"key": "value"},
		}
		if err := ctx.Apply(cm); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		// Delete it in background
		go func() {
			time.Sleep(500 * time.Millisecond)
			if err := ctx.Delete("delete-test", &corev1.ConfigMap{}); err != nil {
				t.Errorf("Delete in goroutine failed: %v", err)
			}
		}()

		// WaitDeleted should block until it's gone
		err := ctx.WaitDeleted("configmap/delete-test")
		if err != nil {
			t.Errorf("WaitDeleted failed: %v", err)
		}

		// Verify it's actually gone
		err = ctx.Get("delete-test", &corev1.ConfigMap{})
		if err == nil {
			t.Error("ConfigMap should be deleted")
		}
	})
}

// TestPatchStrategicMerge verifies strategic merge patch.
func TestPatchStrategicMerge(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a deployment
		deploy := Deployment("patch-test").
			Image("nginx:alpine").
			Replicas(1).
			Build()

		if err := ctx.Apply(deploy); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		// Patch to add a label
		patch := []byte(`{"metadata":{"labels":{"patched":"true"}}}`)
		err := ctx.Patch("deployment/patch-test", patch, PatchStrategic)
		if err != nil {
			t.Fatalf("Patch failed: %v", err)
		}

		// Verify label was added
		var updated appsv1.Deployment
		if err := ctx.Get("patch-test", &updated); err != nil {
			t.Fatalf("Get failed: %v", err)
		}

		if updated.Labels["patched"] != "true" {
			t.Errorf("expected label patched=true, got %v", updated.Labels)
		}
	})
}
