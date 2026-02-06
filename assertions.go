package ilmari

import (
	"context"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ============================================================================
// Helpers to reduce assertion duplication
// ============================================================================

// objectLabels extracts labels from any Kubernetes object that implements ObjectMetaAccessor.
func objectLabels(obj interface{}) (map[string]string, error) {
	accessor, ok := obj.(metav1.ObjectMetaAccessor)
	if !ok {
		return nil, fmt.Errorf("object %T does not support metadata access", obj)
	}
	return accessor.GetObjectMeta().GetLabels(), nil
}

// objectAnnotations extracts annotations from any Kubernetes object that implements ObjectMetaAccessor.
func objectAnnotations(obj interface{}) (map[string]string, error) {
	accessor, ok := obj.(metav1.ObjectMetaAccessor)
	if !ok {
		return nil, fmt.Errorf("object %T does not support metadata access", obj)
	}
	return accessor.GetObjectMeta().GetAnnotations(), nil
}

// checkLabel checks that labels contain the expected key=value pair.
func checkLabel(labels map[string]string, key, value, resourceDesc string) error {
	if labels[key] != value {
		return fmt.Errorf("%s: expected label %s=%s, got %s=%s", resourceDesc, key, value, key, labels[key])
	}
	return nil
}

// checkAnnotation checks that annotations contain the expected key=value pair.
func checkAnnotation(annotations map[string]string, key, value, resourceDesc string) error {
	if annotations[key] != value {
		return fmt.Errorf("%s: expected annotation %s=%s, got %s=%s", resourceDesc, key, value, key, annotations[key])
	}
	return nil
}

// checkPodNoRestarts checks that all container statuses have zero restarts.
func checkPodNoRestarts(pod *corev1.Pod, desc string) error {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.RestartCount > 0 {
			return fmt.Errorf("%s: container %s has %d restarts", desc, cs.Name, cs.RestartCount)
		}
	}
	return nil
}

// checkPodNoOOMKills checks that no containers were OOM-killed.
func checkPodNoOOMKills(pod *corev1.Pod, desc string) error {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.LastTerminationState.Terminated != nil &&
			cs.LastTerminationState.Terminated.Reason == "OOMKilled" {
			return fmt.Errorf("%s: container %s was OOMKilled", desc, cs.Name)
		}
		if cs.State.Terminated != nil && cs.State.Terminated.Reason == "OOMKilled" {
			return fmt.Errorf("%s: container %s was OOMKilled", desc, cs.Name)
		}
	}
	return nil
}

// checkDeploymentProgressing checks that the deployment has a Progressing=True condition.
func checkDeploymentProgressing(deploy *appsv1.Deployment, desc string) error {
	for _, cond := range deploy.Status.Conditions {
		if cond.Type == appsv1.DeploymentProgressing {
			if cond.Status == corev1.ConditionTrue {
				return nil
			}
			return fmt.Errorf("%s not progressing: %s", desc, cond.Message)
		}
	}
	return fmt.Errorf("%s has no Progressing condition", desc)
}

// ============================================================================
// Generic Assertion (uses "kind/name" resource references)
// ============================================================================

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

	ref, err := ParseResourceRef(a.resource)
	if err != nil {
		a.err = err
		return a
	}

	obj, err := a.ctx.getResource(ref.Kind, ref.Name)
	if err != nil {
		a.err = err
		return a
	}
	if obj == nil {
		a.err = fmt.Errorf("resource %s not found", a.resource)
		return a
	}

	labels, err := objectLabels(obj)
	if err != nil {
		a.err = fmt.Errorf("HasLabel: %w", err)
		return a
	}
	a.err = checkLabel(labels, key, value, a.resource)
	return a
}

// HasAnnotation asserts the resource has the given annotation.
func (a *Assertion) HasAnnotation(key, value string) *Assertion {
	if a.err != nil {
		return a
	}

	ref, err := ParseResourceRef(a.resource)
	if err != nil {
		a.err = err
		return a
	}

	obj, err := a.ctx.getResource(ref.Kind, ref.Name)
	if err != nil {
		a.err = err
		return a
	}
	if obj == nil {
		a.err = fmt.Errorf("resource %s not found", a.resource)
		return a
	}

	annotations, err := objectAnnotations(obj)
	if err != nil {
		a.err = fmt.Errorf("HasAnnotation: %w", err)
		return a
	}
	a.err = checkAnnotation(annotations, key, value, a.resource)
	return a
}

// IsReady asserts the resource is ready.
func (a *Assertion) IsReady() *Assertion {
	if a.err != nil {
		return a
	}

	ref, err := ParseResourceRef(a.resource)
	if err != nil {
		a.err = err
		return a
	}

	ready, err := a.ctx.isReady(ref.Kind, ref.Name)
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

	ref, err := ParseResourceRef(a.resource)
	if err != nil {
		a.err = err
		return a
	}

	obj, err := a.ctx.getResource(ref.Kind, ref.Name)
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

	ref, err := ParseResourceRef(a.resource)
	if err != nil {
		a.err = err
		return a
	}

	kind := ref.Kind
	name := ref.Name
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

	ref, err := ParseResourceRef(a.resource)
	if err != nil {
		a.err = err
		return a
	}

	if ref.Kind != "deployment" {
		a.err = fmt.Errorf("IsProgressing: only supports deployments, got %s", ref.Kind)
		return a
	}

	deploy, err := a.ctx.Client.AppsV1().Deployments(a.ctx.Namespace).Get(context.Background(), ref.Name, metav1.GetOptions{})
	if err != nil {
		a.err = err
		return a
	}

	a.err = checkDeploymentProgressing(deploy, "deployment")
	return a
}

// HasNoRestarts asserts the pod's containers have zero restarts.
func (a *Assertion) HasNoRestarts() *Assertion {
	if a.err != nil {
		return a
	}

	ref, err := ParseResourceRef(a.resource)
	if err != nil {
		a.err = err
		return a
	}

	if ref.Kind != "pod" {
		a.err = fmt.Errorf("HasNoRestarts: only supports pods, got %s", ref.Kind)
		return a
	}

	pod, err := a.ctx.Client.CoreV1().Pods(a.ctx.Namespace).Get(context.Background(), ref.Name, metav1.GetOptions{})
	if err != nil {
		a.err = err
		return a
	}

	a.err = checkPodNoRestarts(pod, a.resource)
	return a
}

// LogsContain asserts the pod's logs contain the specified text.
func (a *Assertion) LogsContain(text string) *Assertion {
	if a.err != nil {
		return a
	}

	ref, err := ParseResourceRef(a.resource)
	if err != nil {
		a.err = err
		return a
	}

	if ref.Kind != "pod" {
		a.err = fmt.Errorf("LogsContain: only supports pods, got %s", ref.Kind)
		return a
	}

	logs, err := a.ctx.Logs(ref.Name)
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

	ref, err := ParseResourceRef(a.resource)
	if err != nil {
		a.err = err
		return a
	}

	if ref.Kind != "pod" {
		a.err = fmt.Errorf("NoOOMKills: only supports pods, got %s", ref.Kind)
		return a
	}

	pod, err := a.ctx.Client.CoreV1().Pods(a.ctx.Namespace).Get(context.Background(), ref.Name, metav1.GetOptions{})
	if err != nil {
		a.err = err
		return a
	}

	a.err = checkPodNoOOMKills(pod, a.resource)
	return a
}

// ============================================================================
// Typed Assertions - PVC
// ============================================================================

// PVCAssertion provides fluent assertions for PersistentVolumeClaims.
type PVCAssertion struct {
	ctx  *Context
	name string
	err  error
}

// AssertPVC returns a typed assertion builder for a PVC.
func (c *Context) AssertPVC(name string) *PVCAssertion {
	return &PVCAssertion{ctx: c, name: name}
}

func (a *PVCAssertion) getPVC() (*corev1.PersistentVolumeClaim, error) {
	return a.ctx.Client.CoreV1().PersistentVolumeClaims(a.ctx.Namespace).Get(
		context.Background(), a.name, metav1.GetOptions{})
}

// Exists asserts the PVC exists.
func (a *PVCAssertion) Exists() *PVCAssertion {
	if a.err != nil {
		return a
	}
	if _, err := a.getPVC(); err != nil {
		a.err = fmt.Errorf("PVC %s does not exist: %w", a.name, err)
	}
	return a
}

// IsBound asserts the PVC is bound to a volume.
func (a *PVCAssertion) IsBound() *PVCAssertion {
	if a.err != nil {
		return a
	}
	pvc, err := a.getPVC()
	if err != nil {
		a.err = err
		return a
	}
	if pvc.Status.Phase != corev1.ClaimBound {
		a.err = fmt.Errorf("PVC %s is %s, expected Bound", a.name, pvc.Status.Phase)
	}
	return a
}

// HasStorageClass asserts the PVC uses the specified storage class.
func (a *PVCAssertion) HasStorageClass(expected string) *PVCAssertion {
	if a.err != nil {
		return a
	}
	pvc, err := a.getPVC()
	if err != nil {
		a.err = err
		return a
	}
	actual := ""
	if pvc.Spec.StorageClassName != nil {
		actual = *pvc.Spec.StorageClassName
	}
	if actual != expected {
		a.err = fmt.Errorf("PVC %s has storage class %q, expected %q", a.name, actual, expected)
	}
	return a
}

// HasCapacity asserts the PVC has at least the specified capacity.
func (a *PVCAssertion) HasCapacity(expected string) *PVCAssertion {
	if a.err != nil {
		return a
	}
	pvc, err := a.getPVC()
	if err != nil {
		a.err = err
		return a
	}
	capacity := pvc.Status.Capacity.Storage()
	if capacity == nil {
		a.err = fmt.Errorf("PVC %s has no capacity set", a.name)
		return a
	}
	expectedQty, err := resource.ParseQuantity(expected)
	if err != nil {
		a.err = fmt.Errorf("invalid capacity %q: %w", expected, err)
		return a
	}
	if capacity.Cmp(expectedQty) < 0 {
		a.err = fmt.Errorf("PVC %s has capacity %s, expected at least %s", a.name, capacity.String(), expected)
	}
	return a
}

// HasLabel asserts the PVC has the specified label.
func (a *PVCAssertion) HasLabel(key, value string) *PVCAssertion {
	if a.err != nil {
		return a
	}
	pvc, err := a.getPVC()
	if err != nil {
		a.err = err
		return a
	}
	a.err = checkLabel(pvc.Labels, key, value, fmt.Sprintf("PVC %s", a.name))
	return a
}

// HasAnnotation asserts the PVC has the specified annotation.
func (a *PVCAssertion) HasAnnotation(key, value string) *PVCAssertion {
	if a.err != nil {
		return a
	}
	pvc, err := a.getPVC()
	if err != nil {
		a.err = err
		return a
	}
	a.err = checkAnnotation(pvc.Annotations, key, value, fmt.Sprintf("PVC %s", a.name))
	return a
}

// Error returns any assertion error.
func (a *PVCAssertion) Error() error {
	return a.err
}

// Must panics if any assertion failed.
func (a *PVCAssertion) Must() {
	if a.err != nil {
		panic(a.err)
	}
}

// ============================================================================
// Typed Assertions - Pod
// ============================================================================

// PodAssertion provides fluent assertions for Pods.
type PodAssertion struct {
	ctx  *Context
	name string
	err  error
}

// AssertPod returns a typed assertion builder for a Pod.
func (c *Context) AssertPod(name string) *PodAssertion {
	return &PodAssertion{ctx: c, name: name}
}

func (a *PodAssertion) getPod() (*corev1.Pod, error) {
	return a.ctx.Client.CoreV1().Pods(a.ctx.Namespace).Get(
		context.Background(), a.name, metav1.GetOptions{})
}

// Exists asserts the Pod exists.
func (a *PodAssertion) Exists() *PodAssertion {
	if a.err != nil {
		return a
	}
	if _, err := a.getPod(); err != nil {
		a.err = fmt.Errorf("pod %s does not exist: %w", a.name, err)
	}
	return a
}

// IsReady asserts the Pod is ready.
func (a *PodAssertion) IsReady() *PodAssertion {
	if a.err != nil {
		return a
	}
	pod, err := a.getPod()
	if err != nil {
		a.err = err
		return a
	}
	if !isPodReady(pod) {
		a.err = fmt.Errorf("pod %s is not ready", a.name)
	}
	return a
}

// HasNoRestarts asserts the Pod's containers have zero restarts.
func (a *PodAssertion) HasNoRestarts() *PodAssertion {
	if a.err != nil {
		return a
	}
	pod, err := a.getPod()
	if err != nil {
		a.err = err
		return a
	}
	a.err = checkPodNoRestarts(pod, fmt.Sprintf("pod %s", a.name))
	return a
}

// NoOOMKills asserts the Pod has no OOM-killed containers.
func (a *PodAssertion) NoOOMKills() *PodAssertion {
	if a.err != nil {
		return a
	}
	pod, err := a.getPod()
	if err != nil {
		a.err = err
		return a
	}
	a.err = checkPodNoOOMKills(pod, fmt.Sprintf("pod %s", a.name))
	return a
}

// LogsContain asserts the Pod's logs contain the specified text.
func (a *PodAssertion) LogsContain(text string) *PodAssertion {
	if a.err != nil {
		return a
	}
	logs, err := a.ctx.Logs(a.name)
	if err != nil {
		a.err = err
		return a
	}
	if !strings.Contains(logs, text) {
		a.err = fmt.Errorf("pod %s: logs do not contain %q", a.name, text)
	}
	return a
}

// HasLabel asserts the Pod has the specified label.
func (a *PodAssertion) HasLabel(key, value string) *PodAssertion {
	if a.err != nil {
		return a
	}
	pod, err := a.getPod()
	if err != nil {
		a.err = err
		return a
	}
	a.err = checkLabel(pod.Labels, key, value, fmt.Sprintf("pod %s", a.name))
	return a
}

// HasAnnotation asserts the Pod has the specified annotation.
func (a *PodAssertion) HasAnnotation(key, value string) *PodAssertion {
	if a.err != nil {
		return a
	}
	pod, err := a.getPod()
	if err != nil {
		a.err = err
		return a
	}
	a.err = checkAnnotation(pod.Annotations, key, value, fmt.Sprintf("pod %s", a.name))
	return a
}

// Error returns any assertion error.
func (a *PodAssertion) Error() error {
	return a.err
}

// Must panics if any assertion failed.
func (a *PodAssertion) Must() {
	if a.err != nil {
		panic(a.err)
	}
}

// ============================================================================
// Typed Assertions - Deployment
// ============================================================================

// DeploymentAssertion provides fluent assertions for Deployments.
type DeploymentAssertion struct {
	ctx  *Context
	name string
	err  error
}

// AssertDeployment returns a typed assertion builder for a Deployment.
func (c *Context) AssertDeployment(name string) *DeploymentAssertion {
	return &DeploymentAssertion{ctx: c, name: name}
}

func (a *DeploymentAssertion) getDeployment() (*appsv1.Deployment, error) {
	return a.ctx.Client.AppsV1().Deployments(a.ctx.Namespace).Get(
		context.Background(), a.name, metav1.GetOptions{})
}

// Exists asserts the Deployment exists.
func (a *DeploymentAssertion) Exists() *DeploymentAssertion {
	if a.err != nil {
		return a
	}
	if _, err := a.getDeployment(); err != nil {
		a.err = fmt.Errorf("Deployment %s does not exist: %w", a.name, err)
	}
	return a
}

// HasReplicas asserts the Deployment has the specified ready replicas.
func (a *DeploymentAssertion) HasReplicas(expected int) *DeploymentAssertion {
	if a.err != nil {
		return a
	}
	deploy, err := a.getDeployment()
	if err != nil {
		a.err = err
		return a
	}
	if int(deploy.Status.ReadyReplicas) != expected {
		a.err = fmt.Errorf("Deployment %s: expected %d ready replicas, got %d",
			a.name, expected, deploy.Status.ReadyReplicas)
	}
	return a
}

// IsReady asserts the Deployment is fully available.
func (a *DeploymentAssertion) IsReady() *DeploymentAssertion {
	if a.err != nil {
		return a
	}
	deploy, err := a.getDeployment()
	if err != nil {
		a.err = err
		return a
	}
	desired := int32(1)
	if deploy.Spec.Replicas != nil {
		desired = *deploy.Spec.Replicas
	}
	if deploy.Status.ReadyReplicas != desired {
		a.err = fmt.Errorf("Deployment %s: %d/%d replicas ready", a.name, deploy.Status.ReadyReplicas, desired)
	}
	return a
}

// IsProgressing asserts the Deployment is progressing (not stalled).
func (a *DeploymentAssertion) IsProgressing() *DeploymentAssertion {
	if a.err != nil {
		return a
	}
	deploy, err := a.getDeployment()
	if err != nil {
		a.err = err
		return a
	}
	a.err = checkDeploymentProgressing(deploy, fmt.Sprintf("Deployment %s", a.name))
	return a
}

// HasLabel asserts the Deployment has the specified label.
func (a *DeploymentAssertion) HasLabel(key, value string) *DeploymentAssertion {
	if a.err != nil {
		return a
	}
	deploy, err := a.getDeployment()
	if err != nil {
		a.err = err
		return a
	}
	a.err = checkLabel(deploy.Labels, key, value, fmt.Sprintf("Deployment %s", a.name))
	return a
}

// HasAnnotation asserts the Deployment has the specified annotation.
func (a *DeploymentAssertion) HasAnnotation(key, value string) *DeploymentAssertion {
	if a.err != nil {
		return a
	}
	deploy, err := a.getDeployment()
	if err != nil {
		a.err = err
		return a
	}
	a.err = checkAnnotation(deploy.Annotations, key, value, fmt.Sprintf("Deployment %s", a.name))
	return a
}

// Error returns any assertion error.
func (a *DeploymentAssertion) Error() error {
	return a.err
}

// Must panics if any assertion failed.
func (a *DeploymentAssertion) Must() {
	if a.err != nil {
		panic(a.err)
	}
}

// ============================================================================
// Typed Assertions - Service
// ============================================================================

// ServiceAssertion provides fluent assertions for Services.
type ServiceAssertion struct {
	ctx  *Context
	name string
	err  error
}

// AssertService returns a typed assertion builder for a Service.
func (c *Context) AssertService(name string) *ServiceAssertion {
	return &ServiceAssertion{ctx: c, name: name}
}

func (a *ServiceAssertion) getService() (*corev1.Service, error) {
	return a.ctx.Client.CoreV1().Services(a.ctx.Namespace).Get(
		context.Background(), a.name, metav1.GetOptions{})
}

// Exists asserts the Service exists.
func (a *ServiceAssertion) Exists() *ServiceAssertion {
	if a.err != nil {
		return a
	}
	if _, err := a.getService(); err != nil {
		a.err = fmt.Errorf("Service %s does not exist: %w", a.name, err)
	}
	return a
}

// HasLabel asserts the Service has the specified label.
func (a *ServiceAssertion) HasLabel(key, value string) *ServiceAssertion {
	if a.err != nil {
		return a
	}
	svc, err := a.getService()
	if err != nil {
		a.err = err
		return a
	}
	a.err = checkLabel(svc.Labels, key, value, fmt.Sprintf("Service %s", a.name))
	return a
}

// HasAnnotation asserts the Service has the specified annotation.
func (a *ServiceAssertion) HasAnnotation(key, value string) *ServiceAssertion {
	if a.err != nil {
		return a
	}
	svc, err := a.getService()
	if err != nil {
		a.err = err
		return a
	}
	a.err = checkAnnotation(svc.Annotations, key, value, fmt.Sprintf("Service %s", a.name))
	return a
}

// HasPort asserts the Service exposes the specified port.
func (a *ServiceAssertion) HasPort(port int32) *ServiceAssertion {
	if a.err != nil {
		return a
	}
	svc, err := a.getService()
	if err != nil {
		a.err = err
		return a
	}
	for _, p := range svc.Spec.Ports {
		if p.Port == port {
			return a
		}
	}
	a.err = fmt.Errorf("Service %s does not expose port %d", a.name, port)
	return a
}

// HasSelector asserts the Service has the specified selector.
func (a *ServiceAssertion) HasSelector(key, value string) *ServiceAssertion {
	if a.err != nil {
		return a
	}
	svc, err := a.getService()
	if err != nil {
		a.err = err
		return a
	}
	if svc.Spec.Selector[key] != value {
		a.err = fmt.Errorf("Service %s: expected selector %s=%s, got %s=%s",
			a.name, key, value, key, svc.Spec.Selector[key])
	}
	return a
}

// Error returns any assertion error.
func (a *ServiceAssertion) Error() error {
	return a.err
}

// Must panics if any assertion failed.
func (a *ServiceAssertion) Must() {
	if a.err != nil {
		panic(a.err)
	}
}

// ============================================================================
// Typed Assertions - StatefulSet
// ============================================================================

// StatefulSetAssertion provides fluent assertions for StatefulSets.
type StatefulSetAssertion struct {
	ctx  *Context
	name string
	err  error
}

// AssertStatefulSet returns a typed assertion builder for a StatefulSet.
func (c *Context) AssertStatefulSet(name string) *StatefulSetAssertion {
	return &StatefulSetAssertion{ctx: c, name: name}
}

func (a *StatefulSetAssertion) getStatefulSet() (*appsv1.StatefulSet, error) {
	return a.ctx.Client.AppsV1().StatefulSets(a.ctx.Namespace).Get(
		context.Background(), a.name, metav1.GetOptions{})
}

// Exists asserts the StatefulSet exists.
func (a *StatefulSetAssertion) Exists() *StatefulSetAssertion {
	if a.err != nil {
		return a
	}
	if _, err := a.getStatefulSet(); err != nil {
		a.err = fmt.Errorf("StatefulSet %s does not exist: %w", a.name, err)
	}
	return a
}

// HasReplicas asserts the StatefulSet has the specified ready replicas.
func (a *StatefulSetAssertion) HasReplicas(expected int) *StatefulSetAssertion {
	if a.err != nil {
		return a
	}
	ss, err := a.getStatefulSet()
	if err != nil {
		a.err = err
		return a
	}
	if int(ss.Status.ReadyReplicas) != expected {
		a.err = fmt.Errorf("StatefulSet %s: expected %d ready replicas, got %d",
			a.name, expected, ss.Status.ReadyReplicas)
	}
	return a
}

// IsReady asserts the StatefulSet is fully available.
func (a *StatefulSetAssertion) IsReady() *StatefulSetAssertion {
	if a.err != nil {
		return a
	}
	ss, err := a.getStatefulSet()
	if err != nil {
		a.err = err
		return a
	}
	desired := int32(1)
	if ss.Spec.Replicas != nil {
		desired = *ss.Spec.Replicas
	}
	if ss.Status.ReadyReplicas != desired {
		a.err = fmt.Errorf("StatefulSet %s: %d/%d replicas ready", a.name, ss.Status.ReadyReplicas, desired)
	}
	return a
}

// HasLabel asserts the StatefulSet has the specified label.
func (a *StatefulSetAssertion) HasLabel(key, value string) *StatefulSetAssertion {
	if a.err != nil {
		return a
	}
	ss, err := a.getStatefulSet()
	if err != nil {
		a.err = err
		return a
	}
	a.err = checkLabel(ss.Labels, key, value, fmt.Sprintf("StatefulSet %s", a.name))
	return a
}

// HasAnnotation asserts the StatefulSet has the specified annotation.
func (a *StatefulSetAssertion) HasAnnotation(key, value string) *StatefulSetAssertion {
	if a.err != nil {
		return a
	}
	ss, err := a.getStatefulSet()
	if err != nil {
		a.err = err
		return a
	}
	a.err = checkAnnotation(ss.Annotations, key, value, fmt.Sprintf("StatefulSet %s", a.name))
	return a
}

// Error returns any assertion error.
func (a *StatefulSetAssertion) Error() error {
	return a.err
}

// Must panics if any assertion failed.
func (a *StatefulSetAssertion) Must() {
	if a.err != nil {
		panic(a.err)
	}
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
		timeout:  c.waitTimeout,
		interval: c.pollInterval,
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
			return fmt.Errorf("condition not met within %v", e.timeout)
		}

		<-ticker.C
		if e.fn() {
			return nil
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
		duration: 5 * time.Second,
		interval: c.pollInterval,
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
	deadline := time.Now().Add(c.duration)
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	// Check immediately
	if !c.fn() {
		return fmt.Errorf("condition was false at start")
	}

	for range ticker.C {
		if !c.fn() {
			return fmt.Errorf("condition became false after %v", time.Since(deadline.Add(-c.duration)))
		}
		if time.Now().After(deadline) {
			return nil // success - condition stayed true
		}
	}
	return nil
}
