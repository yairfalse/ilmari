# Ilmari: Go Kubernetes Testing Library

**Native library. No config files. Just code.**

---

## WHAT IS ILMARI

Ilmari connects your Go tests to Kubernetes. That's it.

```go
func TestMyController(t *testing.T) {
    ilmari.Run(t, func(ctx *ilmari.Context) {
        ctx.Apply(myDeployment)
        ctx.WaitReady("deployment/myapp")

        resp := ctx.Forward("svc/myapp", 8080).Get("/health")
        assert.Equal(t, 200, resp.StatusCode)
    })
}
```

```bash
go test
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
3. **Works with go test** - Not a test runner. Enhances existing tests.
4. **client-go powered** - Native K8s client, not kubectl shelling.
5. **Failure-friendly** - Keep resources on failure, dump logs, debug easily.

---

## API

### Context

```go
type Context struct {
    Client    *kubernetes.Clientset
    Namespace string
}

// Resource operations
func (c *Context) Apply(obj runtime.Object) error
func (c *Context) Get(gvk schema.GroupVersionKind, name string) (runtime.Object, error)
func (c *Context) Delete(gvk schema.GroupVersionKind, name string) error
func (c *Context) List(gvk schema.GroupVersionKind) ([]runtime.Object, error)

// Waiting
func (c *Context) WaitReady(resource string) error
func (c *Context) WaitFor(resource string, condition func(obj runtime.Object) bool) error

// Diagnostics
func (c *Context) Logs(pod string) (string, error)
func (c *Context) Events() ([]corev1.Event, error)

// Network
func (c *Context) Forward(svc string, port int) *PortForward
func (c *Context) Exec(pod string, cmd []string) (string, error)
```

### Test Runner

```go
func Run(t *testing.T, fn func(ctx *Context))
func RunWithConfig(t *testing.T, cfg Config, fn func(ctx *Context))
```

### Failure Handling

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

## CURRENT STATE

Skeleton - not yet implemented.

---

## TDD WORKFLOW

```bash
# RED: Write failing test
go test -run TestContextCreatesNamespace  # FAILS

# GREEN: Minimal implementation
go test -run TestContextCreatesNamespace  # PASSES

# REFACTOR: Clean up
go test  # ALL PASS

# COMMIT
git commit -m "feat: Context creates isolated namespace"
```

**Small commits. Always green.**

---

**Connect to K8s. Run your tests. That's Ilmari.**
