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
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/transport/spdy"
)

// Context provides a Kubernetes connection with an isolated namespace for testing.
type Context struct {
	// Client is the Kubernetes clientset
	Client *kubernetes.Clientset

	// Namespace is the isolated test namespace
	Namespace string

	// t is the test instance for logging
	t *testing.T
}

// Config configures the test context behavior.
type Config struct {
	// KeepOnFailure keeps the namespace on test failure (default: true)
	KeepOnFailure bool

	// KeepAlways keeps the namespace even on success (debug mode)
	KeepAlways bool

	// Kubeconfig path (default: uses KUBECONFIG or ~/.kube/config)
	Kubeconfig string
}

// DefaultConfig returns the default configuration.
func DefaultConfig() Config {
	return Config{
		KeepOnFailure: true,
		KeepAlways:    false,
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

	return &Context{
		Client:    client,
		Namespace: namespace,
		t:         t,
	}, nil
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

	c.t.Logf("\n--- End Diagnostics ---")
}

// Apply creates or updates a resource in the test namespace.
func (c *Context) Apply(obj runtime.Object) error {
	ctx := context.Background()

	switch o := obj.(type) {
	case *corev1.ConfigMap:
		cm := o.DeepCopy()
		cm.Namespace = c.Namespace
		_, err := c.Client.CoreV1().ConfigMaps(c.Namespace).Create(ctx, cm, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			_, err = c.Client.CoreV1().ConfigMaps(c.Namespace).Update(ctx, cm, metav1.UpdateOptions{})
		}
		return err

	case *corev1.Secret:
		secret := o.DeepCopy()
		secret.Namespace = c.Namespace
		_, err := c.Client.CoreV1().Secrets(c.Namespace).Create(ctx, secret, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			_, err = c.Client.CoreV1().Secrets(c.Namespace).Update(ctx, secret, metav1.UpdateOptions{})
		}
		return err

	case *corev1.Service:
		svc := o.DeepCopy()
		svc.Namespace = c.Namespace
		_, err := c.Client.CoreV1().Services(c.Namespace).Create(ctx, svc, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			_, err = c.Client.CoreV1().Services(c.Namespace).Update(ctx, svc, metav1.UpdateOptions{})
		}
		return err

	case *corev1.Pod:
		pod := o.DeepCopy()
		pod.Namespace = c.Namespace
		_, err := c.Client.CoreV1().Pods(c.Namespace).Create(ctx, pod, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			_, err = c.Client.CoreV1().Pods(c.Namespace).Update(ctx, pod, metav1.UpdateOptions{})
		}
		return err

	case *appsv1.Deployment:
		deploy := o.DeepCopy()
		deploy.Namespace = c.Namespace
		_, err := c.Client.AppsV1().Deployments(c.Namespace).Create(ctx, deploy, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			_, err = c.Client.AppsV1().Deployments(c.Namespace).Update(ctx, deploy, metav1.UpdateOptions{})
		}
		return err

	case *appsv1.StatefulSet:
		ss := o.DeepCopy()
		ss.Namespace = c.Namespace
		_, err := c.Client.AppsV1().StatefulSets(c.Namespace).Create(ctx, ss, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			_, err = c.Client.AppsV1().StatefulSets(c.Namespace).Update(ctx, ss, metav1.UpdateOptions{})
		}
		return err

	case *appsv1.DaemonSet:
		ds := o.DeepCopy()
		ds.Namespace = c.Namespace
		_, err := c.Client.AppsV1().DaemonSets(c.Namespace).Create(ctx, ds, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			_, err = c.Client.AppsV1().DaemonSets(c.Namespace).Update(ctx, ds, metav1.UpdateOptions{})
		}
		return err

	default:
		return fmt.Errorf("unsupported type: %T", obj)
	}
}

// Get retrieves a resource from the test namespace.
func (c *Context) Get(name string, obj runtime.Object) error {
	ctx := context.Background()

	switch o := obj.(type) {
	case *corev1.ConfigMap:
		got, err := c.Client.CoreV1().ConfigMaps(c.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		*o = *got
		return nil

	case *corev1.Secret:
		got, err := c.Client.CoreV1().Secrets(c.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		*o = *got
		return nil

	case *corev1.Service:
		got, err := c.Client.CoreV1().Services(c.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		*o = *got
		return nil

	case *corev1.Pod:
		got, err := c.Client.CoreV1().Pods(c.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		*o = *got
		return nil

	case *appsv1.Deployment:
		got, err := c.Client.AppsV1().Deployments(c.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		*o = *got
		return nil

	case *appsv1.StatefulSet:
		got, err := c.Client.AppsV1().StatefulSets(c.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		*o = *got
		return nil

	case *appsv1.DaemonSet:
		got, err := c.Client.AppsV1().DaemonSets(c.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		*o = *got
		return nil

	default:
		return fmt.Errorf("unsupported type: %T", obj)
	}
}

// Delete removes a resource from the test namespace.
func (c *Context) Delete(name string, obj runtime.Object) error {
	ctx := context.Background()

	switch obj.(type) {
	case *corev1.ConfigMap:
		return c.Client.CoreV1().ConfigMaps(c.Namespace).Delete(ctx, name, metav1.DeleteOptions{})
	case *corev1.Secret:
		return c.Client.CoreV1().Secrets(c.Namespace).Delete(ctx, name, metav1.DeleteOptions{})
	case *corev1.Service:
		return c.Client.CoreV1().Services(c.Namespace).Delete(ctx, name, metav1.DeleteOptions{})
	case *corev1.Pod:
		return c.Client.CoreV1().Pods(c.Namespace).Delete(ctx, name, metav1.DeleteOptions{})
	case *appsv1.Deployment:
		return c.Client.AppsV1().Deployments(c.Namespace).Delete(ctx, name, metav1.DeleteOptions{})
	case *appsv1.StatefulSet:
		return c.Client.AppsV1().StatefulSets(c.Namespace).Delete(ctx, name, metav1.DeleteOptions{})
	case *appsv1.DaemonSet:
		return c.Client.AppsV1().DaemonSets(c.Namespace).Delete(ctx, name, metav1.DeleteOptions{})
	default:
		return fmt.Errorf("unsupported type: %T", obj)
	}
}

// List retrieves all resources of a type from the test namespace.
func (c *Context) List(list runtime.Object) error {
	ctx := context.Background()
	opts := metav1.ListOptions{}

	switch l := list.(type) {
	case *corev1.ConfigMapList:
		got, err := c.Client.CoreV1().ConfigMaps(c.Namespace).List(ctx, opts)
		if err != nil {
			return err
		}
		*l = *got
		return nil

	case *corev1.SecretList:
		got, err := c.Client.CoreV1().Secrets(c.Namespace).List(ctx, opts)
		if err != nil {
			return err
		}
		*l = *got
		return nil

	case *corev1.ServiceList:
		got, err := c.Client.CoreV1().Services(c.Namespace).List(ctx, opts)
		if err != nil {
			return err
		}
		*l = *got
		return nil

	case *corev1.PodList:
		got, err := c.Client.CoreV1().Pods(c.Namespace).List(ctx, opts)
		if err != nil {
			return err
		}
		*l = *got
		return nil

	case *appsv1.DeploymentList:
		got, err := c.Client.AppsV1().Deployments(c.Namespace).List(ctx, opts)
		if err != nil {
			return err
		}
		*l = *got
		return nil

	case *appsv1.StatefulSetList:
		got, err := c.Client.AppsV1().StatefulSets(c.Namespace).List(ctx, opts)
		if err != nil {
			return err
		}
		*l = *got
		return nil

	case *appsv1.DaemonSetList:
		got, err := c.Client.AppsV1().DaemonSets(c.Namespace).List(ctx, opts)
		if err != nil {
			return err
		}
		*l = *got
		return nil

	default:
		return fmt.Errorf("unsupported type: %T", list)
	}
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
func (c *Context) WaitForTimeout(resource string, condition func(obj interface{}) bool, timeout time.Duration) error {
	parts := strings.SplitN(resource, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid resource format %q, expected kind/name", resource)
	}

	kind := strings.ToLower(parts[0])
	name := parts[1]

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		obj, err := c.getResource(kind, name)
		if err != nil {
			// Only return on permanent errors; retry on transient errors.
			if apierrors.IsNotFound(err) || apierrors.IsForbidden(err) || apierrors.IsInvalid(err) || apierrors.IsBadRequest(err) {
				return err
			}
			// For transient errors, optionally log and continue retrying.
			// c.t.Logf("Transient error getting resource %s/%s: %v", kind, name, err)
			// Continue to next iteration (wait and retry)
		} else if obj != nil && condition(obj) {
			return nil
		}
		if obj != nil && condition(obj) {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for %s", resource)
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

// WaitReadyTimeout waits for a resource to be ready with custom timeout.
func (c *Context) WaitReadyTimeout(resource string, timeout time.Duration) error {
	parts := strings.SplitN(resource, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid resource format %q, expected kind/name", resource)
	}

	kind := strings.ToLower(parts[0])
	name := parts[1]

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		ready, err := c.isReady(kind, name)
		if err != nil {
			// Retry on transient errors, only fail on permanent errors
			if apierrors.IsNotFound(err) || apierrors.IsTooManyRequests(err) {
				// Transient error, continue polling
				break
			}
			// For network errors, retry if temporary
			if ne, ok := err.(interface{ Temporary() bool }); ok && ne.Temporary() {
				break
			}
			return err
		}
		if ready {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for %s to be ready", resource)
		case <-ticker.C:
			// continue polling
		}
	}
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
func (c *Context) Logs(pod string) (string, error) {
	req := c.Client.CoreV1().Pods(c.Namespace).GetLogs(pod, &corev1.PodLogOptions{})
	stream, err := req.Stream(context.Background())
	if err != nil {
		return "", fmt.Errorf("failed to get logs: %w", err)
	}
	defer stream.Close()

	buf := new(strings.Builder)
	if _, err := io.Copy(buf, stream); err != nil {
		return "", fmt.Errorf("failed to read logs: %w", err)
	}
	return buf.String(), nil
}

// Events returns all events in the test namespace.
func (c *Context) Events() ([]corev1.Event, error) {
	list, err := c.Client.CoreV1().Events(c.Namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list events: %w", err)
	}
	return list.Items, nil
}

// Exec executes a command in a pod.
func (c *Context) Exec(pod string, cmd []string) (string, error) {
	config, err := clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
	if err != nil {
		return "", fmt.Errorf("failed to get config: %w", err)
	}

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		return "", fmt.Errorf("failed to add scheme: %w", err)
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

	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return "", fmt.Errorf("failed to create executor: %w", err)
	}

	var stdout, stderr strings.Builder
	err = exec.StreamWithContext(context.Background(), remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return "", fmt.Errorf("exec failed: %w (stderr: %s)", err, stderr.String())
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
}

// Close closes the port forward and waits for cleanup.
func (pf *PortForward) Close() {
	if pf.stopChan != nil {
		close(pf.stopChan)
	}
	if pf.doneChan != nil {
		<-pf.doneChan
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
	parts := strings.SplitN(resource, "/", 2)
	if len(parts) != 2 {
		return &PortForward{err: fmt.Errorf("invalid resource format %q, expected kind/name", resource)}
	}

	kind := strings.ToLower(parts[0])
	name := parts[1]

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
			return &PortForward{err: fmt.Errorf("failed to get service: %w", err)}
		}

		// Find pods matching the service selector
		pods, err := c.Client.CoreV1().Pods(c.Namespace).List(ctx, metav1.ListOptions{
			LabelSelector: labels.SelectorFromSet(svc.Spec.Selector).String(),
		})
		if err != nil {
			return &PortForward{err: fmt.Errorf("failed to list pods: %w", err)}
		}
		if len(pods.Items) == 0 {
			return &PortForward{err: fmt.Errorf("no pods found for service %s", name)}
		}
		podName = pods.Items[0].Name
	default:
		return &PortForward{err: fmt.Errorf("unsupported kind %q, use pod or svc", kind)}
	}

	// Set up port forward
	config, err := clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
	if err != nil {
		return &PortForward{err: fmt.Errorf("failed to get config: %w", err)}
	}

	roundTripper, upgrader, err := spdy.RoundTripperFor(config)
	if err != nil {
		return &PortForward{err: fmt.Errorf("failed to create round tripper: %w", err)}
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
		return &PortForward{err: fmt.Errorf("failed to create port forward: %w", err)}
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
		return &PortForward{err: fmt.Errorf("port forward failed: %w", err)}
	}

	forwardedPorts, err := pf.GetPorts()
	if err != nil || len(forwardedPorts) == 0 {
		return &PortForward{err: fmt.Errorf("failed to get forwarded ports: %w", err)}
	}

	return &PortForward{
		localPort:  int(forwardedPorts[0].Local),
		stopChan:   stopChan,
		doneChan:   doneChan,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}
