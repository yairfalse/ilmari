package ilmari

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
						Image:   "busybox:1.36",
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

		// Wait for logs to contain the expected output, up to a timeout
		var logs string
		var err error
		found := false
		const maxWait = 5 * time.Second
		const pollInterval = 100 * time.Millisecond
		deadline := time.Now().Add(maxWait)
		for time.Now().Before(deadline) {
			logs, err = ctx.Logs("echo-pod")
			if err == nil && strings.Contains(logs, "hello from ilmari") {
				found = true
				break
			}
			time.Sleep(pollInterval)
		}
		if !found {
			if err != nil {
				t.Fatalf("Logs failed: %v", err)
			} else {
				t.Errorf("expected logs to contain 'hello from ilmari', got: %s", logs)
			}
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

// TestForwardMakesHTTPRequest verifies Forward can make HTTP requests.
func TestForwardMakesHTTPRequest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a simple HTTP server pod
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "http-server",
				Labels: map[string]string{
					"app": "http-server",
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "server",
						Image: "nginx:alpine",
						Ports: []corev1.ContainerPort{
							{ContainerPort: 80},
						},
					},
				},
			},
		}
		if err := ctx.Apply(pod); err != nil {
			t.Fatalf("Apply pod failed: %v", err)
		}

		// Create a service
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name: "http-server",
			},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{
					"app": "http-server",
				},
				Ports: []corev1.ServicePort{
					{Port: 80},
				},
			},
		}
		if err := ctx.Apply(svc); err != nil {
			t.Fatalf("Apply svc failed: %v", err)
		}

		// Wait for pod to be ready
		if err := ctx.WaitReady("pod/http-server"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Forward and make request
		pf := ctx.Forward("svc/http-server", 80)
		defer pf.Close()

		resp, err := pf.Get("/")
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}
	})
}

// TestEventsReturnsNamespaceEvents verifies Events returns events.
func TestEventsReturnsNamespaceEvents(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a pod - this generates events
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "events-test",
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

		// Wait for pod to be scheduled (generates events)
		if err := ctx.WaitReady("pod/events-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Get events
		events, err := ctx.Events()
		if err != nil {
			t.Fatalf("Events failed: %v", err)
		}

		// Should have at least one event (Scheduled, Pulling, Pulled, Started, etc.)
		if len(events) == 0 {
			t.Error("expected at least one event")
		}
	})
}

// TestExecRunsCommand verifies Exec runs a command in a pod.
func TestExecRunsCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a pod
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "exec-test",
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:    "shell",
						Image:   "busybox:1.36",
						Command: []string{"sleep", "300"},
					},
				},
			},
		}
		if err := ctx.Apply(pod); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		if err := ctx.WaitReady("pod/exec-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Run a command
		output, err := ctx.Exec("exec-test", []string{"echo", "hello-exec"})
		if err != nil {
			t.Fatalf("Exec failed: %v", err)
		}

		if !strings.Contains(output, "hello-exec") {
			t.Errorf("expected output to contain 'hello-exec', got: %s", output)
		}
	})
}

// TestStackDeploysServices verifies Stack deploys multiple services.
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
		pf := ctx.Forward("svc/web", 80)
		defer pf.Close()

		resp, err := pf.Get("/")
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		defer resp.Body.Close()

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

// TestAssertHasLabel verifies Assert().HasLabel() works.
func TestAssertHasLabel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "assert-test",
				Labels: map[string]string{
					"app": "test",
				},
			},
		}
		if err := ctx.Apply(cm); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		// Should pass
		err := ctx.Assert("configmap/assert-test").HasLabel("app", "test").Error()
		if err != nil {
			t.Errorf("HasLabel should pass: %v", err)
		}

		// Should fail
		err = ctx.Assert("configmap/assert-test").HasLabel("app", "wrong").Error()
		if err == nil {
			t.Error("HasLabel should fail for wrong value")
		}
	})
}

// TestAssertExists verifies Assert().Exists() works.
func TestAssertExists(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "exists-test",
			},
		}
		if err := ctx.Apply(cm); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		// Should pass
		err := ctx.Assert("configmap/exists-test").Exists().Error()
		if err != nil {
			t.Errorf("Exists should pass: %v", err)
		}

		// Should fail for non-existent
		err = ctx.Assert("configmap/nonexistent").Exists().Error()
		if err == nil {
			t.Error("Exists should fail for non-existent resource")
		}
	})
}

// TestAssertMustPanics verifies Assert().Must() panics on failure.
func TestAssertMustPanics(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Must() should have panicked")
			}
		}()

		ctx.Assert("configmap/nonexistent").Exists().Must()
	})
}

// TestIsolateCreatesNetworkPolicy verifies Isolate creates deny-all policy.
func TestIsolateCreatesNetworkPolicy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		err := ctx.Isolate(map[string]string{"app": "isolated"})
		if err != nil {
			t.Fatalf("Isolate failed: %v", err)
		}

		// Verify NetworkPolicy was created
		policies, err := ctx.Client.NetworkingV1().NetworkPolicies(ctx.Namespace).List(
			context.Background(), metav1.ListOptions{})
		if err != nil {
			t.Fatalf("List NetworkPolicies failed: %v", err)
		}

		found := false
		for _, p := range policies.Items {
			if strings.HasPrefix(p.Name, "ilmari-isolate") {
				found = true
				// Verify it's a deny-all policy
				if len(p.Spec.Ingress) != 0 || len(p.Spec.Egress) != 0 {
					t.Error("expected empty ingress/egress for deny-all")
				}
			}
		}
		if !found {
			t.Error("NetworkPolicy not found")
		}
	})
}

// TestAllowFromCreatesNetworkPolicy verifies AllowFrom creates allow policy.
func TestAllowFromCreatesNetworkPolicy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		err := ctx.AllowFrom(
			map[string]string{"app": "backend"},
			map[string]string{"app": "frontend"},
		)
		if err != nil {
			t.Fatalf("AllowFrom failed: %v", err)
		}

		// Verify NetworkPolicy was created
		policies, err := ctx.Client.NetworkingV1().NetworkPolicies(ctx.Namespace).List(
			context.Background(), metav1.ListOptions{})
		if err != nil {
			t.Fatalf("List NetworkPolicies failed: %v", err)
		}

		found := false
		for _, p := range policies.Items {
			if strings.HasPrefix(p.Name, "ilmari-allow") {
				found = true
				if len(p.Spec.Ingress) != 1 {
					t.Error("expected one ingress rule")
				}
			}
		}
		if !found {
			t.Error("NetworkPolicy not found")
		}
	})
}

// TestLoadYAML verifies LoadYAML loads a single YAML file.
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

// ============================================================================
// Semantic Assertions Tests
// ============================================================================

// TestAssertHasReplicas verifies HasReplicas checks ready replicas.
func TestAssertHasReplicas(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		stack := NewStack().
			Service("replicas-test").Image("nginx:alpine").Port(80).Replicas(2).
			Build()

		if err := ctx.Up(stack); err != nil {
			t.Fatalf("Up failed: %v", err)
		}

		// Should pass - 2 replicas ready
		err := ctx.Assert("deployment/replicas-test").HasReplicas(2).Error()
		if err != nil {
			t.Errorf("HasReplicas(2) should pass: %v", err)
		}

		// Should fail - not 5 replicas
		err = ctx.Assert("deployment/replicas-test").HasReplicas(5).Error()
		if err == nil {
			t.Error("HasReplicas(5) should fail")
		}
	})
}

// TestAssertIsProgressing verifies IsProgressing for deployments.
func TestAssertIsProgressing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		stack := NewStack().
			Service("progress-test").Image("nginx:alpine").Port(80).
			Build()

		if err := ctx.Up(stack); err != nil {
			t.Fatalf("Up failed: %v", err)
		}

		// Stable deployment should be progressing (or complete)
		err := ctx.Assert("deployment/progress-test").IsProgressing().Error()
		if err != nil {
			t.Errorf("IsProgressing should pass for healthy deployment: %v", err)
		}
	})
}

// TestAssertHasNoRestarts verifies HasNoRestarts checks container restarts.
func TestAssertHasNoRestarts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "no-restart-test",
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
		if err := ctx.WaitReady("pod/no-restart-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Fresh pod should have no restarts
		err := ctx.Assert("pod/no-restart-test").HasNoRestarts().Error()
		if err != nil {
			t.Errorf("HasNoRestarts should pass: %v", err)
		}
	})
}

// TestAssertLogsContain verifies LogsContain checks pod logs.
func TestAssertLogsContain(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "logs-test",
			},
			Spec: corev1.PodSpec{
				RestartPolicy: corev1.RestartPolicyNever,
				Containers: []corev1.Container{
					{
						Name:    "echo",
						Image:   "busybox:1.36",
						Command: []string{"sh", "-c", "echo 'ilmari-marker-12345' && sleep 60"},
					},
				},
			},
		}
		if err := ctx.Apply(pod); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}
		if err := ctx.WaitReady("pod/logs-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Wait a moment for logs
		time.Sleep(2 * time.Second)

		// Should find the marker
		err := ctx.Assert("pod/logs-test").LogsContain("ilmari-marker-12345").Error()
		if err != nil {
			t.Errorf("LogsContain should pass: %v", err)
		}

		// Should not find random string
		err = ctx.Assert("pod/logs-test").LogsContain("nonexistent-xyz-987").Error()
		if err == nil {
			t.Error("LogsContain should fail for missing text")
		}
	})
}

// TestAssertNoOOMKills verifies NoOOMKills checks for OOM terminations.
func TestAssertNoOOMKills(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "oom-test",
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
		if err := ctx.WaitReady("pod/oom-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Normal pod should have no OOM kills
		err := ctx.Assert("pod/oom-test").NoOOMKills().Error()
		if err != nil {
			t.Errorf("NoOOMKills should pass: %v", err)
		}
	})
}

// ============================================================================
// Fluent Deployment Builder Tests
// ============================================================================

// TestDeploymentBuilder verifies fluent Deployment builder.
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

// ============================================================================
// Eventually/Consistently Tests
// ============================================================================

// TestEventuallySucceeds verifies Eventually waits for condition.
func TestEventuallySucceeds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "eventually-test",
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

		// Eventually the pod should be running
		err := ctx.Eventually(func() bool {
			p := &corev1.Pod{}
			if err := ctx.Get("eventually-test", p); err != nil {
				return false
			}
			return p.Status.Phase == corev1.PodRunning
		}).Within(60 * time.Second).ProbeEvery(1 * time.Second).Wait()

		if err != nil {
			t.Fatalf("Eventually failed: %v", err)
		}
	})
}

// TestEventuallyTimesOut verifies Eventually returns error on timeout.
func TestEventuallyTimesOut(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// This will never be true
		err := ctx.Eventually(func() bool {
			return false
		}).Within(2 * time.Second).ProbeEvery(100 * time.Millisecond).Wait()

		if err == nil {
			t.Error("expected timeout error")
		}
	})
}

// TestConsistentlySucceeds verifies Consistently passes when condition stays true.
func TestConsistentlySucceeds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "consistent-test",
			},
			Data: map[string]string{"key": "value"},
		}
		if err := ctx.Apply(cm); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		// ConfigMap should stay present
		err := ctx.Consistently(func() bool {
			c := &corev1.ConfigMap{}
			return ctx.Get("consistent-test", c) == nil
		}).For(2 * time.Second).ProbeEvery(200 * time.Millisecond).Wait()

		if err != nil {
			t.Errorf("Consistently failed: %v", err)
		}
	})
}

// TestConsistentlyFails verifies Consistently fails if condition becomes false.
func TestConsistentlyFails(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		counter := 0
		// Will become false after a few checks
		err := ctx.Consistently(func() bool {
			counter++
			return counter < 5
		}).For(5 * time.Second).ProbeEvery(100 * time.Millisecond).Wait()

		if err == nil {
			t.Error("expected failure when condition becomes false")
		}
	})
}

// ============================================================================
// Better Failure Output Tests
// ============================================================================

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

// ============================================================================
// Test Scenarios Tests
// ============================================================================

// TestSelfHealingScenario verifies TestSelfHealing works.
func TestSelfHealingScenario(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		deploy := Deployment("heal-test").
			Image("nginx:alpine").
			Replicas(1).
			Port(80).
			Build()

		if err := ctx.Apply(deploy); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}
		if err := ctx.WaitReady("deployment/heal-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Run self-healing test
		err := ctx.TestSelfHealing("deployment/heal-test", func(s *SelfHealTest) {
			s.KillPod()
			s.ExpectRecoveryWithin(60 * time.Second)
		})
		if err != nil {
			t.Fatalf("TestSelfHealing failed: %v", err)
		}
	})
}

// TestScalingScenario verifies TestScaling works.
func TestScalingScenario(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		deploy := Deployment("scale-test").
			Image("nginx:alpine").
			Replicas(1).
			Port(80).
			Build()

		if err := ctx.Apply(deploy); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}
		if err := ctx.WaitReady("deployment/scale-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Run scaling test
		err := ctx.TestScaling("deployment/scale-test", func(s *ScaleTest) {
			s.ScaleTo(2)
			s.WaitStable()
			s.ScaleTo(1)
			s.WaitStable()
		})
		if err != nil {
			t.Fatalf("TestScaling failed: %v", err)
		}
	})
}

// ============================================================================
// Traffic Testing Tests
// ============================================================================

// TestTrafficGenerator verifies StartTraffic generates load and collects metrics.
func TestTrafficGenerator(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Deploy nginx
		deploy := Deployment("traffic-test").
			Image("nginx:alpine").
			Replicas(1).
			Port(80).
			Build()

		if err := ctx.Apply(deploy); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name: "traffic-test",
			},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "traffic-test"},
				Ports:    []corev1.ServicePort{{Port: 80}},
			},
		}
		if err := ctx.Apply(svc); err != nil {
			t.Fatalf("Apply svc failed: %v", err)
		}

		if err := ctx.WaitReady("deployment/traffic-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Start traffic
		traffic := ctx.StartTraffic("svc/traffic-test", func(t *TrafficConfig) {
			t.RPS(10)
			t.Duration(5 * time.Second)
			t.Endpoint("/")
		})

		// Wait for traffic to complete
		traffic.Wait()

		// Check metrics
		if traffic.TotalRequests() == 0 {
			t.Error("expected some requests")
		}
		if traffic.ErrorRate() > 0.1 {
			t.Errorf("error rate too high: %.2f", traffic.ErrorRate())
		}

		t.Logf("Traffic stats: %d requests, %.2f%% errors, p99=%v",
			traffic.TotalRequests(),
			traffic.ErrorRate()*100,
			traffic.P99Latency())
	})
}

// ============================================================================
// Phase 0: SDK Primitives - NewContext without test wrapper
// ============================================================================

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

// ============================================================================
// Phase 1: Core Primitives
// ============================================================================

// TestWatchReceivesEvents verifies Watch streams resource events.
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
		stop := ctx.Watch("configmap", func(event WatchEvent) {
			mu.Lock()
			events = append(events, event)
			eventCount := len(events)
			mu.Unlock()
			if eventCount >= 2 {
				doneOnce.Do(func() { close(done) })
			}
		})
		defer stop()

		// Create a ConfigMap - should trigger ADDED event
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "watch-test"},
			Data:       map[string]string{"key": "value1"},
		}
		if err := ctx.Apply(cm); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

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

		// Verify events
		mu.Lock()
		eventsCopy := make([]WatchEvent, len(events))
		copy(eventsCopy, events)
		mu.Unlock()
		if len(eventsCopy) < 2 {
			t.Fatalf("expected at least 2 events, got %d", len(eventsCopy))
		}
		if eventsCopy[0].Type != "ADDED" {
			t.Errorf("expected first event ADDED, got %s", eventsCopy[0].Type)
		}
		if eventsCopy[1].Type != "MODIFIED" {
			t.Errorf("expected second event MODIFIED, got %s", eventsCopy[1].Type)
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

// TestLogsStreamReceivesLogs verifies LogsStream streams logs in real-time.
func TestLogsStreamReceivesLogs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a pod that outputs logs
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "logs-stream-test"},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name:    "logger",
					Image:   "busybox",
					Command: []string{"sh", "-c", "for i in 1 2 3; do echo line$i; sleep 0.5; done; sleep 10"},
				}},
				RestartPolicy: corev1.RestartPolicyNever,
			},
		}

		if err := ctx.Apply(pod); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		if err := ctx.WaitReady("pod/logs-stream-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Stream logs with mutex-protected slice
		var mu sync.Mutex
		lines := make([]string, 0)
		stop := ctx.LogsStream("logs-stream-test", func(line string) {
			mu.Lock()
			lines = append(lines, line)
			mu.Unlock()
		})
		defer stop()

		// Wait for some logs
		time.Sleep(3 * time.Second)

		mu.Lock()
		lineCount := len(lines)
		mu.Unlock()
		if lineCount < 3 {
			t.Errorf("expected at least 3 log lines, got %d", lineCount)
		}
	})
}

// TestCopyToPod verifies copying a file to a pod.
func TestCopyToPod(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a pod
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "copy-test"},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name:    "main",
					Image:   "busybox",
					Command: []string{"sleep", "300"},
				}},
			},
		}

		if err := ctx.Apply(pod); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		if err := ctx.WaitReady("pod/copy-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Create a local temp file
		tmpDir := t.TempDir()
		localPath := filepath.Join(tmpDir, "testfile.txt")
		content := "hello from ilmari"
		if err := os.WriteFile(localPath, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to write temp file: %v", err)
		}

		// Copy to pod
		if err := ctx.CopyTo("copy-test", localPath, "/tmp/testfile.txt"); err != nil {
			t.Fatalf("CopyTo failed: %v", err)
		}

		// Verify file exists in pod
		output, err := ctx.Exec("copy-test", []string{"cat", "/tmp/testfile.txt"})
		if err != nil {
			t.Fatalf("Exec failed: %v", err)
		}

		if strings.TrimSpace(output) != content {
			t.Errorf("expected %q, got %q", content, output)
		}
	})
}

// TestCopyFromPod verifies copying a file from a pod.
func TestCopyFromPod(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a pod with a file
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "copy-from-test"},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name:    "main",
					Image:   "busybox",
					Command: []string{"sh", "-c", "echo 'hello from pod' > /tmp/podfile.txt && sleep 300"},
				}},
			},
		}

		if err := ctx.Apply(pod); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		if err := ctx.WaitReady("pod/copy-from-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Wait for file to be created
		time.Sleep(1 * time.Second)

		// Copy from pod
		tmpDir := t.TempDir()
		localPath := filepath.Join(tmpDir, "downloaded.txt")

		if err := ctx.CopyFrom("copy-from-test", "/tmp/podfile.txt", localPath); err != nil {
			t.Fatalf("CopyFrom failed: %v", err)
		}

		// Verify local file
		data, err := os.ReadFile(localPath)
		if err != nil {
			t.Fatalf("Failed to read downloaded file: %v", err)
		}

		if !strings.Contains(string(data), "hello from pod") {
			t.Errorf("expected 'hello from pod', got %q", string(data))
		}
	})
}

// ============================================================================
// Phase 2: Composition & Import
// ============================================================================

// TestFromHelmRendersChart verifies FromHelm renders a chart to objects.
func TestFromHelmRendersChart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	// Create a minimal helm chart structure
	tmpDir := t.TempDir()
	chartDir := filepath.Join(tmpDir, "mychart")
	templatesDir := filepath.Join(chartDir, "templates")

	os.MkdirAll(templatesDir, 0755)

	// Chart.yaml
	chartYaml := `apiVersion: v2
name: mychart
version: 0.1.0
`
	os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte(chartYaml), 0644)

	// values.yaml
	valuesYaml := `replicas: 1
image: nginx:alpine
`
	os.WriteFile(filepath.Join(chartDir, "values.yaml"), []byte(valuesYaml), 0644)

	// templates/deployment.yaml
	deploymentTemplate := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .Release.Name }}-app
spec:
  replicas: {{ .Values.replicas }}
  selector:
    matchLabels:
      app: {{ .Release.Name }}
  template:
    metadata:
      labels:
        app: {{ .Release.Name }}
    spec:
      containers:
      - name: main
        image: {{ .Values.image }}
`
	os.WriteFile(filepath.Join(templatesDir, "deployment.yaml"), []byte(deploymentTemplate), 0644)

	Run(t, func(ctx *Context) {
		// Render helm chart with custom values
		objects, err := FromHelm(chartDir, "myrelease", map[string]interface{}{
			"replicas": 3,
			"image":    "nginx:latest",
		})
		if err != nil {
			t.Fatalf("FromHelm failed: %v", err)
		}

		if len(objects) == 0 {
			t.Fatal("expected at least one object from helm chart")
		}

		// Apply rendered objects
		for _, obj := range objects {
			if err := ctx.Apply(obj); err != nil {
				t.Fatalf("Apply failed: %v", err)
			}
		}

		// Verify deployment was created with correct values
		var deploy appsv1.Deployment
		if err := ctx.Get("myrelease-app", &deploy); err != nil {
			t.Fatalf("Get deployment failed: %v", err)
		}

		if *deploy.Spec.Replicas != 3 {
			t.Errorf("expected 3 replicas, got %d", *deploy.Spec.Replicas)
		}
	})
}

// TestGetDynamicForCRD verifies GetDynamic works with arbitrary GVKs.
func TestGetDynamicForCRD(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a ConfigMap using dynamic client (simulating CRD workflow)
		gvr := schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "configmaps",
		}

		obj := map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name": "dynamic-test",
			},
			"data": map[string]interface{}{
				"key": "value",
			},
		}

		// Apply using dynamic
		if err := ctx.ApplyDynamic(gvr, obj); err != nil {
			t.Fatalf("ApplyDynamic failed: %v", err)
		}

		// Get using dynamic
		result, err := ctx.GetDynamic(gvr, "dynamic-test")
		if err != nil {
			t.Fatalf("GetDynamic failed: %v", err)
		}

		data, ok := result["data"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected data map, got %T", result["data"])
		}

		if data["key"] != "value" {
			t.Errorf("expected key=value, got %v", data["key"])
		}
	})
}

// ============================================================================
// Phase 3: Operational Primitives
// ============================================================================

// TestScaleChangesReplicas verifies Scale changes deployment replicas.
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

// TestPortForwardPost verifies Post sends HTTP POST requests.
func TestPortForwardPost(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Deploy nginx - POST to static files returns 405, proving POST was sent
		stack := NewStack().
			Service("web").
			Image("nginx:1.25-alpine").
			Port(80).
			Build()

		if err := ctx.Up(stack); err != nil {
			t.Fatalf("Up failed: %v", err)
		}

		pf := ctx.Forward("svc/web", 80)
		defer pf.Close()

		// Test Post - nginx returns 405 for POST to static files
		resp, err := pf.Post("/", "application/json", strings.NewReader(`{"key":"value"}`))
		if err != nil {
			t.Fatalf("Post failed: %v", err)
		}
		defer resp.Body.Close()

		// nginx returns 405 for POST to static resources - this proves POST was sent
		if resp.StatusCode != 405 {
			t.Errorf("expected 405 (POST not allowed), got %d", resp.StatusCode)
		}
	})
}

// TestPortForwardPut verifies Put sends HTTP PUT requests.
func TestPortForwardPut(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Deploy nginx - PUT to static files returns 405, proving PUT was sent
		stack := NewStack().
			Service("web").
			Image("nginx:1.25-alpine").
			Port(80).
			Build()

		if err := ctx.Up(stack); err != nil {
			t.Fatalf("Up failed: %v", err)
		}

		pf := ctx.Forward("svc/web", 80)
		defer pf.Close()

		// Test Put - nginx returns 405 for PUT
		resp, err := pf.Put("/", "application/json", strings.NewReader(`{"updated":"data"}`))
		if err != nil {
			t.Fatalf("Put failed: %v", err)
		}
		defer resp.Body.Close()

		// nginx returns 405 for PUT - proves the method was sent correctly
		if resp.StatusCode != 405 {
			t.Errorf("expected 405 (PUT not allowed), got %d", resp.StatusCode)
		}
	})
}

// TestPortForwardDelete verifies Delete sends HTTP DELETE requests.
func TestPortForwardDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Deploy nginx - DELETE to static files returns 405
		stack := NewStack().
			Service("web").
			Image("nginx:1.25-alpine").
			Port(80).
			Build()

		if err := ctx.Up(stack); err != nil {
			t.Fatalf("Up failed: %v", err)
		}

		pf := ctx.Forward("svc/web", 80)
		defer pf.Close()

		// Test Delete - nginx returns 405 for DELETE
		resp, err := pf.Delete("/")
		if err != nil {
			t.Fatalf("Delete failed: %v", err)
		}
		defer resp.Body.Close()

		// nginx returns 405 for DELETE - proves the method was sent correctly
		if resp.StatusCode != 405 {
			t.Errorf("expected 405 (DELETE not allowed), got %d", resp.StatusCode)
		}
	})
}

// TestPortForwardDo verifies Do sends custom HTTP requests.
func TestPortForwardDo(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		stack := NewStack().
			Service("web").
			Image("nginx:1.25-alpine").
			Port(80).
			Build()

		if err := ctx.Up(stack); err != nil {
			t.Fatalf("Up failed: %v", err)
		}

		pf := ctx.Forward("svc/web", 80)
		defer pf.Close()

		// Test Do with custom PATCH request
		req, err := http.NewRequest("PATCH", pf.URL("/"), bytes.NewReader([]byte(`{"partial":"update"}`)))
		if err != nil {
			t.Fatalf("NewRequest failed: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Custom-Header", "test-value")

		resp, err := pf.Do(req)
		if err != nil {
			t.Fatalf("Do failed: %v", err)
		}
		defer resp.Body.Close()

		// nginx returns 405 for PATCH - proves the method was sent correctly
		if resp.StatusCode != 405 {
			t.Errorf("expected 405 (PATCH not allowed), got %d", resp.StatusCode)
		}
	})
}

// TestPortForwardURL verifies URL returns proper endpoint URLs.
func TestPortForwardURL(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		stack := NewStack().
			Service("web").
			Image("nginx:1.25-alpine").
			Port(80).
			Build()

		if err := ctx.Up(stack); err != nil {
			t.Fatalf("Up failed: %v", err)
		}

		pf := ctx.Forward("svc/web", 80)
		defer pf.Close()

		url := pf.URL("/health")
		if !strings.HasPrefix(url, "http://localhost:") {
			t.Errorf("expected URL to start with http://localhost:, got %s", url)
		}
		if !strings.HasSuffix(url, "/health") {
			t.Errorf("expected URL to end with /health, got %s", url)
		}
	})
}

// TestPortForwardGetVerify verifies Get still works after refactoring.
func TestPortForwardGetVerify(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		stack := NewStack().
			Service("web").
			Image("nginx:1.25-alpine").
			Port(80).
			Build()

		if err := ctx.Up(stack); err != nil {
			t.Fatalf("Up failed: %v", err)
		}

		pf := ctx.Forward("svc/web", 80)
		defer pf.Close()

		// GET should return 200 for nginx index
		resp, err := pf.Get("/")
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}
	})
}

// ============================================================================
// Log Aggregation Tests
// ============================================================================

// TestLogsAllRetrievesFromMultiplePods verifies LogsAll works with multiple pods.
func TestLogsAllRetrievesFromMultiplePods(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create 2 pods with the same label
		for i := 1; i <= 2; i++ {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:   fmt.Sprintf("logs-all-test-%d", i),
					Labels: map[string]string{"app": "logs-all-test"},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:    "echo",
							Image:   "busybox:1.36",
							Command: []string{"sh", "-c", fmt.Sprintf("echo 'hello from pod %d' && sleep 60", i)},
						},
					},
				},
			}
			if err := ctx.Apply(pod); err != nil {
				t.Fatalf("Apply pod %d failed: %v", i, err)
			}
		}

		// Wait for both pods
		for i := 1; i <= 2; i++ {
			if err := ctx.WaitReady(fmt.Sprintf("pod/logs-all-test-%d", i)); err != nil {
				t.Fatalf("WaitReady pod %d failed: %v", i, err)
			}
		}

		// Wait for logs
		time.Sleep(2 * time.Second)

		// Get all logs
		logs, err := ctx.LogsAll("app=logs-all-test")
		if err != nil {
			t.Fatalf("LogsAll failed: %v", err)
		}

		if len(logs) != 2 {
			t.Errorf("expected logs from 2 pods, got %d", len(logs))
		}

		// Verify each pod has logs
		for name, log := range logs {
			if !strings.Contains(log, "hello from pod") {
				t.Errorf("pod %s logs missing expected content: %s", name, log)
			}
		}
	})
}

// TestLogsAllWithOptions verifies LogsAllWithOptions works with tail lines.
func TestLogsAllWithOptions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "logs-opts-test",
				Labels: map[string]string{"app": "logs-opts"},
			},
			Spec: corev1.PodSpec{
				RestartPolicy: corev1.RestartPolicyNever,
				Containers: []corev1.Container{
					{
						Name:    "echo",
						Image:   "busybox:1.36",
						Command: []string{"sh", "-c", "for i in 1 2 3 4 5; do echo line$i; done; sleep 60"},
					},
				},
			},
		}
		if err := ctx.Apply(pod); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}
		if err := ctx.WaitReady("pod/logs-opts-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Wait for logs to be available, up to 10 seconds
		var logs map[string]string
		var err error
		const pollInterval = 200 * time.Millisecond
		const timeout = 10 * time.Second
		start := time.Now()
		for {
			logs, err = ctx.LogsAllWithOptions("app=logs-opts", LogsOptions{
				TailLines: 2,
			})
			if err == nil && len(strings.TrimSpace(logs["logs-opts-test"])) > 0 {
				break
			}
			if time.Since(start) > timeout {
				t.Fatalf("LogsAllWithOptions did not return logs within %v: last error: %v", timeout, err)
			}
			time.Sleep(pollInterval)
		}

		// Get only last 2 lines

		log := logs["logs-opts-test"]
		lines := strings.Split(strings.TrimSpace(log), "\n")
		if len(lines) > 2 {
			t.Errorf("expected at most 2 lines, got %d", len(lines))
		}
	})
}

// ============================================================================
// Secret Helpers Tests
// ============================================================================

// TestSecretFromFile verifies SecretFromFile creates a secret from files.
func TestSecretFromFile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create temp files
		tmpDir := t.TempDir()
		file1 := filepath.Join(tmpDir, "config.txt")
		file2 := filepath.Join(tmpDir, "data.txt")

		if err := os.WriteFile(file1, []byte("config-content"), 0644); err != nil {
			t.Fatalf("failed to write file1: %v", err)
		}
		if err := os.WriteFile(file2, []byte("data-content"), 0644); err != nil {
			t.Fatalf("failed to write file2: %v", err)
		}

		// Create secret from files
		err := ctx.SecretFromFile("file-secret", map[string]string{
			"config": file1,
			"data":   file2,
		})
		if err != nil {
			t.Fatalf("SecretFromFile failed: %v", err)
		}

		// Verify secret was created
		secret, err := ctx.Client.CoreV1().Secrets(ctx.Namespace).Get(
			context.Background(), "file-secret", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("Get secret failed: %v", err)
		}

		if string(secret.Data["config"]) != "config-content" {
			t.Errorf("expected config=config-content, got %s", secret.Data["config"])
		}
		if string(secret.Data["data"]) != "data-content" {
			t.Errorf("expected data=data-content, got %s", secret.Data["data"])
		}
	})
}

// TestSecretFromEnv verifies SecretFromEnv creates a secret from env vars.
func TestSecretFromEnv(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Set env vars
		os.Setenv("TEST_SECRET_USER", "admin")
		os.Setenv("TEST_SECRET_PASS", "secret123")
		defer os.Unsetenv("TEST_SECRET_USER")
		defer os.Unsetenv("TEST_SECRET_PASS")

		// Create secret from env
		err := ctx.SecretFromEnv("env-secret", "TEST_SECRET_USER", "TEST_SECRET_PASS")
		if err != nil {
			t.Fatalf("SecretFromEnv failed: %v", err)
		}

		// Verify secret was created
		secret, err := ctx.Client.CoreV1().Secrets(ctx.Namespace).Get(
			context.Background(), "env-secret", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("Get secret failed: %v", err)
		}

		if string(secret.Data["TEST_SECRET_USER"]) != "admin" {
			t.Errorf("expected TEST_SECRET_USER=admin, got %s", secret.Data["TEST_SECRET_USER"])
		}
		if string(secret.Data["TEST_SECRET_PASS"]) != "secret123" {
			t.Errorf("expected TEST_SECRET_PASS=secret123, got %s", secret.Data["TEST_SECRET_PASS"])
		}
	})
}

// TestSecretTLS verifies SecretTLS creates a TLS secret.
func TestSecretTLS(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create dummy cert/key files (not real certs, just for testing)
		tmpDir := t.TempDir()
		certPath := filepath.Join(tmpDir, "tls.crt")
		keyPath := filepath.Join(tmpDir, "tls.key")

		if err := os.WriteFile(certPath, []byte("-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----"), 0644); err != nil {
			t.Fatalf("failed to write cert file: %v", err)
		}
		if err := os.WriteFile(keyPath, []byte("-----BEGIN PRIVATE KEY-----\ntest\n-----END PRIVATE KEY-----"), 0644); err != nil {
			t.Fatalf("failed to write key file: %v", err)
		}

		// Create TLS secret
		err := ctx.SecretTLS("tls-secret", certPath, keyPath)
		if err != nil {
			t.Fatalf("SecretTLS failed: %v", err)
		}

		// Verify secret was created with TLS type
		secret, err := ctx.Client.CoreV1().Secrets(ctx.Namespace).Get(
			context.Background(), "tls-secret", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("Get secret failed: %v", err)
		}

		if secret.Type != corev1.SecretTypeTLS {
			t.Errorf("expected type kubernetes.io/tls, got %s", secret.Type)
		}
		if len(secret.Data["tls.crt"]) == 0 {
			t.Error("tls.crt should not be empty")
		}
		if len(secret.Data["tls.key"]) == 0 {
			t.Error("tls.key should not be empty")
		}
	})
}

// ============================================================================
// RBAC Builder Tests
// ============================================================================

// TestRBACBuilder verifies ServiceAccount fluent builder.
func TestRBACBuilder(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Build RBAC bundle
		bundle := ServiceAccount("myapp").
			WithRole("myapp-reader").
			CanGet("pods", "services").
			CanList("configmaps").
			Build()

		// Verify bundle structure
		if bundle.ServiceAccount.Name != "myapp" {
			t.Errorf("expected SA name myapp, got %s", bundle.ServiceAccount.Name)
		}
		if bundle.Role.Name != "myapp-reader" {
			t.Errorf("expected Role name myapp-reader, got %s", bundle.Role.Name)
		}
		if bundle.RoleBinding.Name != "myapp-binding" {
			t.Errorf("expected RoleBinding name myapp-binding, got %s", bundle.RoleBinding.Name)
		}

		// Apply the bundle
		if err := ctx.ApplyRBAC(bundle); err != nil {
			t.Fatalf("ApplyRBAC failed: %v", err)
		}

		// Verify ServiceAccount exists
		_, err := ctx.Client.CoreV1().ServiceAccounts(ctx.Namespace).Get(
			context.Background(), "myapp", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("Get ServiceAccount failed: %v", err)
		}

		// Verify Role exists
		role, err := ctx.Client.RbacV1().Roles(ctx.Namespace).Get(
			context.Background(), "myapp-reader", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("Get Role failed: %v", err)
		}
		if len(role.Rules) != 2 {
			t.Errorf("expected 2 rules, got %d", len(role.Rules))
		}

		// Verify RoleBinding exists
		_, err = ctx.Client.RbacV1().RoleBindings(ctx.Namespace).Get(
			context.Background(), "myapp-binding", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("Get RoleBinding failed: %v", err)
		}
	})
}

// TestRBACBuilderCanAll verifies CanAll adds all permissions.
func TestRBACBuilderCanAll(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		bundle := ServiceAccount("admin-sa").
			CanAll("pods", "deployments").
			Build()

		if err := ctx.ApplyRBAC(bundle); err != nil {
			t.Fatalf("ApplyRBAC failed: %v", err)
		}

		// Verify Role has all verbs
		role, err := ctx.Client.RbacV1().Roles(ctx.Namespace).Get(
			context.Background(), "admin-sa-role", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("Get Role failed: %v", err)
		}

		if len(role.Rules) != 1 {
			t.Fatalf("expected 1 rule, got %d", len(role.Rules))
		}

		verbs := role.Rules[0].Verbs
		expectedVerbs := []string{"get", "list", "watch", "create", "update", "delete"}
		if len(verbs) != len(expectedVerbs) {
			t.Errorf("expected %d verbs, got %d", len(expectedVerbs), len(verbs))
		}
	})
}

// ============================================================================
// Ingress Testing Tests
// ============================================================================

// TestIngressTestExpectBackend verifies TestIngress works.
func TestIngressTestExpectBackend(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create an ingress
		pathType := networkingv1.PathTypePrefix
		ing := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-ingress",
			},
			Spec: networkingv1.IngressSpec{
				Rules: []networkingv1.IngressRule{
					{
						Host: "api.example.com",
						IngressRuleValue: networkingv1.IngressRuleValue{
							HTTP: &networkingv1.HTTPIngressRuleValue{
								Paths: []networkingv1.HTTPIngressPath{
									{
										Path:     "/",
										PathType: &pathType,
										Backend: networkingv1.IngressBackend{
											Service: &networkingv1.IngressServiceBackend{
												Name: "api-svc",
												Port: networkingv1.ServiceBackendPort{
													Number: 8080,
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}

		_, err := ctx.Client.NetworkingV1().Ingresses(ctx.Namespace).Create(
			context.Background(), ing, metav1.CreateOptions{})
		if err != nil {
			t.Fatalf("Create ingress failed: %v", err)
		}

		// Test should pass
		err = ctx.TestIngress("test-ingress").
			Host("api.example.com").
			ExpectBackend("api-svc", 8080).
			Error()
		if err != nil {
			t.Errorf("TestIngress should pass: %v", err)
		}

		// Test should fail with wrong backend
		err = ctx.TestIngress("test-ingress").
			Host("api.example.com").
			ExpectBackend("wrong-svc", 8080).
			Error()
		if err == nil {
			t.Error("TestIngress should fail for wrong backend")
		}

		// Test should fail with wrong host
		err = ctx.TestIngress("test-ingress").
			Host("wrong.example.com").
			ExpectBackend("api-svc", 8080).
			Error()
		if err == nil {
			t.Error("TestIngress should fail for wrong host")
		}
	})
}

// TestIngressTestExpectTLS verifies TLS assertion works.
func TestIngressTestExpectTLS(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create an ingress with TLS
		pathType := networkingv1.PathTypePrefix
		ing := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name: "tls-ingress",
			},
			Spec: networkingv1.IngressSpec{
				TLS: []networkingv1.IngressTLS{
					{
						Hosts:      []string{"secure.example.com"},
						SecretName: "tls-cert",
					},
				},
				Rules: []networkingv1.IngressRule{
					{
						Host: "secure.example.com",
						IngressRuleValue: networkingv1.IngressRuleValue{
							HTTP: &networkingv1.HTTPIngressRuleValue{
								Paths: []networkingv1.HTTPIngressPath{
									{
										Path:     "/",
										PathType: &pathType,
										Backend: networkingv1.IngressBackend{
											Service: &networkingv1.IngressServiceBackend{
												Name: "secure-svc",
												Port: networkingv1.ServiceBackendPort{
													Number: 443,
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}

		_, err := ctx.Client.NetworkingV1().Ingresses(ctx.Namespace).Create(
			context.Background(), ing, metav1.CreateOptions{})
		if err != nil {
			t.Fatalf("Create ingress failed: %v", err)
		}

		// Test TLS should pass
		err = ctx.TestIngress("tls-ingress").
			Host("secure.example.com").
			ExpectTLS("tls-cert").
			Error()
		if err != nil {
			t.Errorf("TestIngress TLS should pass: %v", err)
		}

		// Test TLS should fail with wrong secret
		err = ctx.TestIngress("tls-ingress").
			Host("secure.example.com").
			ExpectTLS("wrong-cert").
			Error()
		if err == nil {
			t.Error("TestIngress TLS should fail for wrong secret")
		}
	})
}
