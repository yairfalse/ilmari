package ilmari

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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
