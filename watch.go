package ilmari

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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

	h := lookupHandler(kind)
	if h == nil {
		return nil, &UnsupportedKindError{
			Operation:      "Watch",
			Kind:           kind,
			SupportedKinds: []string{"pod", "deployment", "configmap", "secret", "service", "statefulset", "daemonset"},
		}
	}

	watcher, err := h.Watch(ctx, c.Client, c.Namespace)
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
	return c.WaitDeletedTimeout(resource, c.waitTimeout)
}

// WaitDeletedTimeout waits for a resource to be deleted with custom timeout.
func (c *Context) WaitDeletedTimeout(resource string, timeout time.Duration) error {
	ref, err := ParseResourceRef(resource)
	if err != nil {
		return err
	}

	kind := ref.Kind
	name := ref.Name

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(c.pollInterval)
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
	if h := lookupHandler(kind); h != nil {
		return h.Exists(context.Background(), c.Client, c.Namespace, name)
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
	ref, err := ParseResourceRef(resource)
	if err != nil {
		return err
	}

	kind := ref.Kind
	name := ref.Name

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
