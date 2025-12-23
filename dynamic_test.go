package ilmari

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// TestGetDynamicForCRD verifies GetDynamic works with arbitrary GVKs.
func TestGetDynamicForCRD(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a ConfigMap using dynamic client (simulating CRD workflow)
		gvr := schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "configmaps",
		}

		obj := map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name": "dynamic-test",
			},
			"data": map[string]interface{}{
				"key": "value",
			},
		}

		// Apply using dynamic
		if err := ctx.ApplyDynamic(gvr, obj); err != nil {
			t.Fatalf("ApplyDynamic failed: %v", err)
		}

		// Get using dynamic
		result, err := ctx.GetDynamic(gvr, "dynamic-test")
		if err != nil {
			t.Fatalf("GetDynamic failed: %v", err)
		}

		data, ok := result["data"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected data map, got %T", result["data"])
		}

		if data["key"] != "value" {
			t.Errorf("expected key=value, got %v", data["key"])
		}
	})
}

// TestDeleteDynamic verifies DeleteDynamic removes resources.
func TestDeleteDynamic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		gvr := schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "configmaps",
		}

		// Create a ConfigMap
		obj := map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name": "delete-test",
			},
			"data": map[string]interface{}{
				"foo": "bar",
			},
		}

		if err := ctx.ApplyDynamic(gvr, obj); err != nil {
			t.Fatalf("ApplyDynamic failed: %v", err)
		}

		// Verify it exists
		_, err := ctx.GetDynamic(gvr, "delete-test")
		if err != nil {
			t.Fatalf("GetDynamic failed: %v", err)
		}

		// Delete it
		if err := ctx.DeleteDynamic(gvr, "delete-test"); err != nil {
			t.Fatalf("DeleteDynamic failed: %v", err)
		}

		// Verify it's gone
		_, err = ctx.GetDynamic(gvr, "delete-test")
		if err == nil {
			t.Error("expected error after delete, got nil")
		}
	})
}

// TestListDynamic verifies ListDynamic returns multiple resources.
func TestListDynamic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		gvr := schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "configmaps",
		}

		// Create multiple ConfigMaps
		for i := 0; i < 3; i++ {
			obj := map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]interface{}{
					"name": "list-test-" + string(rune('a'+i)),
					"labels": map[string]interface{}{
						"test-group": "list-dynamic",
					},
				},
				"data": map[string]interface{}{
					"index": string(rune('0' + i)),
				},
			}

			if err := ctx.ApplyDynamic(gvr, obj); err != nil {
				t.Fatalf("ApplyDynamic failed for item %d: %v", i, err)
			}
		}

		// List all ConfigMaps in namespace
		results, err := ctx.ListDynamic(gvr)
		if err != nil {
			t.Fatalf("ListDynamic failed: %v", err)
		}

		// Should have at least 3 (might have more from other tests or k8s defaults)
		if len(results) < 3 {
			t.Errorf("expected at least 3 configmaps, got %d", len(results))
		}

		// Verify our test items are in the list
		found := 0
		for _, item := range results {
			metadata, ok := item["metadata"].(map[string]interface{})
			if !ok {
				continue
			}
			name, _ := metadata["name"].(string)
			if name == "list-test-a" || name == "list-test-b" || name == "list-test-c" {
				found++
			}
		}

		if found != 3 {
			t.Errorf("expected to find 3 test configmaps, found %d", found)
		}
	})
}

// TestDeleteDynamicNotFound verifies DeleteDynamic returns error for missing resource.
func TestDeleteDynamicNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		gvr := schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "configmaps",
		}

		err := ctx.DeleteDynamic(gvr, "nonexistent-configmap-xyz")
		if err == nil {
			t.Error("expected error for deleting nonexistent resource, got nil")
		}
	})
}
