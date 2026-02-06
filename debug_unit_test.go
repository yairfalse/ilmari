package ilmari

import (
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/stretchr/testify/assert"
)

func TestFormatAge_Zero(t *testing.T) {
	result := formatAge(time.Time{})
	assert.Equal(t, "     ", result)
}

func TestFormatAge_Seconds(t *testing.T) {
	result := formatAge(time.Now().Add(-30 * time.Second))
	assert.Contains(t, result, "s")
	assert.NotContains(t, result, "m")
}

func TestFormatAge_Minutes(t *testing.T) {
	result := formatAge(time.Now().Add(-5 * time.Minute))
	assert.Contains(t, result, "m")
}

func TestFormatAge_Hours(t *testing.T) {
	result := formatAge(time.Now().Add(-3 * time.Hour))
	assert.Contains(t, result, "h")
}

func TestFormatAge_Days(t *testing.T) {
	result := formatAge(time.Now().Add(-48 * time.Hour))
	assert.Contains(t, result, "d")
}

func TestDeploymentConditionsToGeneric(t *testing.T) {
	conditions := []appsv1.DeploymentCondition{
		{
			Type:    appsv1.DeploymentAvailable,
			Status:  corev1.ConditionTrue,
			Message: "has minimum availability",
		},
		{
			Type:    appsv1.DeploymentProgressing,
			Status:  corev1.ConditionFalse,
			Message: "deadline exceeded",
		},
	}

	result := deploymentConditionsToGeneric(conditions)
	assert.Len(t, result, 2)
	assert.Equal(t, "Available", result[0].Type)
	assert.Equal(t, "True", result[0].Status)
	assert.Equal(t, "has minimum availability", result[0].Message)
	assert.Equal(t, "Progressing", result[1].Type)
	assert.Equal(t, "False", result[1].Status)
}

func TestDeploymentConditionsToGeneric_Empty(t *testing.T) {
	result := deploymentConditionsToGeneric(nil)
	assert.Len(t, result, 0)
}

func TestPrintConditions_OnlyNonTrue(t *testing.T) {
	var buf strings.Builder
	conditions := []genericCondition{
		{Type: "Available", Status: "True", Message: "ok"},
		{Type: "Progressing", Status: "False", Message: "deadline exceeded"},
		{Type: "Ready", Status: "Unknown", Message: "checking"},
	}

	printConditions(&buf, conditions)
	output := buf.String()
	// Should NOT print the "True" condition
	assert.NotContains(t, output, "Available")
	// Should print non-True conditions
	assert.Contains(t, output, "Progressing")
	assert.Contains(t, output, "deadline exceeded")
	assert.Contains(t, output, "Ready")
	assert.Contains(t, output, "checking")
}

func TestPrintConditions_AllTrue(t *testing.T) {
	var buf strings.Builder
	conditions := []genericCondition{
		{Type: "Available", Status: "True", Message: "ok"},
		{Type: "Progressing", Status: "True", Message: "ok"},
	}
	printConditions(&buf, conditions)
	assert.Empty(t, buf.String())
}

func TestPrintConditions_Empty(t *testing.T) {
	var buf strings.Builder
	printConditions(&buf, nil)
	assert.Empty(t, buf.String())
}
