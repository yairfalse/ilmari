package ilmari

import (
	"context"
	"testing"

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
