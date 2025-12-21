# Ilmari Alignment Plan

**Goal:** Align ilmari (Go) with seppo (Rust) APIs for consistent "K8s as a library" experience.

---

## 1. API Renames

### PortForward (not Forward)

```go
// BEFORE
pf := ctx.Forward("svc/api", 8080)

// AFTER
pf := ctx.PortForward("svc/api", 8080)
```

**Files to change:**
- `portforward.go` - rename method
- `context.go` - if method defined there
- All tests using Forward

---

## 2. Typed Assertions

### Replace unified Assert with typed methods

```go
// BEFORE
ctx.Assert("deployment/api").HasReplicas(3).Must()

// AFTER
ctx.AssertPod("api-xyz").HasNoRestarts().Must()
ctx.AssertDeployment("api").HasReplicas(3).IsReady().Must()
ctx.AssertService("api").Exists().Must()
ctx.AssertPVC("data").IsBound().HasCapacity("10Gi").Must()
```

**Methods per type:**

| Type | Methods |
|------|---------|
| `AssertPod()` | `Exists`, `IsReady`, `HasNoRestarts`, `NoOOMKills`, `LogsContain`, `HasLabel` |
| `AssertDeployment()` | `Exists`, `HasReplicas`, `IsReady`, `IsProgressing`, `HasLabel` |
| `AssertService()` | `Exists`, `HasLabel` |
| `AssertPVC()` | `Exists`, `IsBound`, `HasStorageClass`, `HasCapacity` |

**Files to change:**
- `assertions.go` - add typed assertion structs
- Keep `Assert()` for backward compat, but add typed versions

---

## 3. Add Metrics()

### Simple CPU/memory access

```go
metrics, err := ctx.Metrics("pod/api-xyz")

fmt.Println(metrics.CPU)           // "250m"
fmt.Println(metrics.Memory)        // "512Mi"
fmt.Println(metrics.CPUPercent)    // 25.0
fmt.Println(metrics.MemoryPercent) // 50.0
```

**Implementation:**
- Uses metrics-server API
- Returns `PodMetrics` struct
- Calculates percentages from pod resource limits

**Files to create/change:**
- `metrics.go` - new file
- Add `Metrics()` method to Context

---

## 4. Add Debug()

### Combined diagnostics dump

```go
ctx.Debug("deployment/api")

// Prints:
// === deployment/api ===
// Status: 3/3 ready
//
// === Pods ===
// api-xyz-123: Running (0 restarts)
// api-xyz-456: Running (0 restarts)
//
// === Events ===
// 10:42:01 Scheduled: Assigned to node-1
// 10:42:02 Pulled: Image pulled
//
// === Logs (last 20 lines) ===
// [api-xyz-123] Server started on :8080
// ...
```

**Files to change:**
- `diagnostics.go` - add Debug() method

---

## 5. Better Error Messages

### Human-readable, not K8s gibberish

```go
// BEFORE
"the server could not find the requested resource"

// AFTER
"Deployment 'api' failed: image 'api:v99' not found in registry gcr.io/myproject"
```

**Files to change:**
- `errors.go` - improve WaitError and other errors
- Wrap errors with context throughout

---

## 6. Examples

Create `examples/` directory:

```
examples/
├── basic/
│   └── main.go          # Deploy nginx, port-forward, check health
├── infra-package/
│   ├── main.go          # CLI entrypoint
│   ├── api.go           # API deployment
│   └── database.go      # DB deployment
├── embedded/
│   └── main.go          # Self-deploying app
├── integration-test/
│   └── api_test.go      # ilmari.Run() examples
└── multi-service/
    └── main.go          # Stack with API + DB + Redis
```

---

## 7. README Updates

### Positioning: "Kubernetes as a library"

Key messages:
- No YAML, just code
- Import and use
- Same API as seppo (Rust)
- Type-safe, IDE autocomplete

---

## Verification Checklist

Before each change:
- [ ] `go build ./...` passes
- [ ] `go test ./... -short` passes
- [ ] `go vet ./...` clean
- [ ] `go fmt ./...` applied

---

## Order of Implementation

1. PortForward rename (simple)
2. Typed assertions (medium)
3. Metrics() (medium)
4. Debug() (medium)
5. Error messages (medium)
6. Examples (after APIs stable)
7. README updates (last)

---

## Git Workflow

**NEVER push to main. Always use feature branches + PRs.**

```bash
git checkout -b feat/rename-portforward
# make changes
git push -u origin feat/rename-portforward
gh pr create --title "Rename Forward to PortForward"
```
