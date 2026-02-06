package ilmari

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestApiGroupForResource_CoreResources(t *testing.T) {
	coreResources := []string{
		"pods", "services", "configmaps", "secrets", "persistentvolumeclaims",
		"serviceaccounts", "namespaces", "nodes", "events", "endpoints",
		"persistentvolumes", "replicationcontrollers", "resourcequotas", "limitranges",
	}
	for _, r := range coreResources {
		t.Run(r, func(t *testing.T) {
			assert.Equal(t, "", apiGroupForResource(r))
		})
	}
}

func TestApiGroupForResource_AppsGroup(t *testing.T) {
	appsResources := []string{
		"deployments", "statefulsets", "daemonsets", "replicasets", "controllerrevisions",
	}
	for _, r := range appsResources {
		t.Run(r, func(t *testing.T) {
			assert.Equal(t, "apps", apiGroupForResource(r))
		})
	}
}

func TestApiGroupForResource_BatchGroup(t *testing.T) {
	assert.Equal(t, "batch", apiGroupForResource("jobs"))
	assert.Equal(t, "batch", apiGroupForResource("cronjobs"))
}

func TestApiGroupForResource_NetworkingGroup(t *testing.T) {
	assert.Equal(t, "networking.k8s.io", apiGroupForResource("ingresses"))
	assert.Equal(t, "networking.k8s.io", apiGroupForResource("networkpolicies"))
}

func TestApiGroupForResource_RBACGroup(t *testing.T) {
	assert.Equal(t, "rbac.authorization.k8s.io", apiGroupForResource("roles"))
	assert.Equal(t, "rbac.authorization.k8s.io", apiGroupForResource("clusterroles"))
}

func TestApiGroupForResource_AutoscalingGroup(t *testing.T) {
	assert.Equal(t, "autoscaling", apiGroupForResource("horizontalpodautoscalers"))
}

func TestApiGroupForResource_PolicyGroup(t *testing.T) {
	assert.Equal(t, "policy", apiGroupForResource("poddisruptionbudgets"))
}

func TestApiGroupForResource_Unknown(t *testing.T) {
	assert.Equal(t, "", apiGroupForResource("unknownresource"))
}

func TestLabelHash(t *testing.T) {
	tests := []struct {
		name     string
		labels   map[string]string
		expected string
	}{
		{
			name:   "short label",
			labels: map[string]string{"a": "b"},
			expected: "ab",
		},
		{
			name:   "long label truncated to 8",
			labels: map[string]string{"verylongkey": "verylongvalue"},
			expected: "verylon",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := labelHash(tt.labels)
			assert.LessOrEqual(t, len(result), 8)
			assert.NotEmpty(t, result)
		})
	}
}

func TestLabelHash_Deterministic(t *testing.T) {
	labels := map[string]string{"app": "test"}
	hash1 := labelHash(labels)
	hash2 := labelHash(labels)
	assert.Equal(t, hash1, hash2)
}

func TestLabelHash_EmptyLabels(t *testing.T) {
	result := labelHash(map[string]string{})
	assert.Equal(t, "", result)
}
