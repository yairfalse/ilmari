package ilmari

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResourceFormatError_Error(t *testing.T) {
	err := &ResourceFormatError{Input: "badformat", Expected: "kind/name"}
	msg := err.Error()
	assert.Contains(t, msg, "badformat")
	assert.Contains(t, msg, "kind/name")
	assert.Contains(t, msg, "deployment/myapp")
}

func TestResourceFormatError_EmptyInput(t *testing.T) {
	err := &ResourceFormatError{Input: "", Expected: "kind/name"}
	msg := err.Error()
	assert.Contains(t, msg, `""`)
	assert.Contains(t, msg, "kind/name")
}

func TestNewResourceFormatError(t *testing.T) {
	err := newResourceFormatError("oops")
	assert.Error(t, err)

	var rfe *ResourceFormatError
	require.True(t, errors.As(err, &rfe))
	assert.Equal(t, "oops", rfe.Input)
	assert.Equal(t, "kind/name", rfe.Expected)
}

func TestResourceNotFoundError_Error(t *testing.T) {
	err := &ResourceNotFoundError{Kind: "deployment", Name: "myapp", Namespace: "test-ns"}
	msg := err.Error()
	assert.Contains(t, msg, "deployment")
	assert.Contains(t, msg, "myapp")
	assert.Contains(t, msg, "test-ns")
	assert.Contains(t, msg, "kubectl get deployment myapp -n test-ns")
}

func TestKubeconfigError_Error(t *testing.T) {
	underlying := fmt.Errorf("file not found")
	err := &KubeconfigError{Underlying: underlying}
	msg := err.Error()
	assert.Contains(t, msg, "file not found")
	assert.Contains(t, msg, "KUBECONFIG")
	assert.Contains(t, msg, "~/.kube/config")
}

func TestKubeconfigError_Unwrap(t *testing.T) {
	underlying := fmt.Errorf("file not found")
	err := &KubeconfigError{Underlying: underlying}
	assert.Equal(t, underlying, err.Unwrap())
	assert.True(t, errors.Is(err, underlying))
}

func TestUnsupportedKindError_Error(t *testing.T) {
	err := &UnsupportedKindError{
		Operation:      "Scale",
		Kind:           "configmap",
		SupportedKinds: []string{"deployment", "statefulset"},
	}
	msg := err.Error()
	assert.Contains(t, msg, "Scale")
	assert.Contains(t, msg, "configmap")
	assert.Contains(t, msg, "deployment, statefulset")
}

func TestUnsupportedKindError_EmptySupportedKinds(t *testing.T) {
	err := &UnsupportedKindError{
		Operation:      "Test",
		Kind:           "foo",
		SupportedKinds: []string{},
	}
	msg := err.Error()
	assert.Contains(t, msg, "Test")
	assert.Contains(t, msg, "foo")
}

func TestWaitError_Error_Full(t *testing.T) {
	err := &WaitError{
		Resource: "deployment/myapp",
		Expected: "ReadyReplicas >= 3",
		Actual:   "ReadyReplicas = 1",
		Pods: []PodStatus{
			{Name: "myapp-abc", Phase: "Running", Ready: true},
			{Name: "myapp-def", Phase: "Pending", Reason: "ImagePullBackOff"},
		},
		Events: []string{
			"myapp-def Failed: Back-off pulling image",
		},
		Hint: "Image failed to pull",
	}
	msg := err.Error()
	assert.Contains(t, msg, "WAIT TIMEOUT")
	assert.Contains(t, msg, "deployment/myapp")
	assert.Contains(t, msg, "ReadyReplicas >= 3")
	assert.Contains(t, msg, "ReadyReplicas = 1")
	assert.Contains(t, msg, "myapp-abc")
	assert.Contains(t, msg, "(Ready)")
	assert.Contains(t, msg, "ImagePullBackOff")
	assert.Contains(t, msg, "Back-off pulling image")
	assert.Contains(t, msg, "Image failed to pull")
}

func TestWaitError_Error_Minimal(t *testing.T) {
	err := &WaitError{Resource: "pod/test"}
	msg := err.Error()
	assert.Contains(t, msg, "WAIT TIMEOUT")
	assert.Contains(t, msg, "pod/test")
	// Should not contain optional sections
	assert.NotContains(t, msg, "Expected:")
	assert.NotContains(t, msg, "Pods:")
	assert.NotContains(t, msg, "Events:")
	assert.NotContains(t, msg, "Hint:")
}

func TestWaitError_Error_PodWithReason(t *testing.T) {
	err := &WaitError{
		Resource: "pod/test",
		Pods: []PodStatus{
			{Name: "test", Phase: "Pending", Reason: "CrashLoopBackOff"},
		},
	}
	msg := err.Error()
	assert.Contains(t, msg, "Pending → CrashLoopBackOff")
}

func TestWaitError_Error_PodWithoutReason(t *testing.T) {
	err := &WaitError{
		Resource: "pod/test",
		Pods: []PodStatus{
			{Name: "test", Phase: "Running"},
		},
	}
	msg := err.Error()
	assert.Contains(t, msg, "test  Running")
	assert.NotContains(t, msg, "→")
}

func TestWaitError_ImplementsError(t *testing.T) {
	var err error = &WaitError{Resource: "pod/test"}
	assert.Error(t, err)
}

func TestAllErrorTypes_ImplementError(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"ResourceFormatError", &ResourceFormatError{Input: "x", Expected: "y"}},
		{"ResourceNotFoundError", &ResourceNotFoundError{Kind: "pod", Name: "x", Namespace: "ns"}},
		{"KubeconfigError", &KubeconfigError{Underlying: fmt.Errorf("test")}},
		{"UnsupportedKindError", &UnsupportedKindError{Operation: "op", Kind: "k"}},
		{"WaitError", &WaitError{Resource: "pod/x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Error(t, tt.err)
			assert.NotEmpty(t, tt.err.Error())
			// Ensure no error messages contain raw newlines without content
			lines := strings.Split(tt.err.Error(), "\n")
			for _, line := range lines {
				if strings.TrimSpace(line) != "" {
					assert.NotEqual(t, "", strings.TrimSpace(line))
				}
			}
		})
	}
}
