//go:build ignore

// Ilmari CI pipeline — powered by Sykli.
//
// Run locally:  sykli
// Run in CI:    sykli (via GitHub Actions)
// Dry run:      go run sykli.go --emit | jq

package main

import sykli "github.com/yairfalse/sykli/sdk/go"

func main() {
	s := sykli.New()

	goFiles := []string{"**/*.go", "go.mod", "go.sum"}

	// === PARALLEL CHECKS ===
	checks := s.Parallel("checks",
		s.Task("vet").
			Run("go vet ./...").
			Inputs(goFiles...).
			Intent("static analysis for suspicious constructs").
			OnFail(sykli.OnFailAnalyze),

		s.Task("lint").
			Run("golangci-lint run").
			Inputs(goFiles...).
			Intent("comprehensive linting with golangci-lint").
			OnFail(sykli.OnFailAnalyze),

		s.Task("build").
			Run("go build ./...").
			Inputs(goFiles...).
			Intent("verify compilation").
			Critical(),
	)

	// === UNIT TESTS (after all checks pass) ===
	s.Task("test").
		Run("go test -run 'Test.*_' -v ./...").
		Inputs(goFiles...).
		AfterGroup(checks).
		Covers("**/*.go").
		Intent("unit tests for pure functions (no cluster required)").
		Critical().
		OnFail(sykli.OnFailAnalyze)

	s.Emit()
}
