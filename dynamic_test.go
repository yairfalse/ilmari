package ilmari

import (
	"os"
	"path/filepath"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// TestFromHelmRendersChart verifies FromHelm renders a chart to objects.
func TestFromHelmRendersChart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	// Create a minimal helm chart structure
	tmpDir := t.TempDir()
	chartDir := filepath.Join(tmpDir, "mychart")
	templatesDir := filepath.Join(chartDir, "templates")

	os.MkdirAll(templatesDir, 0755)

	// Chart.yaml
	chartYaml := `apiVersion: v2
name: mychart
version: 0.1.0
`
	os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte(chartYaml), 0644)

	// values.yaml
	valuesYaml := `replicas: 1
image: nginx:alpine
`
	os.WriteFile(filepath.Join(chartDir, "values.yaml"), []byte(valuesYaml), 0644)

	// templates/deployment.yaml
	deploymentTemplate := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .Release.Name }}-app
spec:
  replicas: {{ .Values.replicas }}
  selector:
    matchLabels:
      app: {{ .Release.Name }}
  template:
    metadata:
      labels:
        app: {{ .Release.Name }}
    spec:
      containers:
      - name: main
        image: {{ .Values.image }}
`
	os.WriteFile(filepath.Join(templatesDir, "deployment.yaml"), []byte(deploymentTemplate), 0644)

	Run(t, func(ctx *Context) {
		// Render helm chart with custom values
		objects, err := FromHelm(chartDir, "myrelease", map[string]interface{}{
			"replicas": 3,
			"image":    "nginx:latest",
		})
		if err != nil {
			t.Fatalf("FromHelm failed: %v", err)
		}

		if len(objects) == 0 {
			t.Fatal("expected at least one object from helm chart")
		}

		// Apply rendered objects
		for _, obj := range objects {
			if err := ctx.Apply(obj); err != nil {
				t.Fatalf("Apply failed: %v", err)
			}
		}

		// Verify deployment was created with correct values
		var deploy appsv1.Deployment
		if err := ctx.Get("myrelease-app", &deploy); err != nil {
			t.Fatalf("Get deployment failed: %v", err)
		}

		if *deploy.Spec.Replicas != 3 {
			t.Errorf("expected 3 replicas, got %d", *deploy.Spec.Replicas)
		}
	})
}

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
