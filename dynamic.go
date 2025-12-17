package ilmari

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"
)

// FromHelm renders a Helm chart to Kubernetes objects.
// chartPath is the path to the chart directory.
// releaseName is the name for the release.
// values are overrides for the chart's values.yaml.
// Returns unstructured objects that can be passed to Apply.
//
// LIMITATIONS: This uses Go's text/template, NOT the full Helm template engine.
// Supported: {{ .Values.x }}, {{ .Release.Name }}, basic if/range/with.
// NOT supported: Helm-specific functions (include, tpl, lookup), subcharts,
// hooks, capabilities checks, complex sprig functions.
// For complex charts, use the official Helm SDK or 'helm template' CLI.
func FromHelm(chartPath, releaseName string, values map[string]interface{}) ([]runtime.Object, error) {
	// Read Chart.yaml
	chartYamlPath := filepath.Join(chartPath, "Chart.yaml")
	if _, err := os.Stat(chartYamlPath); err != nil {
		return nil, fmt.Errorf("Chart.yaml not found: %w", err)
	}

	// Read default values
	defaultValues := make(map[string]interface{})
	valuesPath := filepath.Join(chartPath, "values.yaml")
	if data, err := os.ReadFile(valuesPath); err == nil {
		if err := yaml.Unmarshal(data, &defaultValues); err != nil {
			return nil, fmt.Errorf("failed to parse values.yaml: %w", err)
		}
	}

	// Merge with provided values (provided values take precedence)
	mergedValues := mergeMaps(defaultValues, values)

	// Build template context
	templateContext := map[string]interface{}{
		"Values": mergedValues,
		"Release": map[string]interface{}{
			"Name":      releaseName,
			"Namespace": "default", // Will be overridden when applied
		},
		"Chart": map[string]interface{}{
			"Name": filepath.Base(chartPath),
		},
	}

	// Find all templates
	templatesDir := filepath.Join(chartPath, "templates")
	templateFiles, err := filepath.Glob(filepath.Join(templatesDir, "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("failed to find templates: %w", err)
	}

	var objects []runtime.Object

	for _, tmplPath := range templateFiles {
		// Skip partials/helpers
		if strings.HasPrefix(filepath.Base(tmplPath), "_") {
			continue
		}

		// Read template
		tmplData, err := os.ReadFile(tmplPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read template %s: %w", tmplPath, err)
		}

		// Parse and execute template
		tmpl, err := template.New(filepath.Base(tmplPath)).Parse(string(tmplData))
		if err != nil {
			return nil, fmt.Errorf("failed to parse template %s: %w", tmplPath, err)
		}

		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, templateContext); err != nil {
			return nil, fmt.Errorf("failed to execute template %s: %w", tmplPath, err)
		}

		// Skip empty templates
		rendered := strings.TrimSpace(buf.String())
		if rendered == "" {
			continue
		}

		// Parse YAML to unstructured
		obj := &unstructured.Unstructured{}
		if err := yaml.Unmarshal([]byte(rendered), &obj.Object); err != nil {
			return nil, fmt.Errorf("failed to parse rendered template %s: %w", tmplPath, err)
		}

		objects = append(objects, obj)
	}

	return objects, nil
}

// mergeMaps merges two maps, with values from b taking precedence.
func mergeMaps(a, b map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range a {
		result[k] = v
	}
	for k, v := range b {
		result[k] = v
	}
	return result
}

// ApplyDynamic applies an unstructured object using the dynamic client.
// Useful for CRDs or when you don't have typed structs.
func (c *Context) ApplyDynamic(gvr schema.GroupVersionResource, obj map[string]interface{}) (err error) {
	_, span := c.startSpan(context.Background(), "ilmari.ApplyDynamic",
		attribute.String("group", gvr.Group),
		attribute.String("version", gvr.Version),
		attribute.String("resource", gvr.Resource))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

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
		_, err = c.Dynamic.Resource(gvr).Namespace(c.Namespace).Create(ctx, u, metav1.CreateOptions{})
	} else if getErr != nil {
		err = getErr
	} else {
		// Update
		_, err = c.Dynamic.Resource(gvr).Namespace(c.Namespace).Update(ctx, u, metav1.UpdateOptions{})
	}

	return err
}

// GetDynamic retrieves an object using the dynamic client.
// Returns the object as a map[string]interface{}.
func (c *Context) GetDynamic(gvr schema.GroupVersionResource, name string) (result map[string]interface{}, err error) {
	_, span := c.startSpan(context.Background(), "ilmari.GetDynamic",
		attribute.String("group", gvr.Group),
		attribute.String("version", gvr.Version),
		attribute.String("resource", gvr.Resource),
		attribute.String("name", name))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	u, err := c.Dynamic.Resource(gvr).Namespace(c.Namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return u.Object, nil
}

// DeleteDynamic deletes an object using the dynamic client.
func (c *Context) DeleteDynamic(gvr schema.GroupVersionResource, name string) (err error) {
	_, span := c.startSpan(context.Background(), "ilmari.DeleteDynamic",
		attribute.String("group", gvr.Group),
		attribute.String("version", gvr.Version),
		attribute.String("resource", gvr.Resource),
		attribute.String("name", name))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	err = c.Dynamic.Resource(gvr).Namespace(c.Namespace).Delete(context.Background(), name, metav1.DeleteOptions{})
	return err
}

// ListDynamic lists objects using the dynamic client.
func (c *Context) ListDynamic(gvr schema.GroupVersionResource) (result []map[string]interface{}, err error) {
	_, span := c.startSpan(context.Background(), "ilmari.ListDynamic",
		attribute.String("group", gvr.Group),
		attribute.String("version", gvr.Version),
		attribute.String("resource", gvr.Resource))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	list, err := c.Dynamic.Resource(gvr).Namespace(c.Namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	result = make([]map[string]interface{}, len(list.Items))
	for i, item := range list.Items {
		result[i] = item.Object
	}

	return result, nil
}
