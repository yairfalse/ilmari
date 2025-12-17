package ilmari

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/yaml"
	"time"
)

// ============================================================================
// Stack Builder
// ============================================================================

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

// Resources sets resource limits and requests for the ServiceBuilder.
// Both limits and requests are set to the same values for simplicity.
func (sb *ServiceBuilder) Resources(cpu, memory string) *ServiceBuilder {
	sb.resourceLimits = corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(cpu),
		corev1.ResourceMemory: resource.MustParse(memory),
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

// ============================================================================
// Retry & Kill
// ============================================================================

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

// ============================================================================
// YAML Loading
// ============================================================================

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
	// Sort env keys for deterministic output
	keys := make([]string, 0, len(d.env))
	for k := range d.env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		envVars = append(envVars, corev1.EnvVar{Name: k, Value: d.env[k]})
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

// Build returns the Deployment, or an error if one occurred during building.
func (f *FixtureBuilder) Build() (*appsv1.Deployment, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.deploy, nil
}
