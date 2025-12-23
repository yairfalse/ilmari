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
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
)

// Context provides a Kubernetes connection for SDK operations.
type Context struct {
	// Client is the Kubernetes clientset
	Client *kubernetes.Clientset

	// Dynamic is the dynamic client for CRDs and unstructured resources
	Dynamic dynamic.Interface

	// Namespace is the working namespace
	Namespace string

	// t is the test instance for logging (optional, nil for standalone usage)
	t *testing.T

	// mapper for GVK to GVR discovery
	mapper *restmapper.DeferredDiscoveryRESTMapper

	// ownsNamespace tracks whether Close() should delete the namespace
	ownsNamespace bool

	// restConfig is stored for operations that need it
	restConfig *rest.Config
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
// Set ILMARI_KEEP_ALL=true to keep namespaces after all tests.
func DefaultConfig() Config {
	return Config{
		KeepOnFailure: true,
		KeepAlways:    os.Getenv("ILMARI_KEEP_ALL") == "true",
	}
}

// ============================================================================
// Standalone SDK Usage (no test wrapper required)
// ============================================================================

// ContextOption configures a Context.
type ContextOption func(*contextOptions)

// contextOptions holds configuration for NewContext.
type contextOptions struct {
	kubeconfig     string
	namespace      string // use existing namespace (shared)
	isolatedPrefix string // create isolated namespace with prefix
}

// WithKubeconfig sets a custom kubeconfig path.
func WithKubeconfig(path string) ContextOption {
	return func(o *contextOptions) {
		o.kubeconfig = path
	}
}

// WithNamespace uses an existing namespace (shared mode).
// The namespace will NOT be deleted on Close().
func WithNamespace(ns string) ContextOption {
	return func(o *contextOptions) {
		o.namespace = ns
		o.isolatedPrefix = "" // clear isolated if set
	}
}

// WithIsolatedNamespace creates a new namespace with the given prefix.
// A random suffix is appended. The namespace IS deleted on Close().
func WithIsolatedNamespace(prefix string) ContextOption {
	return func(o *contextOptions) {
		o.isolatedPrefix = prefix
		o.namespace = "" // clear shared if set
	}
}

// NewContext creates a new Context for standalone SDK usage.
// By default, creates an isolated namespace that is deleted on Close().
//
// Examples:
//
//	ctx, _ := ilmari.NewContext()                              // isolated, auto-cleanup
//	ctx, _ := ilmari.NewContext(ilmari.WithNamespace("prod"))  // shared, no cleanup
//	ctx, _ := ilmari.NewContext(ilmari.WithIsolatedNamespace("mytest")) // isolated with prefix
func NewContext(opts ...ContextOption) (*Context, error) {
	options := &contextOptions{
		isolatedPrefix: "ilmari", // default: create isolated namespace
	}
	for _, opt := range opts {
		opt(options)
	}

	// Load kubeconfig
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if options.kubeconfig != "" {
		loadingRules.ExplicitPath = options.kubeconfig
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

	ctx := &Context{
		Client:     client,
		Dynamic:    dynamicClient,
		mapper:     mapper,
		restConfig: config,
	}

	// Handle namespace: shared vs isolated
	if options.namespace != "" {
		// Shared mode: use existing namespace, don't delete on close
		ctx.Namespace = options.namespace
		ctx.ownsNamespace = false
	} else {
		// Isolated mode: create new namespace with random suffix
		id := uuid.New().String()[:8]
		ctx.Namespace = fmt.Sprintf("%s-%s", options.isolatedPrefix, id)
		ctx.ownsNamespace = true

		// Create namespace
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: ctx.Namespace,
				Labels: map[string]string{
					"ilmari.io/managed": "true",
				},
			},
		}

		_, err = client.CoreV1().Namespaces().Create(context.Background(), ns, metav1.CreateOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to create namespace: %w", err)
		}
	}

	return ctx, nil
}

// Close cleans up the Context.
// For isolated namespaces, this deletes the namespace.
// For shared namespaces, this is a no-op.
func (c *Context) Close() {
	if c.ownsNamespace {
		_ = c.cleanup()
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

	return &Context{
		Client:        client,
		Dynamic:       dynamicClient,
		Namespace:     namespace,
		t:             t,
		mapper:        mapper,
		ownsNamespace: true,
		restConfig:    config,
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
	_ = rbacv1.AddToScheme(scheme)
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

	// Handle list types by stripping "List" suffix
	kind := gvk.Kind
	if strings.HasSuffix(kind, "List") {
		kind = strings.TrimSuffix(kind, "List")
		gvk = schema.GroupVersionKind{
			Group:   gvk.Group,
			Version: gvk.Version,
			Kind:    kind,
		}
	}

	mapping, err := c.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return schema.GroupVersionResource{}, fmt.Errorf("failed to get REST mapping: %w", err)
	}
	return mapping.Resource, nil
}

// isClusterScoped checks if a GVR is cluster-scoped using REST mapper.
func (c *Context) isClusterScoped(gvr schema.GroupVersionResource) bool {
	// Use discovery to find the resource
	apiResources, err := c.Client.Discovery().ServerResourcesForGroupVersion(gvr.GroupVersion().String())
	if err != nil {
		return false
	}

	for _, r := range apiResources.APIResources {
		if r.Name == gvr.Resource {
			return !r.Namespaced
		}
	}
	return false
}

// kindToGVR discovers the GVR for a given kind name.
func (c *Context) kindToGVR(kind string) (schema.GroupVersionResource, error) {
	// Normalize kind (gatewayclass -> GatewayClass)
	kindLower := strings.ToLower(kind)

	// Check common mappings first (fast path)
	if gvr, ok := commonKindMappings[kindLower]; ok {
		return gvr, nil
	}

	// Discovery: find all API resources and match by kind
	_, apiResources, err := c.Client.Discovery().ServerGroupsAndResources()
	if err != nil {
		// Ignore partial errors from unavailable API groups
		if !discovery.IsGroupDiscoveryFailedError(err) {
			return schema.GroupVersionResource{}, err
		}
	}

	for _, list := range apiResources {
		if list == nil {
			continue
		}
		gv, parseErr := schema.ParseGroupVersion(list.GroupVersion)
		if parseErr != nil {
			continue
		}
		for _, r := range list.APIResources {
			if strings.ToLower(r.Kind) == kindLower || strings.ToLower(r.Name) == kindLower {
				return schema.GroupVersionResource{
					Group:    gv.Group,
					Version:  gv.Version,
					Resource: r.Name,
				}, nil
			}
		}
	}

	return schema.GroupVersionResource{}, fmt.Errorf("kind %q not found in cluster", kind)
}

// commonKindMappings provides fast-path GVR lookups for common CRDs.
var commonKindMappings = map[string]schema.GroupVersionResource{
	// Gateway API
	"gatewayclass": {Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gatewayclasses"},
	"gateway":      {Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"},
	"httproute":    {Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"},
	"grpcroute":    {Group: "gateway.networking.k8s.io", Version: "v1", Resource: "grpcroutes"},
	"tcproute":     {Group: "gateway.networking.k8s.io", Version: "v1alpha2", Resource: "tcproutes"},
	"udproute":     {Group: "gateway.networking.k8s.io", Version: "v1alpha2", Resource: "udproutes"},
	"tlsroute":     {Group: "gateway.networking.k8s.io", Version: "v1alpha2", Resource: "tlsroutes"},
	// Cert-Manager
	"certificate":   {Group: "cert-manager.io", Version: "v1", Resource: "certificates"},
	"issuer":        {Group: "cert-manager.io", Version: "v1", Resource: "issuers"},
	"clusterissuer": {Group: "cert-manager.io", Version: "v1", Resource: "clusterissuers"},
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
	if c.t != nil {
		c.t.Logf("Deleted test namespace: %s", c.Namespace)
	}
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

// ============================================================================
// Resource CRUD Operations
// ============================================================================

// Apply creates or updates a resource.
// Works with any resource type including CRDs.
// Automatically detects cluster-scoped vs namespaced resources.
func (c *Context) Apply(obj runtime.Object) error {
	gvr, err := c.getGVR(obj)
	if err != nil {
		return err
	}

	// Convert to unstructured
	u, err := toUnstructured(obj)
	if err != nil {
		return fmt.Errorf("failed to convert to unstructured: %w", err)
	}

	ctx := context.Background()

	// Auto-detect: use cluster or namespaced client
	var client dynamic.ResourceInterface
	if c.isClusterScoped(gvr) {
		client = c.Dynamic.Resource(gvr)
	} else {
		u.SetNamespace(c.Namespace)
		client = c.Dynamic.Resource(gvr).Namespace(c.Namespace)
	}

	_, err = client.Create(ctx, u, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		// Get existing to preserve resourceVersion
		existing, getErr := client.Get(ctx, u.GetName(), metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		u.SetResourceVersion(existing.GetResourceVersion())
		_, err = client.Update(ctx, u, metav1.UpdateOptions{})
	}
	return err
}

// Get retrieves a resource.
// Works with any resource type including CRDs.
// Automatically detects cluster-scoped vs namespaced resources.
func (c *Context) Get(name string, obj runtime.Object) error {
	gvr, err := c.getGVR(obj)
	if err != nil {
		return err
	}

	// Auto-detect: use cluster or namespaced client
	var client dynamic.ResourceInterface
	if c.isClusterScoped(gvr) {
		client = c.Dynamic.Resource(gvr)
	} else {
		client = c.Dynamic.Resource(gvr).Namespace(c.Namespace)
	}

	u, err := client.Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	// Convert back to typed object
	return runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, obj)
}

// Delete removes a resource.
// Works with any resource type including CRDs.
// Automatically detects cluster-scoped vs namespaced resources.
func (c *Context) Delete(name string, obj runtime.Object) error {
	gvr, err := c.getGVR(obj)
	if err != nil {
		return err
	}

	// Auto-detect: use cluster or namespaced client
	var client dynamic.ResourceInterface
	if c.isClusterScoped(gvr) {
		client = c.Dynamic.Resource(gvr)
	} else {
		client = c.Dynamic.Resource(gvr).Namespace(c.Namespace)
	}

	return client.Delete(context.Background(), name, metav1.DeleteOptions{})
}

// List retrieves all resources of a type.
// Works with any resource type including CRDs.
// For namespaced resources, lists only in the current namespace.
// For cluster-scoped resources, lists all.
func (c *Context) List(list runtime.Object) error {
	gvr, err := c.getGVR(list)
	if err != nil {
		return err
	}

	// Auto-detect: use cluster or namespaced client
	var client dynamic.ResourceInterface
	if c.isClusterScoped(gvr) {
		client = c.Dynamic.Resource(gvr)
	} else {
		client = c.Dynamic.Resource(gvr).Namespace(c.Namespace)
	}

	u, err := client.List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return err
	}

	// Convert back to typed list
	return runtime.DefaultUnstructuredConverter.FromUnstructured(u.UnstructuredContent(), list)
}

// ============================================================================
// Wait Operations
// ============================================================================

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
			return err
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

	case "pvc", "persistentvolumeclaim":
		pvc, err := c.Client.CoreV1().PersistentVolumeClaims(c.Namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return pvc, err

	default:
		// Try dynamic client for CRDs
		return c.getResourceDynamic(kind, name)
	}
}

// getResourceDynamic fetches a resource using the dynamic client.
// Used for CRDs and unknown resource types.
func (c *Context) getResourceDynamic(kind, name string) (interface{}, error) {
	gvr, err := c.kindToGVR(kind)
	if err != nil {
		return nil, fmt.Errorf("unknown kind %q: %w", kind, err)
	}

	var client dynamic.ResourceInterface
	if c.isClusterScoped(gvr) {
		client = c.Dynamic.Resource(gvr)
	} else {
		client = c.Dynamic.Resource(gvr).Namespace(c.Namespace)
	}

	obj, err := client.Get(context.Background(), name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	return obj, err
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
			return err
		}
		if ready {
			return nil
		}

		select {
		case <-ctx.Done():
			return c.buildWaitError(resource, kind, name)
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

	// Build label selector for filtering pods
	var labelSelector string

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

			// Extract label selector from deployment
			if deploy.Spec.Selector != nil && deploy.Spec.Selector.MatchLabels != nil {
				labelSelector = labels.SelectorFromSet(deploy.Spec.Selector.MatchLabels).String()
			}
		}
	}

	// Get pod statuses (filtered by label selector if available)
	listOpts := metav1.ListOptions{}
	if labelSelector != "" {
		listOpts.LabelSelector = labelSelector
	}
	pods, err := c.Client.CoreV1().Pods(c.Namespace).List(bgCtx, listOpts)
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
						imageName := "<unknown>"
						if len(pod.Spec.Containers) > 0 {
							imageName = pod.Spec.Containers[0].Image
						}
						waitErr.Hint = fmt.Sprintf("Image %q may not exist. Did you forget to build/push?", imageName)
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

	case "pvc", "persistentvolumeclaim":
		pvc, err := c.Client.CoreV1().PersistentVolumeClaims(c.Namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return pvc.Status.Phase == corev1.ClaimBound, nil

	default:
		// Try dynamic client for CRDs - check status.conditions
		obj, err := c.getResourceDynamic(kind, name)
		if err != nil {
			return false, err
		}
		if obj == nil {
			return false, nil
		}
		return c.checkConditions(obj), nil
	}
}

// checkConditions looks for Ready/Accepted/Available/Programmed conditions in CRDs.
// Returns true if any of these conditions have Status: "True".
func (c *Context) checkConditions(obj interface{}) bool {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return false
	}

	conditions, found, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	if !found {
		return false
	}

	for _, cond := range conditions {
		condMap, ok := cond.(map[string]interface{})
		if !ok {
			continue
		}

		condType, _ := condMap["type"].(string)
		status, _ := condMap["status"].(string)

		// Check for common "ready" conditions used by various CRDs
		// - Ready: general readiness (used by many CRDs)
		// - Accepted: Gateway API GatewayClass, Gateway
		// - Programmed: Gateway API Gateway, HTTPRoute
		// - Available: some CRDs use this
		if (condType == "Ready" || condType == "Accepted" || condType == "Available" || condType == "Programmed") &&
			status == "True" {
			return true
		}
	}
	return false
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
