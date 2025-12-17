package ilmari

import (
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
)

func TestScaleChangesReplicas(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		deploy := Deployment("scale-test").
			Image("nginx:alpine").
			Replicas(1).
			Build()

		if err := ctx.Apply(deploy); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		if err := ctx.WaitReady("deployment/scale-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Scale up
		if err := ctx.Scale("deployment/scale-test", 3); err != nil {
			t.Fatalf("Scale failed: %v", err)
		}

		// Verify
		var updated appsv1.Deployment
		if err := ctx.Get("scale-test", &updated); err != nil {
			t.Fatalf("Get failed: %v", err)
		}

		if *updated.Spec.Replicas != 3 {
			t.Errorf("expected 3 replicas, got %d", *updated.Spec.Replicas)
		}
	})
}

// TestRestartTriggersRollingUpdate verifies Restart triggers a rolling update.
func TestRestartTriggersRollingUpdate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		deploy := Deployment("restart-test").
			Image("nginx:alpine").
			Replicas(1).
			Build()

		if err := ctx.Apply(deploy); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		if err := ctx.WaitReady("deployment/restart-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Get original annotation
		var before appsv1.Deployment
		ctx.Get("restart-test", &before)
		beforeAnnotations := before.Spec.Template.Annotations

		// Restart
		if err := ctx.Restart("deployment/restart-test"); err != nil {
			t.Fatalf("Restart failed: %v", err)
		}

		// Verify restart annotation was added
		var after appsv1.Deployment
		ctx.Get("restart-test", &after)

		restartAt := after.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"]
		if restartAt == "" {
			t.Error("expected restartedAt annotation to be set")
		}

		if beforeAnnotations["kubectl.kubernetes.io/restartedAt"] == restartAt {
			t.Error("restartedAt should have changed")
		}
	})
}

// TestRollbackToRevision verifies Rollback reverts to previous revision.
func TestRollbackToRevision(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create v1
		deploy := Deployment("rollback-test").
			Image("nginx:1.24").
			Replicas(1).
			Build()

		if err := ctx.Apply(deploy); err != nil {
			t.Fatalf("Apply v1 failed: %v", err)
		}

		if err := ctx.WaitReady("deployment/rollback-test"); err != nil {
			t.Fatalf("WaitReady v1 failed: %v", err)
		}

		// Update to v2
		deploy.Spec.Template.Spec.Containers[0].Image = "nginx:1.25"
		if err := ctx.Apply(deploy); err != nil {
			t.Fatalf("Apply v2 failed: %v", err)
		}

		// Wait for rollout
		time.Sleep(2 * time.Second)

		// Rollback
		if err := ctx.Rollback("deployment/rollback-test"); err != nil {
			t.Fatalf("Rollback failed: %v", err)
		}

		// Wait for rollback
		time.Sleep(2 * time.Second)

		// Verify image is back to v1
		var after appsv1.Deployment
		ctx.Get("rollback-test", &after)

		image := after.Spec.Template.Spec.Containers[0].Image
		if image != "nginx:1.24" {
			t.Errorf("expected nginx:1.24, got %s", image)
		}
	})
}

// TestCanIChecksPermissions verifies CanI checks RBAC permissions.
func TestCanIChecksPermissions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Should be able to create pods in test namespace
		canCreate, err := ctx.CanI("create", "pods")
		if err != nil {
			t.Fatalf("CanI failed: %v", err)
		}

		if !canCreate {
			t.Error("expected to be able to create pods")
		}

		// Should be able to list deployments
		canList, err := ctx.CanI("list", "deployments")
		if err != nil {
			t.Fatalf("CanI list failed: %v", err)
		}

		if !canList {
			t.Error("expected to be able to list deployments")
		}
	})
}
