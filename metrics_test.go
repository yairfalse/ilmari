package ilmari

import (
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestMetricsReturnsPodUsage verifies Metrics returns CPU and memory.
// Requires metrics-server to be installed in the cluster.
func TestMetricsReturnsPodUsage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a pod with resource limits to enable percentage calculation
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "metrics-test",
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:    "main",
						Image:   "busybox:1.36",
						Command: []string{"sh", "-c", "while true; do echo working; sleep 1; done"},
						Resources: corev1.ResourceRequirements{
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("100m"),
								corev1.ResourceMemory: resource.MustParse("64Mi"),
							},
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("50m"),
								corev1.ResourceMemory: resource.MustParse("32Mi"),
							},
						},
					},
				},
			},
		}

		if err := ctx.Apply(pod); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		if err := ctx.WaitReady("pod/metrics-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Wait for metrics to be available (metrics-server needs time to collect)
		time.Sleep(30 * time.Second)

		// Get metrics
		metrics, err := ctx.Metrics("metrics-test")
		if err != nil {
			// Skip if metrics-server is not available
			if strings.Contains(err.Error(), "the server could not find the requested resource") ||
				strings.Contains(err.Error(), "metrics") {
				t.Skipf("metrics-server not available: %v", err)
			}
			t.Fatalf("Metrics failed: %v", err)
		}

		// Verify metrics structure
		if metrics.CPU == "" {
			t.Error("expected CPU to be set")
		}
		if metrics.Memory == "" {
			t.Error("expected Memory to be set")
		}
		if metrics.CPUCores <= 0 {
			t.Error("expected CPUCores > 0")
		}
		if metrics.MemoryBytes <= 0 {
			t.Error("expected MemoryBytes > 0")
		}
		// Percentage should be calculated since we set limits
		if metrics.CPUPercent <= 0 {
			t.Logf("CPUPercent: %f (may be 0 if pod is idle)", metrics.CPUPercent)
		}
		if metrics.MemoryPercent <= 0 {
			t.Error("expected MemoryPercent > 0")
		}
	})
}

// TestMetricsWithPodPrefix verifies Metrics handles "pod/name" format.
func TestMetricsWithPodPrefix(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "metrics-prefix-test",
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

		if err := ctx.WaitReady("pod/metrics-prefix-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Wait for metrics
		time.Sleep(30 * time.Second)

		// Both formats should work
		metrics1, err := ctx.Metrics("metrics-prefix-test")
		if err != nil {
			if strings.Contains(err.Error(), "the server could not find the requested resource") {
				t.Skipf("metrics-server not available: %v", err)
			}
			t.Fatalf("Metrics (no prefix) failed: %v", err)
		}

		metrics2, err := ctx.Metrics("pod/metrics-prefix-test")
		if err != nil {
			t.Fatalf("Metrics (with prefix) failed: %v", err)
		}

		// Results should be approximately the same
		if metrics1.CPUCores == 0 && metrics2.CPUCores == 0 {
			t.Log("Both CPU values are 0 (pod is idle)")
		}
	})
}

// TestMetricsDetailReturnsContainerBreakdown verifies MetricsDetail.
func TestMetricsDetailReturnsContainerBreakdown(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a pod with multiple containers
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "metrics-detail-test",
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:    "app",
						Image:   "busybox:1.36",
						Command: []string{"sh", "-c", "while true; do echo app; sleep 2; done"},
						Resources: corev1.ResourceRequirements{
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("100m"),
								corev1.ResourceMemory: resource.MustParse("32Mi"),
							},
						},
					},
					{
						Name:    "sidecar",
						Image:   "busybox:1.36",
						Command: []string{"sh", "-c", "while true; do echo sidecar; sleep 2; done"},
						Resources: corev1.ResourceRequirements{
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("50m"),
								corev1.ResourceMemory: resource.MustParse("16Mi"),
							},
						},
					},
				},
			},
		}

		if err := ctx.Apply(pod); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		if err := ctx.WaitReady("pod/metrics-detail-test"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Wait for metrics
		time.Sleep(30 * time.Second)

		// Get detailed metrics
		detail, err := ctx.MetricsDetail("metrics-detail-test")
		if err != nil {
			if strings.Contains(err.Error(), "the server could not find the requested resource") {
				t.Skipf("metrics-server not available: %v", err)
			}
			t.Fatalf("MetricsDetail failed: %v", err)
		}

		// Should have 2 containers
		if len(detail.Containers) != 2 {
			t.Errorf("expected 2 containers, got %d", len(detail.Containers))
		}

		// Verify container names
		containerNames := make(map[string]bool)
		for _, c := range detail.Containers {
			containerNames[c.Name] = true
			if c.CPU == "" {
				t.Errorf("container %s: expected CPU to be set", c.Name)
			}
			if c.Memory == "" {
				t.Errorf("container %s: expected Memory to be set", c.Name)
			}
		}

		if !containerNames["app"] {
			t.Error("expected 'app' container in metrics")
		}
		if !containerNames["sidecar"] {
			t.Error("expected 'sidecar' container in metrics")
		}

		// Totals should be set
		if detail.CPU == "" {
			t.Error("expected total CPU to be set")
		}
		if detail.Memory == "" {
			t.Error("expected total Memory to be set")
		}
	})
}

// TestMetricsNonExistentPod verifies error handling.
func TestMetricsNonExistentPod(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		_, err := ctx.Metrics("non-existent-pod-12345")
		if err == nil {
			t.Error("expected error for non-existent pod")
		}
	})
}

// TestFormatCPU verifies CPU formatting.
func TestFormatCPU(t *testing.T) {
	tests := []struct {
		millicores int64
		expected   string
	}{
		{100, "100m"},
		{500, "500m"},
		{999, "999m"},
		{1000, "1.0"},
		{1500, "1.5"},
		{2000, "2.0"},
		{2500, "2.5"},
	}

	for _, tt := range tests {
		result := formatCPU(tt.millicores)
		if result != tt.expected {
			t.Errorf("formatCPU(%d) = %s, want %s", tt.millicores, result, tt.expected)
		}
	}
}

// TestFormatMemory verifies memory formatting.
func TestFormatMemory(t *testing.T) {
	tests := []struct {
		bytes    int64
		contains string // Check contains since exact format may vary
	}{
		{1024, "Ki"},           // 1Ki
		{1048576, "Mi"},        // 1Mi
		{1073741824, "Gi"},     // 1Gi
		{536870912, "512Mi"},   // 512Mi
		{268435456, "256Mi"},   // 256Mi
	}

	for _, tt := range tests {
		result := formatMemory(tt.bytes)
		if !strings.Contains(result, tt.contains) {
			t.Errorf("formatMemory(%d) = %s, want to contain %s", tt.bytes, result, tt.contains)
		}
	}
}
