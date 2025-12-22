package ilmari

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

// PodMetrics contains CPU and memory usage for a pod.
type PodMetrics struct {
	// CPU usage as a quantity string (e.g., "250m")
	CPU string
	// Memory usage as a quantity string (e.g., "512Mi")
	Memory string
	// CPUPercent is the percentage of CPU limit used (0-100)
	// Returns 0 if no limit is set
	CPUPercent float64
	// MemoryPercent is the percentage of memory limit used (0-100)
	// Returns 0 if no limit is set
	MemoryPercent float64
	// CPUCores is the raw CPU usage in millicores
	CPUCores int64
	// MemoryBytes is the raw memory usage in bytes
	MemoryBytes int64
}

// ContainerMetrics contains CPU and memory usage for a single container.
type ContainerMetrics struct {
	Name          string
	CPU           string
	Memory        string
	CPUPercent    float64
	MemoryPercent float64
	CPUCores      int64
	MemoryBytes   int64
}

// PodMetricsDetail contains detailed metrics for each container in a pod.
type PodMetricsDetail struct {
	PodMetrics
	Containers []ContainerMetrics
}

// Metrics returns CPU and memory usage for a pod.
// Requires metrics-server to be installed in the cluster.
func (c *Context) Metrics(podName string) (*PodMetrics, error) {
	// Handle "pod/name" format
	if strings.HasPrefix(podName, "pod/") {
		podName = strings.TrimPrefix(podName, "pod/")
	}

	metricsClient, err := c.getMetricsClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics client: %w", err)
	}

	// Get pod metrics
	podMetrics, err := metricsClient.MetricsV1beta1().PodMetricses(c.Namespace).Get(
		context.Background(), podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get metrics for pod %s: %w", podName, err)
	}

	// Get pod spec to calculate percentages
	pod, err := c.Client.CoreV1().Pods(c.Namespace).Get(
		context.Background(), podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get pod %s: %w", podName, err)
	}

	return calculatePodMetrics(podMetrics, pod), nil
}

// MetricsDetail returns detailed CPU and memory usage per container.
// Requires metrics-server to be installed in the cluster.
func (c *Context) MetricsDetail(podName string) (*PodMetricsDetail, error) {
	// Handle "pod/name" format
	if strings.HasPrefix(podName, "pod/") {
		podName = strings.TrimPrefix(podName, "pod/")
	}

	metricsClient, err := c.getMetricsClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics client: %w", err)
	}

	// Get pod metrics
	podMetrics, err := metricsClient.MetricsV1beta1().PodMetricses(c.Namespace).Get(
		context.Background(), podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get metrics for pod %s: %w", podName, err)
	}

	// Get pod spec to calculate percentages
	pod, err := c.Client.CoreV1().Pods(c.Namespace).Get(
		context.Background(), podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get pod %s: %w", podName, err)
	}

	return calculatePodMetricsDetail(podMetrics, pod), nil
}

// getMetricsClient returns a metrics client using the same config as the main client.
func (c *Context) getMetricsClient() (*metricsclient.Clientset, error) {
	return metricsclient.NewForConfig(c.restConfig)
}

// calculatePodMetrics aggregates container metrics into pod-level metrics.
func calculatePodMetrics(metrics *metricsv1beta1.PodMetrics, pod *corev1.Pod) *PodMetrics {
	var totalCPU, totalMemory int64
	var totalCPULimit, totalMemoryLimit int64

	// Sum up all container metrics
	for _, container := range metrics.Containers {
		cpu := container.Usage.Cpu()
		mem := container.Usage.Memory()
		totalCPU += cpu.MilliValue()
		totalMemory += mem.Value()
	}

	// Sum up all container limits
	for _, container := range pod.Spec.Containers {
		if limit, ok := container.Resources.Limits[corev1.ResourceCPU]; ok {
			totalCPULimit += limit.MilliValue()
		}
		if limit, ok := container.Resources.Limits[corev1.ResourceMemory]; ok {
			totalMemoryLimit += limit.Value()
		}
	}

	pm := &PodMetrics{
		CPU:         formatCPU(totalCPU),
		Memory:      formatMemory(totalMemory),
		CPUCores:    totalCPU,
		MemoryBytes: totalMemory,
	}

	if totalCPULimit > 0 {
		pm.CPUPercent = float64(totalCPU) / float64(totalCPULimit) * 100
	}
	if totalMemoryLimit > 0 {
		pm.MemoryPercent = float64(totalMemory) / float64(totalMemoryLimit) * 100
	}

	return pm
}

// calculatePodMetricsDetail returns detailed per-container metrics.
func calculatePodMetricsDetail(metrics *metricsv1beta1.PodMetrics, pod *corev1.Pod) *PodMetricsDetail {
	// Build a map of container limits
	limits := make(map[string]struct {
		cpu    int64
		memory int64
	})
	for _, container := range pod.Spec.Containers {
		l := limits[container.Name]
		if limit, ok := container.Resources.Limits[corev1.ResourceCPU]; ok {
			l.cpu = limit.MilliValue()
		}
		if limit, ok := container.Resources.Limits[corev1.ResourceMemory]; ok {
			l.memory = limit.Value()
		}
		limits[container.Name] = l
	}

	var totalCPU, totalMemory int64
	var containers []ContainerMetrics

	for _, container := range metrics.Containers {
		cpu := container.Usage.Cpu().MilliValue()
		mem := container.Usage.Memory().Value()
		totalCPU += cpu
		totalMemory += mem

		cm := ContainerMetrics{
			Name:        container.Name,
			CPU:         formatCPU(cpu),
			Memory:      formatMemory(mem),
			CPUCores:    cpu,
			MemoryBytes: mem,
		}

		if l, ok := limits[container.Name]; ok {
			if l.cpu > 0 {
				cm.CPUPercent = float64(cpu) / float64(l.cpu) * 100
			}
			if l.memory > 0 {
				cm.MemoryPercent = float64(mem) / float64(l.memory) * 100
			}
		}

		containers = append(containers, cm)
	}

	// Calculate totals
	var totalCPULimit, totalMemoryLimit int64
	for _, l := range limits {
		totalCPULimit += l.cpu
		totalMemoryLimit += l.memory
	}

	detail := &PodMetricsDetail{
		PodMetrics: PodMetrics{
			CPU:         formatCPU(totalCPU),
			Memory:      formatMemory(totalMemory),
			CPUCores:    totalCPU,
			MemoryBytes: totalMemory,
		},
		Containers: containers,
	}

	if totalCPULimit > 0 {
		detail.CPUPercent = float64(totalCPU) / float64(totalCPULimit) * 100
	}
	if totalMemoryLimit > 0 {
		detail.MemoryPercent = float64(totalMemory) / float64(totalMemoryLimit) * 100
	}

	return detail
}

// formatCPU formats millicores to a human-readable string.
func formatCPU(millicores int64) string {
	if millicores >= 1000 {
		return fmt.Sprintf("%.1f", float64(millicores)/1000)
	}
	return fmt.Sprintf("%dm", millicores)
}

// formatMemory formats bytes to a human-readable string.
func formatMemory(bytes int64) string {
	q := resource.NewQuantity(bytes, resource.BinarySI)
	return q.String()
}
