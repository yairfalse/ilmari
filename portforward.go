package ilmari

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// PortForward represents an active port forward to a pod or service.
type PortForward struct {
	localPort  int
	stopChan   chan struct{}
	doneChan   chan struct{}
	httpClient *http.Client
	err        error
	span       trace.Span
	closeOnce  sync.Once
}

// Close closes the port forward and waits for cleanup.
// Safe to call multiple times.
func (pf *PortForward) Close() {
	pf.closeOnce.Do(func() {
		if pf.stopChan != nil {
			close(pf.stopChan)
		}
		if pf.doneChan != nil {
			<-pf.doneChan
		}
		if pf.span != nil {
			pf.span.End()
		}
	})
}

// Get makes an HTTP GET request through the port forward.
// Caller is responsible for closing the response body.
func (pf *PortForward) Get(path string) (*http.Response, error) {
	if pf.err != nil {
		return nil, pf.err
	}
	return pf.httpClient.Get(pf.URL(path))
}

// URL returns the full URL for a given path through the port forward.
func (pf *PortForward) URL(path string) string {
	return fmt.Sprintf("http://localhost:%d%s", pf.localPort, path)
}

// Post makes an HTTP POST request through the port forward.
// Caller is responsible for closing the response body.
func (pf *PortForward) Post(path, contentType string, body io.Reader) (*http.Response, error) {
	if pf.err != nil {
		return nil, pf.err
	}
	return pf.httpClient.Post(pf.URL(path), contentType, body)
}

// Put makes an HTTP PUT request through the port forward.
// Caller is responsible for closing the response body.
func (pf *PortForward) Put(path, contentType string, body io.Reader) (*http.Response, error) {
	if pf.err != nil {
		return nil, pf.err
	}
	req, err := http.NewRequest(http.MethodPut, pf.URL(path), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	return pf.httpClient.Do(req)
}

// Delete makes an HTTP DELETE request through the port forward.
// Caller is responsible for closing the response body.
func (pf *PortForward) Delete(path string) (*http.Response, error) {
	if pf.err != nil {
		return nil, pf.err
	}
	req, err := http.NewRequest(http.MethodDelete, pf.URL(path), nil)
	if err != nil {
		return nil, err
	}
	return pf.httpClient.Do(req)
}

// Do executes a custom HTTP request through the port forward.
// Use this for PATCH, OPTIONS, or requests with custom headers.
// Caller is responsible for closing the response body.
func (pf *PortForward) Do(req *http.Request) (*http.Response, error) {
	if pf.err != nil {
		return nil, pf.err
	}
	return pf.httpClient.Do(req)
}

// PortForward creates a port forward to a pod or service.
// Resource format: "svc/name" or "pod/name"
func (c *Context) PortForward(resource string, port int) *PortForward {
	_, span := c.startSpan(context.Background(), "ilmari.PortForward",
		attribute.String("resource", resource),
		attribute.Int("port", port))

	parts := strings.SplitN(resource, "/", 2)
	if len(parts) != 2 {
		span.RecordError(fmt.Errorf("invalid resource format"))
		span.SetStatus(codes.Error, "invalid resource format")
		span.End()
		return &PortForward{err: fmt.Errorf("invalid resource format %q, expected kind/name", resource)}
	}

	kind := strings.ToLower(parts[0])
	name := parts[1]

	// Helper to return error with span handling
	returnErr := func(err error) *PortForward {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.End()
		return &PortForward{err: err}
	}

	// Get pod name
	var podName string
	ctx := context.Background()

	switch kind {
	case "pod":
		podName = name
	case "svc", "service":
		// Get service and find a pod
		svc, err := c.Client.CoreV1().Services(c.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return returnErr(fmt.Errorf("failed to get service: %w", err))
		}

		// Find pods matching the service selector
		pods, err := c.Client.CoreV1().Pods(c.Namespace).List(ctx, metav1.ListOptions{
			LabelSelector: labels.SelectorFromSet(svc.Spec.Selector).String(),
		})
		if err != nil {
			return returnErr(fmt.Errorf("failed to list pods: %w", err))
		}
		if len(pods.Items) == 0 {
			return returnErr(fmt.Errorf("no pods found for service %s", name))
		}
		podName = pods.Items[0].Name
	default:
		return returnErr(fmt.Errorf("unsupported kind %q, use pod or svc", kind))
	}

	// Set up port forward
	config, err := clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
	if err != nil {
		return returnErr(fmt.Errorf("failed to get config: %w", err))
	}

	roundTripper, upgrader, err := spdy.RoundTripperFor(config)
	if err != nil {
		return returnErr(fmt.Errorf("failed to create round tripper: %w", err))
	}

	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", c.Namespace, podName)
	hostIP := strings.TrimPrefix(config.Host, "https://")
	hostIP = strings.TrimPrefix(hostIP, "http://")
	serverURL := url.URL{Scheme: "https", Host: hostIP, Path: path}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: roundTripper}, http.MethodPost, &serverURL)

	stopChan := make(chan struct{}, 1)
	readyChan := make(chan struct{})

	// Use port 0 to get a random available port
	ports := []string{fmt.Sprintf("0:%d", port)}
	pf, err := portforward.New(dialer, ports, stopChan, readyChan, io.Discard, io.Discard)
	if err != nil {
		return returnErr(fmt.Errorf("failed to create port forward: %w", err))
	}

	doneChan := make(chan struct{})
	errChan := make(chan error, 1)
	go func() {
		errChan <- pf.ForwardPorts()
		close(doneChan)
	}()

	// Wait for port forward to be ready
	select {
	case <-readyChan:
	case err := <-errChan:
		return returnErr(fmt.Errorf("port forward failed: %w", err))
	}

	forwardedPorts, err := pf.GetPorts()
	if err != nil || len(forwardedPorts) == 0 {
		return returnErr(fmt.Errorf("failed to get forwarded ports: %w", err))
	}

	// Return with span - it will be closed in PortForward.Close()
	return &PortForward{
		localPort:  int(forwardedPorts[0].Local),
		stopChan:   stopChan,
		doneChan:   doneChan,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		span:       span,
	}
}

// MustPortForward creates a port forward and panics on error.
// Useful for test setup where errors should fail immediately.
func (c *Context) MustPortForward(resource string, port int) *PortForward {
	pf := c.PortForward(resource, port)
	if pf.err != nil {
		panic(fmt.Sprintf("MustPortForward failed: %v", pf.err))
	}
	return pf
}
