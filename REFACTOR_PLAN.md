# Ilmari Refactoring Plan

## Current State

```
context.go      124 KB   4,561 lines   <- MONOLITH
context_test.go  81 KB   3,184 lines   <- Also huge
─────────────────────────────────────────
Total           205 KB   7,745 lines
```

**Problem:** Everything is in one file. Hard to navigate, hard to test in isolation, hard to maintain.

---

## Phase 1: File Split (No Behavior Changes)

Split `context.go` into logical modules. Tests stay in `context_test.go` for now.

### Target Structure

```
ilmari/
├── context.go        # Core: Context, Apply, Get, Delete, List, WaitReady (~800 lines)
├── watch.go          # Watch, WaitDeleted, Patch (~250 lines)
├── portforward.go    # PortForward struct, HTTP methods (~600 lines)
├── builders.go       # Stack, Deployment, Fixture builders (~1,100 lines)
├── operations.go     # Scale, Restart, Rollback, CanI (~250 lines)
├── assertions.go     # Eventually, Consistently, Assert chain (~400 lines)
├── scenarios.go      # TestSelfHealing, TestScaling, Traffic (~500 lines)
├── helpers.go        # Secrets, PVC, RBAC, Logs, Ingress (~700 lines)
├── dynamic.go        # ApplyDynamic, GetDynamic, Helm integration (~250 lines)
├── errors.go         # WaitError, diagnostics formatting (~150 lines)
│
├── context_test.go   # All tests (split later in Phase 2)
├── go.mod
├── go.sum
├── README.md
└── CLAUDE.md
```

### File Breakdown with Rationale

#### `context.go` (~800 lines) - Core
```go
// Core context and CRUD operations
// This is what users import and use first

package ilmari

// Types
type Context struct { ... }
type Config struct { ... }
type ContextOption func(*contextOptions)

// Construction
func NewContext(opts ...ContextOption) (*Context, error)
func Run(t *testing.T, fn func(ctx *Context))
func RunWithConfig(t *testing.T, cfg Config, fn func(ctx *Context))
func (c *Context) Close()

// Options
func WithKubeconfig(path string) ContextOption
func WithNamespace(ns string) ContextOption
func WithIsolatedNamespace(prefix string) ContextOption
func WithTracerProvider(tp trace.TracerProvider) ContextOption

// CRUD (namespace-scoped)
func (c *Context) Apply(obj runtime.Object) error
func (c *Context) Get(name string, obj runtime.Object) error
func (c *Context) Delete(name string, obj runtime.Object) error
func (c *Context) List(list runtime.Object) error

// NEW: Cluster-scoped CRUD
func (c *Context) ApplyCluster(obj runtime.Object) error
func (c *Context) GetCluster(name string, obj runtime.Object) error
func (c *Context) DeleteCluster(name string, obj runtime.Object) error

// Wait
func (c *Context) WaitReady(resource string) error
func (c *Context) WaitReadyTimeout(resource string, timeout time.Duration) error
func (c *Context) WaitFor(resource string, condition func(obj interface{}) bool) error

// Internal
func (c *Context) cleanup() error
func (c *Context) dumpDiagnostics()
func (c *Context) getGVR(obj runtime.Object) (schema.GroupVersionResource, error)
func (c *Context) isClusterScoped(gvr schema.GroupVersionResource) bool  // NEW
```

**Rationale:** This is the entry point. Users should be able to `import "ilmari"` and immediately use `NewContext()`, `Apply()`, `WaitReady()`. Everything else is secondary.

---

#### `watch.go` (~250 lines) - Reactive Operations
```go
// Watch and reactive wait operations

package ilmari

type WatchEvent struct { ... }

func (c *Context) Watch(kind string, callback func(WatchEvent)) func()
func (c *Context) WaitDeleted(resource string) error
func (c *Context) WaitDeletedTimeout(resource string, timeout time.Duration) error
func (c *Context) Patch(resource string, patch []byte, patchType PatchType) error
```

**Rationale:** Watch-based operations are reactive and have different error modes than CRUD. Separating them makes the codebase easier to reason about.

---

#### `portforward.go` (~600 lines) - Network Access
```go
// Port forwarding and HTTP client for testing endpoints

package ilmari

type PortForward struct {
    LocalPort int
    stopChan  chan struct{}
    // ...
}

func (c *Context) Forward(resource string, port int) *PortForward
func (c *Context) MustForward(resource string, port int) *PortForward

func (pf *PortForward) Close()
func (pf *PortForward) URL(path string) string
func (pf *PortForward) Get(path string) (*http.Response, error)
func (pf *PortForward) Post(path, contentType string, body io.Reader) (*http.Response, error)
func (pf *PortForward) Put(path, contentType string, body io.Reader) (*http.Response, error)
func (pf *PortForward) Delete(path string) (*http.Response, error)
func (pf *PortForward) Do(req *http.Request) (*http.Response, error)
```

**Rationale:** Port forwarding is a self-contained subsystem with its own lifecycle (start/stop), goroutines, and error handling. Natural boundary.

---

#### `builders.go` (~1,100 lines) - Resource Construction
```go
// Fluent builders for common K8s resources

package ilmari

// Deployment builder
type DeploymentBuilder struct { ... }
func Deployment(name string) *DeploymentBuilder
func (d *DeploymentBuilder) Image(image string) *DeploymentBuilder
func (d *DeploymentBuilder) Replicas(n int32) *DeploymentBuilder
func (d *DeploymentBuilder) Port(port int32) *DeploymentBuilder
func (d *DeploymentBuilder) Env(key, value string) *DeploymentBuilder
func (d *DeploymentBuilder) Build() *appsv1.Deployment

// Stack builder (multi-service)
type Stack struct { ... }
type ServiceBuilder struct { ... }
func NewStack() *Stack
func (s *Stack) Service(name string) *ServiceBuilder
func (c *Context) Up(stack *Stack) error

// Fixture loading
type FixtureBuilder struct { ... }
func (c *Context) LoadFixture(path string) *FixtureBuilder
func (c *Context) LoadYAML(path string) error
func (c *Context) LoadYAMLDir(dir string) error
```

**Rationale:** Builders are a DSL layer on top of k8s types. They don't touch the cluster directly - they just construct objects. Clear separation of concerns.

---

#### `operations.go` (~250 lines) - Cluster Operations
```go
// High-level cluster operations

package ilmari

func (c *Context) Scale(resource string, replicas int) error
func (c *Context) Restart(resource string) error
func (c *Context) Rollback(resource string) error
func (c *Context) Kill(resource string) error
func (c *Context) Retry(attempts int, fn func() error) error
func (c *Context) CanI(verb, resource string) (bool, error)
```

**Rationale:** These are "do something" operations vs CRUD's "manage state". They have retry logic, multiple API calls, and complex success conditions.

---

#### `assertions.go` (~400 lines) - Test Assertions
```go
// Assertion chains and eventual consistency helpers

package ilmari

// Eventually/Consistently
type EventuallyBuilder struct { ... }
type ConsistentlyBuilder struct { ... }
func (c *Context) Eventually(fn func() bool) *EventuallyBuilder
func (c *Context) Consistently(fn func() bool) *ConsistentlyBuilder

// Fluent assertions
type Assertion struct { ... }
func (c *Context) Assert(resource string) *Assertion
func (a *Assertion) Exists() *Assertion
func (a *Assertion) HasLabel(key, value string) *Assertion
func (a *Assertion) HasReplicas(n int32) *Assertion
func (a *Assertion) LogsContain(text string) *Assertion
func (a *Assertion) Error() error
func (a *Assertion) Must()
```

**Rationale:** Assertion code is test-specific. Not all users need it (operators, scripts). Keeping it separate allows potential build tag exclusion.

---

#### `scenarios.go` (~500 lines) - Test Scenarios
```go
// Canned test scenarios for common patterns

package ilmari

// Self-healing test
type SelfHealTest struct { ... }
func (c *Context) TestSelfHealing(resource string, fn func(*SelfHealTest)) error

// Scale test
type ScaleTest struct { ... }
func (c *Context) TestScaling(resource string, fn func(*ScaleTest)) error

// Traffic test
type Traffic struct { ... }
type TrafficConfig struct { ... }
func (c *Context) StartTraffic(resource string, fn func(*TrafficConfig)) *Traffic
func (t *Traffic) Wait()
func (t *Traffic) ErrorRate() float64
func (t *Traffic) P99Latency() time.Duration
```

**Rationale:** These are higher-level test patterns built on primitives. Power users only. Could be a separate package in the future.

---

#### `helpers.go` (~700 lines) - Convenience Functions
```go
// Helper functions for common tasks

package ilmari

// Logs
func (c *Context) Logs(pod string) (string, error)
func (c *Context) LogsStream(pod string, fn func(line string)) func()
func (c *Context) LogsAll(selector string) (map[string]string, error)
func (c *Context) Events() ([]corev1.Event, error)

// Exec/Copy
func (c *Context) Exec(pod string, cmd []string) (string, error)
func (c *Context) CopyTo(pod, localPath, remotePath string) error
func (c *Context) CopyFrom(pod, remotePath, localPath string) error

// Secrets
func (c *Context) SecretFromFile(name string, files map[string]string) error
func (c *Context) SecretFromEnv(name string, keys ...string) error
func (c *Context) SecretTLS(name, certPath, keyPath string) error

// PVC
func (c *Context) WaitPVCBound(resource string) error

// RBAC
type RBACBundle struct { ... }
type RBACBuilder struct { ... }
func ServiceAccount(name string) *RBACBuilder
func (c *Context) ApplyRBAC(bundle *RBACBundle) error

// Network Policy
func (c *Context) Isolate(selector map[string]string) error
func (c *Context) AllowFrom(target, source map[string]string) error

// Ingress
type IngressTest struct { ... }
func (c *Context) TestIngress(name string) *IngressTest
```

**Rationale:** These are convenience wrappers. Users could do this manually with `Apply()` but these save boilerplate. Grouping them keeps the core API clean.

---

#### `dynamic.go` (~200 lines) - CRDs
```go
// Dynamic client operations for CRDs and unstructured resources

package ilmari

func (c *Context) ApplyDynamic(gvr schema.GroupVersionResource, obj map[string]interface{}) error
func (c *Context) GetDynamic(gvr schema.GroupVersionResource, name string) (map[string]interface{}, error)
func (c *Context) DeleteDynamic(gvr schema.GroupVersionResource, name string) error
func (c *Context) ListDynamic(gvr schema.GroupVersionResource) ([]map[string]interface{}, error)

// NEW: Cluster-scoped dynamic operations
func (c *Context) ApplyDynamicCluster(gvr schema.GroupVersionResource, obj map[string]interface{}) error
func (c *Context) GetDynamicCluster(gvr schema.GroupVersionResource, name string) (map[string]interface{}, error)
```

**Rationale:** Dynamic client is a different API than typed client. CRD users need this, but most users don't. Keeps complexity isolated.

---

#### `errors.go` (~150 lines) - Error Types
```go
// Rich error types with diagnostics

package ilmari

type WaitError struct {
    Resource string
    Expected string
    Actual   string
    Pods     []PodStatus
    Events   []string
    Hint     string
}

type PodStatus struct { ... }

func (e *WaitError) Error() string
func (c *Context) buildWaitError(resource, kind, name string) *WaitError
```

**Rationale:** Error formatting is complex (multi-line, hints, pod status). Isolating it makes the main code cleaner and errors easier to enhance.

---

## Phase 2: Missing Features

### 2.1 Cluster-Scoped Resource Support

**Problem:** All operations use `Namespace(c.Namespace)` which fails for GatewayClass, ClusterRole, etc.

**Solution:** Add scope detection and cluster-scoped methods.

```go
// context.go

// isClusterScoped checks if a GVR is cluster-scoped using REST mapper
func (c *Context) isClusterScoped(gvr schema.GroupVersionResource) bool {
    mapping, err := c.mapper.RESTMapping(
        schema.GroupKind{Group: gvr.Group, Kind: gvr.Resource},
        gvr.Version,
    )
    if err != nil {
        return false
    }
    return mapping.Scope.Name() == meta.RESTScopeNameRoot
}

// ApplyCluster creates or updates a cluster-scoped resource
func (c *Context) ApplyCluster(obj runtime.Object) error {
    gvr, err := c.getGVR(obj)
    if err != nil {
        return err
    }

    u, err := toUnstructured(obj)
    if err != nil {
        return err
    }
    // Don't set namespace for cluster-scoped resources

    ctx := context.Background()
    _, err = c.Dynamic.Resource(gvr).Create(ctx, u, metav1.CreateOptions{})
    if apierrors.IsAlreadyExists(err) {
        existing, getErr := c.Dynamic.Resource(gvr).Get(ctx, u.GetName(), metav1.GetOptions{})
        if getErr != nil {
            return getErr
        }
        u.SetResourceVersion(existing.GetResourceVersion())
        _, err = c.Dynamic.Resource(gvr).Update(ctx, u, metav1.UpdateOptions{})
    }
    return err
}

// Alternative: Auto-detect scope in Apply()
func (c *Context) Apply(obj runtime.Object) error {
    gvr, err := c.getGVR(obj)
    if err != nil {
        return err
    }

    u, err := toUnstructured(obj)
    if err != nil {
        return err
    }

    ctx := context.Background()

    // Auto-detect: use cluster or namespaced client
    var client dynamic.ResourceInterface
    if c.isClusterScoped(gvr) {
        client = c.Dynamic.Resource(gvr)
    } else {
        u.SetNamespace(c.Namespace)
        client = c.Dynamic.Resource(gvr).Namespace(c.Namespace)
    }

    _, err = client.Create(ctx, u, metav1.CreateOptions{})
    // ... rest of logic
}
```

**Rationale:** Gateway API testing requires GatewayClass (cluster-scoped). Without this, neither Seppo nor Ilmari can test RAUTA properly.

---

### 2.2 CRD Support in WaitReady

**Problem:** `getResource()` and `isReady()` only handle built-in types.

**Solution:** Use dynamic client with condition checking for unknown types.

```go
// context.go

func (c *Context) getResource(kind, name string) (interface{}, error) {
    ctx := context.Background()

    switch kind {
    case "pod":
        return c.Client.CoreV1().Pods(c.Namespace).Get(ctx, name, metav1.GetOptions{})
    case "deployment":
        return c.Client.AppsV1().Deployments(c.Namespace).Get(ctx, name, metav1.GetOptions{})
    // ... existing cases ...

    default:
        // NEW: Try dynamic client for CRDs
        return c.getResourceDynamic(kind, name)
    }
}

func (c *Context) getResourceDynamic(kind, name string) (interface{}, error) {
    // Map kind to GVR (needs discovery)
    gvr, err := c.kindToGVR(kind)
    if err != nil {
        return nil, fmt.Errorf("unknown kind %q: %w", kind, err)
    }

    var client dynamic.ResourceInterface
    if c.isClusterScoped(gvr) {
        client = c.Dynamic.Resource(gvr)
    } else {
        client = c.Dynamic.Resource(gvr).Namespace(c.Namespace)
    }

    return client.Get(context.Background(), name, metav1.GetOptions{})
}

func (c *Context) isReady(kind, name string) (bool, error) {
    // ... existing switch cases ...

    default:
        // NEW: Check status.conditions for CRDs
        obj, err := c.getResourceDynamic(kind, name)
        if err != nil {
            return false, err
        }
        return c.checkConditions(obj)
    }
}

// checkConditions looks for Ready/Accepted/Available conditions
func (c *Context) checkConditions(obj interface{}) bool {
    u, ok := obj.(*unstructured.Unstructured)
    if !ok {
        return false
    }

    conditions, found, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
    if !found {
        return false
    }

    for _, cond := range conditions {
        c, ok := cond.(map[string]interface{})
        if !ok {
            continue
        }

        condType, _ := c["type"].(string)
        status, _ := c["status"].(string)

        // Check for common "ready" conditions
        if (condType == "Ready" || condType == "Accepted" || condType == "Available" || condType == "Programmed") &&
           status == "True" {
            return true
        }
    }
    return false
}
```

**Rationale:** Gateway API uses `Accepted` and `Programmed` conditions. Without this, `WaitReady("gateway/my-gw")` fails.

---

### 2.3 Kind-to-GVR Discovery

**Problem:** `WaitReady("gatewayclass/rauta")` needs to map "gatewayclass" → GVR.

**Solution:** Use discovery API to find GVR from kind.

```go
// dynamic.go

// kindToGVR discovers the GVR for a given kind name
func (c *Context) kindToGVR(kind string) (schema.GroupVersionResource, error) {
    // Normalize kind (gatewayclass -> GatewayClass)
    kind = strings.ToLower(kind)

    // Check common mappings first (fast path)
    if gvr, ok := commonKindMappings[kind]; ok {
        return gvr, nil
    }

    // Discovery: find all API resources and match by kind
    _, apiResources, err := c.Client.Discovery().ServerGroupsAndResources()
    if err != nil {
        return schema.GroupVersionResource{}, err
    }

    for _, list := range apiResources {
        gv, _ := schema.ParseGroupVersion(list.GroupVersion)
        for _, r := range list.APIResources {
            if strings.ToLower(r.Kind) == kind || strings.ToLower(r.Name) == kind {
                return schema.GroupVersionResource{
                    Group:    gv.Group,
                    Version:  gv.Version,
                    Resource: r.Name,
                }, nil
            }
        }
    }

    return schema.GroupVersionResource{}, fmt.Errorf("kind %q not found", kind)
}

var commonKindMappings = map[string]schema.GroupVersionResource{
    "gatewayclass": {Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gatewayclasses"},
    "gateway":      {Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"},
    "httproute":    {Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"},
    // Add more as needed
}
```

**Rationale:** Users shouldn't need to know GVRs. `WaitReady("gateway/my-gw")` is much better than `WaitReadyGVR(gvr, "my-gw")`.

---

## Phase 3: Test Split (Future)

Split `context_test.go` to match source files:

```
context_test.go      -> Tests for context.go
watch_test.go        -> Tests for watch.go
portforward_test.go  -> Tests for portforward.go
builders_test.go     -> Tests for builders.go
operations_test.go   -> Tests for operations.go
assertions_test.go   -> Tests for assertions.go
scenarios_test.go    -> Tests for scenarios.go
helpers_test.go      -> Tests for helpers.go
dynamic_test.go      -> Tests for dynamic.go
```

**Rationale:** Allows running targeted tests (`go test -run TestPortForward`) and parallel test execution.

---

## Implementation Order

```
Week 1: Phase 1 - File Split (no behavior changes)
├── Create new files with moved code
├── Ensure all tests pass
├── No new features yet
└── PR: "refactor: split context.go into modules"

Week 2: Phase 2.1 - Cluster-Scoped Resources
├── Add isClusterScoped()
├── Add ApplyCluster/GetCluster/DeleteCluster
├── OR: Auto-detect in Apply()
├── Add tests with GatewayClass
└── PR: "feat: cluster-scoped resource support"

Week 3: Phase 2.2 - CRD WaitReady
├── Add kindToGVR discovery
├── Add checkConditions for CRDs
├── Update getResource/isReady fallback
├── Add tests with Gateway API
└── PR: "feat: CRD support in WaitReady"

Week 4: Phase 3 - Test Split (optional)
├── Split context_test.go
├── Add integration test tags
└── PR: "refactor: split test files"
```

---

## Breaking Changes

**None.** All changes are additive:
- Existing `Apply()` continues to work for namespaced resources
- New `ApplyCluster()` added for cluster-scoped
- OR: `Apply()` auto-detects (no API change)

---

## Sync with Seppo

After Ilmari changes, port to Seppo (Rust):

| Ilmari (Go) | Seppo (Rust) |
|-------------|--------------|
| `ApplyCluster()` | `ctx.apply_cluster()` |
| `isClusterScoped()` | `is_cluster_scoped()` |
| `kindToGVR()` | `kind_to_gvr()` |
| `checkConditions()` | `check_conditions()` |

Same API, native implementations.

---

## Summary

| Phase | Scope | Lines Changed | Risk |
|-------|-------|---------------|------|
| 1 | File split | ~0 (just moves) | Low |
| 2.1 | Cluster-scoped | ~100 new | Medium |
| 2.2 | CRD WaitReady | ~150 new | Medium |
| 3 | Test split | ~0 (just moves) | Low |

**Total new code:** ~250 lines
**Total reorganization:** ~4,500 lines moved to proper homes

The monolith becomes a well-organized library.
