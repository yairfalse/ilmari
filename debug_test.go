package ilmari

import (
	"bytes"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestDebugDeploymentOutputsStatus verifies Debug outputs deployment diagnostics.
func TestDebugDeploymentOutputsStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a deployment
		deploy := Deployment("debug-deploy-test").
			Image("busybox:1.36").
			Replicas(1).
			Build()
		deploy.Spec.Template.Spec.Containers[0].Command = []string{"sh", "-c", "echo 'debug test output' && sleep 300"}

		if err := ctx.Apply(deploy); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		if err := ctx.WaitReady("deployment/debug-deploy-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Capture debug output
		var buf bytes.Buffer
		err := ctx.DebugWithOptions("deployment/debug-deploy-test", DebugOptions{
			Writer:   &buf,
			LogLines: 10,
		})
		if err != nil {
			t.Fatalf("Debug failed: %v", err)
		}

		output := buf.String()

		// Verify output contains expected sections
		if !strings.Contains(output, "=== deployment/debug-deploy-test ===") {
			t.Errorf("expected deployment header, got: %s", output)
		}
		if !strings.Contains(output, "Status:") {
			t.Errorf("expected status line, got: %s", output)
		}
		if !strings.Contains(output, "=== Pods ===") {
			t.Errorf("expected pods section, got: %s", output)
		}
		if !strings.Contains(output, "=== Events ===") {
			t.Errorf("expected events section, got: %s", output)
		}
		if !strings.Contains(output, "=== Logs") {
			t.Errorf("expected logs section, got: %s", output)
		}
	})
}

// TestDebugPodOutputsStatus verifies Debug outputs pod diagnostics.
func TestDebugPodOutputsStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a pod
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "debug-pod-test",
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:    "main",
						Image:   "busybox:1.36",
						Command: []string{"sh", "-c", "echo 'hello from debug test' && sleep 300"},
					},
				},
			},
		}

		if err := ctx.Apply(pod); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		if err := ctx.WaitReady("pod/debug-pod-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Capture debug output
		var buf bytes.Buffer
		err := ctx.DebugWithOptions("pod/debug-pod-test", DebugOptions{
			Writer:   &buf,
			LogLines: 10,
		})
		if err != nil {
			t.Fatalf("Debug failed: %v", err)
		}

		output := buf.String()

		// Verify output contains expected content
		if !strings.Contains(output, "=== Pods ===") {
			t.Errorf("expected pods section, got: %s", output)
		}
		if !strings.Contains(output, "debug-pod-test") {
			t.Errorf("expected pod name in output, got: %s", output)
		}
		if !strings.Contains(output, "hello from debug test") {
			t.Errorf("expected log output, got: %s", output)
		}
	})
}

// TestDebugWithOptionsHideEvents verifies HideEvents option works.
func TestDebugWithOptionsHideEvents(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a pod
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "debug-hide-events-test",
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:    "main",
						Image:   "busybox:1.36",
						Command: []string{"sleep", "300"},
					},
				},
			},
		}

		if err := ctx.Apply(pod); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		if err := ctx.WaitReady("pod/debug-hide-events-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Capture debug output with events hidden
		var buf bytes.Buffer
		err := ctx.DebugWithOptions("pod/debug-hide-events-test", DebugOptions{
			Writer:     &buf,
			HideEvents: true,
		})
		if err != nil {
			t.Fatalf("Debug failed: %v", err)
		}

		output := buf.String()

		// Should NOT contain events section
		if strings.Contains(output, "=== Events ===") {
			t.Errorf("expected no events section when HideEvents=true, got: %s", output)
		}
		// Should still have logs
		if !strings.Contains(output, "=== Logs") {
			t.Errorf("expected logs section, got: %s", output)
		}
	})
}

// TestDebugWithOptionsHideLogs verifies HideLogs option works.
func TestDebugWithOptionsHideLogs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a pod
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "debug-hide-logs-test",
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:    "main",
						Image:   "busybox:1.36",
						Command: []string{"sleep", "300"},
					},
				},
			},
		}

		if err := ctx.Apply(pod); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		if err := ctx.WaitReady("pod/debug-hide-logs-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Capture debug output with logs hidden
		var buf bytes.Buffer
		err := ctx.DebugWithOptions("pod/debug-hide-logs-test", DebugOptions{
			Writer:   &buf,
			HideLogs: true,
		})
		if err != nil {
			t.Fatalf("Debug failed: %v", err)
		}

		output := buf.String()

		// Should NOT contain logs section
		if strings.Contains(output, "=== Logs") {
			t.Errorf("expected no logs section when HideLogs=true, got: %s", output)
		}
		// Should still have events
		if !strings.Contains(output, "=== Events ===") {
			t.Errorf("expected events section, got: %s", output)
		}
	})
}

// TestDebugInvalidResourceFormat verifies error on invalid format.
func TestDebugInvalidResourceFormat(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		err := ctx.Debug("invalid-no-slash")
		if err == nil {
			t.Error("expected error for invalid resource format")
		}
		if !strings.Contains(err.Error(), "invalid resource format") {
			t.Errorf("expected 'invalid resource format' error, got: %v", err)
		}
	})
}

// TestDebugUnsupportedKind verifies error on unsupported resource kind.
func TestDebugUnsupportedKind(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		err := ctx.Debug("configmap/test")
		if err == nil {
			t.Error("expected error for unsupported kind")
		}
		if !strings.Contains(err.Error(), "unsupported resource kind") {
			t.Errorf("expected 'unsupported resource kind' error, got: %v", err)
		}
	})
}

// TestDebugStatefulSetOutputsStatus verifies Debug works with StatefulSets.
func TestDebugStatefulSetOutputsStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a statefulset
		ss := NewStack().
			Service("debug-ss-test").
			Image("busybox:1.36").
			Command("sh", "-c", "echo 'statefulset debug' && sleep 300").
			Replicas(1).
			Build()

		if err := ctx.Up(ss); err != nil {
			t.Fatalf("Up failed: %v", err)
		}

		if err := ctx.WaitReady("deployment/debug-ss-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Debug the deployment created by Stack
		var buf bytes.Buffer
		err := ctx.DebugWithOptions("deployment/debug-ss-test", DebugOptions{
			Writer:   &buf,
			LogLines: 5,
		})
		if err != nil {
			t.Fatalf("Debug failed: %v", err)
		}

		output := buf.String()
		if !strings.Contains(output, "Status:") {
			t.Errorf("expected status in output, got: %s", output)
		}
	})
}
