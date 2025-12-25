package ilmari

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
)

// ============================================================================
// Logs and Diagnostics
// ============================================================================

// Logs retrieves logs from a pod.
func (c *Context) Logs(pod string) (string, error) {
	req := c.Client.CoreV1().Pods(c.Namespace).GetLogs(pod, &corev1.PodLogOptions{})
	stream, err := req.Stream(context.Background())
	if err != nil {
		return "", fmt.Errorf("failed to get logs: %w", err)
	}
	defer func() { _ = stream.Close() }()

	buf := new(strings.Builder)
	if _, err = io.Copy(buf, stream); err != nil {
		return "", fmt.Errorf("failed to read logs: %w", err)
	}
	return buf.String(), nil
}

// LogsWithOptions retrieves logs from a pod with options.
func (c *Context) LogsWithOptions(pod string, opts LogsOptions) (string, error) {
	logOpts := &corev1.PodLogOptions{}
	if opts.Container != "" {
		logOpts.Container = opts.Container
	}
	if opts.Since > 0 {
		sinceSeconds := int64(opts.Since.Seconds())
		logOpts.SinceSeconds = &sinceSeconds
	}
	if opts.TailLines > 0 {
		logOpts.TailLines = &opts.TailLines
	}

	req := c.Client.CoreV1().Pods(c.Namespace).GetLogs(pod, logOpts)
	stream, err := req.Stream(context.Background())
	if err != nil {
		return "", fmt.Errorf("failed to get logs: %w", err)
	}
	defer func() { _ = stream.Close() }()

	buf := new(strings.Builder)
	if _, err = io.Copy(buf, stream); err != nil {
		return "", fmt.Errorf("failed to read logs: %w", err)
	}
	return buf.String(), nil
}

// LogsStream streams logs from a pod in real-time.
// Returns a stop function to cancel the stream. Safe to call multiple times.
// Each line is passed to the callback as it arrives.
func (c *Context) LogsStream(pod string, callback func(line string)) func() {
	stopChan := make(chan struct{})
	var stopOnce sync.Once

	go func() {
		req := c.Client.CoreV1().Pods(c.Namespace).GetLogs(pod, &corev1.PodLogOptions{
			Follow: true,
		})

		stream, err := req.Stream(context.Background())
		if err != nil {
			return
		}
		defer func() { _ = stream.Close() }()

		reader := bufio.NewReader(stream)
		for {
			select {
			case <-stopChan:
				return
			default:
				line, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				callback(strings.TrimSuffix(line, "\n"))
			}
		}
	}()

	return func() {
		stopOnce.Do(func() {
			close(stopChan)
		})
	}
}

// Events returns all events in the test namespace.
func (c *Context) Events() ([]corev1.Event, error) {
	list, err := c.Client.CoreV1().Events(c.Namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list events: %w", err)
	}
	return list.Items, nil
}

// ============================================================================
// Exec and Copy
// ============================================================================

// Exec executes a command in a pod.
func (c *Context) Exec(pod string, cmd []string) (string, error) {
	config, err := clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
	if err != nil {
		return "", fmt.Errorf("failed to get config: %w", err)
	}

	scheme := runtime.NewScheme()
	if err = corev1.AddToScheme(scheme); err != nil {
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

// CopyTo copies a local file to a pod.
// Uses tar to transfer the file via exec.
func (c *Context) CopyTo(pod, localPath, remotePath string) error {
	// Read local file
	data, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("failed to read local file: %w", err)
	}

	// Get config for exec
	config, err := clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	execScheme := runtime.NewScheme()
	if err = corev1.AddToScheme(execScheme); err != nil {
		return fmt.Errorf("failed to add scheme: %w", err)
	}

	// Create tar archive in memory
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)

	// Get remote filename from path
	remoteDir := filepath.Dir(remotePath)
	remoteFile := filepath.Base(remotePath)

	// Add file to tar
	hdr := &tar.Header{
		Name: remoteFile,
		Mode: 0644,
		Size: int64(len(data)),
	}
	if err = tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("failed to write tar header: %w", err)
	}
	if _, err = tw.Write(data); err != nil {
		return fmt.Errorf("failed to write tar data: %w", err)
	}
	if err = tw.Close(); err != nil {
		return fmt.Errorf("failed to close tar: %w", err)
	}

	// Execute tar extract in pod
	req := c.Client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod).
		Namespace(c.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command: []string{"tar", "-xf", "-", "-C", remoteDir},
			Stdin:   true,
			Stdout:  true,
			Stderr:  true,
		}, runtime.NewParameterCodec(execScheme))

	executor, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("failed to create executor: %w", err)
	}

	var stdout, stderr strings.Builder
	err = executor.StreamWithContext(context.Background(), remotecommand.StreamOptions{
		Stdin:  &tarBuf,
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return fmt.Errorf("copy failed: %w (stderr: %s)", err, stderr.String())
	}

	return nil
}

// CopyFrom copies a file from a pod to a local path.
// Uses tar to transfer the file via exec.
func (c *Context) CopyFrom(pod, remotePath, localPath string) error {
	// Get config for exec
	config, err := clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	execScheme := runtime.NewScheme()
	if err = corev1.AddToScheme(execScheme); err != nil {
		return fmt.Errorf("failed to add scheme: %w", err)
	}

	// Execute tar create in pod
	remoteDir := filepath.Dir(remotePath)
	remoteFile := filepath.Base(remotePath)

	req := c.Client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod).
		Namespace(c.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command: []string{"tar", "-cf", "-", "-C", remoteDir, remoteFile},
			Stdout:  true,
			Stderr:  true,
		}, runtime.NewParameterCodec(execScheme))

	executor, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("failed to create executor: %w", err)
	}

	var tarBuf, stderr bytes.Buffer
	err = executor.StreamWithContext(context.Background(), remotecommand.StreamOptions{
		Stdout: &tarBuf,
		Stderr: &stderr,
	})
	if err != nil {
		return fmt.Errorf("copy failed: %w (stderr: %s)", err, stderr.String())
	}

	// Extract from tar
	tr := tar.NewReader(&tarBuf)
	hdr, err := tr.Next()
	if err != nil {
		return fmt.Errorf("failed to read tar header: %w", err)
	}

	// Create local file
	outFile, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("failed to create local file: %w", err)
	}
	defer func() { _ = outFile.Close() }()

	if _, err = io.CopyN(outFile, tr, hdr.Size); err != nil {
		return fmt.Errorf("failed to write local file: %w", err)
	}

	return nil
}

// ============================================================================
// Log Aggregation
// ============================================================================

// LogsOptions configures log retrieval.
type LogsOptions struct {
	// Container specifies which container to get logs from (for multi-container pods)
	Container string
	// Since returns logs newer than this duration
	Since time.Duration
	// TailLines limits output to the last N lines
	TailLines int64
}

// LogsAll retrieves logs from all pods matching the label selector.
// Returns a map of pod name to logs.
// Selector format: "key=value" or "key1=value1,key2=value2"
func (c *Context) LogsAll(selector string) (map[string]string, error) {
	return c.LogsAllWithOptions(selector, LogsOptions{})
}

// LogsAllWithOptions retrieves logs from all pods matching the selector with options.
// Returns a map of pod name to log contents.
//
// Error handling: Individual pod errors are stored in the result map as "[error: ...]"
// rather than failing the entire operation. This allows collecting logs from healthy pods
// even when some pods are failing. The function only returns an error for selector/listing
// failures. Check individual values for "[error" prefix to detect per-pod failures.
func (c *Context) LogsAllWithOptions(selector string, opts LogsOptions) (map[string]string, error) {
	// List pods matching selector
	pods, err := c.Client.CoreV1().Pods(c.Namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	result := make(map[string]string)

	for _, pod := range pods.Items {
		logOpts := &corev1.PodLogOptions{}
		if opts.Container != "" {
			logOpts.Container = opts.Container
		}
		if opts.Since > 0 {
			sinceSeconds := int64(opts.Since.Seconds())
			logOpts.SinceSeconds = &sinceSeconds
		}
		if opts.TailLines > 0 {
			logOpts.TailLines = &opts.TailLines
		}

		req := c.Client.CoreV1().Pods(c.Namespace).GetLogs(pod.Name, logOpts)
		stream, streamErr := req.Stream(context.Background())
		if streamErr != nil {
			// Store error message instead of failing entirely
			result[pod.Name] = fmt.Sprintf("[error: %v]", streamErr)
			continue
		}

		buf := new(strings.Builder)
		_, copyErr := io.Copy(buf, stream)
		_ = stream.Close()
		if copyErr != nil {
			result[pod.Name] = fmt.Sprintf("[error reading: %v]", copyErr)
			continue
		}

		result[pod.Name] = buf.String()
	}

	return result, nil
}

// ============================================================================
// Secret Helpers
// ============================================================================

// SecretFromFile creates a Secret from file contents.
// The files map keys become the secret data keys, values are file paths.
func (c *Context) SecretFromFile(name string, files map[string]string) error {
	data := make(map[string][]byte)
	for key, path := range files {
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("failed to read file %s: %w", path, readErr)
		}
		data[key] = content
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Data: data,
	}

	return c.Apply(secret)
}

// SecretFromEnv creates a Secret from environment variables.
// Each key is looked up in the current process environment.
func (c *Context) SecretFromEnv(name string, keys ...string) error {
	data := make(map[string][]byte)
	for _, key := range keys {
		value, ok := os.LookupEnv(key)
		if !ok {
			return fmt.Errorf("environment variable %s is not set", key)
		}
		data[key] = []byte(value)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Data: data,
	}

	return c.Apply(secret)
}

// SecretTLS creates a TLS Secret from certificate and key files.
func (c *Context) SecretTLS(name, certPath, keyPath string) error {
	certData, err := os.ReadFile(certPath)
	if err != nil {
		return fmt.Errorf("failed to read certificate: %w", err)
	}

	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("failed to read key: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			corev1.TLSCertKey:       certData,
			corev1.TLSPrivateKeyKey: keyData,
		},
	}

	return c.Apply(secret)
}

// ============================================================================
// PVC Helpers
// ============================================================================

// WaitPVCBound waits for a PVC to be bound.
// Resource format: "pvc/name" or just "name"
func (c *Context) WaitPVCBound(resource string) error {
	return c.WaitPVCBoundTimeout(resource, 60*time.Second)
}

// WaitPVCBoundTimeout waits for a PVC to be bound with custom timeout.
func (c *Context) WaitPVCBoundTimeout(resource string, timeout time.Duration) error {
	// Extract name (remove pvc/ prefix if present)
	name := resource
	if strings.HasPrefix(resource, "pvc/") {
		name = strings.TrimPrefix(resource, "pvc/")
	} else if strings.HasPrefix(resource, "persistentvolumeclaim/") {
		name = strings.TrimPrefix(resource, "persistentvolumeclaim/")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		pvc, getErr := c.Client.CoreV1().PersistentVolumeClaims(c.Namespace).Get(
			ctx, name, metav1.GetOptions{})
		if getErr != nil {
			if !apierrors.IsNotFound(getErr) {
				return getErr
			}
		} else if pvc.Status.Phase == corev1.ClaimBound {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for PVC %s to be bound", name)
		case <-ticker.C:
		}
	}
}

// getPVC is a helper that parses the resource string and fetches the PVC.
// Returns nil and sets a.err if the resource is invalid or not a PVC.
func (a *Assertion) getPVC(methodName string) *corev1.PersistentVolumeClaim {
	parts := strings.SplitN(a.resource, "/", 2)
	if len(parts) != 2 {
		a.err = fmt.Errorf("invalid resource format %q", a.resource)
		return nil
	}

	kind := strings.ToLower(parts[0])
	name := parts[1]

	if kind != "pvc" && kind != "persistentvolumeclaim" {
		a.err = fmt.Errorf("%s only works with PVCs, got %s", methodName, kind)
		return nil
	}

	pvc, err := a.ctx.Client.CoreV1().PersistentVolumeClaims(a.ctx.Namespace).Get(
		context.Background(), name, metav1.GetOptions{})
	if err != nil {
		a.err = err
		return nil
	}
	return pvc
}

// IsBound asserts the PVC is bound.
func (a *Assertion) IsBound() *Assertion {
	if a.err != nil {
		return a
	}

	pvc := a.getPVC("IsBound")
	if pvc == nil {
		return a
	}

	if pvc.Status.Phase != corev1.ClaimBound {
		a.err = fmt.Errorf("PVC %s is not bound (phase: %s)", pvc.Name, pvc.Status.Phase)
	}
	return a
}

// HasStorageClass asserts the PVC has the specified storage class.
func (a *Assertion) HasStorageClass(class string) *Assertion {
	if a.err != nil {
		return a
	}

	pvc := a.getPVC("HasStorageClass")
	if pvc == nil {
		return a
	}

	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != class {
		actual := "<nil>"
		if pvc.Spec.StorageClassName != nil {
			actual = *pvc.Spec.StorageClassName
		}
		a.err = fmt.Errorf("expected storage class %s, got %s", class, actual)
	}
	return a
}

// HasCapacity asserts the PVC has at least the specified capacity.
func (a *Assertion) HasCapacity(capacity string) *Assertion {
	if a.err != nil {
		return a
	}

	pvc := a.getPVC("HasCapacity")
	if pvc == nil {
		return a
	}

	expected := resource.MustParse(capacity)
	actual := pvc.Status.Capacity[corev1.ResourceStorage]

	if actual.Cmp(expected) < 0 {
		a.err = fmt.Errorf("expected capacity >= %s, got %s", capacity, actual.String())
	}
	return a
}

// ============================================================================
// RBAC Builder
// ============================================================================

// RBACBundle contains a ServiceAccount, Role, and RoleBinding.
type RBACBundle struct {
	ServiceAccount *corev1.ServiceAccount
	Role           *rbacv1.Role
	RoleBinding    *rbacv1.RoleBinding
}

// RBACBuilder provides a fluent API for building RBAC resources.
type RBACBuilder struct {
	name     string
	roleName string
	rules    []rbacv1.PolicyRule
}

// ServiceAccount creates a new RBACBuilder with the given name.
func ServiceAccount(name string) *RBACBuilder {
	return &RBACBuilder{
		name:     name,
		roleName: name + "-role",
		rules:    make([]rbacv1.PolicyRule, 0),
	}
}

// WithRole sets a custom role name.
func (r *RBACBuilder) WithRole(roleName string) *RBACBuilder {
	r.roleName = roleName
	return r
}

// apiGroupForResource returns the correct API group for a resource.
// Returns "" for core resources, or the appropriate group for others.
func apiGroupForResource(resource string) string {
	switch resource {
	// Core API group ("")
	case "pods", "services", "configmaps", "secrets", "persistentvolumeclaims",
		"serviceaccounts", "namespaces", "nodes", "events", "endpoints",
		"persistentvolumes", "replicationcontrollers", "resourcequotas", "limitranges":
		return ""
	// apps group
	case "deployments", "statefulsets", "daemonsets", "replicasets", "controllerrevisions":
		return "apps"
	// batch group
	case "jobs", "cronjobs":
		return "batch"
	// networking.k8s.io group
	case "ingresses", "networkpolicies", "ingressclasses":
		return "networking.k8s.io"
	// rbac.authorization.k8s.io group
	case "roles", "rolebindings", "clusterroles", "clusterrolebindings":
		return "rbac.authorization.k8s.io"
	// autoscaling group
	case "horizontalpodautoscalers":
		return "autoscaling"
	// policy group
	case "poddisruptionbudgets":
		return "policy"
	default:
		// Default to core for unknown resources
		return ""
	}
}

// addRule adds a policy rule with the correct API group for each resource.
func (r *RBACBuilder) addRule(verbs []string, resources ...string) {
	// Group resources by their API group
	byGroup := make(map[string][]string)
	for _, res := range resources {
		group := apiGroupForResource(res)
		byGroup[group] = append(byGroup[group], res)
	}

	// Create separate rules for each API group
	for group, groupResources := range byGroup {
		r.rules = append(r.rules, rbacv1.PolicyRule{
			APIGroups: []string{group},
			Resources: groupResources,
			Verbs:     verbs,
		})
	}
}

// CanGet adds get permission for the specified resources.
func (r *RBACBuilder) CanGet(resources ...string) *RBACBuilder {
	r.addRule([]string{"get"}, resources...)
	return r
}

// CanList adds list permission for the specified resources.
func (r *RBACBuilder) CanList(resources ...string) *RBACBuilder {
	r.addRule([]string{"list"}, resources...)
	return r
}

// CanWatch adds watch permission for the specified resources.
func (r *RBACBuilder) CanWatch(resources ...string) *RBACBuilder {
	r.addRule([]string{"watch"}, resources...)
	return r
}

// CanCreate adds create permission for the specified resources.
func (r *RBACBuilder) CanCreate(resources ...string) *RBACBuilder {
	r.addRule([]string{"create"}, resources...)
	return r
}

// CanUpdate adds update permission for the specified resources.
func (r *RBACBuilder) CanUpdate(resources ...string) *RBACBuilder {
	r.addRule([]string{"update"}, resources...)
	return r
}

// CanDelete adds delete permission for the specified resources.
func (r *RBACBuilder) CanDelete(resources ...string) *RBACBuilder {
	r.addRule([]string{"delete"}, resources...)
	return r
}

// Can adds custom verbs for the specified resources.
func (r *RBACBuilder) Can(verbs []string, resources ...string) *RBACBuilder {
	r.addRule(verbs, resources...)
	return r
}

// CanAll adds all permissions (get, list, watch, create, update, delete) for resources.
func (r *RBACBuilder) CanAll(resources ...string) *RBACBuilder {
	r.addRule([]string{"get", "list", "watch", "create", "update", "delete"}, resources...)
	return r
}

// ForAPIGroup adds a rule with explicit API group for custom resources.
// Use this for CRDs or resources not in the built-in mapping.
func (r *RBACBuilder) ForAPIGroup(apiGroup string, verbs []string, resources ...string) *RBACBuilder {
	r.rules = append(r.rules, rbacv1.PolicyRule{
		APIGroups: []string{apiGroup},
		Resources: resources,
		Verbs:     verbs,
	})
	return r
}

// Build creates the RBACBundle with ServiceAccount, Role, and RoleBinding.
func (r *RBACBuilder) Build() *RBACBundle {
	return &RBACBundle{
		ServiceAccount: &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name: r.name,
			},
		},
		Role: &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name: r.roleName,
			},
			Rules: r.rules,
		},
		RoleBinding: &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: r.name + "-binding",
			},
			Subjects: []rbacv1.Subject{
				{
					Kind: "ServiceAccount",
					Name: r.name,
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "Role",
				Name:     r.roleName,
			},
		},
	}
}

// ApplyRBAC applies all resources in the RBACBundle.
func (c *Context) ApplyRBAC(bundle *RBACBundle) error {
	if err := c.Apply(bundle.ServiceAccount); err != nil {
		return fmt.Errorf("failed to apply ServiceAccount: %w", err)
	}
	if err := c.Apply(bundle.Role); err != nil {
		return fmt.Errorf("failed to apply Role: %w", err)
	}
	if err := c.Apply(bundle.RoleBinding); err != nil {
		return fmt.Errorf("failed to apply RoleBinding: %w", err)
	}

	return nil
}

// ============================================================================
// Ingress Testing
// ============================================================================

// IngressTest provides fluent testing for Ingress resources.
type IngressTest struct {
	ctx  *Context
	name string
	host string
	path string
	err  error
}

// TestIngress creates an IngressTest for the given ingress name.
func (c *Context) TestIngress(name string) *IngressTest {
	return &IngressTest{
		ctx:  c,
		name: name,
	}
}

// Host sets the host to test.
func (i *IngressTest) Host(host string) *IngressTest {
	if i.err != nil {
		return i
	}
	i.host = host
	return i
}

// Path sets the path to test.
func (i *IngressTest) Path(path string) *IngressTest {
	if i.err != nil {
		return i
	}
	i.path = path
	return i
}

// ExpectBackend asserts the ingress routes to the expected backend.
// Backend format: "svc/name" or just "name"
func (i *IngressTest) ExpectBackend(backend string, port int) *IngressTest {
	if i.err != nil {
		return i
	}

	// Get ingress
	ing, err := i.ctx.Client.NetworkingV1().Ingresses(i.ctx.Namespace).Get(
		context.Background(), i.name, metav1.GetOptions{})
	if err != nil {
		i.err = fmt.Errorf("failed to get ingress %s: %w", i.name, err)
		return i
	}

	// Parse expected backend
	expectedSvc := backend
	if strings.HasPrefix(backend, "svc/") {
		expectedSvc = strings.TrimPrefix(backend, "svc/")
	}

	// Find matching rule
	found := false
	for _, rule := range ing.Spec.Rules {
		// Check host match (empty host in test means match any)
		if i.host != "" && rule.Host != i.host {
			continue
		}

		if rule.HTTP == nil {
			continue
		}

		for _, p := range rule.HTTP.Paths {
			// Check path match (empty path in test means match any)
			pathMatches := i.path == "" || p.Path == i.path || strings.HasPrefix(i.path, p.Path)

			if pathMatches {
				if p.Backend.Service != nil {
					actualSvc := p.Backend.Service.Name
					actualPort := 0
					if p.Backend.Service.Port.Number != 0 {
						actualPort = int(p.Backend.Service.Port.Number)
					}

					if actualSvc == expectedSvc && actualPort == port {
						found = true
						break
					}
				}
			}
		}
		if found {
			break
		}
	}

	if !found {
		i.err = fmt.Errorf("ingress %s does not route host=%q path=%q to %s:%d",
			i.name, i.host, i.path, expectedSvc, port)
	}

	return i
}

// ExpectTLS asserts the ingress has TLS configured for the host.
func (i *IngressTest) ExpectTLS(secretName string) *IngressTest {
	if i.err != nil {
		return i
	}

	ing, err := i.ctx.Client.NetworkingV1().Ingresses(i.ctx.Namespace).Get(
		context.Background(), i.name, metav1.GetOptions{})
	if err != nil {
		i.err = fmt.Errorf("failed to get ingress %s: %w", i.name, err)
		return i
	}

	found := false
	for _, tls := range ing.Spec.TLS {
		if tls.SecretName == secretName {
			// Check if host matches (if specified)
			if i.host == "" {
				found = true
				break
			}
			for _, host := range tls.Hosts {
				if host == i.host {
					found = true
					break
				}
			}
		}
		if found {
			break
		}
	}

	if !found {
		i.err = fmt.Errorf("ingress %s does not have TLS secret %s for host %q",
			i.name, secretName, i.host)
	}

	return i
}

// Error returns any error from the test chain.
func (i *IngressTest) Error() error {
	return i.err
}

// Must panics if there was an error in the test chain.
func (i *IngressTest) Must() {
	if i.err != nil {
		panic(fmt.Sprintf("IngressTest failed: %v", i.err))
	}
}

// ============================================================================
// Network Policy
// ============================================================================

// Isolate creates a NetworkPolicy that denies all ingress/egress to pods
// matching the given selector.
func (c *Context) Isolate(selector map[string]string) error {
	policy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("ilmari-isolate-%s", labelHash(selector)),
			Namespace: c.Namespace,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: selector,
			},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
				networkingv1.PolicyTypeEgress,
			},
			// Empty ingress/egress means deny all
		},
	}

	_, err := c.Client.NetworkingV1().NetworkPolicies(c.Namespace).Create(
		context.Background(), policy, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create NetworkPolicy: %w", err)
	}
	return nil
}

// AllowFrom creates a NetworkPolicy that allows traffic from source pods
// to target pods.
func (c *Context) AllowFrom(target, source map[string]string) error {
	policy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("ilmari-allow-%s-from-%s", labelHash(target), labelHash(source)),
			Namespace: c.Namespace,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: target,
			},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
			},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					From: []networkingv1.NetworkPolicyPeer{
						{
							PodSelector: &metav1.LabelSelector{
								MatchLabels: source,
							},
						},
					},
				},
			},
		},
	}

	_, err := c.Client.NetworkingV1().NetworkPolicies(c.Namespace).Create(
		context.Background(), policy, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create NetworkPolicy: %w", err)
	}
	return nil
}

// labelHash creates a short hash from label selectors for naming.
func labelHash(labels map[string]string) string {
	var parts []string
	for k, v := range labels {
		parts = append(parts, k+v)
	}
	// Simple hash - just use first 8 chars of joined string
	joined := strings.Join(parts, "")
	if len(joined) > 8 {
		joined = joined[:8]
	}
	return joined
}
