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

### Stack Builder

Deploy multi-service stacks with a fluent API:

```go
stack := ilmari.NewStack().
    Service("api").Image("myapp:v1").Port(8080).Replicas(2).
    Service("db").Image("postgres:15").Port(5432).
    Service("worker").Image("myapp:v1").Command("worker").
    Build()

ctx.Up(stack) // Deploys all and waits for ready
```

### Isolated Namespaces

Each test runs in its own namespace. Cleaned up on success, kept for debugging on failure.

```go
ilmari.Run(t, func(ctx *ilmari.Context) {
    // ctx.Namespace = "ilmari-test-a1b2c3d4"
})
```

Set `ILMARI_KEEP_ALL=true` to keep namespaces after all tests (for debugging).

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

Supported: Pod, Deployment, StatefulSet, DaemonSet, ConfigMap, Secret, Service.

### Wait Operations

```go
// Wait for ready (60s default)
ctx.WaitReady("pod/myapp")
ctx.WaitReady("deployment/myapp")

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
logs, _ := ctx.Logs("my-pod")
events, _ := ctx.Events()
output, _ := ctx.Exec("my-pod", []string{"cat", "/etc/config"})
```

### Failure Output

On test failure, ilmari dumps diagnostics with kubectl hints:

```
--- Ilmari Diagnostics ---
Namespace: ilmari-test-a1b2c3d4 (kept for debugging)

--- Pods ---
  myapp-xyz: ImagePullBackOff
  --- Logs (myapp-xyz) ---
    Error: connection refused

--- Events ---
  myapp-xyz Pulling: Pulling image "myapp:test"
  myapp-xyz Failed: Failed to pull image

--- kubectl commands ---
  kubectl get pods -n ilmari-test-a1b2c3d4
  kubectl describe pods -n ilmari-test-a1b2c3d4
  kubectl logs -n ilmari-test-a1b2c3d4 <pod-name>

--- End Diagnostics ---
```

## Requirements

- Go 1.21+
- Kubernetes cluster (kind, minikube, or real cluster)
- Valid kubeconfig (`~/.kube/config`)

## Running Tests

```bash
# Run all tests
go test -v

# Skip integration tests
go test -short

# Keep all namespaces for debugging
ILMARI_KEEP_ALL=true go test -v
```

## Family

| Language | Library | K8s Client |
|----------|---------|------------|
| Rust | [seppo](https://github.com/yairfalse/seppo) | kube-rs |
| Go | **ilmari** | client-go |

Seppo and Ilmari are both smith gods in Finnish mythology (Kalevala).

## License

Apache-2.0
