# Ilmari

Go SDK for Kubernetes. Define infrastructure in code, not YAML.

## Why?

We got tired of:
- YAML files scattered everywhere
- Helm charts we can't debug
- kubectl scripts held together with bash
- "Infrastructure as Code" that's actually "Infrastructure as Config Files"

Ilmari lets you work with Kubernetes using actual code. Go code. With types, IDE support, and the full power of a real programming language.

```go
ctx, _ := ilmari.NewContext()
defer ctx.Close()

// This is a Deployment. In Go. With autocomplete.
deploy := ilmari.Deployment("api").
    Image("myapp:v1").
    Replicas(3).
    Port(8080).
    Build()

ctx.Apply(deploy)
ctx.WaitReady("deployment/api")
```

No YAML. No templates. No string interpolation. Just code.

## Install

```bash
go get github.com/yairfalse/ilmari
```

## Examples

### Deploy and verify

```go
ctx, _ := ilmari.NewContext()
defer ctx.Close()

ctx.Apply(myDeployment)
ctx.WaitReady("deployment/myapp")

// Port forward and check health
pf := ctx.Forward("svc/myapp", 8080)
defer pf.Close()
resp, _ := pf.Get("/health")
```

### Integration tests

```go
func TestMyController(t *testing.T) {
    ilmari.Run(t, func(ctx *ilmari.Context) {
        ctx.Apply(myDeployment)
        ctx.WaitReady("deployment/myapp")

        // Test your stuff
        // Namespace auto-cleaned on success, kept on failure for debugging
    })
}
```

### Multi-service stack

```go
stack := ilmari.NewStack().
    Service("api").Image("myapp:v1").Port(8080).Replicas(2).
    Service("db").Image("postgres:15").Port(5432).
    Service("redis").Image("redis:7").Port(6379).
    Build()

ctx.Up(stack) // Deploys all, waits for ready
```

### Work with existing namespaces

```go
// Create isolated namespace (cleaned up on Close)
ctx, _ := ilmari.NewContext()

// Use existing namespace (never deleted)
ctx, _ := ilmari.NewContext(ilmari.WithNamespace("production"))

// Isolated with custom prefix
ctx, _ := ilmari.NewContext(ilmari.WithIsolatedNamespace("mytest"))
// Creates "mytest-a1b2c3d4", deleted on Close()
```

## What you can do

```go
// Resources
ctx.Apply(obj)              // Create or update
ctx.Get("name", &obj)       // Fetch into typed object
ctx.Delete("name", &obj)    // Remove
ctx.List(&objList)          // List all

// Wait for things
ctx.WaitReady("deployment/myapp")
ctx.WaitFor("pod/myapp", func(obj interface{}) bool {
    return obj.(*corev1.Pod).Status.PodIP != ""
})

// Interact with pods
ctx.Forward("svc/myapp", 8080)              // Port forward
ctx.Exec("mypod", []string{"ls", "-la"})    // Run commands
ctx.Logs("mypod")                           // Get logs

// Test helpers
ctx.Eventually(checkFunc).Within(30 * time.Second).Wait()
ctx.Consistently(checkFunc).For(10 * time.Second).Wait()

// Chaos
ctx.Kill("pod/myapp-xyz")
ctx.Isolate(map[string]string{"app": "myapp"})
```

## Philosophy

**Code over config.** YAML is a data format that accidentally became a programming language. We'd rather use an actual programming language.

**Native, not wrapped.** Ilmari uses client-go directly. No shelling out to kubectl, no external dependencies.

**Honest errors.** When something fails, you get real diagnostics—pod states, events, logs—not just "timeout waiting for resource".

## Requirements

- Go 1.21+
- A Kubernetes cluster (kind, minikube, or real)
- kubeconfig (`~/.kube/config`)

## Family

| Language | Library | K8s Client |
|----------|---------|------------|
| Rust | [seppo](https://github.com/yairfalse/seppo) | kube-rs |
| Go | **ilmari** | client-go |

Seppo and Ilmari are smith gods from Finnish mythology (Kalevala). Same ideas, native implementations for each language.

## Status

Early. The API works but will evolve. We use it for our own projects.

## License

Apache-2.0
