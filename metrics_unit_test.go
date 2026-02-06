package ilmari

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
)

func TestUnitFormatCPU_Millicores(t *testing.T) {
	tests := []struct {
		millicores int64
		expected   string
	}{
		{0, "0m"},
		{1, "1m"},
		{100, "100m"},
		{250, "250m"},
		{999, "999m"},
	}
	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, formatCPU(tt.millicores))
		})
	}
}

func TestUnitFormatCPU_Cores(t *testing.T) {
	tests := []struct {
		millicores int64
		expected   string
	}{
		{1000, "1.0"},
		{1500, "1.5"},
		{2000, "2.0"},
		{2500, "2.5"},
		{10000, "10.0"},
	}
	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, formatCPU(tt.millicores))
		})
	}
}

func TestUnitFormatMemory(t *testing.T) {
	tests := []struct {
		name     string
		bytes    int64
		expected string
	}{
		{"zero", 0, "0"},
		{"1Ki", 1024, "1Ki"},
		{"1Mi", 1024 * 1024, "1Mi"},
		{"512Mi", 512 * 1024 * 1024, "512Mi"},
		{"1Gi", 1024 * 1024 * 1024, "1Gi"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatMemory(tt.bytes)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestUnitCalculatePodMetrics(t *testing.T) {
	cpuLimit := resource.MustParse("1000m")
	memLimit := resource.MustParse("512Mi")

	metrics := &metricsv1beta1.PodMetrics{
		Containers: []metricsv1beta1.ContainerMetrics{
			{
				Name: "app",
				Usage: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("250m"),
					corev1.ResourceMemory: resource.MustParse("128Mi"),
				},
			},
		},
	}
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "app",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    cpuLimit,
							corev1.ResourceMemory: memLimit,
						},
					},
				},
			},
		},
	}

	result := calculatePodMetrics(metrics, pod)
	assert.Equal(t, "250m", result.CPU)
	assert.Equal(t, int64(250), result.CPUCores)
	assert.InDelta(t, 25.0, result.CPUPercent, 0.1)
	assert.InDelta(t, 25.0, result.MemoryPercent, 0.1)
}

func TestUnitCalculatePodMetrics_NoLimits(t *testing.T) {
	metrics := &metricsv1beta1.PodMetrics{
		Containers: []metricsv1beta1.ContainerMetrics{
			{
				Name: "app",
				Usage: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("64Mi"),
				},
			},
		},
	}
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "app"},
			},
		},
	}

	result := calculatePodMetrics(metrics, pod)
	assert.Equal(t, "100m", result.CPU)
	assert.Equal(t, float64(0), result.CPUPercent)
	assert.Equal(t, float64(0), result.MemoryPercent)
}

func TestUnitCalculatePodMetrics_MultipleContainers(t *testing.T) {
	metrics := &metricsv1beta1.PodMetrics{
		Containers: []metricsv1beta1.ContainerMetrics{
			{
				Name: "app",
				Usage: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("200m"),
					corev1.ResourceMemory: resource.MustParse("128Mi"),
				},
			},
			{
				Name: "sidecar",
				Usage: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("50m"),
					corev1.ResourceMemory: resource.MustParse("32Mi"),
				},
			},
		},
	}
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "app",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("256Mi"),
						},
					},
				},
				{
					Name: "sidecar",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("64Mi"),
						},
					},
				},
			},
		},
	}

	result := calculatePodMetrics(metrics, pod)
	assert.Equal(t, int64(250), result.CPUCores)
	assert.InDelta(t, float64(250)/float64(600)*100, result.CPUPercent, 0.1)
}

func TestUnitCalculatePodMetricsDetail(t *testing.T) {
	metrics := &metricsv1beta1.PodMetrics{
		Containers: []metricsv1beta1.ContainerMetrics{
			{
				Name: "app",
				Usage: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("250m"),
					corev1.ResourceMemory: resource.MustParse("128Mi"),
				},
			},
			{
				Name: "sidecar",
				Usage: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("50m"),
					corev1.ResourceMemory: resource.MustParse("32Mi"),
				},
			},
		},
	}
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "app",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1000m"),
							corev1.ResourceMemory: resource.MustParse("512Mi"),
						},
					},
				},
				{
					Name: "sidecar",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("200m"),
							corev1.ResourceMemory: resource.MustParse("64Mi"),
						},
					},
				},
			},
		},
	}

	result := calculatePodMetricsDetail(metrics, pod)
	assert.Len(t, result.Containers, 2)
	assert.Equal(t, "app", result.Containers[0].Name)
	assert.Equal(t, "250m", result.Containers[0].CPU)
	assert.InDelta(t, 25.0, result.Containers[0].CPUPercent, 0.1)
	assert.Equal(t, "sidecar", result.Containers[1].Name)
	assert.Equal(t, "50m", result.Containers[1].CPU)
	assert.InDelta(t, 25.0, result.Containers[1].CPUPercent, 0.1)

	// Check totals
	assert.Equal(t, int64(300), result.CPUCores)
}
