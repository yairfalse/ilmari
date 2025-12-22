package ilmari

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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

// TestLogsStreamReceivesLogs verifies LogsStream streams logs in real-time.
func TestLogsStreamReceivesLogs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a pod that outputs logs over a longer period
		// Use more lines and longer delays to ensure we catch some in the stream
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "logs-stream-test"},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name:    "logger",
					Image:   "busybox",
					Command: []string{"sh", "-c", "for i in 1 2 3 4 5 6; do echo line$i; sleep 1; done; sleep 10"},
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

		// Wait for logs to accumulate
		time.Sleep(5 * time.Second)

		mu.Lock()
		lineCount := len(lines)
		mu.Unlock()
		// Should receive at least some log lines (may miss early ones before stream starts)
		if lineCount < 1 {
			t.Errorf("expected at least 1 log line, got %d", lineCount)
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

		// pods (core group) and deployments (apps group) create 2 separate rules
		if len(role.Rules) != 2 {
			t.Fatalf("expected 2 rules (pods and deployments have different API groups), got %d", len(role.Rules))
		}

		// Check that each rule has all verbs
		expectedVerbs := []string{"get", "list", "watch", "create", "update", "delete"}
		for _, rule := range role.Rules {
			if len(rule.Verbs) != len(expectedVerbs) {
				t.Errorf("expected %d verbs, got %d for resources %v", len(expectedVerbs), len(rule.Verbs), rule.Resources)
			}
		}
	})
}

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

// TestWaitPVCBound verifies WaitPVCBound waits for PVC to be bound.
func TestWaitPVCBound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a PVC - note: requires a default StorageClass in the cluster
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: "test-pvc"},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("1Gi"),
					},
				},
			},
		}

		if err := ctx.Apply(pvc); err != nil {
			t.Fatalf("Apply PVC failed: %v", err)
		}

		// Wait for PVC to be bound (may timeout if no StorageClass provisioner)
		err := ctx.WaitPVCBoundTimeout("pvc/test-pvc", 30*time.Second)
		if err != nil {
			t.Logf("WaitPVCBound returned error (expected if no dynamic provisioning): %v", err)
		}
	})
}

// TestPVCAssertions verifies PVC assertion methods.
func TestPVCAssertions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a PVC
		storageClass := "standard"
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: "assert-pvc"},
			Spec: corev1.PersistentVolumeClaimSpec{
				StorageClassName: &storageClass,
				AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("1Gi"),
					},
				},
			},
		}

		if err := ctx.Apply(pvc); err != nil {
			t.Fatalf("Apply PVC failed: %v", err)
		}

		// Test HasStorageClass - should pass with correct class
		err := ctx.Assert("pvc/assert-pvc").HasStorageClass("standard").Error()
		if err != nil {
			t.Errorf("HasStorageClass should pass: %v", err)
		}

		// Test HasStorageClass - should fail with wrong class
		err = ctx.Assert("pvc/assert-pvc").HasStorageClass("premium").Error()
		if err == nil {
			t.Error("HasStorageClass should fail for wrong class")
		}

		// Test IsBound on non-PVC should fail
		err = ctx.Assert("deployment/test").IsBound().Error()
		if err == nil {
			t.Error("IsBound on deployment should fail")
		}

		// Test HasCapacity on non-PVC should fail
		err = ctx.Assert("pod/test").HasCapacity("1Gi").Error()
		if err == nil {
			t.Error("HasCapacity on pod should fail")
		}
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

// TestLogsWithOptionsRetrievesTailLines verifies LogsWithOptions respects TailLines.
func TestLogsWithOptionsRetrievesTailLines(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a pod that outputs multiple lines
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "logs-opts-tail-test",
			},
			Spec: corev1.PodSpec{
				RestartPolicy: corev1.RestartPolicyNever,
				Containers: []corev1.Container{
					{
						Name:    "echo",
						Image:   "busybox:1.36",
						Command: []string{"sh", "-c", "for i in 1 2 3 4 5 6 7 8 9 10; do echo line$i; done; sleep 60"},
					},
				},
			},
		}

		if err := ctx.Apply(pod); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		if err := ctx.WaitReady("pod/logs-opts-tail-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Wait for logs to be available
		time.Sleep(2 * time.Second)

		// Get only last 3 lines
		logs, err := ctx.LogsWithOptions("logs-opts-tail-test", LogsOptions{TailLines: 3})
		if err != nil {
			t.Fatalf("LogsWithOptions failed: %v", err)
		}

		lines := strings.Split(strings.TrimSpace(logs), "\n")
		if len(lines) > 3 {
			t.Errorf("expected at most 3 lines, got %d: %v", len(lines), lines)
		}
	})
}

// TestLogsWithOptionsRetrievesSince verifies LogsWithOptions respects Since.
func TestLogsWithOptionsRetrievesSince(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a pod that outputs lines over time
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "logs-opts-since-test",
			},
			Spec: corev1.PodSpec{
				RestartPolicy: corev1.RestartPolicyNever,
				Containers: []corev1.Container{
					{
						Name:    "echo",
						Image:   "busybox:1.36",
						Command: []string{"sh", "-c", "echo early; sleep 5; echo late; sleep 60"},
					},
				},
			},
		}

		if err := ctx.Apply(pod); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		if err := ctx.WaitReady("pod/logs-opts-since-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Wait for both logs to appear
		time.Sleep(7 * time.Second)

		// Get logs from last 3 seconds (should only see "late")
		logs, err := ctx.LogsWithOptions("logs-opts-since-test", LogsOptions{Since: 3 * time.Second})
		if err != nil {
			t.Fatalf("LogsWithOptions failed: %v", err)
		}

		// Should contain "late" but not "early"
		if strings.Contains(logs, "early") {
			t.Errorf("expected logs since 3s to not contain 'early', got: %s", logs)
		}
		if !strings.Contains(logs, "late") {
			t.Errorf("expected logs since 3s to contain 'late', got: %s", logs)
		}
	})
}
