package ilmari

import (
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func mustParseQuantity(s string) resource.Quantity {
	q, err := resource.ParseQuantity(s)
	if err != nil {
		panic(err)
	}
	return q
}

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
// Typed Assertions Tests
// ============================================================================

// TestAssertPodTyped verifies typed pod assertions.
func TestAssertPodTyped(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "typed-pod-test",
				Labels: map[string]string{
					"app": "typed-test",
				},
				Annotations: map[string]string{
					"note": "testing",
				},
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
		if err := ctx.WaitReady("pod/typed-pod-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Test Exists
		if err := ctx.AssertPod("typed-pod-test").Exists().Error(); err != nil {
			t.Errorf("AssertPod.Exists failed: %v", err)
		}

		// Test IsReady
		if err := ctx.AssertPod("typed-pod-test").IsReady().Error(); err != nil {
			t.Errorf("AssertPod.IsReady failed: %v", err)
		}

		// Test HasNoRestarts
		if err := ctx.AssertPod("typed-pod-test").HasNoRestarts().Error(); err != nil {
			t.Errorf("AssertPod.HasNoRestarts failed: %v", err)
		}

		// Test NoOOMKills
		if err := ctx.AssertPod("typed-pod-test").NoOOMKills().Error(); err != nil {
			t.Errorf("AssertPod.NoOOMKills failed: %v", err)
		}

		// Test HasLabel
		if err := ctx.AssertPod("typed-pod-test").HasLabel("app", "typed-test").Error(); err != nil {
			t.Errorf("AssertPod.HasLabel failed: %v", err)
		}

		// Test HasAnnotation
		if err := ctx.AssertPod("typed-pod-test").HasAnnotation("note", "testing").Error(); err != nil {
			t.Errorf("AssertPod.HasAnnotation failed: %v", err)
		}

		// Test chaining
		if err := ctx.AssertPod("typed-pod-test").
			Exists().
			IsReady().
			HasNoRestarts().
			NoOOMKills().
			HasLabel("app", "typed-test").
			Error(); err != nil {
			t.Errorf("Chained assertions failed: %v", err)
		}

		// Test failure case
		if err := ctx.AssertPod("nonexistent").Exists().Error(); err == nil {
			t.Error("Expected error for nonexistent pod")
		}
	})
}

// TestAssertDeploymentTyped verifies typed deployment assertions.
func TestAssertDeploymentTyped(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		deploy := Deployment("typed-deploy-test").
			Image("nginx:alpine").
			Port(80).
			Replicas(2).
			Build()

		deploy.Labels = map[string]string{"app": "typed-test"}
		deploy.Annotations = map[string]string{"team": "platform"}

		if err := ctx.Apply(deploy); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}
		if err := ctx.WaitReady("deployment/typed-deploy-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Test Exists
		if err := ctx.AssertDeployment("typed-deploy-test").Exists().Error(); err != nil {
			t.Errorf("AssertDeployment.Exists failed: %v", err)
		}

		// Test HasReplicas
		if err := ctx.AssertDeployment("typed-deploy-test").HasReplicas(2).Error(); err != nil {
			t.Errorf("AssertDeployment.HasReplicas failed: %v", err)
		}

		// Test IsReady
		if err := ctx.AssertDeployment("typed-deploy-test").IsReady().Error(); err != nil {
			t.Errorf("AssertDeployment.IsReady failed: %v", err)
		}

		// Test IsProgressing
		if err := ctx.AssertDeployment("typed-deploy-test").IsProgressing().Error(); err != nil {
			t.Errorf("AssertDeployment.IsProgressing failed: %v", err)
		}

		// Test HasLabel
		if err := ctx.AssertDeployment("typed-deploy-test").HasLabel("app", "typed-test").Error(); err != nil {
			t.Errorf("AssertDeployment.HasLabel failed: %v", err)
		}

		// Test HasAnnotation
		if err := ctx.AssertDeployment("typed-deploy-test").HasAnnotation("team", "platform").Error(); err != nil {
			t.Errorf("AssertDeployment.HasAnnotation failed: %v", err)
		}

		// Test chaining
		if err := ctx.AssertDeployment("typed-deploy-test").
			Exists().
			HasReplicas(2).
			IsReady().
			IsProgressing().
			Error(); err != nil {
			t.Errorf("Chained assertions failed: %v", err)
		}

		// Test failure case
		if err := ctx.AssertDeployment("typed-deploy-test").HasReplicas(10).Error(); err == nil {
			t.Error("Expected error for wrong replica count")
		}
	})
}

// TestAssertServiceTyped verifies typed service assertions.
func TestAssertServiceTyped(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name: "typed-svc-test",
				Labels: map[string]string{
					"app": "typed-test",
				},
				Annotations: map[string]string{
					"note": "testing",
				},
			},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{
					"app": "myapp",
				},
				Ports: []corev1.ServicePort{
					{Port: 8080},
				},
			},
		}
		if err := ctx.Apply(svc); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		// Test Exists
		if err := ctx.AssertService("typed-svc-test").Exists().Error(); err != nil {
			t.Errorf("AssertService.Exists failed: %v", err)
		}

		// Test HasLabel
		if err := ctx.AssertService("typed-svc-test").HasLabel("app", "typed-test").Error(); err != nil {
			t.Errorf("AssertService.HasLabel failed: %v", err)
		}

		// Test HasAnnotation
		if err := ctx.AssertService("typed-svc-test").HasAnnotation("note", "testing").Error(); err != nil {
			t.Errorf("AssertService.HasAnnotation failed: %v", err)
		}

		// Test HasPort
		if err := ctx.AssertService("typed-svc-test").HasPort(8080).Error(); err != nil {
			t.Errorf("AssertService.HasPort failed: %v", err)
		}

		// Test HasSelector
		if err := ctx.AssertService("typed-svc-test").HasSelector("app", "myapp").Error(); err != nil {
			t.Errorf("AssertService.HasSelector failed: %v", err)
		}

		// Test chaining
		if err := ctx.AssertService("typed-svc-test").
			Exists().
			HasLabel("app", "typed-test").
			HasPort(8080).
			HasSelector("app", "myapp").
			Error(); err != nil {
			t.Errorf("Chained assertions failed: %v", err)
		}

		// Test failure case
		if err := ctx.AssertService("typed-svc-test").HasPort(9999).Error(); err == nil {
			t.Error("Expected error for wrong port")
		}
	})
}

// TestAssertPVCTyped verifies typed PVC assertions.
func TestAssertPVCTyped(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		storageClass := "standard"
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name: "typed-pvc-test",
				Labels: map[string]string{
					"app": "typed-test",
				},
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				StorageClassName: &storageClass,
				AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: mustParseQuantity("1Gi"),
					},
				},
			},
		}
		if err := ctx.Apply(pvc); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		// Test Exists
		if err := ctx.AssertPVC("typed-pvc-test").Exists().Error(); err != nil {
			t.Errorf("AssertPVC.Exists failed: %v", err)
		}

		// Test HasLabel
		if err := ctx.AssertPVC("typed-pvc-test").HasLabel("app", "typed-test").Error(); err != nil {
			t.Errorf("AssertPVC.HasLabel failed: %v", err)
		}

		// Test HasStorageClass
		if err := ctx.AssertPVC("typed-pvc-test").HasStorageClass("standard").Error(); err != nil {
			t.Errorf("AssertPVC.HasStorageClass failed: %v", err)
		}

		// Test failure case
		if err := ctx.AssertPVC("nonexistent").Exists().Error(); err == nil {
			t.Error("Expected error for nonexistent PVC")
		}

		// Note: IsBound and HasCapacity require dynamic provisioner,
		// which may not be available in all test environments
	})
}

// TestAssertStatefulSetTyped verifies typed StatefulSet assertions.
func TestAssertStatefulSetTyped(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		replicas := int32(1)
		ss := &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: "typed-ss-test",
				Labels: map[string]string{
					"app": "typed-test",
				},
			},
			Spec: appsv1.StatefulSetSpec{
				ServiceName: "typed-ss-test",
				Replicas:    &replicas,
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"app": "typed-ss-test"},
				},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"app": "typed-ss-test"},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:    "main",
								Image:   "nginx:alpine",
								Command: []string{"nginx", "-g", "daemon off;"},
							},
						},
					},
				},
			},
		}
		if err := ctx.Apply(ss); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}
		if err := ctx.WaitReady("statefulset/typed-ss-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Test Exists
		if err := ctx.AssertStatefulSet("typed-ss-test").Exists().Error(); err != nil {
			t.Errorf("AssertStatefulSet.Exists failed: %v", err)
		}

		// Test HasReplicas
		if err := ctx.AssertStatefulSet("typed-ss-test").HasReplicas(1).Error(); err != nil {
			t.Errorf("AssertStatefulSet.HasReplicas failed: %v", err)
		}

		// Test IsReady
		if err := ctx.AssertStatefulSet("typed-ss-test").IsReady().Error(); err != nil {
			t.Errorf("AssertStatefulSet.IsReady failed: %v", err)
		}

		// Test HasLabel
		if err := ctx.AssertStatefulSet("typed-ss-test").HasLabel("app", "typed-test").Error(); err != nil {
			t.Errorf("AssertStatefulSet.HasLabel failed: %v", err)
		}

		// Test chaining
		if err := ctx.AssertStatefulSet("typed-ss-test").
			Exists().
			HasReplicas(1).
			IsReady().
			Error(); err != nil {
			t.Errorf("Chained assertions failed: %v", err)
		}

		// Test failure case
		if err := ctx.AssertStatefulSet("nonexistent").Exists().Error(); err == nil {
			t.Error("Expected error for nonexistent StatefulSet")
		}
	})
}
