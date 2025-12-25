package ilmari

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestStackDeploysServices(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Build a stack with two services
		stack := NewStack().
			Service("web").Image("nginx:alpine").Port(80).
			Service("worker").Image("busybox:1.36").Command("sleep", "300").
			Build()

		// Deploy and wait
		if err := ctx.Up(stack); err != nil {
			t.Fatalf("Up failed: %v", err)
		}

		// Verify both deployments exist
		webDeploy := &appsv1.Deployment{}
		if err := ctx.Get("web", webDeploy); err != nil {
			t.Fatalf("Get web deployment failed: %v", err)
		}
		if *webDeploy.Spec.Replicas != 1 {
			t.Errorf("expected 1 replica, got %d", *webDeploy.Spec.Replicas)
		}

		workerDeploy := &appsv1.Deployment{}
		if err := ctx.Get("worker", workerDeploy); err != nil {
			t.Fatalf("Get worker deployment failed: %v", err)
		}

		// Verify web service exists (worker has no port, so no service)
		webSvc := &corev1.Service{}
		if err := ctx.Get("web", webSvc); err != nil {
			t.Fatalf("Get web service failed: %v", err)
		}

		// Test port forward to web service
		pf := ctx.PortForward("svc/web", 80)
		defer pf.Close()

		resp, err := pf.Get("/")
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != 200 {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}
	})
}

// TestStackWithResources verifies Resources sets resource limits.
func TestStackWithResources(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		stack := NewStack().
			Service("limited").Image("busybox:1.36").Command("sleep", "300").
			Resources("100m", "64Mi").
			Build()

		if err := ctx.Up(stack); err != nil {
			t.Fatalf("Up failed: %v", err)
		}

		deploy := &appsv1.Deployment{}
		if err := ctx.Get("limited", deploy); err != nil {
			t.Fatalf("Get failed: %v", err)
		}

		container := deploy.Spec.Template.Spec.Containers[0]
		cpuLimit := container.Resources.Limits.Cpu()
		memLimit := container.Resources.Limits.Memory()

		if cpuLimit.String() != "100m" {
			t.Errorf("expected cpu limit 100m, got %s", cpuLimit.String())
		}
		if memLimit.String() != "64Mi" {
			t.Errorf("expected memory limit 64Mi, got %s", memLimit.String())
		}
	})
}

// TestRetrySucceedsOnTransientFailure verifies Retry with exponential backoff.
func TestRetrySucceedsOnTransientFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		attempts := 0
		err := ctx.Retry(3, func() error {
			attempts++
			if attempts < 3 {
				return context.DeadlineExceeded // transient error
			}
			return nil
		})

		if err != nil {
			t.Fatalf("Retry failed: %v", err)
		}
		if attempts != 3 {
			t.Errorf("expected 3 attempts, got %d", attempts)
		}
	})
}

// TestRetryFailsAfterMaxAttempts verifies Retry returns error after max attempts.
func TestRetryFailsAfterMaxAttempts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		attempts := 0
		err := ctx.Retry(3, func() error {
			attempts++
			return context.DeadlineExceeded
		})

		if err == nil {
			t.Fatal("expected error after max attempts")
		}
		if attempts != 3 {
			t.Errorf("expected 3 attempts, got %d", attempts)
		}
	})
}

// TestKillDeletesPod verifies Kill deletes a pod immediately.
func TestKillDeletesPod(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "kill-test",
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
		if err := ctx.WaitReady("pod/kill-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Kill the pod
		if err := ctx.Kill("pod/kill-test"); err != nil {
			t.Fatalf("Kill failed: %v", err)
		}

		// Pod should be gone or terminating
		time.Sleep(500 * time.Millisecond)
		got := &corev1.Pod{}
		err := ctx.Get("kill-test", got)
		if err == nil && got.DeletionTimestamp == nil {
			t.Error("expected pod to be deleted or terminating")
		}
	})
}

func TestLoadYAML(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create temp YAML file
		yamlContent := `apiVersion: v1
kind: ConfigMap
metadata:
  name: yaml-test
data:
  key: value
`
		tmpDir := t.TempDir()
		yamlPath := filepath.Join(tmpDir, "test.yaml")
		if err := os.WriteFile(yamlPath, []byte(yamlContent), 0644); err != nil {
			t.Fatalf("Failed to write yaml file: %v", err)
		}

		// Load it
		if err := ctx.LoadYAML(yamlPath); err != nil {
			t.Fatalf("LoadYAML failed: %v", err)
		}

		// Verify ConfigMap was created
		cm := &corev1.ConfigMap{}
		if err := ctx.Get("yaml-test", cm); err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if cm.Data["key"] != "value" {
			t.Errorf("expected key=value, got key=%s", cm.Data["key"])
		}
	})
}

// TestLoadYAMLMultiDoc verifies LoadYAML handles multi-document YAML.
func TestLoadYAMLMultiDoc(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		yamlContent := `apiVersion: v1
kind: ConfigMap
metadata:
  name: multi-1
data:
  n: "1"
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: multi-2
data:
  n: "2"
`
		tmpDir := t.TempDir()
		yamlPath := filepath.Join(tmpDir, "multi.yaml")
		if err := os.WriteFile(yamlPath, []byte(yamlContent), 0644); err != nil {
			t.Fatalf("Failed to write yaml file: %v", err)
		}

		if err := ctx.LoadYAML(yamlPath); err != nil {
			t.Fatalf("LoadYAML failed: %v", err)
		}

		// Both should exist
		cm1 := &corev1.ConfigMap{}
		if err := ctx.Get("multi-1", cm1); err != nil {
			t.Fatalf("Get multi-1 failed: %v", err)
		}
		cm2 := &corev1.ConfigMap{}
		if err := ctx.Get("multi-2", cm2); err != nil {
			t.Fatalf("Get multi-2 failed: %v", err)
		}
	})
}

// TestLoadYAMLDir verifies LoadYAMLDir loads all YAML files from directory.
func TestLoadYAMLDir(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		tmpDir := t.TempDir()

		// Create two yaml files
		yaml1 := `apiVersion: v1
kind: ConfigMap
metadata:
  name: dir-test-1
`
		yaml2 := `apiVersion: v1
kind: ConfigMap
metadata:
  name: dir-test-2
`
		if err := os.WriteFile(filepath.Join(tmpDir, "a.yaml"), []byte(yaml1), 0644); err != nil {
			t.Fatalf("Failed to write a.yaml: %v", err)
		}
		if err := os.WriteFile(filepath.Join(tmpDir, "b.yml"), []byte(yaml2), 0644); err != nil {
			t.Fatalf("Failed to write b.yml: %v", err)
		}
		// Also create a non-yaml file that should be ignored
		if err := os.WriteFile(filepath.Join(tmpDir, "ignore.txt"), []byte("ignored"), 0644); err != nil {
			t.Fatalf("Failed to write ignore.txt: %v", err)
		}

		if err := ctx.LoadYAMLDir(tmpDir); err != nil {
			t.Fatalf("LoadYAMLDir failed: %v", err)
		}

		// Both ConfigMaps should exist
		cm1 := &corev1.ConfigMap{}
		if err := ctx.Get("dir-test-1", cm1); err != nil {
			t.Fatalf("Get dir-test-1 failed: %v", err)
		}
		cm2 := &corev1.ConfigMap{}
		if err := ctx.Get("dir-test-2", cm2); err != nil {
			t.Fatalf("Get dir-test-2 failed: %v", err)
		}
	})
}

// TestLoadYAMLWindowsLineEndings verifies YAML with CRLF line endings works.
func TestLoadYAMLWindowsLineEndings(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// YAML with Windows line endings (CRLF)
		yamlContent := "apiVersion: v1\r\nkind: ConfigMap\r\nmetadata:\r\n  name: crlf-test\r\ndata:\r\n  key: value\r\n"

		tmpDir := t.TempDir()
		yamlPath := filepath.Join(tmpDir, "crlf.yaml")
		if err := os.WriteFile(yamlPath, []byte(yamlContent), 0644); err != nil {
			t.Fatalf("Failed to write yaml file: %v", err)
		}

		if err := ctx.LoadYAML(yamlPath); err != nil {
			t.Fatalf("LoadYAML with CRLF failed: %v", err)
		}

		cm := &corev1.ConfigMap{}
		if err := ctx.Get("crlf-test", cm); err != nil {
			t.Fatalf("Get failed: %v", err)
		}
	})
}

func TestDeploymentBuilder(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		deploy := Deployment("fluent-test").
			Image("nginx:alpine").
			Replicas(2).
			Port(80).
			Env("FOO", "bar").
			Build()

		if err := ctx.Apply(deploy); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		if err := ctx.WaitReady("deployment/fluent-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		got := &appsv1.Deployment{}
		if err := ctx.Get("fluent-test", got); err != nil {
			t.Fatalf("Get failed: %v", err)
		}

		if *got.Spec.Replicas != 2 {
			t.Errorf("expected 2 replicas, got %d", *got.Spec.Replicas)
		}

		container := got.Spec.Template.Spec.Containers[0]
		if container.Image != "nginx:alpine" {
			t.Errorf("expected image nginx:alpine, got %s", container.Image)
		}
	})
}

// TestDeploymentBuilderWithProbes verifies WithProbes adds sensible defaults.
func TestDeploymentBuilderWithProbes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		deploy := Deployment("probes-test").
			Image("nginx:alpine").
			Port(80).
			WithProbes().
			Build()

		if err := ctx.Apply(deploy); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		got := &appsv1.Deployment{}
		if err := ctx.Get("probes-test", got); err != nil {
			t.Fatalf("Get failed: %v", err)
		}

		container := got.Spec.Template.Spec.Containers[0]
		if container.LivenessProbe == nil {
			t.Error("expected liveness probe")
		}
		if container.ReadinessProbe == nil {
			t.Error("expected readiness probe")
		}
	})
}

// TestLoadFixture verifies LoadFixture loads YAML with overrides.
func TestLoadFixture(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create fixture file
		yamlContent := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: fixture-test
spec:
  replicas: 3
  selector:
    matchLabels:
      app: fixture-test
  template:
    metadata:
      labels:
        app: fixture-test
    spec:
      containers:
      - name: main
        image: nginx:1.20
        ports:
        - containerPort: 80
`
		tmpDir := t.TempDir()
		fixturePath := filepath.Join(tmpDir, "deployment.yaml")
		if err := os.WriteFile(fixturePath, []byte(yamlContent), 0644); err != nil {
			t.Fatalf("Failed to write fixture: %v", err)
		}

		// Load with overrides
		deploy, err := ctx.LoadFixture(fixturePath).
			WithImage("nginx:alpine").
			WithReplicas(1).
			Build()
		if err != nil {
			t.Fatalf("Build failed: %v", err)
		}

		if err := ctx.Apply(deploy); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		if err := ctx.WaitReady("deployment/fixture-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		got := &appsv1.Deployment{}
		if err := ctx.Get("fixture-test", got); err != nil {
			t.Fatalf("Get failed: %v", err)
		}

		// Should have overridden values
		if *got.Spec.Replicas != 1 {
			t.Errorf("expected 1 replica (overridden), got %d", *got.Spec.Replicas)
		}
		if got.Spec.Template.Spec.Containers[0].Image != "nginx:alpine" {
			t.Errorf("expected image nginx:alpine (overridden), got %s", got.Spec.Template.Spec.Containers[0].Image)
		}
	})
}
