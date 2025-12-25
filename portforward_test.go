package ilmari

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestForwardMakesHTTPRequest verifies Forward can make HTTP requests.
func TestForwardMakesHTTPRequest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Create a simple HTTP server pod
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "http-server",
				Labels: map[string]string{
					"app": "http-server",
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "server",
						Image: "nginx:alpine",
						Ports: []corev1.ContainerPort{
							{ContainerPort: 80},
						},
					},
				},
			},
		}
		if err := ctx.Apply(pod); err != nil {
			t.Fatalf("Apply pod failed: %v", err)
		}

		// Create a service
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name: "http-server",
			},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{
					"app": "http-server",
				},
				Ports: []corev1.ServicePort{
					{Port: 80},
				},
			},
		}
		if err := ctx.Apply(svc); err != nil {
			t.Fatalf("Apply svc failed: %v", err)
		}

		// Wait for pod to be ready
		if err := ctx.WaitReady("pod/http-server"); err != nil {
			t.Fatalf("WaitReady failed: %v", err)
		}

		// Forward and make request
		pf := ctx.PortForward("svc/http-server", 80)
		defer pf.Close()

		resp, err := pf.Get("/")
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != 200 {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}
	})
}

// TestPortForwardPost verifies Post sends HTTP POST requests.
func TestPortForwardPost(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Deploy nginx - POST to static files returns 405, proving POST was sent
		stack := NewStack().
			Service("web").
			Image("nginx:1.25-alpine").
			Port(80).
			Build()

		if err := ctx.Up(stack); err != nil {
			t.Fatalf("Up failed: %v", err)
		}

		pf := ctx.PortForward("svc/web", 80)
		defer pf.Close()

		// Test Post - nginx returns 405 for POST to static files
		resp, err := pf.Post("/", "application/json", strings.NewReader(`{"key":"value"}`))
		if err != nil {
			t.Fatalf("Post failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		// nginx returns 405 for POST to static resources - this proves POST was sent
		if resp.StatusCode != 405 {
			t.Errorf("expected 405 (POST not allowed), got %d", resp.StatusCode)
		}
	})
}

// TestPortForwardPut verifies Put sends HTTP PUT requests.
func TestPortForwardPut(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Deploy nginx - PUT to static files returns 405, proving PUT was sent
		stack := NewStack().
			Service("web").
			Image("nginx:1.25-alpine").
			Port(80).
			Build()

		if err := ctx.Up(stack); err != nil {
			t.Fatalf("Up failed: %v", err)
		}

		pf := ctx.PortForward("svc/web", 80)
		defer pf.Close()

		// Test Put - nginx returns 405 for PUT
		resp, err := pf.Put("/", "application/json", strings.NewReader(`{"updated":"data"}`))
		if err != nil {
			t.Fatalf("Put failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		// nginx returns 405 for PUT - proves the method was sent correctly
		if resp.StatusCode != 405 {
			t.Errorf("expected 405 (PUT not allowed), got %d", resp.StatusCode)
		}
	})
}

// TestPortForwardDelete verifies Delete sends HTTP DELETE requests.
func TestPortForwardDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		// Deploy nginx - DELETE to static files returns 405
		stack := NewStack().
			Service("web").
			Image("nginx:1.25-alpine").
			Port(80).
			Build()

		if err := ctx.Up(stack); err != nil {
			t.Fatalf("Up failed: %v", err)
		}

		pf := ctx.PortForward("svc/web", 80)
		defer pf.Close()

		// Test Delete - nginx returns 405 for DELETE
		resp, err := pf.Delete("/")
		if err != nil {
			t.Fatalf("Delete failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		// nginx returns 405 for DELETE - proves the method was sent correctly
		if resp.StatusCode != 405 {
			t.Errorf("expected 405 (DELETE not allowed), got %d", resp.StatusCode)
		}
	})
}

// TestPortForwardDo verifies Do sends custom HTTP requests.
func TestPortForwardDo(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		stack := NewStack().
			Service("web").
			Image("nginx:1.25-alpine").
			Port(80).
			Build()

		if err := ctx.Up(stack); err != nil {
			t.Fatalf("Up failed: %v", err)
		}

		pf := ctx.PortForward("svc/web", 80)
		defer pf.Close()

		// Test Do with custom PATCH request
		req, err := http.NewRequest("PATCH", pf.URL("/"), bytes.NewReader([]byte(`{"partial":"update"}`)))
		if err != nil {
			t.Fatalf("NewRequest failed: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Custom-Header", "test-value")

		resp, err := pf.Do(req)
		if err != nil {
			t.Fatalf("Do failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		// nginx returns 405 for PATCH - proves the method was sent correctly
		if resp.StatusCode != 405 {
			t.Errorf("expected 405 (PATCH not allowed), got %d", resp.StatusCode)
		}
	})
}

// TestPortForwardURL verifies URL returns proper endpoint URLs.
func TestPortForwardURL(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		stack := NewStack().
			Service("web").
			Image("nginx:1.25-alpine").
			Port(80).
			Build()

		if err := ctx.Up(stack); err != nil {
			t.Fatalf("Up failed: %v", err)
		}

		pf := ctx.PortForward("svc/web", 80)
		defer pf.Close()

		url := pf.URL("/health")
		if !strings.HasPrefix(url, "http://localhost:") {
			t.Errorf("expected URL to start with http://localhost:, got %s", url)
		}
		if !strings.HasSuffix(url, "/health") {
			t.Errorf("expected URL to end with /health, got %s", url)
		}
	})
}

// TestPortForwardGetVerify verifies Get still works after refactoring.
func TestPortForwardGetVerify(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	Run(t, func(ctx *Context) {
		stack := NewStack().
			Service("web").
			Image("nginx:1.25-alpine").
			Port(80).
			Build()

		if err := ctx.Up(stack); err != nil {
			t.Fatalf("Up failed: %v", err)
		}

		pf := ctx.PortForward("svc/web", 80)
		defer pf.Close()

		// GET should return 200 for nginx index
		resp, err := pf.Get("/")
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != 200 {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}
	})
}
