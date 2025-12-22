# Ilmari

Go SDK for Kubernetes. Define infrastructure in code, not YAML.

## Quick Start

```go
package main

import "github.com/yairfalse/ilmari"

func main() {
    ctx, _ := ilmari.NewContext()
    defer ctx.Close()

    // Deploy nginx
    deploy := ilmari.Deployment("nginx").
        Image("nginx:latest").
        Port(80).
        Replicas(2).
        Build()

    ctx.Apply(deploy)
    ctx.WaitReady("deployment/nginx")

    // Port forward and test
    pf := ctx.PortForward("deployment/nginx", 80)
    defer pf.Close()
    resp, _ := pf.Get("/")
    fmt.Println(resp.StatusCode) // 200
}
```

## Install

```bash
go get github.com/yairfalse/ilmari
```

Requires Go 1.21+, a Kubernetes cluster, and a kubeconfig.

## Why Ilmari?

We got tired of:
- YAML files scattered everywhere
- Helm charts we can't debug
- kubectl scripts held together with bash
- "Infrastructure as Code" that's actually "Infrastructure as Config Files"

Ilmari gives you real code. Go code. With types, IDE support, and the full power of a programming language.

## Use Cases

### Integration Testing

```go
func TestMyApp(t *testing.T) {
    ilmari.Run(t, func(ctx *ilmari.Context) {
        ctx.Apply(myDeployment)
        ctx.WaitReady("deployment/myapp")

        pf := ctx.PortForward("svc/myapp", 8080)
        resp, _ := pf.Get("/health")
        assert.Equal(t, 200, resp.StatusCode)

        // Namespace auto-cleaned on success
        // Kept with diagnostics on failure
    })
}
```

### Scripts & Automation

```go
ctx, _ := ilmari.NewContext(ilmari.WithNamespace("production"))
defer ctx.Close()

ctx.Scale("deployment/api", 5)
ctx.WaitReady("deployment/api")
```

### Operators & Controllers

```go
ctx, _ := ilmari.NewContext(ilmari.WithNamespace(req.Namespace))
ctx.Apply(generatedResources)
ctx.WaitReady("deployment/" + name)
```

## Core API

### Context

```go
// Isolated namespace (auto-cleanup)
ctx, _ := ilmari.NewContext()

// Existing namespace (no cleanup)
ctx, _ := ilmari.NewContext(ilmari.WithNamespace("prod"))

// Custom prefix for isolation
ctx, _ := ilmari.NewContext(ilmari.WithIsolatedNamespace("test"))
// Creates "test-a1b2c3d4", deleted on Close()

defer ctx.Close()
```

### Resources

```go
ctx.Apply(obj)              // Create or update
ctx.Get("name", &obj)       // Fetch into typed object
ctx.Delete("name", &obj)    // Remove
ctx.List(&objList)          // List all in namespace
ctx.Patch("kind/name", patch, patchType)
```

### Waiting

```go
ctx.WaitReady("deployment/myapp")
ctx.WaitReady("pod/mypod")
ctx.WaitReady("statefulset/mydb")

ctx.WaitFor("pod/myapp", func(obj interface{}) bool {
    return obj.(*corev1.Pod).Status.PodIP != ""
})

ctx.WaitDeleted("pod/old-pod")

// Eventual consistency
ctx.Eventually(func() bool {
    return checkSomething()
}).Within(30 * time.Second).Wait()

// Stability check
ctx.Consistently(func() bool {
    return stillHealthy()
}).For(10 * time.Second).Wait()
```

### Pod Interaction

```go
// Logs
logs, _ := ctx.Logs("mypod")
ctx.LogsStream("mypod", func(line string) {
    fmt.Println(line)
})

// Execute commands
output, _ := ctx.Exec("mypod", []string{"ls", "-la"})

// Copy files
ctx.CopyTo("mypod", "local.txt", "/tmp/remote.txt")
ctx.CopyFrom("mypod", "/tmp/remote.txt", "local.txt")

// Events
events, _ := ctx.Events()
```

### Port Forwarding

```go
pf := ctx.PortForward("svc/myapp", 8080)
defer pf.Close()

// HTTP methods
resp, _ := pf.Get("/health")
resp, _ := pf.Post("/api", "application/json", body)
resp, _ := pf.Put("/api/item", "application/json", body)
resp, _ := pf.Delete("/api/item/123")

// Custom requests
req, _ := http.NewRequest("PATCH", pf.URL("/api"), body)
resp, _ := pf.Do(req)
```

### Operations

```go
ctx.Scale("deployment/api", 5)
ctx.Restart("deployment/api")    // Rolling restart
ctx.Rollback("deployment/api")   // Undo last rollout
ctx.Kill("pod/myapp-xyz")        // Delete pod

ctx.Retry(3, func() error {
    return somethingFlaky()
})

allowed, _ := ctx.CanI("create", "pods")
```

## Builders

### Deployment

```go
deploy := ilmari.Deployment("api").
    Image("myapp:v1").
    Replicas(3).
    Port(8080).
    Env("LOG_LEVEL", "debug").
    WithProbes().
    Build()

ctx.Apply(deploy)
```

### Stack (Multi-Service)

```go
stack := ilmari.NewStack().
    Service("api").
        Image("myapp:v1").
        Port(8080).
        Replicas(2).
        Env("DB_HOST", "db").
    Service("db").
        Image("postgres:15").
        Port(5432).
    Service("redis").
        Image("redis:7").
        Port(6379).
    Build()

ctx.Up(stack)  // Deploys all, waits for ready
```

### RBAC

```go
rbac := ilmari.ServiceAccount("my-sa").
    WithRole("my-role").
    CanGet("pods", "services").
    CanList("deployments").
    CanCreate("configmaps").
    CanAll("secrets").  // get, list, watch, create, update, delete
    Build()

ctx.ApplyRBAC(rbac)
```

## Secrets

```go
// From files
ctx.SecretFromFile("certs", map[string]string{
    "ca.crt":  "/path/to/ca.crt",
    "tls.crt": "/path/to/tls.crt",
})

// From environment
ctx.SecretFromEnv("api-keys", "API_KEY", "API_SECRET")

// TLS secret
ctx.SecretTLS("my-tls", "cert.pem", "key.pem")
```

## Assertions

### Typed Assertions (Recommended)

```go
// Pod assertions
ctx.AssertPod("api-xyz").
    Exists().
    IsReady().
    HasNoRestarts().
    NoOOMKills().
    LogsContain("Server started").
    HasLabel("app", "api").
    Must()

// Deployment assertions
ctx.AssertDeployment("api").
    Exists().
    HasReplicas(3).
    IsReady().
    IsProgressing().
    HasLabel("app", "api").
    Must()

// Service assertions
ctx.AssertService("api").
    Exists().
    HasPort(8080).
    HasSelector("app", "api").
    Must()

// PVC assertions
ctx.AssertPVC("data").
    Exists().
    IsBound().
    HasStorageClass("standard").
    HasCapacity("10Gi").
    Must()

// StatefulSet assertions
ctx.AssertStatefulSet("db").
    Exists().
    HasReplicas(3).
    IsReady().
    Must()
```

### Generic Assertions

```go
ctx.Assert("deployment/api").
    Exists().
    HasLabel("app", "api").
    HasReplicas(3).
    IsProgressing().
    Must()

ctx.Assert("pod/api-xyz").
    HasNoRestarts().
    NoOOMKills().
    LogsContain("Server started").
    Must()
```

## Network Policy

```go
// Isolate pods (deny all ingress)
ctx.Isolate(map[string]string{"app": "db"})

// Allow specific traffic
ctx.AllowFrom(
    map[string]string{"app": "db"},      // target
    map[string]string{"app": "api"},     // source
)
```

## Ingress Testing

```go
ctx.TestIngress("my-ingress").
    Host("api.example.com").
    Path("/v1").
    ExpectBackend("api-svc", 8080).
    ExpectTLS("api-tls-secret").
    Must()
```

## Log Aggregation

```go
// All pods matching selector
logs, _ := ctx.LogsAll("app=myapp")
for pod, log := range logs {
    fmt.Printf("=== %s ===\n%s\n", pod, log)
}

// With options
logs, _ := ctx.LogsAllWithOptions("app=myapp", ilmari.LogsOptions{
    Container: "main",
    Since:     5 * time.Minute,
    TailLines: 100,
})
```

## YAML Loading

```go
// Single file
ctx.LoadYAML("manifests/deploy.yaml")

// Directory
ctx.LoadYAMLDir("manifests/")

// With overrides
deploy, _ := ctx.LoadFixture("fixtures/base.yaml").
    WithImage("myapp:test").
    WithReplicas(1).
    Build()
```

## Helm

```go
objects, _ := ilmari.FromHelm("./charts/myapp", "release-name", map[string]interface{}{
    "replicaCount": 3,
    "image": map[string]interface{}{
        "tag": "v1.2.3",
    },
})

for _, obj := range objects {
    ctx.Apply(obj)
}
```

## Test Scenarios

```go
// Self-healing test
ctx.TestSelfHealing("deployment/api", func(s *ilmari.SelfHealTest) {
    s.KillPod()
    s.ExpectRecoveryWithin(30 * time.Second)
})

// Scaling test
ctx.TestScaling("deployment/api", func(s *ilmari.ScaleTest) {
    s.ScaleTo(5)
    s.WaitStable()
})

// Traffic test
traffic := ctx.StartTraffic("svc/api", func(t *ilmari.TrafficConfig) {
    t.RPS(100)
    t.Duration(1 * time.Minute)
    t.Endpoint("/health")
})
traffic.Wait()
fmt.Printf("Error rate: %.2f%%\n", traffic.ErrorRate())
fmt.Printf("P99 latency: %v\n", traffic.P99Latency())
```

## Test Failure Diagnostics

When a test fails, ilmari keeps the namespace and dumps diagnostics:

```
FAIL: TestMyController

Namespace: ilmari-test-a1b2c3 (kept for debugging)

--- Pod logs (myapp-xyz) ---
Error: connection refused to database:5432

--- Events ---
0:01 Pod scheduled
0:02 Pulling image myapp:test
0:05 BackOff pulling image

--- Resources ---
deployment/myapp  0/1 ready
pod/myapp-xyz     ImagePullBackOff

--- kubectl commands ---
  kubectl get pods -n ilmari-test-a1b2c3
  kubectl logs -n ilmari-test-a1b2c3 myapp-xyz
```

## Tracing

```go
import "go.opentelemetry.io/otel"

tp := initTracerProvider()  // Your OTEL setup
ctx, _ := ilmari.NewContext(ilmari.WithTracerProvider(tp))

// All operations are now traced
ctx.Apply(deploy)      // Creates span: ilmari.Apply
ctx.WaitReady("...")   // Creates span: ilmari.WaitReady
```

## Dynamic Resources (CRDs)

```go
gvr := schema.GroupVersionResource{
    Group:    "mygroup.io",
    Version:  "v1",
    Resource: "myresources",
}

ctx.ApplyDynamic(gvr, map[string]interface{}{
    "apiVersion": "mygroup.io/v1",
    "kind":       "MyResource",
    "metadata":   map[string]interface{}{"name": "test"},
    "spec":       map[string]interface{}{"foo": "bar"},
})

obj, _ := ctx.GetDynamic(gvr, "test")
```

## Philosophy

**Code over config.** YAML is a data format that accidentally became a programming language. We use a real programming language.

**Native, not wrapped.** Ilmari uses client-go directly. No shelling out to kubectl, no external dependencies.

**Honest errors.** When something fails, you get real diagnostics—pod states, events, logs—not just "timeout waiting for resource".

## Family

| Language | Library | K8s Client |
|----------|---------|------------|
| Rust | [seppo](https://github.com/yairfalse/seppo) | kube-rs |
| Go | **ilmari** | client-go |

Seppo and Ilmari are smith gods from Finnish mythology (Kalevala). Same ideas, native implementations.

## Status

v0.1.0 - The API works. We use it for our own projects.

## License

Apache-2.0
