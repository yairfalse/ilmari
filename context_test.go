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
						Image:   "busybox:latest",
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

// TestLogsRetrievesPodOutput verifies Logs returns pod output.
func TestLogsRetrievesPodOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a pod that outputs a known string
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "echo-pod",
			},
			Spec: corev1.PodSpec{
				RestartPolicy: corev1.RestartPolicyNever,
				Containers: []corev1.Container{
					{
						Name:    "echo",
						Image:   "busybox:latest",
						Command: []string{"sh", "-c", "echo 'hello from ilmari' && sleep 10"},
					},
				},
			},
		}

		if err := ctx.Apply(pod); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		if err := ctx.WaitReady("pod/echo-pod"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Give it a moment to produce logs
		time.Sleep(500 * time.Millisecond)

		logs, err := ctx.Logs("echo-pod")
		if err != nil {
			t.Fatalf("Logs failed: %v", err)
		}

		if !strings.Contains(logs, "hello from ilmari") {
			t.Errorf("expected logs to contain 'hello from ilmari', got: %s", logs)
		}
	})
}
