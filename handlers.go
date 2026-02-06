package ilmari

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
)

// resourceHandler provides polymorphic operations for a Kubernetes resource kind.
// Implementations handle kind-specific API calls, replacing scattered switch statements.
type resourceHandler interface {
	// Get fetches a resource by name. Returns (nil, nil) if not found.
	Get(ctx context.Context, client kubernetes.Interface, ns, name string) (interface{}, error)

	// IsReady checks whether the resource is in a ready state.
	IsReady(obj interface{}) bool

	// Watch starts watching resources of this kind.
	Watch(ctx context.Context, client kubernetes.Interface, ns string) (watch.Interface, error)

	// Exists checks if a resource exists by name.
	Exists(ctx context.Context, client kubernetes.Interface, ns, name string) (bool, error)

	// Delete removes a resource by name.
	Delete(ctx context.Context, client kubernetes.Interface, ns, name string) error
}

// handlers maps kind names to their handler implementations.
var handlers = map[string]resourceHandler{
	"pod":                    podHandler{},
	"deployment":             deploymentHandler{},
	"statefulset":            statefulSetHandler{},
	"daemonset":              daemonSetHandler{},
	"configmap":              configMapHandler{},
	"secret":                 secretHandler{},
	"service":                serviceHandler{},
	"pvc":                    pvcHandler{},
	"persistentvolumeclaim":  pvcHandler{},
}

// lookupHandler returns the handler for a kind, or nil if not found.
func lookupHandler(kind string) resourceHandler {
	return handlers[kind]
}

// ============================================================================
// Pod
// ============================================================================

type podHandler struct{}

func (h podHandler) Get(ctx context.Context, client kubernetes.Interface, ns, name string) (interface{}, error) {
	pod, err := client.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	return pod, err
}

func (h podHandler) IsReady(obj interface{}) bool {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return false
	}
	return isPodReady(pod)
}

func (h podHandler) Watch(ctx context.Context, client kubernetes.Interface, ns string) (watch.Interface, error) {
	return client.CoreV1().Pods(ns).Watch(ctx, metav1.ListOptions{})
}

func (h podHandler) Exists(ctx context.Context, client kubernetes.Interface, ns, name string) (bool, error) {
	_, err := client.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

func (h podHandler) Delete(ctx context.Context, client kubernetes.Interface, ns, name string) error {
	return client.CoreV1().Pods(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

// ============================================================================
// Deployment
// ============================================================================

type deploymentHandler struct{}

func (h deploymentHandler) Get(ctx context.Context, client kubernetes.Interface, ns, name string) (interface{}, error) {
	deploy, err := client.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	return deploy, err
}

func (h deploymentHandler) IsReady(obj interface{}) bool {
	deploy, ok := obj.(*appsv1.Deployment)
	if !ok {
		return false
	}
	return isDeploymentReady(deploy)
}

func (h deploymentHandler) Watch(ctx context.Context, client kubernetes.Interface, ns string) (watch.Interface, error) {
	return client.AppsV1().Deployments(ns).Watch(ctx, metav1.ListOptions{})
}

func (h deploymentHandler) Exists(ctx context.Context, client kubernetes.Interface, ns, name string) (bool, error) {
	_, err := client.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

func (h deploymentHandler) Delete(ctx context.Context, client kubernetes.Interface, ns, name string) error {
	return client.AppsV1().Deployments(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

// ============================================================================
// StatefulSet
// ============================================================================

type statefulSetHandler struct{}

func (h statefulSetHandler) Get(ctx context.Context, client kubernetes.Interface, ns, name string) (interface{}, error) {
	ss, err := client.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	return ss, err
}

func (h statefulSetHandler) IsReady(obj interface{}) bool {
	ss, ok := obj.(*appsv1.StatefulSet)
	if !ok {
		return false
	}
	return isStatefulSetReady(ss)
}

func (h statefulSetHandler) Watch(ctx context.Context, client kubernetes.Interface, ns string) (watch.Interface, error) {
	return client.AppsV1().StatefulSets(ns).Watch(ctx, metav1.ListOptions{})
}

func (h statefulSetHandler) Exists(ctx context.Context, client kubernetes.Interface, ns, name string) (bool, error) {
	_, err := client.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

func (h statefulSetHandler) Delete(ctx context.Context, client kubernetes.Interface, ns, name string) error {
	return client.AppsV1().StatefulSets(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

// ============================================================================
// DaemonSet
// ============================================================================

type daemonSetHandler struct{}

func (h daemonSetHandler) Get(ctx context.Context, client kubernetes.Interface, ns, name string) (interface{}, error) {
	ds, err := client.AppsV1().DaemonSets(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	return ds, err
}

func (h daemonSetHandler) IsReady(obj interface{}) bool {
	ds, ok := obj.(*appsv1.DaemonSet)
	if !ok {
		return false
	}
	return isDaemonSetReady(ds)
}

func (h daemonSetHandler) Watch(ctx context.Context, client kubernetes.Interface, ns string) (watch.Interface, error) {
	return client.AppsV1().DaemonSets(ns).Watch(ctx, metav1.ListOptions{})
}

func (h daemonSetHandler) Exists(ctx context.Context, client kubernetes.Interface, ns, name string) (bool, error) {
	_, err := client.AppsV1().DaemonSets(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

func (h daemonSetHandler) Delete(ctx context.Context, client kubernetes.Interface, ns, name string) error {
	return client.AppsV1().DaemonSets(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

// ============================================================================
// ConfigMap
// ============================================================================

type configMapHandler struct{}

func (h configMapHandler) Get(ctx context.Context, client kubernetes.Interface, ns, name string) (interface{}, error) {
	cm, err := client.CoreV1().ConfigMaps(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	return cm, err
}

func (h configMapHandler) IsReady(_ interface{}) bool {
	return true // ConfigMaps are always ready once they exist
}

func (h configMapHandler) Watch(ctx context.Context, client kubernetes.Interface, ns string) (watch.Interface, error) {
	return client.CoreV1().ConfigMaps(ns).Watch(ctx, metav1.ListOptions{})
}

func (h configMapHandler) Exists(ctx context.Context, client kubernetes.Interface, ns, name string) (bool, error) {
	_, err := client.CoreV1().ConfigMaps(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

func (h configMapHandler) Delete(ctx context.Context, client kubernetes.Interface, ns, name string) error {
	return client.CoreV1().ConfigMaps(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

// ============================================================================
// Secret
// ============================================================================

type secretHandler struct{}

func (h secretHandler) Get(ctx context.Context, client kubernetes.Interface, ns, name string) (interface{}, error) {
	secret, err := client.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	return secret, err
}

func (h secretHandler) IsReady(_ interface{}) bool {
	return true // Secrets are always ready once they exist
}

func (h secretHandler) Watch(ctx context.Context, client kubernetes.Interface, ns string) (watch.Interface, error) {
	return client.CoreV1().Secrets(ns).Watch(ctx, metav1.ListOptions{})
}

func (h secretHandler) Exists(ctx context.Context, client kubernetes.Interface, ns, name string) (bool, error) {
	_, err := client.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

func (h secretHandler) Delete(ctx context.Context, client kubernetes.Interface, ns, name string) error {
	return client.CoreV1().Secrets(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

// ============================================================================
// Service
// ============================================================================

type serviceHandler struct{}

func (h serviceHandler) Get(ctx context.Context, client kubernetes.Interface, ns, name string) (interface{}, error) {
	svc, err := client.CoreV1().Services(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	return svc, err
}

func (h serviceHandler) IsReady(_ interface{}) bool {
	return true // Services are always ready once they exist
}

func (h serviceHandler) Watch(ctx context.Context, client kubernetes.Interface, ns string) (watch.Interface, error) {
	return client.CoreV1().Services(ns).Watch(ctx, metav1.ListOptions{})
}

func (h serviceHandler) Exists(ctx context.Context, client kubernetes.Interface, ns, name string) (bool, error) {
	_, err := client.CoreV1().Services(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

func (h serviceHandler) Delete(ctx context.Context, client kubernetes.Interface, ns, name string) error {
	return client.CoreV1().Services(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

// ============================================================================
// PVC
// ============================================================================

type pvcHandler struct{}

func (h pvcHandler) Get(ctx context.Context, client kubernetes.Interface, ns, name string) (interface{}, error) {
	pvc, err := client.CoreV1().PersistentVolumeClaims(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	return pvc, err
}

func (h pvcHandler) IsReady(obj interface{}) bool {
	pvc, ok := obj.(*corev1.PersistentVolumeClaim)
	if !ok {
		return false
	}
	return pvc.Status.Phase == corev1.ClaimBound
}

func (h pvcHandler) Watch(ctx context.Context, client kubernetes.Interface, ns string) (watch.Interface, error) {
	return client.CoreV1().PersistentVolumeClaims(ns).Watch(ctx, metav1.ListOptions{})
}

func (h pvcHandler) Exists(ctx context.Context, client kubernetes.Interface, ns, name string) (bool, error) {
	_, err := client.CoreV1().PersistentVolumeClaims(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

func (h pvcHandler) Delete(ctx context.Context, client kubernetes.Interface, ns, name string) error {
	return client.CoreV1().PersistentVolumeClaims(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

