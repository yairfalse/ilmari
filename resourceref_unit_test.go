package ilmari

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseResourceRef_Valid(t *testing.T) {
	tests := []struct {
		input    string
		wantKind string
		wantName string
	}{
		{"deployment/myapp", "deployment", "myapp"},
		{"pod/nginx-xyz", "pod", "nginx-xyz"},
		{"svc/api", "svc", "api"},
		{"Deployment/MyApp", "deployment", "MyApp"},
		{"PVC/data", "pvc", "data"},
		{"service/my-svc", "service", "my-svc"},
		{"configmap/my-config", "configmap", "my-config"},
		{"statefulset/db", "statefulset", "db"},
		{"daemonset/monitor", "daemonset", "monitor"},
		{"persistentvolumeclaim/data", "persistentvolumeclaim", "data"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			ref, err := ParseResourceRef(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.wantKind, ref.Kind)
			assert.Equal(t, tt.wantName, ref.Name)
		})
	}
}

func TestParseResourceRef_Invalid(t *testing.T) {
	tests := []string{
		"",
		"noprefix",
		"/noname",
		"noslash",
		"kind/",
		"/name",
	}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			_, err := ParseResourceRef(input)
			assert.Error(t, err)
			var rfe *ResourceFormatError
			assert.True(t, errors.As(err, &rfe))
		})
	}
}

func TestResourceRef_String(t *testing.T) {
	ref := ResourceRef{Kind: "deployment", Name: "myapp"}
	assert.Equal(t, "deployment/myapp", ref.String())
}

func TestResourceRef_RoundTrip(t *testing.T) {
	original := "deployment/myapp"
	ref, err := ParseResourceRef(original)
	require.NoError(t, err)
	assert.Equal(t, original, ref.String())
}

func TestMustParseResourceRef_Valid(t *testing.T) {
	ref := MustParseResourceRef("pod/test")
	assert.Equal(t, "pod", ref.Kind)
	assert.Equal(t, "test", ref.Name)
}

func TestMustParseResourceRef_Panics(t *testing.T) {
	assert.Panics(t, func() {
		MustParseResourceRef("invalid")
	})
}

func TestParseResourceRefWithDefault_WithKind(t *testing.T) {
	ref := ParseResourceRefWithDefault("pod/myapp", "pod")
	assert.Equal(t, "pod", ref.Kind)
	assert.Equal(t, "myapp", ref.Name)
}

func TestParseResourceRefWithDefault_WithoutKind(t *testing.T) {
	ref := ParseResourceRefWithDefault("myapp", "pod")
	assert.Equal(t, "pod", ref.Kind)
	assert.Equal(t, "myapp", ref.Name)
}

func TestParseResourceRefWithDefault_DifferentDefault(t *testing.T) {
	ref := ParseResourceRefWithDefault("data-volume", "pvc")
	assert.Equal(t, "pvc", ref.Kind)
	assert.Equal(t, "data-volume", ref.Name)
}

func TestParseResourceRefWithDefault_PVCPrefix(t *testing.T) {
	ref := ParseResourceRefWithDefault("pvc/data", "pvc")
	assert.Equal(t, "pvc", ref.Kind)
	assert.Equal(t, "data", ref.Name)
}

func TestParseResourceRefWithDefault_PersistentVolumeClaimPrefix(t *testing.T) {
	ref := ParseResourceRefWithDefault("persistentvolumeclaim/data", "pvc")
	assert.Equal(t, "persistentvolumeclaim", ref.Kind)
	assert.Equal(t, "data", ref.Name)
}

func TestParseResourceRefWithDefault_CaseNormalization(t *testing.T) {
	ref := ParseResourceRefWithDefault("Pod/test", "pod")
	assert.Equal(t, "pod", ref.Kind)
	assert.Equal(t, "test", ref.Name)
}
