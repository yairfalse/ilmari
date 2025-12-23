package ilmari

import (
	"fmt"
	"strings"
)

// ============================================================================
// Error Helpers - Provide actionable remediation hints
// ============================================================================

// ResourceFormatError is returned when a resource string is malformed.
type ResourceFormatError struct {
	Input    string
	Expected string
}

func (e *ResourceFormatError) Error() string {
	return fmt.Sprintf("invalid resource format %q\n"+
		"  Expected: %s\n"+
		"  Examples: \"deployment/myapp\", \"pod/nginx-xyz\", \"svc/api\"",
		e.Input, e.Expected)
}

// newResourceFormatError creates an error for invalid resource format.
func newResourceFormatError(input string) error {
	return &ResourceFormatError{
		Input:    input,
		Expected: "kind/name",
	}
}

// ResourceNotFoundError provides hints when a resource doesn't exist.
type ResourceNotFoundError struct {
	Kind      string
	Name      string
	Namespace string
}

func (e *ResourceNotFoundError) Error() string {
	return fmt.Sprintf("%s %q not found in namespace %q\n"+
		"  → Verify the resource exists: kubectl get %s %s -n %s\n"+
		"  → Check for typos in the name\n"+
		"  → Ensure you're in the correct namespace",
		e.Kind, e.Name, e.Namespace, e.Kind, e.Name, e.Namespace)
}

// KubeconfigError provides hints for kubeconfig issues.
type KubeconfigError struct {
	Underlying error
}

func (e *KubeconfigError) Error() string {
	return fmt.Sprintf("failed to load kubeconfig: %v\n"+
		"  → Set KUBECONFIG environment variable\n"+
		"  → Or ensure ~/.kube/config exists\n"+
		"  → For in-cluster: verify ServiceAccount permissions",
		e.Underlying)
}

func (e *KubeconfigError) Unwrap() error {
	return e.Underlying
}

// UnsupportedKindError is returned when an operation doesn't support a resource kind.
type UnsupportedKindError struct {
	Operation      string
	Kind           string
	SupportedKinds []string
}

func (e *UnsupportedKindError) Error() string {
	return fmt.Sprintf("%s does not support kind %q\n"+
		"  Supported: %s",
		e.Operation, e.Kind, strings.Join(e.SupportedKinds, ", "))
}

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
