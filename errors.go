package ilmari

import (
	"fmt"
	"strings"
)

// WaitError provides rich diagnostic information when a wait operation fails.
type WaitError struct {
	Resource string
	Expected string
	Actual   string
	Pods     []PodStatus
	Events   []string
	Hint     string
}

// PodStatus represents a pod's status for diagnostics.
type PodStatus struct {
	Name   string
	Phase  string
	Reason string
	Ready  bool
}

// Error implements the error interface with rich formatting.
func (e *WaitError) Error() string {
	var b strings.Builder
	b.WriteString("\n━━━ WAIT TIMEOUT ━━━\n")
	b.WriteString(fmt.Sprintf("Resource: %s\n", e.Resource))
	if e.Expected != "" {
		b.WriteString(fmt.Sprintf("Expected: %s\n", e.Expected))
	}
	if e.Actual != "" {
		b.WriteString(fmt.Sprintf("Actual:   %s\n", e.Actual))
	}

	if len(e.Pods) > 0 {
		b.WriteString("\nPods:\n")
		for _, p := range e.Pods {
			readyStr := ""
			if p.Ready {
				readyStr = " (Ready)"
			}
			if p.Reason != "" {
				b.WriteString(fmt.Sprintf("  %s  %s → %s%s\n", p.Name, p.Phase, p.Reason, readyStr))
			} else {
				b.WriteString(fmt.Sprintf("  %s  %s%s\n", p.Name, p.Phase, readyStr))
			}
		}
	}

	if len(e.Events) > 0 {
		b.WriteString("\nEvents:\n")
		for _, ev := range e.Events {
			b.WriteString(fmt.Sprintf("  %s\n", ev))
		}
	}

	if e.Hint != "" {
		b.WriteString(fmt.Sprintf("\nHint: %s\n", e.Hint))
	}

	b.WriteString("━━━━━━━━━━━━━━━━━━━\n")
	return b.String()
}
