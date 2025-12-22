package ilmari

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DebugOptions configures debug output.
type DebugOptions struct {
	// Writer is where to write output (default: os.Stdout)
	Writer io.Writer
	// LogLines is the number of log lines to show per pod (default: 20)
	LogLines int64
	// ShowEvents controls whether to show events (default: true)
	ShowEvents bool
	// ShowLogs controls whether to show logs (default: true)
	ShowLogs bool
}

// Debug prints combined diagnostics for a resource.
// Supports deployment, statefulset, daemonset, and pod.
func (c *Context) Debug(resource string) error {
	return c.DebugWithOptions(resource, DebugOptions{})
}

// DebugWithOptions prints combined diagnostics with custom options.
func (c *Context) DebugWithOptions(resource string, opts DebugOptions) error {
	// Set defaults
	if opts.Writer == nil {
		opts.Writer = os.Stdout
	}
	if opts.LogLines == 0 {
		opts.LogLines = 20
	}
	if !opts.ShowEvents && !opts.ShowLogs {
		opts.ShowEvents = true
		opts.ShowLogs = true
	}

	parts := strings.SplitN(resource, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid resource format %q, expected kind/name", resource)
	}

	kind := strings.ToLower(parts[0])
	name := parts[1]

	switch kind {
	case "deployment":
		return c.debugDeployment(name, opts)
	case "statefulset":
		return c.debugStatefulSet(name, opts)
	case "daemonset":
		return c.debugDaemonSet(name, opts)
	case "pod":
		return c.debugPod(name, opts)
	default:
		return fmt.Errorf("unsupported resource kind %q for Debug", kind)
	}
}

func (c *Context) debugDeployment(name string, opts DebugOptions) error {
	w := opts.Writer
	ctx := context.Background()

	deploy, err := c.Client.AppsV1().Deployments(c.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get deployment: %w", err)
	}

	// Header
	fmt.Fprintf(w, "=== deployment/%s ===\n", name)
	desired := int32(1)
	if deploy.Spec.Replicas != nil {
		desired = *deploy.Spec.Replicas
	}
	fmt.Fprintf(w, "Status: %d/%d ready\n", deploy.Status.ReadyReplicas, desired)
	printConditions(w, deploymentConditionsToGeneric(deploy.Status.Conditions))

	// Get pods
	pods, err := c.getPodsForSelector(deploy.Spec.Selector.MatchLabels)
	if err != nil {
		return err
	}

	c.debugPods(w, pods, opts)
	return nil
}

func (c *Context) debugStatefulSet(name string, opts DebugOptions) error {
	w := opts.Writer
	ctx := context.Background()

	ss, err := c.Client.AppsV1().StatefulSets(c.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get statefulset: %w", err)
	}

	// Header
	fmt.Fprintf(w, "=== statefulset/%s ===\n", name)
	desired := int32(1)
	if ss.Spec.Replicas != nil {
		desired = *ss.Spec.Replicas
	}
	fmt.Fprintf(w, "Status: %d/%d ready\n", ss.Status.ReadyReplicas, desired)

	// Get pods
	pods, err := c.getPodsForSelector(ss.Spec.Selector.MatchLabels)
	if err != nil {
		return err
	}

	c.debugPods(w, pods, opts)
	return nil
}

func (c *Context) debugDaemonSet(name string, opts DebugOptions) error {
	w := opts.Writer
	ctx := context.Background()

	ds, err := c.Client.AppsV1().DaemonSets(c.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get daemonset: %w", err)
	}

	// Header
	fmt.Fprintf(w, "=== daemonset/%s ===\n", name)
	fmt.Fprintf(w, "Status: %d/%d ready\n", ds.Status.NumberReady, ds.Status.DesiredNumberScheduled)

	// Get pods
	pods, err := c.getPodsForSelector(ds.Spec.Selector.MatchLabels)
	if err != nil {
		return err
	}

	c.debugPods(w, pods, opts)
	return nil
}

func (c *Context) debugPod(name string, opts DebugOptions) error {
	w := opts.Writer
	ctx := context.Background()

	pod, err := c.Client.CoreV1().Pods(c.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get pod: %w", err)
	}

	c.debugPods(w, []corev1.Pod{*pod}, opts)
	return nil
}

func (c *Context) debugPods(w io.Writer, pods []corev1.Pod, opts DebugOptions) {
	if len(pods) == 0 {
		fmt.Fprintln(w, "\n=== Pods ===")
		fmt.Fprintln(w, "(no pods found)")
		return
	}

	// Sort pods by name
	sort.Slice(pods, func(i, j int) bool {
		return pods[i].Name < pods[j].Name
	})

	// Print pod status
	fmt.Fprintln(w, "\n=== Pods ===")
	for _, pod := range pods {
		status := string(pod.Status.Phase)
		restarts := int32(0)
		for _, cs := range pod.Status.ContainerStatuses {
			restarts += cs.RestartCount
		}
		fmt.Fprintf(w, "%s: %s (%d restarts)\n", pod.Name, status, restarts)
	}

	// Print events if enabled
	if opts.ShowEvents {
		c.debugEvents(w, pods)
	}

	// Print logs if enabled
	if opts.ShowLogs {
		c.debugLogs(w, pods, opts.LogLines)
	}
}

func (c *Context) debugEvents(w io.Writer, pods []corev1.Pod) {
	ctx := context.Background()

	// Collect pod names for filtering
	podNames := make(map[string]bool)
	for _, pod := range pods {
		podNames[pod.Name] = true
	}

	events, err := c.Client.CoreV1().Events(c.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(w, "\n=== Events ===\n(error fetching events: %v)\n", err)
		return
	}

	// Filter and sort events
	var relevant []corev1.Event
	for _, ev := range events.Items {
		if podNames[ev.InvolvedObject.Name] {
			relevant = append(relevant, ev)
		}
	}

	if len(relevant) == 0 {
		fmt.Fprintln(w, "\n=== Events ===")
		fmt.Fprintln(w, "(no events)")
		return
	}

	// Sort by time
	sort.Slice(relevant, func(i, j int) bool {
		ti := relevant[i].LastTimestamp.Time
		tj := relevant[j].LastTimestamp.Time
		if ti.IsZero() {
			ti = relevant[i].EventTime.Time
		}
		if tj.IsZero() {
			tj = relevant[j].EventTime.Time
		}
		return ti.Before(tj)
	})

	// Take last 10 events
	if len(relevant) > 10 {
		relevant = relevant[len(relevant)-10:]
	}

	fmt.Fprintln(w, "\n=== Events ===")
	for _, ev := range relevant {
		ts := ev.LastTimestamp.Time
		if ts.IsZero() {
			ts = ev.EventTime.Time
		}
		age := formatAge(ts)
		fmt.Fprintf(w, "%s %s: %s\n", age, ev.Reason, ev.Message)
	}
}

func (c *Context) debugLogs(w io.Writer, pods []corev1.Pod, tailLines int64) {
	fmt.Fprintf(w, "\n=== Logs (last %d lines) ===\n", tailLines)

	for _, pod := range pods {
		// Skip non-running pods
		if pod.Status.Phase != corev1.PodRunning {
			fmt.Fprintf(w, "[%s] (pod not running: %s)\n", pod.Name, pod.Status.Phase)
			continue
		}

		logs, err := c.LogsWithOptions(pod.Name, LogsOptions{TailLines: tailLines})
		if err != nil {
			fmt.Fprintf(w, "[%s] (error: %v)\n", pod.Name, err)
			continue
		}

		if logs == "" {
			fmt.Fprintf(w, "[%s] (no logs)\n", pod.Name)
			continue
		}

		// Prefix each line with pod name
		lines := strings.Split(strings.TrimSuffix(logs, "\n"), "\n")
		for _, line := range lines {
			fmt.Fprintf(w, "[%s] %s\n", pod.Name, line)
		}
	}
}

func (c *Context) getPodsForSelector(selector map[string]string) ([]corev1.Pod, error) {
	labelSelector := metav1.FormatLabelSelector(&metav1.LabelSelector{
		MatchLabels: selector,
	})

	pods, err := c.Client.CoreV1().Pods(c.Namespace).List(
		context.Background(),
		metav1.ListOptions{LabelSelector: labelSelector},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	return pods.Items, nil
}

type genericCondition struct {
	Type    string
	Status  string
	Message string
}

func deploymentConditionsToGeneric(conditions []appsv1.DeploymentCondition) []genericCondition {
	result := make([]genericCondition, len(conditions))
	for i, c := range conditions {
		result[i] = genericCondition{
			Type:    string(c.Type),
			Status:  string(c.Status),
			Message: c.Message,
		}
	}
	return result
}

func printConditions(w io.Writer, conditions []genericCondition) {
	for _, c := range conditions {
		if c.Status != "True" {
			fmt.Fprintf(w, "Condition %s: %s (%s)\n", c.Type, c.Status, c.Message)
		}
	}
}

func formatAge(t time.Time) string {
	if t.IsZero() {
		return "     "
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%3ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%3dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%3dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%3dd", int(d.Hours()/24))
	}
}
