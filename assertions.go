package ilmari

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Assert returns an assertion builder for the given resource.
// Resource format: "kind/name" (e.g., "pod/myapp", "deployment/nginx")
func (c *Context) Assert(resource string) *Assertion {
	return &Assertion{
		ctx:      c,
		resource: resource,
	}
}

// Assertion provides fluent assertions for Kubernetes resources.
type Assertion struct {
	ctx      *Context
	resource string
	err      error
}

// HasLabel asserts the resource has the given label with value.
func (a *Assertion) HasLabel(key, value string) *Assertion {
	if a.err != nil {
		return a
	}

	parts := strings.SplitN(a.resource, "/", 2)
	if len(parts) != 2 {
		a.err = fmt.Errorf("invalid resource format %q", a.resource)
		return a
	}

	obj, err := a.ctx.getResource(strings.ToLower(parts[0]), parts[1])
	if err != nil {
		a.err = err
		return a
	}
	if obj == nil {
		a.err = fmt.Errorf("resource %s not found", a.resource)
		return a
	}

	// Get labels from the object
	var labels map[string]string
	switch o := obj.(type) {
	case *corev1.Pod:
		labels = o.Labels
	case *corev1.ConfigMap:
		labels = o.Labels
	case *corev1.Secret:
		labels = o.Labels
	case *corev1.Service:
		labels = o.Labels
	case *appsv1.Deployment:
		labels = o.Labels
	case *appsv1.StatefulSet:
		labels = o.Labels
	case *appsv1.DaemonSet:
		labels = o.Labels
	default:
		a.err = fmt.Errorf("HasLabel: unsupported resource type %T", obj)
		return a
	}

	if labels[key] != value {
		a.err = fmt.Errorf("expected label %s=%s, got %s=%s", key, value, key, labels[key])
	}
	return a
}

// HasAnnotation asserts the resource has the given annotation.
func (a *Assertion) HasAnnotation(key, value string) *Assertion {
	if a.err != nil {
		return a
	}

	parts := strings.SplitN(a.resource, "/", 2)
	if len(parts) != 2 {
		a.err = fmt.Errorf("invalid resource format %q", a.resource)
		return a
	}

	obj, err := a.ctx.getResource(strings.ToLower(parts[0]), parts[1])
	if err != nil {
		a.err = err
		return a
	}
	if obj == nil {
		a.err = fmt.Errorf("resource %s not found", a.resource)
		return a
	}

	var annotations map[string]string
	switch o := obj.(type) {
	case *corev1.Pod:
		annotations = o.Annotations
	case *corev1.ConfigMap:
		annotations = o.Annotations
	case *corev1.Secret:
		annotations = o.Annotations
	case *corev1.Service:
		annotations = o.Annotations
	case *appsv1.Deployment:
		annotations = o.Annotations
	case *appsv1.StatefulSet:
		annotations = o.Annotations
	case *appsv1.DaemonSet:
		annotations = o.Annotations
	default:
		a.err = fmt.Errorf("HasAnnotation: unsupported resource type %T", obj)
		return a
	}

	if annotations[key] != value {
		a.err = fmt.Errorf("expected annotation %s=%s, got %s=%s", key, value, key, annotations[key])
	}
	return a
}

// IsReady asserts the resource is ready.
func (a *Assertion) IsReady() *Assertion {
	if a.err != nil {
		return a
	}

	parts := strings.SplitN(a.resource, "/", 2)
	if len(parts) != 2 {
		a.err = fmt.Errorf("invalid resource format %q", a.resource)
		return a
	}

	ready, err := a.ctx.isReady(strings.ToLower(parts[0]), parts[1])
	if err != nil {
		a.err = err
		return a
	}
	if !ready {
		a.err = fmt.Errorf("resource %s is not ready", a.resource)
	}
	return a
}

// Exists asserts the resource exists.
func (a *Assertion) Exists() *Assertion {
	if a.err != nil {
		return a
	}

	parts := strings.SplitN(a.resource, "/", 2)
	if len(parts) != 2 {
		a.err = fmt.Errorf("invalid resource format %q", a.resource)
		return a
	}

	obj, err := a.ctx.getResource(strings.ToLower(parts[0]), parts[1])
	if err != nil {
		a.err = err
		return a
	}
	if obj == nil {
		a.err = fmt.Errorf("resource %s does not exist", a.resource)
	}
	return a
}

// Error returns any assertion error.
func (a *Assertion) Error() error {
	return a.err
}

// Must panics if any assertion failed.
// WARNING: This will panic and stop test execution immediately.
// Use Error() instead if you need to handle failures gracefully.
func (a *Assertion) Must() {
	if a.err != nil {
		panic(a.err)
	}
}

// HasReplicas asserts the deployment/statefulset has the specified ready replicas.
func (a *Assertion) HasReplicas(expected int) *Assertion {
	if a.err != nil {
		return a
	}

	parts := strings.SplitN(a.resource, "/", 2)
	if len(parts) != 2 {
		a.err = fmt.Errorf("invalid resource format %q", a.resource)
		return a
	}

	kind := strings.ToLower(parts[0])
	name := parts[1]
	ctx := context.Background()

	var ready int32
	switch kind {
	case "deployment":
		deploy, err := a.ctx.Client.AppsV1().Deployments(a.ctx.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			a.err = err
			return a
		}
		ready = deploy.Status.ReadyReplicas
	case "statefulset":
		ss, err := a.ctx.Client.AppsV1().StatefulSets(a.ctx.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			a.err = err
			return a
		}
		ready = ss.Status.ReadyReplicas
	default:
		a.err = fmt.Errorf("HasReplicas: unsupported kind %s (use deployment or statefulset)", kind)
		return a
	}

	if int(ready) != expected {
		a.err = fmt.Errorf("expected %d ready replicas, got %d", expected, ready)
	}
	return a
}

// IsProgressing asserts the deployment is progressing (not stalled).
func (a *Assertion) IsProgressing() *Assertion {
	if a.err != nil {
		return a
	}

	parts := strings.SplitN(a.resource, "/", 2)
	if len(parts) != 2 {
		a.err = fmt.Errorf("invalid resource format %q", a.resource)
		return a
	}

	kind := strings.ToLower(parts[0])
	name := parts[1]

	if kind != "deployment" {
		a.err = fmt.Errorf("IsProgressing: only supports deployments, got %s", kind)
		return a
	}

	deploy, err := a.ctx.Client.AppsV1().Deployments(a.ctx.Namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		a.err = err
		return a
	}

	// Check for Progressing condition
	for _, cond := range deploy.Status.Conditions {
		if cond.Type == appsv1.DeploymentProgressing {
			if cond.Status == corev1.ConditionTrue {
				return a // progressing
			}
			a.err = fmt.Errorf("deployment not progressing: %s", cond.Message)
			return a
		}
	}

	a.err = fmt.Errorf("deployment has no Progressing condition")
	return a
}

// HasNoRestarts asserts the pod's containers have zero restarts.
func (a *Assertion) HasNoRestarts() *Assertion {
	if a.err != nil {
		return a
	}

	parts := strings.SplitN(a.resource, "/", 2)
	if len(parts) != 2 {
		a.err = fmt.Errorf("invalid resource format %q", a.resource)
		return a
	}

	kind := strings.ToLower(parts[0])
	name := parts[1]

	if kind != "pod" {
		a.err = fmt.Errorf("HasNoRestarts: only supports pods, got %s", kind)
		return a
	}

	pod, err := a.ctx.Client.CoreV1().Pods(a.ctx.Namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		a.err = err
		return a
	}

	for _, cs := range pod.Status.ContainerStatuses {
		if cs.RestartCount > 0 {
			a.err = fmt.Errorf("container %s has %d restarts", cs.Name, cs.RestartCount)
			return a
		}
	}
	return a
}

// LogsContain asserts the pod's logs contain the specified text.
func (a *Assertion) LogsContain(text string) *Assertion {
	if a.err != nil {
		return a
	}

	parts := strings.SplitN(a.resource, "/", 2)
	if len(parts) != 2 {
		a.err = fmt.Errorf("invalid resource format %q", a.resource)
		return a
	}

	kind := strings.ToLower(parts[0])
	name := parts[1]

	if kind != "pod" {
		a.err = fmt.Errorf("LogsContain: only supports pods, got %s", kind)
		return a
	}

	logs, err := a.ctx.Logs(name)
	if err != nil {
		a.err = err
		return a
	}

	if !strings.Contains(logs, text) {
		a.err = fmt.Errorf("logs do not contain %q", text)
	}
	return a
}

// NoOOMKills asserts the pod has no containers terminated due to OOM.
func (a *Assertion) NoOOMKills() *Assertion {
	if a.err != nil {
		return a
	}

	parts := strings.SplitN(a.resource, "/", 2)
	if len(parts) != 2 {
		a.err = fmt.Errorf("invalid resource format %q", a.resource)
		return a
	}

	kind := strings.ToLower(parts[0])
	name := parts[1]

	if kind != "pod" {
		a.err = fmt.Errorf("NoOOMKills: only supports pods, got %s", kind)
		return a
	}

	pod, err := a.ctx.Client.CoreV1().Pods(a.ctx.Namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		a.err = err
		return a
	}

	for _, cs := range pod.Status.ContainerStatuses {
		if cs.LastTerminationState.Terminated != nil {
			if cs.LastTerminationState.Terminated.Reason == "OOMKilled" {
				a.err = fmt.Errorf("container %s was OOMKilled", cs.Name)
				return a
			}
		}
		if cs.State.Terminated != nil {
			if cs.State.Terminated.Reason == "OOMKilled" {
				a.err = fmt.Errorf("container %s was OOMKilled", cs.Name)
				return a
			}
		}
	}
	return a
}

// ============================================================================
// Eventually/Consistently - Flakiness Protection
// ============================================================================

// EventuallyBuilder polls a condition until it becomes true or times out.
type EventuallyBuilder struct {
	ctx      *Context
	fn       func() bool
	timeout  time.Duration
	interval time.Duration
}

// Eventually creates an EventuallyBuilder that waits for a condition.
func (c *Context) Eventually(fn func() bool) *EventuallyBuilder {
	return &EventuallyBuilder{
		ctx:      c,
		fn:       fn,
		timeout:  30 * time.Second, // default
		interval: 1 * time.Second,  // default
	}
}

// Within sets the maximum time to wait.
func (e *EventuallyBuilder) Within(timeout time.Duration) *EventuallyBuilder {
	e.timeout = timeout
	return e
}

// ProbeEvery sets the polling interval.
func (e *EventuallyBuilder) ProbeEvery(interval time.Duration) *EventuallyBuilder {
	e.interval = interval
	return e
}

// Wait blocks until the condition is true or timeout is reached.
func (e *EventuallyBuilder) Wait() error {
	_, span := e.ctx.startSpan(context.Background(), "ilmari.Eventually",
		attribute.Int64("timeout_ms", e.timeout.Milliseconds()),
		attribute.Int64("interval_ms", e.interval.Milliseconds()))
	defer span.End()

	deadline := time.Now().Add(e.timeout)
	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()

	// Check immediately
	if e.fn() {
		return nil
	}

	for {
		// Check deadline before waiting on ticker
		if time.Now().After(deadline) {
			err := fmt.Errorf("condition not met within %v", e.timeout)
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return err
		}

		select {
		case <-ticker.C:
			if e.fn() {
				return nil
			}
		}
	}
}

// ConsistentlyBuilder checks that a condition stays true for a duration.
type ConsistentlyBuilder struct {
	ctx      *Context
	fn       func() bool
	duration time.Duration
	interval time.Duration
}

// Consistently creates a ConsistentlyBuilder that checks a condition stays true.
func (c *Context) Consistently(fn func() bool) *ConsistentlyBuilder {
	return &ConsistentlyBuilder{
		ctx:      c,
		fn:       fn,
		duration: 5 * time.Second,        // default
		interval: 500 * time.Millisecond, // default
	}
}

// For sets how long the condition must stay true.
func (c *ConsistentlyBuilder) For(duration time.Duration) *ConsistentlyBuilder {
	c.duration = duration
	return c
}

// ProbeEvery sets the checking interval.
func (c *ConsistentlyBuilder) ProbeEvery(interval time.Duration) *ConsistentlyBuilder {
	c.interval = interval
	return c
}

// Wait blocks and checks the condition repeatedly for the duration.
func (c *ConsistentlyBuilder) Wait() error {
	_, span := c.ctx.startSpan(context.Background(), "ilmari.Consistently",
		attribute.Int64("duration_ms", c.duration.Milliseconds()),
		attribute.Int64("interval_ms", c.interval.Milliseconds()))
	defer span.End()

	deadline := time.Now().Add(c.duration)
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	// Check immediately
	if !c.fn() {
		err := fmt.Errorf("condition was false at start")
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	for {
		select {
		case <-ticker.C:
			if !c.fn() {
				err := fmt.Errorf("condition became false after %v", time.Since(deadline.Add(-c.duration)))
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return err
			}
			if time.Now().After(deadline) {
				return nil // success - condition stayed true
			}
		}
	}
}
