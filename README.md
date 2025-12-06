# Ilmari

Go Kubernetes testing library. Native library. No config files. Just code.

```go
func TestMyController(t *testing.T) {
    ilmari.Run(t, func(ctx *ilmari.Context) {
        ctx.Apply(myDeployment)
        ctx.WaitReady("deployment/myapp")

        resp := ctx.Forward("svc/myapp", 8080).Get("/health")
        defer resp.Body.Close()
        assert.Equal(t, 200, resp.StatusCode)
    })
}
```

```bash
go test
```

Like testify gives you assertions, ilmari gives you Kubernetes.

## Install

```bash
go get github.com/yairfalse/ilmari
```

## Features

### Isolated Namespaces

Each test runs in its own namespace. Cleaned up on success, kept for debugging on failure.

```go
ilmari.Run(t, func(ctx *ilmari.Context) {
    // ctx.Namespace = "ilmari-test-a1b2c3d4"
    // Automatically created and cleaned up
})
```

### Resource Operations

```go
// Apply (create or update)
ctx.Apply(deployment)
ctx.Apply(configMap)
ctx.Apply(secret)

// Get
pod := &corev1.Pod{}
ctx.Get("my-pod", pod)

// Delete
ctx.Delete("my-pod", &corev1.Pod{})

// List
pods := &corev1.PodList{}
ctx.List(pods)
```

Supported types: Pod, Deployment, StatefulSet, DaemonSet, ConfigMap, Secret, Service.

### Wait Operations

```go
// Wait for ready (60s default timeout)
ctx.WaitReady("pod/myapp")
ctx.WaitReady("deployment/myapp")
ctx.WaitReady("statefulset/myapp")
ctx.WaitReady("daemonset/myapp")

// Custom timeout
ctx.WaitReadyTimeout("pod/myapp", 2*time.Minute)

// Custom condition
ctx.WaitFor("pod/myapp", func(obj interface{}) bool {
    pod := obj.(*corev1.Pod)
    return pod.Status.PodIP != ""
})
```

### Port Forwarding

```go
pf := ctx.Forward("svc/myapp", 8080)
defer pf.Close()

resp, err := pf.Get("/health")
defer resp.Body.Close()
```

### Diagnostics

```go
// Get pod logs
logs, err := ctx.Logs("my-pod")

// Get namespace events
events, err := ctx.Events()

// Execute command in pod
output, err := ctx.Exec("my-pod", []string{"cat", "/etc/config"})
```

### Failure Handling

On test failure, ilmari keeps the namespace and dumps diagnostics:

```
FAIL: TestMyController

--- Ilmari Diagnostics ---
Namespace: ilmari-test-a1b2c3d4 (kept for debugging)

--- Pods ---
  myapp-xyz: ImagePullBackOff
  --- Logs (myapp-xyz) ---
    Error: connection refused to database:5432

--- Events ---
  myapp-xyz Scheduled: Successfully assigned...
  myapp-xyz Pulling: Pulling image "myapp:test"
  myapp-xyz Failed: Failed to pull image...

--- End Diagnostics ---
```

## Requirements

- Go 1.21+
- Kubernetes cluster (kind, minikube, or real cluster)
- Valid kubeconfig (`~/.kube/config`)

## Running Tests

```bash
# Run all tests (requires cluster)
go test -v

# Skip integration tests
go test -short
```

## Family

| Language | Library | K8s Client |
|----------|---------|------------|
| Rust | [seppo](https://github.com/yairfalse/seppo) | kube-rs |
| Go | **ilmari** | client-go |

Seppo and Ilmari are both smith gods in Finnish mythology (Kalevala).

## License

Apache-2.0
