# Ilmari: Go Kubernetes SDK

**Native library. No config files. Just code.**

---

## WHAT IS ILMARI

Ilmari connects your Go code to Kubernetes. Tests, scripts, operators, CLI tools.

```go
// Testing
func TestMyController(t *testing.T) {
    ilmari.Run(t, func(ctx *ilmari.Context) {
        ctx.Apply(myDeployment)
        ctx.WaitReady("deployment/myapp")

        resp := ctx.Forward("svc/myapp", 8080).Get("/health")
        assert.Equal(t, 200, resp.StatusCode)
    })
}

// Standalone SDK
func main() {
    ctx, _ := ilmari.NewContext()
    defer ctx.Close()

    ctx.Apply(myDeployment)
    ctx.WaitReady("deployment/myapp")
    ctx.Scale("deployment/myapp", 3)
}
```

Like testify gives you assertions, ilmari gives you Kubernetes.

---

## FAMILY

| Language | Library | K8s Client |
|----------|---------|------------|
| Rust | [seppo](https://github.com/yairfalse/seppo) | kube-rs |
| Go | **ilmari** | client-go |

Seppo and Ilmari are both smith gods in Finnish mythology (Kalevala).
Same concepts, native implementations.

---

## CORE PHILOSOPHY

1. **Native library** - Not a CLI, not a framework. A library.
2. **No config files** - No YAML, no TOML. Just Go code.
3. **client-go powered** - Native K8s client, not kubectl shelling.
4. **Works anywhere** - Tests, scripts, operators, CLI tools.
5. **Failure-friendly** - Keep resources on failure, dump logs, debug easily.

---

## API

### Context

```go
type Context struct {
    Client    *kubernetes.Clientset
    Dynamic   dynamic.Interface
    Namespace string
}

// Creation
func NewContext(opts ...ContextOption) (*Context, error)
func (c *Context) Close()

// Options
func WithNamespace(ns string) ContextOption      // shared, no cleanup
func WithIsolatedNamespace(prefix string) ContextOption  // isolated, auto-cleanup
func WithKubeconfig(path string) ContextOption

// Resource operations
func (c *Context) Apply(obj runtime.Object) error
func (c *Context) Get(name string, obj runtime.Object) error
func (c *Context) Delete(name string, obj runtime.Object) error
func (c *Context) List(list runtime.Object) error
func (c *Context) Patch(resource string, patch []byte, patchType PatchType) error

// Dynamic (for CRDs)
func (c *Context) ApplyDynamic(gvr schema.GroupVersionResource, obj map[string]interface{}) error
func (c *Context) GetDynamic(gvr schema.GroupVersionResource, name string) (map[string]interface{}, error)

// Waiting
func (c *Context) WaitReady(resource string) error
func (c *Context) WaitReadyTimeout(resource string, timeout time.Duration) error
func (c *Context) WaitFor(resource string, condition func(obj interface{}) bool) error
func (c *Context) WaitDeleted(resource string) error

// Eventually/Consistently
func (c *Context) Eventually(fn func() bool) *EventuallyBuilder
func (c *Context) Consistently(fn func() bool) *ConsistentlyBuilder

// Diagnostics
func (c *Context) Logs(pod string) (string, error)
func (c *Context) LogsWithOptions(pod string, opts LogsOptions) (string, error)
func (c *Context) LogsStream(pod string, fn func(line string)) (stop func())
func (c *Context) Events() ([]corev1.Event, error)
func (c *Context) Debug(resource string) error
func (c *Context) DebugWithOptions(resource string, opts DebugOptions) error

// Metrics (requires metrics-server)
func (c *Context) Metrics(pod string) (*PodMetrics, error)
func (c *Context) MetricsDetail(pod string) (*PodMetricsDetail, error)

// Network
func (c *Context) PortForward(svc string, port int) *PortForward
func (c *Context) Exec(pod string, cmd []string) (string, error)
func (c *Context) CopyTo(pod, localPath, remotePath string) error
func (c *Context) CopyFrom(pod, remotePath, localPath string) error

// Operations
func (c *Context) Scale(resource string, replicas int32) error
func (c *Context) Restart(resource string) error
func (c *Context) Rollback(resource string) error
func (c *Context) Kill(resource string) error
func (c *Context) Retry(attempts int, fn func() error) error
func (c *Context) CanI(verb, resource string) (bool, error)

// Network Policy
func (c *Context) Isolate(selector map[string]string) error
func (c *Context) AllowFrom(target, source map[string]string) error

// Watch
func (c *Context) Watch(kind string, fn func(event WatchEvent)) (stop func())

// YAML
func (c *Context) LoadYAML(path string) error
func (c *Context) LoadYAMLDir(dir string) error
func (c *Context) LoadFixture(path string) *FixtureBuilder

// Stack
func (c *Context) Up(stack *Stack) error
```

### PortForward

```go
type PortForward struct {}

func (pf *PortForward) Get(path string) (*http.Response, error)
func (pf *PortForward) Post(path, contentType string, body io.Reader) (*http.Response, error)
func (pf *PortForward) Put(path, contentType string, body io.Reader) (*http.Response, error)
func (pf *PortForward) Delete(path string) (*http.Response, error)
func (pf *PortForward) Do(req *http.Request) (*http.Response, error)
func (pf *PortForward) URL(path string) string
func (pf *PortForward) Close()
```

### Assertions

```go
// Typed assertions (recommended)
func (c *Context) AssertPod(name string) *PodAssertion
func (c *Context) AssertDeployment(name string) *DeploymentAssertion
func (c *Context) AssertService(name string) *ServiceAssertion
func (c *Context) AssertPVC(name string) *PVCAssertion
func (c *Context) AssertStatefulSet(name string) *StatefulSetAssertion

// PodAssertion
func (a *PodAssertion) Exists() *PodAssertion
func (a *PodAssertion) IsReady() *PodAssertion
func (a *PodAssertion) HasNoRestarts() *PodAssertion
func (a *PodAssertion) NoOOMKills() *PodAssertion
func (a *PodAssertion) LogsContain(text string) *PodAssertion
func (a *PodAssertion) HasLabel(key, value string) *PodAssertion
func (a *PodAssertion) HasAnnotation(key, value string) *PodAssertion
func (a *PodAssertion) Error() error
func (a *PodAssertion) Must()

// DeploymentAssertion
func (a *DeploymentAssertion) Exists() *DeploymentAssertion
func (a *DeploymentAssertion) HasReplicas(n int) *DeploymentAssertion
func (a *DeploymentAssertion) IsReady() *DeploymentAssertion
func (a *DeploymentAssertion) IsProgressing() *DeploymentAssertion
func (a *DeploymentAssertion) HasLabel(key, value string) *DeploymentAssertion
func (a *DeploymentAssertion) HasAnnotation(key, value string) *DeploymentAssertion
func (a *DeploymentAssertion) Error() error
func (a *DeploymentAssertion) Must()

// ServiceAssertion
func (a *ServiceAssertion) Exists() *ServiceAssertion
func (a *ServiceAssertion) HasLabel(key, value string) *ServiceAssertion
func (a *ServiceAssertion) HasAnnotation(key, value string) *ServiceAssertion
func (a *ServiceAssertion) HasPort(port int32) *ServiceAssertion
func (a *ServiceAssertion) HasSelector(key, value string) *ServiceAssertion
func (a *ServiceAssertion) Error() error
func (a *ServiceAssertion) Must()

// PVCAssertion
func (a *PVCAssertion) Exists() *PVCAssertion
func (a *PVCAssertion) IsBound() *PVCAssertion
func (a *PVCAssertion) HasStorageClass(class string) *PVCAssertion
func (a *PVCAssertion) HasCapacity(capacity string) *PVCAssertion
func (a *PVCAssertion) HasLabel(key, value string) *PVCAssertion
func (a *PVCAssertion) Error() error
func (a *PVCAssertion) Must()

// StatefulSetAssertion
func (a *StatefulSetAssertion) Exists() *StatefulSetAssertion
func (a *StatefulSetAssertion) HasReplicas(n int) *StatefulSetAssertion
func (a *StatefulSetAssertion) IsReady() *StatefulSetAssertion
func (a *StatefulSetAssertion) HasLabel(key, value string) *StatefulSetAssertion
func (a *StatefulSetAssertion) Error() error
func (a *StatefulSetAssertion) Must()

// Generic assertions (legacy)
func (c *Context) Assert(resource string) *Assertion
func (a *Assertion) Exists() *Assertion
func (a *Assertion) HasLabel(key, value string) *Assertion
func (a *Assertion) Error() error
func (a *Assertion) Must()
```

### Builders

```go
// Deployment builder
func Deployment(name string) *DeploymentBuilder
func (d *DeploymentBuilder) Image(image string) *DeploymentBuilder
func (d *DeploymentBuilder) Replicas(n int32) *DeploymentBuilder
func (d *DeploymentBuilder) Port(port int32) *DeploymentBuilder
func (d *DeploymentBuilder) Env(key, value string) *DeploymentBuilder
func (d *DeploymentBuilder) WithProbes() *DeploymentBuilder
func (d *DeploymentBuilder) Build() *appsv1.Deployment

// Stack builder (multi-service)
func NewStack() *StackBuilder
func (s *StackBuilder) Service(name string) *StackBuilder
func (s *StackBuilder) Image(image string) *StackBuilder
func (s *StackBuilder) Port(port int32) *StackBuilder
func (s *StackBuilder) Command(cmd ...string) *StackBuilder
func (s *StackBuilder) Resources(cpu, memory string) *StackBuilder
func (s *StackBuilder) Replicas(n int32) *StackBuilder
func (s *StackBuilder) Build() *Stack

// RBAC builder (ServiceAccount + Role + RoleBinding)
func ServiceAccount(name string) *RBACBuilder
func (r *RBACBuilder) WithRole(name string) *RBACBuilder
func (r *RBACBuilder) CanGet(resources ...string) *RBACBuilder
func (r *RBACBuilder) CanList(resources ...string) *RBACBuilder
func (r *RBACBuilder) CanWatch(resources ...string) *RBACBuilder
func (r *RBACBuilder) CanCreate(resources ...string) *RBACBuilder
func (r *RBACBuilder) CanUpdate(resources ...string) *RBACBuilder
func (r *RBACBuilder) CanDelete(resources ...string) *RBACBuilder
func (r *RBACBuilder) CanAll(resources ...string) *RBACBuilder
func (r *RBACBuilder) Build() *RBACBundle
func (c *Context) ApplyRBAC(bundle *RBACBundle) error
```

### Secret Helpers

```go
func (c *Context) SecretFromFile(name string, files map[string]string) error
func (c *Context) SecretFromEnv(name string, keys ...string) error
func (c *Context) SecretTLS(name, certPath, keyPath string) error
```

### PVC Helpers

```go
func (c *Context) WaitPVCBound(resource string) error
func (c *Context) WaitPVCBoundTimeout(resource string, timeout time.Duration) error
```

### Log Aggregation

```go
func (c *Context) LogsAll(selector string) (map[string]string, error)
func (c *Context) LogsAllWithOptions(selector string, opts LogsOptions) (map[string]string, error)

type LogsOptions struct {
    Container string
    Since     time.Duration
    TailLines int64
}
```

### Ingress Testing

```go
func (c *Context) TestIngress(name string) *IngressTest
func (i *IngressTest) Host(host string) *IngressTest
func (i *IngressTest) Path(path string) *IngressTest
func (i *IngressTest) ExpectBackend(svc string, port int) *IngressTest
func (i *IngressTest) ExpectTLS(secretName string) *IngressTest
func (i *IngressTest) Error() error
func (i *IngressTest) Must()
```

### Test Scenarios

```go
func (c *Context) TestSelfHealing(resource string, fn func(s *SelfHealTest)) error
func (c *Context) TestScaling(resource string, fn func(s *ScaleTest)) error
func (c *Context) StartTraffic(svc string, fn func(t *TrafficConfig)) *Traffic
```

### Test Runner

```go
func Run(t *testing.T, fn func(ctx *Context))
func RunWithConfig(t *testing.T, cfg Config, fn func(ctx *Context))
```

---

## USE CASES

**Testing** - Integration tests against real K8s
```go
ilmari.Run(t, func(ctx *ilmari.Context) { ... })
```

**Scripts** - Automation and deployment scripts
```go
ctx, _ := ilmari.NewContext(ilmari.WithNamespace("prod"))
```

**CLI Tools** - kubectl alternatives in Go
```go
ctx, _ := ilmari.NewContext()
ctx.Apply(manifest)
```

**Operators** - Controller runtime helpers
```go
ctx, _ := ilmari.NewContext(ilmari.WithNamespace(req.Namespace))
```

---

## FAILURE HANDLING (Test Mode)

On test **success**:
- Cleanup namespace
- Delete resources

On test **failure**:
- Keep namespace alive
- Dump pod logs
- Dump events
- Print resource state

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
```

---

**Connect to K8s. Run your code. That's Ilmari.**
