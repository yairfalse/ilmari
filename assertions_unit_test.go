package ilmari

import (
	"testing"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ============================================================================
// objectLabels / objectAnnotations
// ============================================================================

func TestObjectLabels_Pod_(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web"}},
	}
	labels, err := objectLabels(pod)
	assert.NoError(t, err)
	assert.Equal(t, "web", labels["app"])
}

func TestObjectLabels_Deployment_(t *testing.T) {
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"tier": "backend"}},
	}
	labels, err := objectLabels(deploy)
	assert.NoError(t, err)
	assert.Equal(t, "backend", labels["tier"])
}

func TestObjectLabels_NonK8sObject_(t *testing.T) {
	_, err := objectLabels("not a k8s object")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not support metadata access")
}

func TestObjectAnnotations_Service_(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"note": "test"}},
	}
	annotations, err := objectAnnotations(svc)
	assert.NoError(t, err)
	assert.Equal(t, "test", annotations["note"])
}

func TestObjectAnnotations_NilAnnotations_(t *testing.T) {
	pod := &corev1.Pod{}
	annotations, err := objectAnnotations(pod)
	assert.NoError(t, err)
	assert.Nil(t, annotations)
}

// ============================================================================
// checkLabel / checkAnnotation
// ============================================================================

func TestCheckLabel_Match_(t *testing.T) {
	labels := map[string]string{"app": "web", "env": "prod"}
	err := checkLabel(labels, "app", "web", "test resource")
	assert.NoError(t, err)
}

func TestCheckLabel_Mismatch_(t *testing.T) {
	labels := map[string]string{"app": "web"}
	err := checkLabel(labels, "app", "api", "pod myapp")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "pod myapp")
	assert.Contains(t, err.Error(), "expected label app=api")
	assert.Contains(t, err.Error(), "got app=web")
}

func TestCheckLabel_Missing_(t *testing.T) {
	labels := map[string]string{}
	err := checkLabel(labels, "app", "web", "resource")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "got app=")
}

func TestCheckAnnotation_Match_(t *testing.T) {
	annotations := map[string]string{"note": "test"}
	err := checkAnnotation(annotations, "note", "test", "resource")
	assert.NoError(t, err)
}

func TestCheckAnnotation_Mismatch_(t *testing.T) {
	annotations := map[string]string{"note": "old"}
	err := checkAnnotation(annotations, "note", "new", "svc myapp")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "svc myapp")
}

// ============================================================================
// checkPodNoRestarts
// ============================================================================

func TestCheckPodNoRestarts_NoRestarts_(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "main", RestartCount: 0},
			},
		},
	}
	err := checkPodNoRestarts(pod, "pod test")
	assert.NoError(t, err)
}

func TestCheckPodNoRestarts_HasRestarts_(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "main", RestartCount: 3},
			},
		},
	}
	err := checkPodNoRestarts(pod, "pod test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "container main has 3 restarts")
}

func TestCheckPodNoRestarts_EmptyStatuses_(t *testing.T) {
	pod := &corev1.Pod{}
	err := checkPodNoRestarts(pod, "pod test")
	assert.NoError(t, err)
}

// ============================================================================
// checkPodNoOOMKills
// ============================================================================

func TestCheckPodNoOOMKills_NoOOM_(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "main"},
			},
		},
	}
	err := checkPodNoOOMKills(pod, "pod test")
	assert.NoError(t, err)
}

func TestCheckPodNoOOMKills_LastTerminationOOM_(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "main",
					LastTerminationState: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled"},
					},
				},
			},
		},
	}
	err := checkPodNoOOMKills(pod, "pod test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "container main was OOMKilled")
}

func TestCheckPodNoOOMKills_CurrentStateOOM_(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "main",
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled"},
					},
				},
			},
		},
	}
	err := checkPodNoOOMKills(pod, "pod test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "container main was OOMKilled")
}

func TestCheckPodNoOOMKills_NonOOMTermination_(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "main",
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{Reason: "Completed"},
					},
				},
			},
		},
	}
	err := checkPodNoOOMKills(pod, "pod test")
	assert.NoError(t, err)
}

// ============================================================================
// checkDeploymentProgressing
// ============================================================================

func TestCheckDeploymentProgressing_True_(t *testing.T) {
	deploy := &appsv1.Deployment{
		Status: appsv1.DeploymentStatus{
			Conditions: []appsv1.DeploymentCondition{
				{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionTrue},
			},
		},
	}
	err := checkDeploymentProgressing(deploy, "Deployment test")
	assert.NoError(t, err)
}

func TestCheckDeploymentProgressing_False_(t *testing.T) {
	deploy := &appsv1.Deployment{
		Status: appsv1.DeploymentStatus{
			Conditions: []appsv1.DeploymentCondition{
				{
					Type:    appsv1.DeploymentProgressing,
					Status:  corev1.ConditionFalse,
					Message: "deadline exceeded",
				},
			},
		},
	}
	err := checkDeploymentProgressing(deploy, "Deployment test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not progressing")
	assert.Contains(t, err.Error(), "deadline exceeded")
}

func TestCheckDeploymentProgressing_NoCondition_(t *testing.T) {
	deploy := &appsv1.Deployment{
		Status: appsv1.DeploymentStatus{},
	}
	err := checkDeploymentProgressing(deploy, "Deployment test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "has no Progressing condition")
}
