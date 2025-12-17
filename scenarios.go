package ilmari

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ============================================================================
// Test Scenarios
// ============================================================================

// SelfHealTest provides a fluent API for self-healing tests.
type SelfHealTest struct {
	ctx             *Context
	resource        string
	deploymentName  string
	err             error
	recoveryTimeout time.Duration
}

// TestSelfHealing runs a self-healing test on a deployment.
func (c *Context) TestSelfHealing(resource string, fn func(*SelfHealTest)) error {
	parts := strings.SplitN(resource, "/", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "deployment" {
		return fmt.Errorf("TestSelfHealing requires deployment/name format")
	}

	test := &SelfHealTest{
		ctx:             c,
		resource:        resource,
		deploymentName:  parts[1],
		recoveryTimeout: 60 * time.Second,
	}

	fn(test)
	return test.err
}

// KillPod kills one pod from the deployment.
func (s *SelfHealTest) KillPod() {
	if s.err != nil {
		return
	}

	// Find a pod from this deployment
	pods, err := s.ctx.Client.CoreV1().Pods(s.ctx.Namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", s.deploymentName),
	})
	if err != nil {
		s.err = fmt.Errorf("failed to list pods: %w", err)
		return
	}
	if len(pods.Items) == 0 {
		s.err = fmt.Errorf("no pods found for deployment %s", s.deploymentName)
		return
	}

	// Kill the first pod
	podName := pods.Items[0].Name
	s.err = s.ctx.Kill("pod/" + podName)
}

// ExpectRecoveryWithin sets the expected recovery time and waits.
func (s *SelfHealTest) ExpectRecoveryWithin(timeout time.Duration) {
	if s.err != nil {
		return
	}

	s.recoveryTimeout = timeout
	s.err = s.ctx.WaitReadyTimeout(s.resource, timeout)
}

// ScaleTest provides a fluent API for scaling tests.
type ScaleTest struct {
	ctx            *Context
	resource       string
	deploymentName string
	err            error
}

// TestScaling runs a scaling test on a deployment.
func (c *Context) TestScaling(resource string, fn func(*ScaleTest)) error {
	parts := strings.SplitN(resource, "/", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "deployment" {
		return fmt.Errorf("TestScaling requires deployment/name format")
	}

	test := &ScaleTest{
		ctx:            c,
		resource:       resource,
		deploymentName: parts[1],
	}

	fn(test)
	return test.err
}

// ScaleTo scales the deployment to n replicas.
func (s *ScaleTest) ScaleTo(n int) {
	if s.err != nil {
		return
	}

	deploy, err := s.ctx.Client.AppsV1().Deployments(s.ctx.Namespace).Get(
		context.Background(), s.deploymentName, metav1.GetOptions{})
	if err != nil {
		s.err = fmt.Errorf("failed to get deployment: %w", err)
		return
	}

	replicas := int32(n)
	deploy.Spec.Replicas = &replicas

	_, err = s.ctx.Client.AppsV1().Deployments(s.ctx.Namespace).Update(
		context.Background(), deploy, metav1.UpdateOptions{})
	if err != nil {
		s.err = fmt.Errorf("failed to scale deployment: %w", err)
	}
}

// WaitStable waits for the deployment to be stable at current replica count.
func (s *ScaleTest) WaitStable() {
	if s.err != nil {
		return
	}

	s.err = s.ctx.WaitReadyTimeout(s.resource, 60*time.Second)
}

// ============================================================================
// Traffic Testing
// ============================================================================

// TrafficConfig configures traffic generation.
type TrafficConfig struct {
	rps      int
	duration time.Duration
	endpoint string
}

// RPS sets the requests per second.
func (t *TrafficConfig) RPS(n int) {
	t.rps = n
}

// Duration sets how long to generate traffic.
func (t *TrafficConfig) Duration(d time.Duration) {
	t.duration = d
}

// Endpoint sets the HTTP endpoint to hit.
func (t *TrafficConfig) Endpoint(path string) {
	t.endpoint = path
}

// Traffic represents an ongoing or completed traffic test.
type Traffic struct {
	ctx       *Context
	pf        *PortForward
	config    TrafficConfig
	done      chan struct{}
	mu        sync.Mutex
	total     int64
	errors    int64
	latencies []time.Duration
}

// StartTraffic starts generating HTTP traffic to a service.
func (c *Context) StartTraffic(resource string, fn func(*TrafficConfig)) *Traffic {
	config := TrafficConfig{
		rps:      10,
		duration: 10 * time.Second,
		endpoint: "/",
	}
	fn(&config)

	// Set up port forward
	pf := c.Forward(resource, 80)

	traffic := &Traffic{
		ctx:       c,
		pf:        pf,
		config:    config,
		done:      make(chan struct{}),
		latencies: make([]time.Duration, 0, config.rps*int(config.duration.Seconds())),
	}

	// Start traffic generation in background
	go traffic.run()

	return traffic
}

// run generates traffic in the background.
func (t *Traffic) run() {
	defer close(t.done)
	defer t.pf.Close()

	if t.pf.err != nil {
		return
	}

	interval := time.Duration(float64(time.Second) / float64(t.config.rps))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	deadline := time.Now().Add(t.config.duration)
	client := &http.Client{Timeout: 5 * time.Second}

	for time.Now().Before(deadline) {
		<-ticker.C

		start := time.Now()
		resp, err := client.Get(fmt.Sprintf("http://localhost:%d%s", t.pf.localPort, t.config.endpoint))
		latency := time.Since(start)

		t.mu.Lock()
		t.total++
		t.latencies = append(t.latencies, latency)
		if err != nil || (resp != nil && resp.StatusCode >= 400) {
			t.errors++
		}
		t.mu.Unlock()

		if resp != nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}
}

// Wait blocks until traffic generation completes.
func (t *Traffic) Wait() {
	<-t.done
}

// Stop stops traffic generation early.
func (t *Traffic) Stop() {
	t.pf.Close()
	<-t.done
}

// TotalRequests returns the total number of requests made.
func (t *Traffic) TotalRequests() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.total
}

// ErrorRate returns the fraction of failed requests (0.0 to 1.0).
func (t *Traffic) ErrorRate() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.total == 0 {
		return 0
	}
	return float64(t.errors) / float64(t.total)
}

// P99Latency returns the 99th percentile latency.
func (t *Traffic) P99Latency() time.Duration {
	t.mu.Lock()
	if len(t.latencies) == 0 {
		t.mu.Unlock()
		return 0
	}
	// Copy the slice while holding the lock
	latenciesCopy := make([]time.Duration, len(t.latencies))
	copy(latenciesCopy, t.latencies)
	t.mu.Unlock()

	// Sort the copy
	sort.Slice(latenciesCopy, func(i, j int) bool {
		return latenciesCopy[i] < latenciesCopy[j]
	})

	// Get 99th percentile index
	idx := int(float64(len(latenciesCopy)) * 0.99)
	if idx >= len(latenciesCopy) {
		idx = len(latenciesCopy) - 1
	}
	return latenciesCopy[idx]
}
