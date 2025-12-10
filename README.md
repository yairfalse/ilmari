# Ilmari

Go Kubernetes SDK. Native library. No config files. Just code.

```go
// Standalone usage - no test framework required
ctx, _ := ilmari.NewContext()
defer ctx.Close()

ctx.Apply(myDeployment)
ctx.WaitReady("deployment/myapp")

pf := ctx.Forward("svc/myapp", 8080)
defer pf.Close()
resp, _ := pf.Get("/health")
```

Like client-go gives you API access, ilmari gives you operations.

## Install

```bash
go get github.com/yairfalse/ilmari
```

## Usage Modes

### Standalone SDK

```go
// Create context with isolated namespace (auto-cleaned on Close)
ctx, err := ilmari.NewContext()
defer ctx.Close()

// Or use an existing namespace (not deleted on Close)
ctx, err := ilmari.NewContext(ilmari.WithNamespace("production"))
defer ctx.Close()

// Or isolated with custom prefix
ctx, err := ilmari.NewContext(ilmari.WithIsolatedNamespace("myapp"))
// Creates "myapp-a1b2c3d4", deleted on Close()
```

### Test Integration

```go
func TestMyController(t *testing.T) {
    ilmari.Run(t, func(ctx *ilmari.Context) {
        ctx.Apply(myDeployment)
        ctx.WaitReady("deployment/myapp")
        // Namespace auto-cleaned on success, kept on failure
    })
}
```

## Features

### Resource Operations

```go
// Apply (create or update)
ctx.Apply(deployment)
ctx.Apply(configMap)

// Get
pod := &corev1.Pod{}
ctx.Get("my-pod", pod)

// Delete
ctx.Delete("my-pod", &corev1.Pod{})

// List
pods := &corev1.PodList{}
ctx.List(pods)
```

### Stack Builder

Deploy multi-service stacks:

```go
stack := ilmari.NewStack().
    Service("api").Image("myapp:v1").Port(8080).Replicas(2).
    Service("db").Image("postgres:15").Port(5432).
    Build()

ctx.Up(stack) // Deploys all and waits for ready
```

### Fluent Deployment Builder

```go
deploy := ilmari.Deployment("myapp").
    Image("nginx:alpine").
    Replicas(3).
    Port(80).
    WithProbes().
    Build()

ctx.Apply(deploy)
```

### Wait Operations

```go
ctx.WaitReady("deployment/myapp")
ctx.WaitReadyTimeout("pod/myapp", 2*time.Minute)

// Custom condition
ctx.WaitFor("pod/myapp", func(obj interface{}) bool {
    pod := obj.(*corev1.Pod)
    return pod.Status.PodIP != ""
})

// Flakiness protection
ctx.Eventually(func() bool {
    return checkCondition()
}).Within(30 * time.Second).Wait()

ctx.Consistently(func() bool {
    return staysTrue()
}).For(10 * time.Second).Wait()
```

### Port Forwarding & Exec

```go
pf := ctx.Forward("svc/myapp", 8080)
defer pf.Close()
resp, _ := pf.Get("/health")

output, _ := ctx.Exec("my-pod", []string{"cat", "/etc/config"})
logs, _ := ctx.Logs("my-pod")
```

### Assertions

```go
err := ctx.Assert("deployment/myapp").
    HasReplicas(3).
    IsProgressing().
    Error()

ctx.Assert("pod/myapp").HasNoRestarts().NoOOMKills().Must()
```

### Chaos Testing

```go
ctx.Kill("pod/myapp-xyz")
ctx.Isolate(map[string]string{"app": "myapp"}) // Network isolation
```

## Namespace Policies

| Mode | Creation | Cleanup | Use Case |
|------|----------|---------|----------|
| `NewContext()` | Creates `ilmari-xxxxx` | Deleted on `Close()` | Default, isolated |
| `WithNamespace("prod")` | Uses existing | Never deleted | Production, shared |
| `WithIsolatedNamespace("test")` | Creates `test-xxxxx` | Deleted on `Close()` | Custom prefix |

## Requirements

- Go 1.21+
- Kubernetes cluster (kind, minikube, or real cluster)
- Valid kubeconfig (`~/.kube/config`)

## Family

| Language | Library | K8s Client |
|----------|---------|------------|
| Rust | [seppo](https://github.com/yairfalse/seppo) | kube-rs |
| Go | **ilmari** | client-go |

Seppo and Ilmari are both smith gods in Finnish mythology (Kalevala).
Same concepts, native implementations.

## License

Apache-2.0
