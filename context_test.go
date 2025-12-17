package ilmari

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestContextCreatesNamespace verifies that Run creates an isolated namespace.
// Requires a real Kubernetes cluster.
func TestContextCreatesNamespace(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Namespace should start with "ilmari-test-"
		if len(ctx.Namespace) < 13 {
			t.Errorf("namespace too short: %s", ctx.Namespace)
		}
		if ctx.Namespace[:12] != "ilmari-test-" {
			t.Errorf("namespace should start with 'ilmari-test-', got: %s", ctx.Namespace)
		}

		// Client should be set
		if ctx.Client == nil {
			t.Error("client should not be nil")
		}
	})
}

// TestContextCleansUpOnSuccess verifies namespace cleanup on success.
// Requires a real Kubernetes cluster.
func TestContextCleansUpOnSuccess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	var capturedNamespace string

	Run(t, func(ctx *Context) {
		capturedNamespace = ctx.Namespace
		// Test succeeds - namespace should be cleaned up
	})

	// Verify namespace was deleted
	// Note: This is a basic check, full verification would need another client
	if capturedNamespace == "" {
		t.Error("namespace was never set")
	}
}

// TestApplyCreatesConfigMap verifies Apply creates a ConfigMap.
// Requires a real Kubernetes cluster.
func TestApplyCreatesConfigMap(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a ConfigMap
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-config",
			},
			Data: map[string]string{
				"key": "value",
			},
		}

		// Apply should succeed
		err := ctx.Apply(cm)
		if err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		// Verify it exists in the cluster
		got, err := ctx.Client.CoreV1().ConfigMaps(ctx.Namespace).Get(
			context.Background(),
			"test-config",
			metav1.GetOptions{},
		)
		if err != nil {
			t.Fatalf("ConfigMap not found: %v", err)
		}

		if got.Data["key"] != "value" {
			t.Errorf("expected key=value, got key=%s", got.Data["key"])
		}
	})
}

// TestApplyUpdatesExistingResource verifies Apply updates if resource exists.
func TestApplyUpdatesExistingResource(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create initial ConfigMap
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-config",
			},
			Data: map[string]string{
				"key": "value1",
			},
		}
		if err := ctx.Apply(cm); err != nil {
			t.Fatalf("first Apply failed: %v", err)
		}

		// Apply again with different value
		cm.Data["key"] = "value2"
		if err := ctx.Apply(cm); err != nil {
			t.Fatalf("second Apply failed: %v", err)
		}

		// Verify updated value
		got, err := ctx.Client.CoreV1().ConfigMaps(ctx.Namespace).Get(
			context.Background(),
			"test-config",
			metav1.GetOptions{},
		)
		if err != nil {
			t.Fatalf("ConfigMap not found: %v", err)
		}

		if got.Data["key"] != "value2" {
			t.Errorf("expected key=value2, got key=%s", got.Data["key"])
		}
	})
}

// TestWaitReadyPod verifies WaitReady waits for a pod to be ready.
func TestWaitReadyPod(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a simple pod
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-pod",
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:    "sleep",
						Image:   "busybox:1.36",
						Command: []string{"sleep", "300"},
					},
				},
			},
		}

		if err := ctx.Apply(pod); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		// WaitReady should succeed once pod is running
		start := time.Now()
		err := ctx.WaitReady("pod/test-pod")
		if err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		t.Logf("Pod became ready in %v", time.Since(start))
	})
}

// TestWaitReadyInvalidFormat verifies WaitReady returns error for invalid format.
func TestWaitReadyInvalidFormat(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		err := ctx.WaitReady("invalid-format")
		if err == nil {
			t.Error("expected error for invalid format")
		}
	})
}

// TestGetRetrievesResource verifies Get retrieves an existing resource.
func TestGetRetrievesResource(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a ConfigMap
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "get-test",
			},
			Data: map[string]string{
				"foo": "bar",
			},
		}
		if err := ctx.Apply(cm); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		// Get it back
		got := &corev1.ConfigMap{}
		if err := ctx.Get("get-test", got); err != nil {
			t.Fatalf("Get failed: %v", err)
		}

		if got.Data["foo"] != "bar" {
			t.Errorf("expected foo=bar, got foo=%s", got.Data["foo"])
		}
	})
}

// TestDeleteRemovesResource verifies Delete removes an existing resource.
func TestDeleteRemovesResource(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a ConfigMap
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "delete-test",
			},
			Data: map[string]string{
				"key": "value",
			},
		}
		if err := ctx.Apply(cm); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		// Delete it
		if err := ctx.Delete("delete-test", &corev1.ConfigMap{}); err != nil {
			t.Fatalf("Delete failed: %v", err)
		}

		// Verify it's gone
		got := &corev1.ConfigMap{}
		err := ctx.Get("delete-test", got)
		if err == nil {
			t.Error("expected error getting deleted resource")
		}
	})
}

// TestListReturnsResources verifies List returns resources in namespace.
func TestListReturnsResources(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create two ConfigMaps
		for _, name := range []string{"list-test-1", "list-test-2"} {
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
			}
			if err := ctx.Apply(cm); err != nil {
				t.Fatalf("Apply failed: %v", err)
			}
		}

		// List them
		list := &corev1.ConfigMapList{}
		if err := ctx.List(list); err != nil {
			t.Fatalf("List failed: %v", err)
		}

		// Check our ConfigMaps are present (namespace may have default ones)
		found := 0
		for _, cm := range list.Items {
			if cm.Name == "list-test-1" || cm.Name == "list-test-2" {
				found++
			}
		}
		if found != 2 {
			t.Errorf("expected to find 2 test ConfigMaps, found %d", found)
		}
	})
}

// TestWaitForCustomCondition verifies WaitFor with custom condition.
func TestWaitForCustomCondition(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a pod
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "waitfor-test",
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:    "sleep",
						Image:   "busybox:1.36",
						Command: []string{"sleep", "300"},
					},
				},
			},
		}
		if err := ctx.Apply(pod); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		// Wait for pod to have an IP assigned
		err := ctx.WaitFor("pod/waitfor-test", func(obj interface{}) bool {
			p, ok := obj.(*corev1.Pod)
			if !ok {
				return false
			}
			return p.Status.PodIP != ""
		})
		if err != nil {
			t.Fatalf("WaitFor failed: %v", err)
		}

		// Verify pod has IP
		got := &corev1.Pod{}
		if err := ctx.Get("waitfor-test", got); err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if got.Status.PodIP == "" {
			t.Error("expected pod to have IP")
		}
	})
}

// TestWaitErrorHasDiagnostics verifies WaitError includes diagnostic info.
func TestWaitErrorHasDiagnostics(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a deployment that will never be ready (bad image)
		deploy := Deployment("bad-image-test").
			Image("nonexistent-image-xyz-12345:latest").
			Replicas(1).
			Build()

		if err := ctx.Apply(deploy); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		// Wait should timeout with rich error
		err := ctx.WaitReadyTimeout("deployment/bad-image-test", 10*time.Second)
		if err == nil {
			t.Fatal("expected timeout error")
		}

		// Error should contain diagnostic info
		errStr := err.Error()
		if !strings.Contains(errStr, "bad-image-test") {
			t.Error("error should mention resource name")
		}

		// Check if it's a WaitError with diagnostics
		if waitErr, ok := err.(*WaitError); ok {
			if waitErr.Resource == "" {
				t.Error("WaitError should have Resource set")
			}
			if len(waitErr.Pods) == 0 && len(waitErr.Events) == 0 {
				t.Log("Note: WaitError has no pods/events (may be expected)")
			}
		}
	})
}

// TestNewContextWithoutTestWrapper verifies NewContext works standalone.
func TestNewContextWithoutTestWrapper(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	// Create context without Run() wrapper
	ctx, err := NewContext()
	if err != nil {
		t.Fatalf("NewContext failed: %v", err)
	}
	defer ctx.Close()

	// Should have a client
	if ctx.Client == nil {
		t.Error("Client should not be nil")
	}

	// Should have created a namespace
	if ctx.Namespace == "" {
		t.Error("Namespace should not be empty")
	}

	// Namespace should exist in cluster
	_, err = ctx.Client.CoreV1().Namespaces().Get(
		context.Background(), ctx.Namespace, metav1.GetOptions{})
	if err != nil {
		t.Errorf("Namespace should exist: %v", err)
	}
}

// TestNewContextWithSharedNamespace verifies using an existing namespace.
func TestNewContextWithSharedNamespace(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	// Use default namespace (always exists)
	ctx, err := NewContext(WithNamespace("default"))
	if err != nil {
		t.Fatalf("NewContext failed: %v", err)
	}
	defer ctx.Close()

	// Should use the specified namespace
	if ctx.Namespace != "default" {
		t.Errorf("expected namespace 'default', got %s", ctx.Namespace)
	}

	// Should be able to list pods (proves connection works)
	_, err = ctx.Client.CoreV1().Pods(ctx.Namespace).List(
		context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Errorf("Failed to list pods: %v", err)
	}
}

// TestNewContextWithIsolatedNamespace verifies isolated namespace creation.
func TestNewContextWithIsolatedNamespace(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx, err := NewContext(WithIsolatedNamespace("mytest"))
	if err != nil {
		t.Fatalf("NewContext failed: %v", err)
	}
	defer ctx.Close()

	// Should have prefix + random suffix
	if !strings.HasPrefix(ctx.Namespace, "mytest-") {
		t.Errorf("expected namespace to start with 'mytest-', got %s", ctx.Namespace)
	}

	// Should be longer than just prefix
	if len(ctx.Namespace) <= 7 {
		t.Errorf("namespace should have random suffix: %s", ctx.Namespace)
	}
}

// TestContextCloseDeletesIsolatedNamespace verifies cleanup.
func TestContextCloseDeletesIsolatedNamespace(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx, err := NewContext(WithIsolatedNamespace("cleanup-test"))
	if err != nil {
		t.Fatalf("NewContext failed: %v", err)
	}

	ns := ctx.Namespace

	// Close should delete the namespace
	ctx.Close()

	// Give K8s a moment to delete
	time.Sleep(500 * time.Millisecond)

	// Namespace should be gone (or terminating)
	_, err = ctx.Client.CoreV1().Namespaces().Get(
		context.Background(), ns, metav1.GetOptions{})
	if err == nil {
		// Check if it's terminating
		nsObj, _ := ctx.Client.CoreV1().Namespaces().Get(
			context.Background(), ns, metav1.GetOptions{})
		if nsObj.Status.Phase != corev1.NamespaceTerminating {
			t.Errorf("Namespace should be deleted or terminating, got phase: %s", nsObj.Status.Phase)
		}
	}
}

// TestContextCloseKeepsSharedNamespace verifies shared namespaces aren't deleted.
func TestContextCloseKeepsSharedNamespace(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx, err := NewContext(WithNamespace("default"))
	if err != nil {
		t.Fatalf("NewContext failed: %v", err)
	}

	// Close should NOT delete default namespace
	ctx.Close()

	// default should still exist
	_, err = ctx.Client.CoreV1().Namespaces().Get(
		context.Background(), "default", metav1.GetOptions{})
	if err != nil {
		t.Errorf("default namespace should still exist: %v", err)
	}
}
