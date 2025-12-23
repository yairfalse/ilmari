# Ilmari: Go Kubernetes SDK

**A learning project for Kubernetes SDK patterns in Go**

---

## PROJECT STATUS

**What's Implemented:**
- Context with namespace management (isolated or shared)
- CRUD: Apply, Get, Delete, List, Patch
- Dynamic client for CRDs (ApplyDynamic, GetDynamic)
- WaitReady, WaitFor, WaitDeleted, WaitPVCBound
- Eventually/Consistently for async conditions
- PortForward with HTTP methods (Get, Post, Put, Delete)
- Logs, LogsStream, Events
- Exec, CopyTo, CopyFrom
- Scale, Restart, Rollback, Kill
- Debug (combined diagnostics)
- Metrics (requires metrics-server)
- Typed assertions (Pod, Deployment, Service, PVC, StatefulSet)
- Test runner with auto-cleanup and failure diagnostics

**Code Stats:**
- ~10,000 lines of Go
- 85+ Context methods
- Comprehensive error types with hints

---

## ARCHITECTURE OVERVIEW

```
┌─────────────────────────────────────────────────────────────┐
│  Your Code                                                  │
│    └── ilmari.Run(t, func(ctx *Context) { ... })           │
│    └── ilmari.NewContext()                                  │
└─────────────────────────────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────┐
│  Context                                                     │
│  ├── client-go Clientset                                    │
│  ├── Dynamic client (for CRDs)                              │
│  ├── Namespace (isolated or shared)                         │
│  └── Cleanup on Close()                                     │
└─────────────────────────────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────┐
│  Kubernetes Cluster                                          │
└─────────────────────────────────────────────────────────────┘
```

---

## CORE API

### Context Creation

```go
// Isolated namespace (auto-cleanup)
ctx, _ := ilmari.NewContext()
defer ctx.Close()

// Existing namespace (no cleanup)
ctx, _ := ilmari.NewContext(ilmari.WithNamespace("prod"))

// Custom prefix
ctx, _ := ilmari.NewContext(ilmari.WithIsolatedNamespace("test"))
// Creates "test-a1b2c3d4", deleted on Close()
```

### Resource Operations

```go
ctx.Apply(obj)                    // Create or update
ctx.Get("name", &obj)             // Fetch into typed object
ctx.Delete("name", &obj)          // Remove
ctx.List(&objList)                // List all in namespace
ctx.Patch("kind/name", patch, patchType)

// Dynamic (for CRDs)
ctx.ApplyDynamic(gvr, unstructured)
ctx.GetDynamic(gvr, "name")
ctx.DeleteDynamic(gvr, "name")
ctx.ListDynamic(gvr)
```

### Waiting

```go
ctx.WaitReady("deployment/myapp")
ctx.WaitReadyTimeout("pod/mypod", 60*time.Second)
ctx.WaitFor("pod/myapp", func(obj interface{}) bool {
    return obj.(*corev1.Pod).Status.PodIP != ""
})
ctx.WaitDeleted("pod/old-pod")
ctx.WaitPVCBound("pvc/data")

// Eventual consistency
ctx.Eventually(func() bool { return checkSomething() }).
    Within(30 * time.Second).Wait()

ctx.Consistently(func() bool { return stillHealthy() }).
    For(10 * time.Second).Wait()
```

### Diagnostics

```go
logs, _ := ctx.Logs("mypod")
ctx.LogsStream("mypod", func(line string) { fmt.Println(line) })
events, _ := ctx.Events()
ctx.Debug("deployment/api")  // Combined: status, pods, events, logs
metrics, _ := ctx.Metrics("pod/api-xyz")
```

### Pod Interaction

```go
output, _ := ctx.Exec("mypod", []string{"ls", "-la"})
ctx.CopyTo("mypod", "local.txt", "/tmp/remote.txt")
ctx.CopyFrom("mypod", "/tmp/remote.txt", "local.txt")
```

### Operations

```go
ctx.Scale("deployment/api", 5)
ctx.Restart("deployment/api")     // Rolling restart
ctx.Rollback("deployment/api")    // Undo last rollout
ctx.Kill("pod/myapp-xyz")         // Delete pod
allowed, _ := ctx.CanI("create", "pods")
```

### Port Forwarding

```go
pf := ctx.PortForward("svc/myapp", 8080)
defer pf.Close()

resp, _ := pf.Get("/health")
resp, _ := pf.Post("/api", "application/json", body)
resp, _ := pf.Put("/api/item", "application/json", body)
resp, _ := pf.Delete("/api/item/123")
```

### Typed Assertions

```go
ctx.AssertPod("api-xyz").
    Exists().
    IsReady().
    HasNoRestarts().
    LogsContain("Server started").
    Must()

ctx.AssertDeployment("api").
    Exists().
    HasReplicas(3).
    IsReady().
    Must()

ctx.AssertService("api").
    Exists().
    HasPort(8080).
    HasSelector("app", "api").
    Must()

ctx.AssertPVC("data").
    Exists().
    IsBound().
    HasStorageClass("standard").
    Must()
```

### Builders

```go
deploy := ilmari.Deployment("api").
    Image("myapp:v1").
    Replicas(3).
    Port(8080).
    Env("LOG_LEVEL", "debug").
    Build()

stack := ilmari.NewStack().
    Service("api").Image("myapp:v1").Port(8080).Replicas(2).
    Service("db").Image("postgres:15").Port(5432).
    Build()

ctx.Up(stack)  // Deploys all, waits for ready
```

### Secrets

```go
ctx.SecretFromFile("certs", map[string]string{
    "ca.crt":  "/path/to/ca.crt",
    "tls.crt": "/path/to/tls.crt",
})
ctx.SecretTLS("my-tls", "cert.pem", "key.pem")
```

---

## FILE LOCATIONS

| What | Where |
|------|-------|
| Context + CRUD | `context.go` |
| Watch + Patch | `watch.go` |
| Port forwarding | `portforward.go` |
| Typed assertions | `assertions.go` |
| Builders | `builders.go` |
| Operations | `operations.go` |
| Helpers | `helpers.go` |
| Dynamic client | `dynamic.go` |
| Metrics | `metrics.go` |
| Debug | `debug.go` |
| Test scenarios | `scenarios.go` |
| Error types | `errors.go` |

---

## TEST RUNNER

```go
func TestMyController(t *testing.T) {
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

### Failure Diagnostics

On test **failure**, ilmari:
- Keeps namespace alive for debugging
- Dumps pod logs
- Dumps events
- Prints resource state
- Shows kubectl commands

---

## AGENT INSTRUCTIONS

When working on this codebase:

1. **Read first** - Understand existing patterns
2. **client-go native** - No kubectl shelling
3. **Error handling** - Use rich error types with hints
4. **Test coverage** - Run `go test ./...`

**This is a learning project** - exploring Kubernetes SDK patterns in Go.
