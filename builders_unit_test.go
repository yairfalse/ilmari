package ilmari

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeploymentBuilder_Basic(t *testing.T) {
	deploy := Deployment("myapp").
		Image("nginx:latest").
		Build()

	assert.Equal(t, "myapp", deploy.Name)
	assert.Equal(t, int32(1), *deploy.Spec.Replicas)
	require.Len(t, deploy.Spec.Template.Spec.Containers, 1)
	assert.Equal(t, "nginx:latest", deploy.Spec.Template.Spec.Containers[0].Image)
	assert.Equal(t, "myapp", deploy.Spec.Template.Spec.Containers[0].Name)
}

func TestDeploymentBuilder_Replicas(t *testing.T) {
	deploy := Deployment("myapp").
		Image("nginx:latest").
		Replicas(3).
		Build()

	assert.Equal(t, int32(3), *deploy.Spec.Replicas)
}

func TestDeploymentBuilder_Port(t *testing.T) {
	deploy := Deployment("myapp").
		Image("nginx:latest").
		Port(8080).
		Build()

	require.Len(t, deploy.Spec.Template.Spec.Containers[0].Ports, 1)
	assert.Equal(t, int32(8080), deploy.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort)
}

func TestDeploymentBuilder_NoPort(t *testing.T) {
	deploy := Deployment("myapp").
		Image("nginx:latest").
		Build()

	assert.Nil(t, deploy.Spec.Template.Spec.Containers[0].Ports)
}

func TestDeploymentBuilder_Env(t *testing.T) {
	deploy := Deployment("myapp").
		Image("nginx:latest").
		Env("LOG_LEVEL", "debug").
		Env("PORT", "8080").
		Build()

	envVars := deploy.Spec.Template.Spec.Containers[0].Env
	assert.Len(t, envVars, 2)
	// Sorted alphabetically
	assert.Equal(t, "LOG_LEVEL", envVars[0].Name)
	assert.Equal(t, "debug", envVars[0].Value)
	assert.Equal(t, "PORT", envVars[1].Name)
	assert.Equal(t, "8080", envVars[1].Value)
}

func TestDeploymentBuilder_Command(t *testing.T) {
	deploy := Deployment("myapp").
		Image("nginx:latest").
		Command("sh", "-c", "echo hello").
		Build()

	assert.Equal(t, []string{"sh", "-c", "echo hello"}, deploy.Spec.Template.Spec.Containers[0].Command)
}

func TestDeploymentBuilder_WithProbes(t *testing.T) {
	deploy := Deployment("myapp").
		Image("nginx:latest").
		Port(8080).
		WithProbes().
		Build()

	container := deploy.Spec.Template.Spec.Containers[0]
	require.NotNil(t, container.LivenessProbe)
	require.NotNil(t, container.ReadinessProbe)
	assert.Equal(t, int32(8080), container.LivenessProbe.TCPSocket.Port.IntVal)
	assert.Equal(t, int32(8080), container.ReadinessProbe.TCPSocket.Port.IntVal)
}

func TestDeploymentBuilder_WithProbes_NoPort(t *testing.T) {
	deploy := Deployment("myapp").
		Image("nginx:latest").
		WithProbes().
		Build()

	container := deploy.Spec.Template.Spec.Containers[0]
	// No probes when no port
	assert.Nil(t, container.LivenessProbe)
	assert.Nil(t, container.ReadinessProbe)
}

func TestDeploymentBuilder_Labels(t *testing.T) {
	deploy := Deployment("myapp").
		Image("nginx:latest").
		Build()

	// Selector and template labels should match
	assert.Equal(t, map[string]string{"app": "myapp"}, deploy.Spec.Selector.MatchLabels)
	assert.Equal(t, map[string]string{"app": "myapp"}, deploy.Spec.Template.Labels)
}

func TestStack_SingleService(t *testing.T) {
	stack := NewStack().
		Service("api").Image("myapp:v1").Port(8080).Replicas(2).
		Build()

	require.Len(t, stack.services, 1)
	assert.Equal(t, "api", stack.services[0].name)
	assert.Equal(t, "myapp:v1", stack.services[0].image)
	assert.Equal(t, int32(8080), stack.services[0].port)
	assert.Equal(t, int32(2), stack.services[0].replicas)
}

func TestStack_MultipleServices(t *testing.T) {
	stack := NewStack().
		Service("api").Image("myapp:v1").Port(8080).
		Service("db").Image("postgres:15").Port(5432).
		Build()

	require.Len(t, stack.services, 2)
	assert.Equal(t, "api", stack.services[0].name)
	assert.Equal(t, "db", stack.services[1].name)
}

func TestStack_DefaultReplicas(t *testing.T) {
	stack := NewStack().
		Service("api").Image("myapp:v1").
		Build()

	assert.Equal(t, int32(1), stack.services[0].replicas)
}

func TestServiceBuilder_Env(t *testing.T) {
	stack := NewStack().
		Service("api").Image("myapp:v1").Env("DB_HOST", "localhost").
		Build()

	assert.Equal(t, "localhost", stack.services[0].env["DB_HOST"])
}

func TestServiceBuilder_Command(t *testing.T) {
	stack := NewStack().
		Service("api").Image("myapp:v1").Command("./start.sh", "--dev").
		Build()

	assert.Equal(t, []string{"./start.sh", "--dev"}, stack.services[0].command)
}

func TestServiceBuilder_Resources(t *testing.T) {
	stack := NewStack().
		Service("api").Image("myapp:v1").Resources("100m", "128Mi").
		Build()

	require.NotNil(t, stack.services[0].resourceLimits)
	assert.Equal(t, "100m", stack.services[0].resourceLimits.Cpu().String())
	assert.Equal(t, "128Mi", stack.services[0].resourceLimits.Memory().String())
}

func TestServiceBuilder_EnvFrom_ConfigMap(t *testing.T) {
	stack := NewStack().
		Service("api").Image("myapp:v1").EnvFrom("configmap/my-config").
		Build()

	require.Len(t, stack.services[0].envFrom, 1)
	require.NotNil(t, stack.services[0].envFrom[0].ConfigMapRef)
	assert.Equal(t, "my-config", stack.services[0].envFrom[0].ConfigMapRef.Name)
}

func TestServiceBuilder_EnvFrom_Secret(t *testing.T) {
	stack := NewStack().
		Service("api").Image("myapp:v1").EnvFrom("secret/my-secret").
		Build()

	require.Len(t, stack.services[0].envFrom, 1)
	require.NotNil(t, stack.services[0].envFrom[0].SecretRef)
	assert.Equal(t, "my-secret", stack.services[0].envFrom[0].SecretRef.Name)
}

func TestServiceBuilder_EnvFrom_InvalidFormat(t *testing.T) {
	stack := NewStack().
		Service("api").Image("myapp:v1").EnvFrom("invalid").
		Build()

	assert.Empty(t, stack.services[0].envFrom)
}

func TestServiceBuilder_EnvFrom_UnknownKind(t *testing.T) {
	stack := NewStack().
		Service("api").Image("myapp:v1").EnvFrom("unknown/thing").
		Build()

	// Unknown kind is silently ignored
	assert.Empty(t, stack.services[0].envFrom)
}

func TestRBACBuilder_Build(t *testing.T) {
	bundle := ServiceAccount("test-sa").
		CanGet("pods", "services").
		CanCreate("configmaps").
		Build()

	require.NotNil(t, bundle.ServiceAccount)
	assert.Equal(t, "test-sa", bundle.ServiceAccount.Name)

	require.NotNil(t, bundle.Role)
	assert.Equal(t, "test-sa-role", bundle.Role.Name)
	assert.True(t, len(bundle.Role.Rules) >= 2) // At least 2 rules (different API groups)

	require.NotNil(t, bundle.RoleBinding)
	assert.Equal(t, "test-sa-binding", bundle.RoleBinding.Name)
	assert.Equal(t, "test-sa", bundle.RoleBinding.Subjects[0].Name)
	assert.Equal(t, "test-sa-role", bundle.RoleBinding.RoleRef.Name)
}

func TestRBACBuilder_WithRole(t *testing.T) {
	bundle := ServiceAccount("test-sa").
		WithRole("custom-role").
		Build()

	assert.Equal(t, "custom-role", bundle.Role.Name)
	assert.Equal(t, "custom-role", bundle.RoleBinding.RoleRef.Name)
}

func TestRBACBuilder_CanAll(t *testing.T) {
	bundle := ServiceAccount("test-sa").
		CanAll("pods").
		Build()

	require.Len(t, bundle.Role.Rules, 1)
	assert.ElementsMatch(t, []string{"get", "list", "watch", "create", "update", "delete"}, bundle.Role.Rules[0].Verbs)
}

func TestRBACBuilder_ForAPIGroup(t *testing.T) {
	bundle := ServiceAccount("test-sa").
		ForAPIGroup("cert-manager.io", []string{"get", "list"}, "certificates", "issuers").
		Build()

	require.Len(t, bundle.Role.Rules, 1)
	assert.Equal(t, []string{"cert-manager.io"}, bundle.Role.Rules[0].APIGroups)
	assert.ElementsMatch(t, []string{"certificates", "issuers"}, bundle.Role.Rules[0].Resources)
}
