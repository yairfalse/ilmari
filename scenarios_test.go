package ilmari

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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
