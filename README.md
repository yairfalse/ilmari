# Ilmari

Go Kubernetes testing library. Connect to K8s, run your tests.

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

## Family

| Language | Library | K8s Client |
|----------|---------|------------|
| Rust | [seppo](https://github.com/yairfalse/seppo) | kube-rs |
| Go | **ilmari** | client-go |

Seppo and Ilmari are both smith gods in Finnish mythology (Kalevala).

## Status

Early development. See [CLAUDE.md](CLAUDE.md) for the roadmap.

## License

Apache-2.0
