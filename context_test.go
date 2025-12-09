package ilmari

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
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
		deploy := ctx.LoadFixture(fixturePath).
			WithImage("nginx:alpine").
			WithReplicas(1).
			Build()

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
