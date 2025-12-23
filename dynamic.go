package ilmari

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// ApplyDynamic applies an unstructured object using the dynamic client.
// Useful for CRDs or when you don't have typed structs.
func (c *Context) ApplyDynamic(gvr schema.GroupVersionResource, obj map[string]interface{}) error {
	// Set namespace if not set
	metadata, ok := obj["metadata"].(map[string]interface{})
	if !ok {
		metadata = make(map[string]interface{})
		obj["metadata"] = metadata
	}
	if _, ok := metadata["namespace"]; !ok {
		metadata["namespace"] = c.Namespace
	}

	u := &unstructured.Unstructured{Object: obj}
	name := u.GetName()

	ctx := context.Background()

	// Try to get existing resource
	_, getErr := c.Dynamic.Resource(gvr).Namespace(c.Namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(getErr) {
		// Create
		_, err := c.Dynamic.Resource(gvr).Namespace(c.Namespace).Create(ctx, u, metav1.CreateOptions{})
		return err
	} else if getErr != nil {
		return getErr
	}

	// Update
	_, err := c.Dynamic.Resource(gvr).Namespace(c.Namespace).Update(ctx, u, metav1.UpdateOptions{})
	return err
}

// GetDynamic retrieves an object using the dynamic client.
// Returns the object as a map[string]interface{}.
func (c *Context) GetDynamic(gvr schema.GroupVersionResource, name string) (map[string]interface{}, error) {
	u, err := c.Dynamic.Resource(gvr).Namespace(c.Namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return u.Object, nil
}

// DeleteDynamic deletes an object using the dynamic client.
func (c *Context) DeleteDynamic(gvr schema.GroupVersionResource, name string) error {
	return c.Dynamic.Resource(gvr).Namespace(c.Namespace).Delete(context.Background(), name, metav1.DeleteOptions{})
}

// ListDynamic lists objects using the dynamic client.
func (c *Context) ListDynamic(gvr schema.GroupVersionResource) ([]map[string]interface{}, error) {
	list, err := c.Dynamic.Resource(gvr).Namespace(c.Namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	result := make([]map[string]interface{}, len(list.Items))
	for i, item := range list.Items {
		result[i] = item.Object
	}

	return result, nil
}
