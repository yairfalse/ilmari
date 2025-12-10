// Package ilmari provides Kubernetes testing utilities for Go.
//
// Ilmari connects your tests to Kubernetes with isolated namespaces,
// automatic cleanup, and failure diagnostics.
//
// Basic usage:
//
//	func TestMyController(t *testing.T) {
//	    ilmari.Run(t, func(ctx *ilmari.Context) {
//	        ctx.Apply(myDeployment)
//	        ctx.WaitReady("deployment/myapp")
//	    })
//	}
package ilmari

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/labels"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/transport/spdy"
	"sigs.k8s.io/yaml"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Context provides a Kubernetes connection with an isolated namespace for testing.
type Context struct {
	// Client is the Kubernetes clientset
	Client *kubernetes.Clientset

	// Dynamic is the dynamic client for CRDs and unstructured resources
	Dynamic dynamic.Interface

	// Namespace is the isolated test namespace
	Namespace string

	// t is the test instance for logging
	t *testing.T

	// tracer for OpenTelemetry tracing (optional)
	tracer trace.Tracer

	// mapper for GVK to GVR discovery
	mapper *restmapper.DeferredDiscoveryRESTMapper
}

// Config configures the test context behavior.
type Config struct {
	// KeepOnFailure keeps the namespace on test failure (default: true)
	KeepOnFailure bool

	// KeepAlways keeps the namespace even on success (debug mode)
	KeepAlways bool

	// Kubeconfig path (default: uses KUBECONFIG or ~/.kube/config)
	Kubeconfig string

	// TracerProvider for OpenTelemetry tracing (optional)
	TracerProvider trace.TracerProvider
}

// DefaultConfig returns the default configuration.
// Set ILMARI_KEEP_ALL=true to keep namespaces after all tests.
func DefaultConfig() Config {
	return Config{
		KeepOnFailure: true,
		KeepAlways:    os.Getenv("ILMARI_KEEP_ALL") == "true",
	}
}

// Run executes a test function with a fresh Context.
// The namespace is automatically cleaned up on success.
func Run(t *testing.T, fn func(ctx *Context)) {
	RunWithConfig(t, DefaultConfig(), fn)
}

// RunWithConfig executes a test function with custom configuration.
func RunWithConfig(t *testing.T, cfg Config, fn func(ctx *Context)) {
	ctx, err := newContext(t, cfg)
	if err != nil {
		t.Fatalf("failed to create ilmari context: %v", err)
	}

	defer func() {
		if t.Failed() && cfg.KeepOnFailure {
			t.Logf("Test failed - keeping namespace %s for debugging", ctx.Namespace)
			ctx.dumpDiagnostics()
			return
		}
		if cfg.KeepAlways {
			t.Logf("KeepAlways enabled - keeping namespace %s", ctx.Namespace)
			return
		}
		if err := ctx.cleanup(); err != nil {
			t.Logf("warning: failed to cleanup namespace: %v", err)
		}
	}()

	fn(ctx)
}

// newContext creates a new test context with an isolated namespace.
func newContext(t *testing.T, cfg Config) (*Context, error) {
	// Load kubeconfig
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if cfg.Kubeconfig != "" {
		loadingRules.ExplicitPath = cfg.Kubeconfig
	}

	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules,
		&clientcmd.ConfigOverrides{},
	).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	// Create clientset
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Create dynamic client for CRDs
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	// Create discovery client and REST mapper
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create discovery client: %w", err)
	}
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(
		&cachedDiscovery{DiscoveryInterface: discoveryClient},
	)

	// Generate unique namespace
	id := uuid.New().String()[:8]
	namespace := fmt.Sprintf("ilmari-test-%s", id)

	// Create namespace
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
			Labels: map[string]string{
				"ilmari.io/test": "true",
			},
		},
	}

	_, err = client.CoreV1().Namespaces().Create(context.Background(), ns, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create namespace: %w", err)
	}

	t.Logf("Created test namespace: %s", namespace)

	// Initialize tracer if configured
	var tracer trace.Tracer
	if cfg.TracerProvider != nil {
		tracer = cfg.TracerProvider.Tracer("ilmari")
	} else {
		tracer = otel.Tracer("ilmari")
	}

	return &Context{
		Client:    client,
		Dynamic:   dynamicClient,
		Namespace: namespace,
		t:         t,
		tracer:    tracer,
		mapper:    mapper,
	}, nil
}

// cachedDiscovery wraps DiscoveryInterface with a simple in-memory cache.
type cachedDiscovery struct {
	discovery.DiscoveryInterface
}

func (c *cachedDiscovery) Fresh() bool {
	return true
}

func (c *cachedDiscovery) Invalidate() {}

// scheme for converting typed objects to unstructured
var scheme = runtime.NewScheme()

func init() {
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	_ = networkingv1.AddToScheme(scheme)
}

// toUnstructured converts a typed object to unstructured.
func toUnstructured(obj runtime.Object) (*unstructured.Unstructured, error) {
	content, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, err
	}
	u := &unstructured.Unstructured{Object: content}
	return u, nil
}

// getGVR returns the GroupVersionResource for the given object.
func (c *Context) getGVR(obj runtime.Object) (schema.GroupVersionResource, error) {
	gvk := obj.GetObjectKind().GroupVersionKind()
	if gvk.Empty() {
		// Try to get GVK from scheme
		gvks, _, err := scheme.ObjectKinds(obj)
		if err != nil || len(gvks) == 0 {
			return schema.GroupVersionResource{}, fmt.Errorf("cannot determine GVK for %T", obj)
		}
		gvk = gvks[0]
	}

	mapping, err := c.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return schema.GroupVersionResource{}, fmt.Errorf("failed to get REST mapping: %w", err)
	}
	return mapping.Resource, nil
}

// startSpan starts a new span for tracing.
func (c *Context) startSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	attrs = append(attrs, attribute.String("namespace", c.Namespace))
	return c.tracer.Start(ctx, name, trace.WithAttributes(attrs...))
}

// cleanup deletes the test namespace.
func (c *Context) cleanup() error {
	err := c.Client.CoreV1().Namespaces().Delete(
		context.Background(),
		c.Namespace,
		metav1.DeleteOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to delete namespace: %w", err)
	}
	c.t.Logf("Deleted test namespace: %s", c.Namespace)
	return nil
}

// dumpDiagnostics prints diagnostic information on test failure.
func (c *Context) dumpDiagnostics() {
	c.t.Logf("\n--- Ilmari Diagnostics ---")
	c.t.Logf("Namespace: %s (kept for debugging)", c.Namespace)

	// Dump pod logs and states
	pods, err := c.Client.CoreV1().Pods(c.Namespace).List(context.Background(), metav1.ListOptions{})
	if err == nil {
		c.t.Logf("\n--- Pods ---")
		for _, pod := range pods.Items {
			status := string(pod.Status.Phase)
			if len(pod.Status.ContainerStatuses) > 0 {
				cs := pod.Status.ContainerStatuses[0]
				if cs.State.Waiting != nil {
					status = cs.State.Waiting.Reason
				}
			}
			c.t.Logf("  %s: %s", pod.Name, status)

			// Dump logs for all pods
			logs, err := c.Logs(pod.Name)
			if err == nil && logs != "" {
				c.t.Logf("  --- Logs (%s) ---", pod.Name)
				for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
					c.t.Logf("    %s", line)
				}
			}
		}
	}

	// Dump events
	events, err := c.Events()
	if err == nil && len(events) > 0 {
		c.t.Logf("\n--- Events ---")
		for _, ev := range events {
			c.t.Logf("  %s %s: %s", ev.InvolvedObject.Name, ev.Reason, ev.Message)
		}
	}

	// kubectl hints
	c.t.Logf("\n--- kubectl commands ---")
	c.t.Logf("  kubectl get pods -n %s", c.Namespace)
	c.t.Logf("  kubectl describe pods -n %s", c.Namespace)
	c.t.Logf("  kubectl logs -n %s <pod-name>", c.Namespace)
	c.t.Logf("  kubectl get events -n %s", c.Namespace)

	c.t.Logf("\n--- End Diagnostics ---")
}

// Apply creates or updates a resource in the test namespace.
// Works with any resource type including CRDs.
func (c *Context) Apply(obj runtime.Object) (err error) {
	gvr, err := c.getGVR(obj)
	if err != nil {
		return err
	}

	_, span := c.startSpan(context.Background(), "ilmari.Apply",
		attribute.String("resource", gvr.Resource),
		attribute.String("group", gvr.Group))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	// Convert to unstructured
	u, err := toUnstructured(obj)
	if err != nil {
		return fmt.Errorf("failed to convert to unstructured: %w", err)
	}
	u.SetNamespace(c.Namespace)

	ctx := context.Background()
	_, err = c.Dynamic.Resource(gvr).Namespace(c.Namespace).Create(ctx, u, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		// Get existing to preserve resourceVersion
		existing, getErr := c.Dynamic.Resource(gvr).Namespace(c.Namespace).Get(ctx, u.GetName(), metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		u.SetResourceVersion(existing.GetResourceVersion())
		_, err = c.Dynamic.Resource(gvr).Namespace(c.Namespace).Update(ctx, u, metav1.UpdateOptions{})
	}
	return err
}

// Get retrieves a resource from the test namespace.
// Works with any resource type including CRDs.
func (c *Context) Get(name string, obj runtime.Object) (err error) {
	gvr, err := c.getGVR(obj)
	if err != nil {
		return err
	}

	_, span := c.startSpan(context.Background(), "ilmari.Get",
		attribute.String("resource", gvr.Resource),
		attribute.String("name", name))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	u, err := c.Dynamic.Resource(gvr).Namespace(c.Namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	// Convert back to typed object
	return runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, obj)
}

// Delete removes a resource from the test namespace.
// Works with any resource type including CRDs.
func (c *Context) Delete(name string, obj runtime.Object) (err error) {
	gvr, err := c.getGVR(obj)
	if err != nil {
		return err
	}

	_, span := c.startSpan(context.Background(), "ilmari.Delete",
		attribute.String("resource", gvr.Resource),
		attribute.String("name", name))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	return c.Dynamic.Resource(gvr).Namespace(c.Namespace).Delete(context.Background(), name, metav1.DeleteOptions{})
}

// List retrieves all resources of a type from the test namespace.
// Works with any resource type including CRDs.
func (c *Context) List(list runtime.Object) (err error) {
	gvr, err := c.getGVR(list)
	if err != nil {
		return err
	}

	_, span := c.startSpan(context.Background(), "ilmari.List",
		attribute.String("resource", gvr.Resource))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	u, err := c.Dynamic.Resource(gvr).Namespace(c.Namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return err
	}

	// Convert back to typed list
	return runtime.DefaultUnstructuredConverter.FromUnstructured(u.UnstructuredContent(), list)
}

// WaitReady waits for a resource to be ready.
// Resource format: "kind/name" (e.g., "pod/myapp", "deployment/nginx")
func (c *Context) WaitReady(resource string) error {
	return c.WaitReadyTimeout(resource, 60*time.Second)
}

// WaitFor waits for a custom condition on a resource.
// Resource format: "kind/name" (e.g., "pod/myapp", "deployment/nginx")
func (c *Context) WaitFor(resource string, condition func(obj interface{}) bool) error {
	return c.WaitForTimeout(resource, condition, 60*time.Second)
}

// WaitForTimeout waits for a custom condition with timeout.
func (c *Context) WaitForTimeout(resource string, condition func(obj interface{}) bool, timeout time.Duration) (err error) {
	_, span := c.startSpan(context.Background(), "ilmari.WaitFor",
		attribute.String("resource", resource),
		attribute.Int64("timeout_ms", timeout.Milliseconds()))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	parts := strings.SplitN(resource, "/", 2)
	if len(parts) != 2 {
		err = fmt.Errorf("invalid resource format %q, expected kind/name", resource)
		return err
	}

	kind := strings.ToLower(parts[0])
	name := parts[1]

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		obj, getErr := c.getResource(kind, name)
		if getErr != nil {
			err = getErr
			return err
		}
		if obj != nil && condition(obj) {
			return nil
		}

		select {
		case <-ctx.Done():
			err = fmt.Errorf("timeout waiting for %s", resource)
			return err
		case <-ticker.C:
		}
	}
}

// getResource fetches a resource by kind and name.
func (c *Context) getResource(kind, name string) (interface{}, error) {
	ctx := context.Background()

	switch kind {
	case "pod":
		pod, err := c.Client.CoreV1().Pods(c.Namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return pod, err

	case "deployment":
		deploy, err := c.Client.AppsV1().Deployments(c.Namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return deploy, err

	case "statefulset":
		ss, err := c.Client.AppsV1().StatefulSets(c.Namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return ss, err

	case "daemonset":
		ds, err := c.Client.AppsV1().DaemonSets(c.Namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return ds, err

	case "configmap":
		cm, err := c.Client.CoreV1().ConfigMaps(c.Namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return cm, err

	case "secret":
		secret, err := c.Client.CoreV1().Secrets(c.Namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return secret, err

	case "service":
		svc, err := c.Client.CoreV1().Services(c.Namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return svc, err

	default:
		return nil, fmt.Errorf("unsupported kind: %s", kind)
	}
}

// WaitError provides rich diagnostic information when a wait operation fails.
type WaitError struct {
	Resource string
	Expected string
	Actual   string
	Pods     []PodStatus
	Events   []string
	Hint     string
}

// PodStatus represents a pod's status for diagnostics.
type PodStatus struct {
	Name   string
	Phase  string
	Reason string
	Ready  bool
}

// Error implements the error interface with rich formatting.
func (e *WaitError) Error() string {
	var b strings.Builder
	b.WriteString("\n━━━ WAIT TIMEOUT ━━━\n")
	b.WriteString(fmt.Sprintf("Resource: %s\n", e.Resource))
	if e.Expected != "" {
		b.WriteString(fmt.Sprintf("Expected: %s\n", e.Expected))
	}
	if e.Actual != "" {
		b.WriteString(fmt.Sprintf("Actual:   %s\n", e.Actual))
	}

	if len(e.Pods) > 0 {
		b.WriteString("\nPods:\n")
		for _, p := range e.Pods {
			readyStr := ""
			if p.Ready {
				readyStr = " (Ready)"
			}
			if p.Reason != "" {
				b.WriteString(fmt.Sprintf("  %s  %s → %s%s\n", p.Name, p.Phase, p.Reason, readyStr))
			} else {
				b.WriteString(fmt.Sprintf("  %s  %s%s\n", p.Name, p.Phase, readyStr))
			}
		}
	}

	if len(e.Events) > 0 {
		b.WriteString("\nEvents:\n")
		for _, ev := range e.Events {
			b.WriteString(fmt.Sprintf("  %s\n", ev))
		}
	}

	if e.Hint != "" {
		b.WriteString(fmt.Sprintf("\nHint: %s\n", e.Hint))
	}

	b.WriteString("━━━━━━━━━━━━━━━━━━━\n")
	return b.String()
}

// WaitReadyTimeout waits for a resource to be ready with custom timeout.
func (c *Context) WaitReadyTimeout(resource string, timeout time.Duration) (err error) {
	_, span := c.startSpan(context.Background(), "ilmari.WaitReady",
		attribute.String("resource", resource),
		attribute.Int64("timeout_ms", timeout.Milliseconds()))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	parts := strings.SplitN(resource, "/", 2)
	if len(parts) != 2 {
		err = fmt.Errorf("invalid resource format %q, expected kind/name", resource)
		return err
	}

	kind := strings.ToLower(parts[0])
	name := parts[1]

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		var ready bool
		ready, err = c.isReady(kind, name)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}

		select {
		case <-ctx.Done():
			err = c.buildWaitError(resource, kind, name)
			return err
		case <-ticker.C:
		}
	}
}

// buildWaitError creates a rich error with diagnostics.
func (c *Context) buildWaitError(resource, kind, name string) *WaitError {
	waitErr := &WaitError{
		Resource: resource,
	}

	bgCtx := context.Background()

	// Get deployment status if applicable
	if kind == "deployment" {
		deploy, err := c.Client.AppsV1().Deployments(c.Namespace).Get(bgCtx, name, metav1.GetOptions{})
		if err == nil {
			var desired int32 = 1
			if deploy.Spec.Replicas != nil {
				desired = *deploy.Spec.Replicas
			}
			waitErr.Expected = fmt.Sprintf("ReadyReplicas >= %d", desired)
			waitErr.Actual = fmt.Sprintf("ReadyReplicas = %d", deploy.Status.ReadyReplicas)
		}
	}

	// Get pod statuses
	pods, err := c.Client.CoreV1().Pods(c.Namespace).List(bgCtx, metav1.ListOptions{})
	if err == nil {
		for _, pod := range pods.Items {
			ps := PodStatus{
				Name:  pod.Name,
				Phase: string(pod.Status.Phase),
			}

			// Check container statuses for more details
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.Ready {
					ps.Ready = true
				}
				if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
					ps.Reason = cs.State.Waiting.Reason
					// Add hints for common issues
					if strings.Contains(ps.Reason, "ImagePull") {
						waitErr.Hint = fmt.Sprintf("Image %q may not exist. Did you forget to build/push?", pod.Spec.Containers[0].Image)
					}
					if ps.Reason == "CrashLoopBackOff" {
						waitErr.Hint = "Container is crash-looping. Check logs for errors."
					}
				}
			}

			waitErr.Pods = append(waitErr.Pods, ps)
		}
	}

	// Get recent events
	events, err := c.Events()
	if err == nil {
		for _, ev := range events {
			if ev.Type == "Warning" || ev.Reason == "Failed" || ev.Reason == "BackOff" {
				waitErr.Events = append(waitErr.Events, fmt.Sprintf("%s %s: %s", ev.InvolvedObject.Name, ev.Reason, ev.Message))
			}
		}
		// Limit to last 5 events
		if len(waitErr.Events) > 5 {
			waitErr.Events = waitErr.Events[len(waitErr.Events)-5:]
		}
	}

	return waitErr
}

// isReady checks if a resource is ready based on its type.
func (c *Context) isReady(kind, name string) (bool, error) {
	ctx := context.Background()

	switch kind {
	case "pod":
		pod, err := c.Client.CoreV1().Pods(c.Namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return isPodReady(pod), nil

	case "deployment":
		deploy, err := c.Client.AppsV1().Deployments(c.Namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return isDeploymentReady(deploy), nil

	case "statefulset":
		ss, err := c.Client.AppsV1().StatefulSets(c.Namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return isStatefulSetReady(ss), nil

	case "daemonset":
		ds, err := c.Client.AppsV1().DaemonSets(c.Namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return isDaemonSetReady(ds), nil

	default:
		return false, fmt.Errorf("unsupported resource type: %s", kind)
	}
}

func isPodReady(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func isDeploymentReady(deploy *appsv1.Deployment) bool {
	if deploy.Spec.Replicas == nil {
		return deploy.Status.ReadyReplicas > 0
	}
	return deploy.Status.ReadyReplicas == *deploy.Spec.Replicas
}

func isStatefulSetReady(ss *appsv1.StatefulSet) bool {
	if ss.Spec.Replicas == nil {
		return ss.Status.ReadyReplicas > 0
	}
	return ss.Status.ReadyReplicas == *ss.Spec.Replicas
}

func isDaemonSetReady(ds *appsv1.DaemonSet) bool {
	return ds.Status.NumberReady == ds.Status.DesiredNumberScheduled &&
		ds.Status.DesiredNumberScheduled > 0
}

// Logs retrieves logs from a pod.
func (c *Context) Logs(pod string) (result string, err error) {
	_, span := c.startSpan(context.Background(), "ilmari.Logs",
		attribute.String("pod", pod))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	req := c.Client.CoreV1().Pods(c.Namespace).GetLogs(pod, &corev1.PodLogOptions{})
	stream, err := req.Stream(context.Background())
	if err != nil {
		err = fmt.Errorf("failed to get logs: %w", err)
		return "", err
	}
	defer stream.Close()

	buf := new(strings.Builder)
	if _, err = io.Copy(buf, stream); err != nil {
		err = fmt.Errorf("failed to read logs: %w", err)
		return "", err
	}
	return buf.String(), nil
}

// Events returns all events in the test namespace.
func (c *Context) Events() (events []corev1.Event, err error) {
	_, span := c.startSpan(context.Background(), "ilmari.Events")
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	list, err := c.Client.CoreV1().Events(c.Namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		err = fmt.Errorf("failed to list events: %w", err)
		return nil, err
	}
	return list.Items, nil
}

// Exec executes a command in a pod.
func (c *Context) Exec(pod string, cmd []string) (result string, err error) {
	_, span := c.startSpan(context.Background(), "ilmari.Exec",
		attribute.String("pod", pod),
		attribute.StringSlice("command", cmd))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	config, err := clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
	if err != nil {
		err = fmt.Errorf("failed to get config: %w", err)
		return "", err
	}

	scheme := runtime.NewScheme()
	if err = corev1.AddToScheme(scheme); err != nil {
		err = fmt.Errorf("failed to add scheme: %w", err)
		return "", err
	}

	req := c.Client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod).
		Namespace(c.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command: cmd,
			Stdout:  true,
			Stderr:  true,
		}, runtime.NewParameterCodec(scheme))

	executor, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		err = fmt.Errorf("failed to create executor: %w", err)
		return "", err
	}

	var stdout, stderr strings.Builder
	err = executor.StreamWithContext(context.Background(), remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		err = fmt.Errorf("exec failed: %w (stderr: %s)", err, stderr.String())
		return "", err
	}

	return stdout.String(), nil
}

// PortForward represents an active port forward to a pod or service.
type PortForward struct {
	localPort  int
	stopChan   chan struct{}
	doneChan   chan struct{}
	httpClient *http.Client
	err        error
	span       trace.Span
}

// Close closes the port forward and waits for cleanup.
func (pf *PortForward) Close() {
	if pf.stopChan != nil {
		close(pf.stopChan)
	}
	if pf.doneChan != nil {
		<-pf.doneChan
	}
	if pf.span != nil {
		pf.span.End()
	}
}

// Get makes an HTTP GET request through the port forward.
// Caller is responsible for closing the response body.
func (pf *PortForward) Get(path string) (*http.Response, error) {
	if pf.err != nil {
		return nil, pf.err
	}
	url := fmt.Sprintf("http://localhost:%d%s", pf.localPort, path)
	return pf.httpClient.Get(url)
}

// Forward creates a port forward to a pod or service.
// Resource format: "svc/name" or "pod/name"
func (c *Context) Forward(resource string, port int) *PortForward {
	_, span := c.startSpan(context.Background(), "ilmari.Forward",
		attribute.String("resource", resource),
		attribute.Int("port", port))

	parts := strings.SplitN(resource, "/", 2)
	if len(parts) != 2 {
		span.RecordError(fmt.Errorf("invalid resource format"))
		span.SetStatus(codes.Error, "invalid resource format")
		span.End()
		return &PortForward{err: fmt.Errorf("invalid resource format %q, expected kind/name", resource)}
	}

	kind := strings.ToLower(parts[0])
	name := parts[1]

	// Helper to return error with span handling
	returnErr := func(err error) *PortForward {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.End()
		return &PortForward{err: err}
	}

	// Get pod name
	var podName string
	ctx := context.Background()

	switch kind {
	case "pod":
		podName = name
	case "svc", "service":
		// Get service and find a pod
		svc, err := c.Client.CoreV1().Services(c.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return returnErr(fmt.Errorf("failed to get service: %w", err))
		}

		// Find pods matching the service selector
		pods, err := c.Client.CoreV1().Pods(c.Namespace).List(ctx, metav1.ListOptions{
			LabelSelector: labels.SelectorFromSet(svc.Spec.Selector).String(),
		})
		if err != nil {
			return returnErr(fmt.Errorf("failed to list pods: %w", err))
		}
		if len(pods.Items) == 0 {
			return returnErr(fmt.Errorf("no pods found for service %s", name))
		}
		podName = pods.Items[0].Name
	default:
		return returnErr(fmt.Errorf("unsupported kind %q, use pod or svc", kind))
	}

	// Set up port forward
	config, err := clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
	if err != nil {
		return returnErr(fmt.Errorf("failed to get config: %w", err))
	}

	roundTripper, upgrader, err := spdy.RoundTripperFor(config)
	if err != nil {
		return returnErr(fmt.Errorf("failed to create round tripper: %w", err))
	}

	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", c.Namespace, podName)
	hostIP := strings.TrimPrefix(config.Host, "https://")
	hostIP = strings.TrimPrefix(hostIP, "http://")
	serverURL := url.URL{Scheme: "https", Host: hostIP, Path: path}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: roundTripper}, http.MethodPost, &serverURL)

	stopChan := make(chan struct{}, 1)
	readyChan := make(chan struct{})

	// Use port 0 to get a random available port
	ports := []string{fmt.Sprintf("0:%d", port)}
	pf, err := portforward.New(dialer, ports, stopChan, readyChan, io.Discard, io.Discard)
	if err != nil {
		return returnErr(fmt.Errorf("failed to create port forward: %w", err))
	}

	doneChan := make(chan struct{})
	errChan := make(chan error, 1)
	go func() {
		errChan <- pf.ForwardPorts()
		close(doneChan)
	}()

	// Wait for port forward to be ready
	select {
	case <-readyChan:
	case err := <-errChan:
		return returnErr(fmt.Errorf("port forward failed: %w", err))
	}

	forwardedPorts, err := pf.GetPorts()
	if err != nil || len(forwardedPorts) == 0 {
		return returnErr(fmt.Errorf("failed to get forwarded ports: %w", err))
	}

	// Return with span - it will be closed in PortForward.Close()
	return &PortForward{
		localPort:  int(forwardedPorts[0].Local),
		stopChan:   stopChan,
		doneChan:   doneChan,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		span:       span,
	}
}

// MustForward creates a port forward and panics on error.
// Useful for test setup where errors should fail immediately.
func (c *Context) MustForward(resource string, port int) *PortForward {
	pf := c.Forward(resource, port)
	if pf.err != nil {
		panic(fmt.Sprintf("MustForward failed: %v", pf.err))
	}
	return pf
}

// Stack represents a collection of services to deploy together.
type Stack struct {
	services []*ServiceBuilder
}

// ServiceBuilder builds a service configuration.
type ServiceBuilder struct {
	stack          *Stack
	name           string
	image          string
	port           int32
	replicas       int32
	env            map[string]string
	command        []string
	resourceLimits corev1.ResourceList
	envFrom        []corev1.EnvFromSource
}

// NewStack creates a new empty stack.
func NewStack() *Stack {
	return &Stack{}
}

// Service adds a new service to the stack and returns its builder.
func (s *Stack) Service(name string) *ServiceBuilder {
	sb := &ServiceBuilder{
		stack:    s,
		name:     name,
		replicas: 1,
		env:      make(map[string]string),
	}
	s.services = append(s.services, sb)
	return sb
}

// Image sets the container image.
func (sb *ServiceBuilder) Image(image string) *ServiceBuilder {
	sb.image = image
	return sb
}

// Port sets the container port.
func (sb *ServiceBuilder) Port(port int) *ServiceBuilder {
	sb.port = int32(port)
	return sb
}

// Replicas sets the number of replicas.
func (sb *ServiceBuilder) Replicas(n int) *ServiceBuilder {
	sb.replicas = int32(n)
	return sb
}

// Env adds an environment variable.
func (sb *ServiceBuilder) Env(key, value string) *ServiceBuilder {
	sb.env[key] = value
	return sb
}

// Command sets the container command.
func (sb *ServiceBuilder) Command(cmd ...string) *ServiceBuilder {
	sb.command = cmd
	return sb
}

// EnvFrom loads environment variables from a ConfigMap or Secret.
// Resource format: "configmap/name" or "secret/name"
func (sb *ServiceBuilder) EnvFrom(resource string) *ServiceBuilder {
	parts := strings.SplitN(resource, "/", 2)
	if len(parts) != 2 {
		return sb
	}

	kind := strings.ToLower(parts[0])
	name := parts[1]

	switch kind {
	case "configmap":
		sb.envFrom = append(sb.envFrom, corev1.EnvFromSource{
			ConfigMapRef: &corev1.ConfigMapEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: name},
			},
		})
	case "secret":
		sb.envFrom = append(sb.envFrom, corev1.EnvFromSource{
			SecretRef: &corev1.SecretEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: name},
			},
		})
	}
	return sb
}

// Service adds another service to the stack (for chaining).
func (sb *ServiceBuilder) Service(name string) *ServiceBuilder {
	return sb.stack.Service(name)
}

// Build returns the stack (for ending the chain).
func (sb *ServiceBuilder) Build() *Stack {
	return sb.stack
}

// Up deploys the stack and waits for all services to be ready.
func (c *Context) Up(stack *Stack) (err error) {
	_, span := c.startSpan(context.Background(), "ilmari.Up",
		attribute.Int("service_count", len(stack.services)))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	// Deploy all services
	for _, svc := range stack.services {
		if err = c.deployService(svc); err != nil {
			err = fmt.Errorf("failed to deploy %s: %w", svc.name, err)
			return err
		}
	}

	// Wait for all deployments to be ready
	for _, svc := range stack.services {
		if err = c.WaitReady("deployment/" + svc.name); err != nil {
			err = fmt.Errorf("failed waiting for %s: %w", svc.name, err)
			return err
		}
	}

	return nil
}

// deployService creates a Deployment and Service for the given config.
func (c *Context) deployService(sb *ServiceBuilder) error {
	// Build env vars
	var envVars []corev1.EnvVar
	for k, v := range sb.env {
		envVars = append(envVars, corev1.EnvVar{Name: k, Value: v})
	}

	// Create Deployment
	replicas := sb.replicas
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: sb.name,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": sb.name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": sb.name},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    sb.name,
							Image:   sb.image,
							Command: sb.command,
							Env:     envVars,
							EnvFrom: sb.envFrom,
						},
					},
				},
			},
		},
	}

	// Add port if specified
	if sb.port > 0 {
		deploy.Spec.Template.Spec.Containers[0].Ports = []corev1.ContainerPort{
			{ContainerPort: sb.port},
		}
	}

	// Add resource limits if specified
	if sb.resourceLimits != nil {
		deploy.Spec.Template.Spec.Containers[0].Resources = corev1.ResourceRequirements{
			Limits:   sb.resourceLimits,
			Requests: sb.resourceLimits,
		}
	}

	if err := c.Apply(deploy); err != nil {
		return err
	}

	// Create Service if port specified
	if sb.port > 0 {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name: sb.name,
			},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": sb.name},
				Ports: []corev1.ServicePort{
					{Port: sb.port, TargetPort: intstr.FromInt32(sb.port)},
				},
			},
		}
		if err := c.Apply(svc); err != nil {
			return err
		}
	}

	return nil
}

// Retry executes fn up to maxAttempts times with exponential backoff.
// Backoff starts at 100ms and doubles each attempt, capped at 5s.
// Returns nil on first success, or the last error after all attempts fail.
func (c *Context) Retry(maxAttempts int, fn func() error) (err error) {
	_, span := c.startSpan(context.Background(), "ilmari.Retry",
		attribute.Int("max_attempts", maxAttempts))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	backoff := 100 * time.Millisecond
	for i := 0; i < maxAttempts; i++ {
		err = fn()
		if err == nil {
			return nil
		}
		if i < maxAttempts-1 {
			time.Sleep(backoff)
			backoff *= 2
			if backoff > 5*time.Second {
				backoff = 5 * time.Second
			}
		}
	}
	return err
}

// Kill deletes a pod, useful for chaos testing.
// Resource format: "pod/name" or just "name" (assumes pod)
func (c *Context) Kill(resource string) (err error) {
	_, span := c.startSpan(context.Background(), "ilmari.Kill",
		attribute.String("resource", resource))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	// Parse resource
	var podName string
	if strings.Contains(resource, "/") {
		parts := strings.SplitN(resource, "/", 2)
		if strings.ToLower(parts[0]) != "pod" {
			err = fmt.Errorf("Kill only supports pods, got %s", parts[0])
			return err
		}
		podName = parts[1]
	} else {
		podName = resource
	}

	// Delete with zero grace period for immediate termination
	grace := int64(0)
	err = c.Client.CoreV1().Pods(c.Namespace).Delete(
		context.Background(),
		podName,
		metav1.DeleteOptions{GracePeriodSeconds: &grace},
	)
	return err
}

// LoadYAML loads and applies a YAML file from the given path.
// Supports single or multi-document YAML files.
func (c *Context) LoadYAML(path string) (err error) {
	_, span := c.startSpan(context.Background(), "ilmari.LoadYAML",
		attribute.String("path", path))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", path, err)
	}

	return c.applyYAML(data)
}

// LoadYAMLDir loads and applies all YAML files from a directory.
// Only processes .yaml and .yml files in the top-level directory (non-recursive).
func (c *Context) LoadYAMLDir(dir string) (err error) {
	_, span := c.startSpan(context.Background(), "ilmari.LoadYAMLDir",
		attribute.String("dir", dir))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read directory %s: %w", dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
			if err := c.LoadYAML(filepath.Join(dir, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// applyYAML parses and applies YAML content.
func (c *Context) applyYAML(data []byte) error {
	// Normalize line endings (CRLF -> LF) for Windows compatibility
	content := strings.ReplaceAll(string(data), "\r\n", "\n")
	// Split multi-document YAML
	docs := strings.Split(content, "\n---")
	for _, doc := range docs {
		doc = strings.TrimSpace(doc)
		if doc == "" || doc == "---" {
			continue
		}

		// Parse to unstructured to determine type
		var obj unstructured.Unstructured
		if err := yaml.Unmarshal([]byte(doc), &obj.Object); err != nil {
			return fmt.Errorf("failed to parse YAML: %w", err)
		}

		if len(obj.Object) == 0 {
			continue
		}

		// Try to apply as typed resource first
		if err := c.applyTypedYAML([]byte(doc), obj.GetKind()); err != nil {
			return err
		}
	}
	return nil
}

// applyTypedYAML applies YAML as a typed resource.
func (c *Context) applyTypedYAML(data []byte, kind string) error {
	switch kind {
	case "ConfigMap":
		var obj corev1.ConfigMap
		if err := yaml.Unmarshal(data, &obj); err != nil {
			return err
		}
		return c.Apply(&obj)
	case "Secret":
		var obj corev1.Secret
		if err := yaml.Unmarshal(data, &obj); err != nil {
			return err
		}
		return c.Apply(&obj)
	case "Service":
		var obj corev1.Service
		if err := yaml.Unmarshal(data, &obj); err != nil {
			return err
		}
		return c.Apply(&obj)
	case "Pod":
		var obj corev1.Pod
		if err := yaml.Unmarshal(data, &obj); err != nil {
			return err
		}
		return c.Apply(&obj)
	case "Deployment":
		var obj appsv1.Deployment
		if err := yaml.Unmarshal(data, &obj); err != nil {
			return err
		}
		return c.Apply(&obj)
	case "StatefulSet":
		var obj appsv1.StatefulSet
		if err := yaml.Unmarshal(data, &obj); err != nil {
			return err
		}
		return c.Apply(&obj)
	case "DaemonSet":
		var obj appsv1.DaemonSet
		if err := yaml.Unmarshal(data, &obj); err != nil {
			return err
		}
		return c.Apply(&obj)
	default:
		return fmt.Errorf("unsupported kind in YAML: %s", kind)
	}
}

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

// ApplyCRD applies a custom resource using the dynamic client.
func (c *Context) ApplyCRD(gvr schema.GroupVersionResource, obj *unstructured.Unstructured) (err error) {
	_, span := c.startSpan(context.Background(), "ilmari.ApplyCRD",
		attribute.String("group", gvr.Group),
		attribute.String("version", gvr.Version),
		attribute.String("resource", gvr.Resource),
		attribute.String("name", obj.GetName()))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	obj.SetNamespace(c.Namespace)
	ctx := context.Background()

	_, err = c.Dynamic.Resource(gvr).Namespace(c.Namespace).Create(ctx, obj, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		// Log the original create error for debugging
		c.t.Logf("ApplyCRD: create returned IsAlreadyExists for %s/%s: %v", gvr.Resource, obj.GetName(), err)
		updateErr := err // preserve original create error
		_, err = c.Dynamic.Resource(gvr).Namespace(c.Namespace).Update(ctx, obj, metav1.UpdateOptions{})
		if err != nil {
			// Wrap both errors for better diagnostics
			return fmt.Errorf("ApplyCRD: create returned IsAlreadyExists (%v), but update failed: %w", updateErr, err)
		}
		return nil
	}
	return err
}

// GetCRD retrieves a custom resource.
func (c *Context) GetCRD(gvr schema.GroupVersionResource, name string) (*unstructured.Unstructured, error) {
	_, span := c.startSpan(context.Background(), "ilmari.GetCRD",
		attribute.String("group", gvr.Group),
		attribute.String("version", gvr.Version),
		attribute.String("resource", gvr.Resource),
		attribute.String("name", name))
	var err error
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	result, err := c.Dynamic.Resource(gvr).Namespace(c.Namespace).Get(context.Background(), name, metav1.GetOptions{})
	return result, err
}

// DeleteCRD deletes a custom resource.
func (c *Context) DeleteCRD(gvr schema.GroupVersionResource, name string) (err error) {
	_, span := c.startSpan(context.Background(), "ilmari.DeleteCRD",
		attribute.String("group", gvr.Group),
		attribute.String("version", gvr.Version),
		attribute.String("resource", gvr.Resource),
		attribute.String("name", name))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	return c.Dynamic.Resource(gvr).Namespace(c.Namespace).Delete(context.Background(), name, metav1.DeleteOptions{})
}

// Isolate creates a NetworkPolicy that blocks all ingress/egress traffic.
// Useful for testing network isolation. The policy name is derived from the
// selector, so calling Isolate multiple times with the same selector updates
// the existing policy, while different selectors create separate policies.
func (c *Context) Isolate(podSelector map[string]string) (err error) {
	_, span := c.startSpan(context.Background(), "ilmari.Isolate")
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	// Generate deterministic name from selector
	policyName := "ilmari-isolate"
	if len(podSelector) > 0 {
		// Use first key-value for name suffix (keeps it short and deterministic)
		for k, v := range podSelector {
			policyName = fmt.Sprintf("ilmari-isolate-%s-%s", k, v)
			break
		}
	}

	policy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      policyName,
			Namespace: c.Namespace,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: podSelector,
			},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
				networkingv1.PolicyTypeEgress,
			},
			// Empty ingress/egress = deny all
			Ingress: []networkingv1.NetworkPolicyIngressRule{},
			Egress:  []networkingv1.NetworkPolicyEgressRule{},
		},
	}

	_, err = c.Client.NetworkingV1().NetworkPolicies(c.Namespace).Create(
		context.Background(), policy, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		_, err = c.Client.NetworkingV1().NetworkPolicies(c.Namespace).Update(
			context.Background(), policy, metav1.UpdateOptions{})
	}
	return err
}

// AllowFrom creates a NetworkPolicy allowing traffic from specific pods.
func (c *Context) AllowFrom(targetSelector, sourceSelector map[string]string) (err error) {
	_, span := c.startSpan(context.Background(), "ilmari.AllowFrom")
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	policy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("ilmari-allow-%s", uuid.New().String()[:8]),
			Namespace: c.Namespace,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: targetSelector,
			},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
			},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					From: []networkingv1.NetworkPolicyPeer{
						{
							PodSelector: &metav1.LabelSelector{
								MatchLabels: sourceSelector,
							},
						},
					},
				},
			},
		},
	}

	_, err = c.Client.NetworkingV1().NetworkPolicies(c.Namespace).Create(
		context.Background(), policy, metav1.CreateOptions{})
	return err
}

// Resources sets resource limits and requests for the ServiceBuilder.
// Both limits and requests are set to the same values for simplicity.
func (sb *ServiceBuilder) Resources(cpu, memory string) *ServiceBuilder {
	sb.resourceLimits = corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(cpu),
		corev1.ResourceMemory: resource.MustParse(memory),
	}
	return sb
}

// ============================================================================
// Fluent Deployment Builder
// ============================================================================

// DeploymentBuilder provides a fluent API for building Deployments.
type DeploymentBuilder struct {
	name       string
	image      string
	replicas   int32
	port       int32
	env        map[string]string
	command    []string
	withProbes bool
}

// Deployment creates a new DeploymentBuilder with the given name.
func Deployment(name string) *DeploymentBuilder {
	return &DeploymentBuilder{
		name:     name,
		replicas: 1,
		env:      make(map[string]string),
	}
}

// Image sets the container image.
func (d *DeploymentBuilder) Image(image string) *DeploymentBuilder {
	d.image = image
	return d
}

// Replicas sets the number of replicas.
func (d *DeploymentBuilder) Replicas(n int) *DeploymentBuilder {
	d.replicas = int32(n)
	return d
}

// Port sets the container port.
func (d *DeploymentBuilder) Port(port int) *DeploymentBuilder {
	d.port = int32(port)
	return d
}

// Env adds an environment variable.
func (d *DeploymentBuilder) Env(key, value string) *DeploymentBuilder {
	d.env[key] = value
	return d
}

// Command sets the container command.
func (d *DeploymentBuilder) Command(cmd ...string) *DeploymentBuilder {
	d.command = cmd
	return d
}

// WithProbes adds sensible default liveness and readiness probes.
func (d *DeploymentBuilder) WithProbes() *DeploymentBuilder {
	d.withProbes = true
	return d
}

// Build creates the Deployment object.
func (d *DeploymentBuilder) Build() *appsv1.Deployment {
	// Build env vars
	var envVars []corev1.EnvVar
	for k, v := range d.env {
		envVars = append(envVars, corev1.EnvVar{Name: k, Value: v})
	}

	container := corev1.Container{
		Name:    d.name,
		Image:   d.image,
		Command: d.command,
		Env:     envVars,
	}

	if d.port > 0 {
		container.Ports = []corev1.ContainerPort{{ContainerPort: d.port}}
	}

	if d.withProbes && d.port > 0 {
		container.LivenessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{
					Port: intstr.FromInt32(d.port),
				},
			},
			InitialDelaySeconds: 5,
			PeriodSeconds:       10,
		}
		container.ReadinessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{
					Port: intstr.FromInt32(d.port),
				},
			},
			InitialDelaySeconds: 2,
			PeriodSeconds:       5,
		}
	}

	replicas := d.replicas
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: d.name,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": d.name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": d.name},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{container},
				},
			},
		},
	}
}

// ============================================================================
// Fixture Loading with Overrides
// ============================================================================

// FixtureBuilder loads YAML and allows overrides before applying.
type FixtureBuilder struct {
	deploy *appsv1.Deployment
	err    error
}

// LoadFixture loads a Deployment from a YAML file.
func (c *Context) LoadFixture(path string) *FixtureBuilder {
	data, err := os.ReadFile(path)
	if err != nil {
		return &FixtureBuilder{err: fmt.Errorf("failed to read fixture: %w", err)}
	}

	var deploy appsv1.Deployment
	if err := yaml.Unmarshal(data, &deploy); err != nil {
		return &FixtureBuilder{err: fmt.Errorf("failed to parse fixture: %w", err)}
	}

	return &FixtureBuilder{deploy: &deploy}
}

// WithImage overrides the container image.
func (f *FixtureBuilder) WithImage(image string) *FixtureBuilder {
	if f.err != nil || f.deploy == nil {
		return f
	}
	if len(f.deploy.Spec.Template.Spec.Containers) > 0 {
		f.deploy.Spec.Template.Spec.Containers[0].Image = image
	}
	return f
}

// WithReplicas overrides the replica count.
func (f *FixtureBuilder) WithReplicas(n int) *FixtureBuilder {
	if f.err != nil || f.deploy == nil {
		return f
	}
	replicas := int32(n)
	f.deploy.Spec.Replicas = &replicas
	return f
}

// Build returns the Deployment (or panics on error).
func (f *FixtureBuilder) Build() *appsv1.Deployment {
	if f.err != nil {
		panic(f.err)
	}
	return f.deploy
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
		select {
		case <-ticker.C:
			if e.fn() {
				return nil
			}
			if time.Now().After(deadline) {
				err := fmt.Errorf("condition not met within %v", e.timeout)
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return err
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
		duration: 5 * time.Second,  // default
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

// ============================================================================
// Test Scenarios
// ============================================================================

// SelfHealTest provides a fluent API for self-healing tests.
type SelfHealTest struct {
	ctx             *Context
	resource        string
	deploymentName  string
	err             error
	recoveryTimeout time.Duration
}

// TestSelfHealing runs a self-healing test on a deployment.
func (c *Context) TestSelfHealing(resource string, fn func(*SelfHealTest)) error {
	parts := strings.SplitN(resource, "/", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "deployment" {
		return fmt.Errorf("TestSelfHealing requires deployment/name format")
	}

	test := &SelfHealTest{
		ctx:             c,
		resource:        resource,
		deploymentName:  parts[1],
		recoveryTimeout: 60 * time.Second,
	}

	fn(test)
	return test.err
}

// KillPod kills one pod from the deployment.
func (s *SelfHealTest) KillPod() {
	if s.err != nil {
		return
	}

	// Find a pod from this deployment
	pods, err := s.ctx.Client.CoreV1().Pods(s.ctx.Namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", s.deploymentName),
	})
	if err != nil {
		s.err = fmt.Errorf("failed to list pods: %w", err)
		return
	}
	if len(pods.Items) == 0 {
		s.err = fmt.Errorf("no pods found for deployment %s", s.deploymentName)
		return
	}

	// Kill the first pod
	podName := pods.Items[0].Name
	s.err = s.ctx.Kill("pod/" + podName)
}

// ExpectRecoveryWithin sets the expected recovery time and waits.
func (s *SelfHealTest) ExpectRecoveryWithin(timeout time.Duration) {
	if s.err != nil {
		return
	}

	s.recoveryTimeout = timeout
	s.err = s.ctx.WaitReadyTimeout(s.resource, timeout)
}

// ScaleTest provides a fluent API for scaling tests.
type ScaleTest struct {
	ctx            *Context
	resource       string
	deploymentName string
	err            error
}

// TestScaling runs a scaling test on a deployment.
func (c *Context) TestScaling(resource string, fn func(*ScaleTest)) error {
	parts := strings.SplitN(resource, "/", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "deployment" {
		return fmt.Errorf("TestScaling requires deployment/name format")
	}

	test := &ScaleTest{
		ctx:            c,
		resource:       resource,
		deploymentName: parts[1],
	}

	fn(test)
	return test.err
}

// ScaleTo scales the deployment to n replicas.
func (s *ScaleTest) ScaleTo(n int) {
	if s.err != nil {
		return
	}

	deploy, err := s.ctx.Client.AppsV1().Deployments(s.ctx.Namespace).Get(
		context.Background(), s.deploymentName, metav1.GetOptions{})
	if err != nil {
		s.err = fmt.Errorf("failed to get deployment: %w", err)
		return
	}

	replicas := int32(n)
	deploy.Spec.Replicas = &replicas

	_, err = s.ctx.Client.AppsV1().Deployments(s.ctx.Namespace).Update(
		context.Background(), deploy, metav1.UpdateOptions{})
	if err != nil {
		s.err = fmt.Errorf("failed to scale deployment: %w", err)
	}
}

// WaitStable waits for the deployment to be stable at current replica count.
func (s *ScaleTest) WaitStable() {
	if s.err != nil {
		return
	}

	s.err = s.ctx.WaitReadyTimeout(s.resource, 60*time.Second)
}

// ============================================================================
// Traffic Testing
// ============================================================================

// TrafficConfig configures traffic generation.
type TrafficConfig struct {
	rps      int
	duration time.Duration
	endpoint string
}

// RPS sets the requests per second.
func (t *TrafficConfig) RPS(n int) {
	t.rps = n
}

// Duration sets how long to generate traffic.
func (t *TrafficConfig) Duration(d time.Duration) {
	t.duration = d
}

// Endpoint sets the HTTP endpoint to hit.
func (t *TrafficConfig) Endpoint(path string) {
	t.endpoint = path
}

// Traffic represents an ongoing or completed traffic test.
type Traffic struct {
	ctx       *Context
	pf        *PortForward
	config    TrafficConfig
	done      chan struct{}
	mu        sync.Mutex
	total     int64
	errors    int64
	latencies []time.Duration
}

// StartTraffic starts generating HTTP traffic to a service.
func (c *Context) StartTraffic(resource string, fn func(*TrafficConfig)) *Traffic {
	config := TrafficConfig{
		rps:      10,
		duration: 10 * time.Second,
		endpoint: "/",
	}
	fn(&config)

	// Set up port forward
	pf := c.Forward(resource, 80)

	traffic := &Traffic{
		ctx:       c,
		pf:        pf,
		config:    config,
		done:      make(chan struct{}),
		latencies: make([]time.Duration, 0, config.rps*int(config.duration.Seconds())),
	}

	// Start traffic generation in background
	go traffic.run()

	return traffic
}

// run generates traffic in the background.
func (t *Traffic) run() {
	defer close(t.done)
	defer t.pf.Close()

	if t.pf.err != nil {
		return
	}

	interval := time.Duration(float64(time.Second) / float64(t.config.rps))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	deadline := time.Now().Add(t.config.duration)
	client := &http.Client{Timeout: 5 * time.Second}

	for time.Now().Before(deadline) {
		<-ticker.C

		start := time.Now()
		resp, err := client.Get(fmt.Sprintf("http://localhost:%d%s", t.pf.localPort, t.config.endpoint))
		latency := time.Since(start)

		t.mu.Lock()
		t.total++
		t.latencies = append(t.latencies, latency)
		if err != nil || (resp != nil && resp.StatusCode >= 400) {
			t.errors++
		}
		t.mu.Unlock()

		if resp != nil {
			resp.Body.Close()
		}
	}
}

// Wait blocks until traffic generation completes.
func (t *Traffic) Wait() {
	<-t.done
}

// Stop stops traffic generation early.
func (t *Traffic) Stop() {
	t.pf.Close()
	<-t.done
}

// TotalRequests returns the total number of requests made.
func (t *Traffic) TotalRequests() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.total
}

// ErrorRate returns the fraction of failed requests (0.0 to 1.0).
func (t *Traffic) ErrorRate() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.total == 0 {
		return 0
	}
	return float64(t.errors) / float64(t.total)
}

// P99Latency returns the 99th percentile latency.
func (t *Traffic) P99Latency() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.latencies) == 0 {
		return 0
	}

	// Copy and sort
	sorted := make([]time.Duration, len(t.latencies))
	copy(sorted, t.latencies)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})

	// Get 99th percentile index
	idx := int(float64(len(sorted)) * 0.99)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
