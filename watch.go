package ilmari

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
)

// ============================================================================
// Watch, WaitDeleted, Patch - Phase 1 Core Primitives
// ============================================================================

// WatchEvent represents a Kubernetes watch event.
type WatchEvent struct {
	Type   string      // ADDED, MODIFIED, DELETED
	Object interface{} // The resource object
}

// Watch starts watching resources of the given kind.
// Returns a stop function to cancel the watch and an error if the watch couldn't start.
// The stop function is safe to call multiple times.
// The callback is invoked for each event (ADDED, MODIFIED, DELETED).
func (c *Context) Watch(kind string, callback func(WatchEvent)) (func(), error) {
	ctx := context.Background()
	kind = strings.ToLower(kind)

	// Map of kind to watcher factory functions
	watcherFactories := map[string]func(ctx context.Context, c *Context) (watch.Interface, error){
		"pod": func(ctx context.Context, c *Context) (watch.Interface, error) {
			return c.Client.CoreV1().Pods(c.Namespace).Watch(ctx, metav1.ListOptions{})
		},
		"deployment": func(ctx context.Context, c *Context) (watch.Interface, error) {
			return c.Client.AppsV1().Deployments(c.Namespace).Watch(ctx, metav1.ListOptions{})
		},
		"configmap": func(ctx context.Context, c *Context) (watch.Interface, error) {
			return c.Client.CoreV1().ConfigMaps(c.Namespace).Watch(ctx, metav1.ListOptions{})
		},
		"secret": func(ctx context.Context, c *Context) (watch.Interface, error) {
			return c.Client.CoreV1().Secrets(c.Namespace).Watch(ctx, metav1.ListOptions{})
		},
		"service": func(ctx context.Context, c *Context) (watch.Interface, error) {
			return c.Client.CoreV1().Services(c.Namespace).Watch(ctx, metav1.ListOptions{})
		},
		"statefulset": func(ctx context.Context, c *Context) (watch.Interface, error) {
			return c.Client.AppsV1().StatefulSets(c.Namespace).Watch(ctx, metav1.ListOptions{})
		},
		"daemonset": func(ctx context.Context, c *Context) (watch.Interface, error) {
			return c.Client.AppsV1().DaemonSets(c.Namespace).Watch(ctx, metav1.ListOptions{})
		},
	}

	factory, ok := watcherFactories[kind]
	if !ok {
		return nil, &UnsupportedKindError{
			Operation:      "Watch",
			Kind:           kind,
			SupportedKinds: []string{"pod", "deployment", "configmap", "secret", "service", "statefulset", "daemonset"},
		}
	}

	watcher, err := factory(ctx, c)
	if err != nil {
		return nil, err
	}

	stopChan := make(chan struct{})
	var stopOnce sync.Once

	// Use a buffered channel to decouple event delivery from callback execution.
	eventChan := make(chan WatchEvent, 100)

	// Goroutine to invoke the callback for each event.
	go func() {
		for evt := range eventChan {
			callback(evt)
		}
	}()

	// Goroutine to process watch events.
	go func() {
		defer watcher.Stop()
		for {
			select {
			case <-stopChan:
				close(eventChan)
				return
			case event, ok := <-watcher.ResultChan():
				if !ok {
					close(eventChan)
					return
				}
				evt := WatchEvent{
					Type:   string(event.Type),
					Object: event.Object,
				}
				// Non-blocking send; drop event if buffer is full to avoid blocking.
				select {
				case eventChan <- evt:
				default:
					// Event dropped - buffer full.
				}
			}
		}
	}()

	return func() {
		stopOnce.Do(func() {
			close(stopChan)
		})
	}, nil
}

// WaitDeleted waits for a resource to be deleted.
// Resource format: "kind/name" (e.g., "pod/myapp", "configmap/myconfig")
func (c *Context) WaitDeleted(resource string) error {
	return c.WaitDeletedTimeout(resource, 60*time.Second)
}

// WaitDeletedTimeout waits for a resource to be deleted with custom timeout.
func (c *Context) WaitDeletedTimeout(resource string, timeout time.Duration) error {
	parts := strings.SplitN(resource, "/", 2)
	if len(parts) != 2 {
		return newResourceFormatError(resource)
	}

	kind := strings.ToLower(parts[0])
	name := parts[1]

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		exists, err := c.resourceExists(kind, name)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for %s to be deleted", resource)
		case <-ticker.C:
		}
	}
}

// resourceExists checks if a resource exists.
func (c *Context) resourceExists(kind, name string) (bool, error) {
	ctx := context.Background()

	type existsFunc func(ctx context.Context, c *Context, name string) (bool, error)

	resourceFuncs := map[string]existsFunc{
		"pod": func(ctx context.Context, c *Context, name string) (bool, error) {
			_, err := c.Client.CoreV1().Pods(c.Namespace).Get(ctx, name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return err == nil, err
		},
		"deployment": func(ctx context.Context, c *Context, name string) (bool, error) {
			_, err := c.Client.AppsV1().Deployments(c.Namespace).Get(ctx, name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return err == nil, err
		},
		"configmap": func(ctx context.Context, c *Context, name string) (bool, error) {
			_, err := c.Client.CoreV1().ConfigMaps(c.Namespace).Get(ctx, name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return err == nil, err
		},
		"secret": func(ctx context.Context, c *Context, name string) (bool, error) {
			_, err := c.Client.CoreV1().Secrets(c.Namespace).Get(ctx, name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return err == nil, err
		},
		"service": func(ctx context.Context, c *Context, name string) (bool, error) {
			_, err := c.Client.CoreV1().Services(c.Namespace).Get(ctx, name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return err == nil, err
		},
		"statefulset": func(ctx context.Context, c *Context, name string) (bool, error) {
			_, err := c.Client.AppsV1().StatefulSets(c.Namespace).Get(ctx, name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return err == nil, err
		},
		"daemonset": func(ctx context.Context, c *Context, name string) (bool, error) {
			_, err := c.Client.AppsV1().DaemonSets(c.Namespace).Get(ctx, name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return err == nil, err
		},
	}

	if fn, ok := resourceFuncs[kind]; ok {
		return fn(ctx, c, name)
	}
	return false, &UnsupportedKindError{
		Operation:      "WaitDeleted",
		Kind:           kind,
		SupportedKinds: []string{"pod", "deployment", "configmap", "secret", "service", "statefulset", "daemonset"},
	}
}

// PatchType specifies the type of patch operation.
type PatchType string

const (
	// PatchStrategic uses strategic merge patch (default for K8s resources)
	PatchStrategic PatchType = "strategic"
	// PatchMerge uses JSON merge patch (RFC 7386)
	PatchMerge PatchType = "merge"
	// PatchJSON uses JSON patch (RFC 6902)
	PatchJSON PatchType = "json"
)

// Patch applies a patch to a resource.
// Resource format: "kind/name" (e.g., "deployment/myapp")
func (c *Context) Patch(resource string, patch []byte, patchType PatchType) error {
	parts := strings.SplitN(resource, "/", 2)
	if len(parts) != 2 {
		return newResourceFormatError(resource)
	}

	kind := strings.ToLower(parts[0])
	name := parts[1]

	// Convert PatchType to Kubernetes patch type
	var k8sPatchType types.PatchType
	switch patchType {
	case PatchStrategic:
		k8sPatchType = types.StrategicMergePatchType
	case PatchMerge:
		k8sPatchType = types.MergePatchType
	case PatchJSON:
		k8sPatchType = types.JSONPatchType
	default:
		k8sPatchType = types.StrategicMergePatchType
	}

	ctx := context.Background()

	patchFuncs := map[string]func() error{
		"pod": func() error {
			_, e := c.Client.CoreV1().Pods(c.Namespace).Patch(ctx, name, k8sPatchType, patch, metav1.PatchOptions{})
			return e
		},
		"deployment": func() error {
			_, e := c.Client.AppsV1().Deployments(c.Namespace).Patch(ctx, name, k8sPatchType, patch, metav1.PatchOptions{})
			return e
		},
		"configmap": func() error {
			_, e := c.Client.CoreV1().ConfigMaps(c.Namespace).Patch(ctx, name, k8sPatchType, patch, metav1.PatchOptions{})
			return e
		},
		"secret": func() error {
			_, e := c.Client.CoreV1().Secrets(c.Namespace).Patch(ctx, name, k8sPatchType, patch, metav1.PatchOptions{})
			return e
		},
		"service": func() error {
			_, e := c.Client.CoreV1().Services(c.Namespace).Patch(ctx, name, k8sPatchType, patch, metav1.PatchOptions{})
			return e
		},
		"statefulset": func() error {
			_, e := c.Client.AppsV1().StatefulSets(c.Namespace).Patch(ctx, name, k8sPatchType, patch, metav1.PatchOptions{})
			return e
		},
		"daemonset": func() error {
			_, e := c.Client.AppsV1().DaemonSets(c.Namespace).Patch(ctx, name, k8sPatchType, patch, metav1.PatchOptions{})
			return e
		},
	}

	patchFunc, ok := patchFuncs[kind]
	if !ok {
		return &UnsupportedKindError{
			Operation:      "Patch",
			Kind:           kind,
			SupportedKinds: []string{"pod", "deployment", "configmap", "secret", "service", "statefulset", "daemonset"},
		}
	}
	return patchFunc()
}
