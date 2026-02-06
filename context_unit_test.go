package ilmari

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestIsPodReady_Running_ReadyCondition(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
		},
	}
	assert.True(t, isPodReady(pod))
}

func TestIsPodReady_Running_NotReady(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionFalse},
			},
		},
	}
	assert.False(t, isPodReady(pod))
}

func TestIsPodReady_Pending(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
		},
	}
	assert.False(t, isPodReady(pod))
}

func TestIsPodReady_Running_NoConditions(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
	assert.False(t, isPodReady(pod))
}

func TestIsPodReady_Succeeded(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Phase: corev1.PodSucceeded,
		},
	}
	assert.False(t, isPodReady(pod))
}

func int32Ptr(i int32) *int32 { return &i }

func TestIsDeploymentReady_AllReady(t *testing.T) {
	deploy := &appsv1.Deployment{
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(3),
		},
		Status: appsv1.DeploymentStatus{
			ReadyReplicas: 3,
		},
	}
	assert.True(t, isDeploymentReady(deploy))
}

func TestIsDeploymentReady_NotAllReady(t *testing.T) {
	deploy := &appsv1.Deployment{
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(3),
		},
		Status: appsv1.DeploymentStatus{
			ReadyReplicas: 2,
		},
	}
	assert.False(t, isDeploymentReady(deploy))
}

func TestIsDeploymentReady_NilReplicas(t *testing.T) {
	deploy := &appsv1.Deployment{
		Spec: appsv1.DeploymentSpec{},
		Status: appsv1.DeploymentStatus{
			ReadyReplicas: 1,
		},
	}
	assert.True(t, isDeploymentReady(deploy))
}

func TestIsDeploymentReady_NilReplicas_ZeroReady(t *testing.T) {
	deploy := &appsv1.Deployment{
		Spec:   appsv1.DeploymentSpec{},
		Status: appsv1.DeploymentStatus{},
	}
	assert.False(t, isDeploymentReady(deploy))
}

func TestIsStatefulSetReady_AllReady(t *testing.T) {
	ss := &appsv1.StatefulSet{
		Spec: appsv1.StatefulSetSpec{
			Replicas: int32Ptr(3),
		},
		Status: appsv1.StatefulSetStatus{
			ReadyReplicas: 3,
		},
	}
	assert.True(t, isStatefulSetReady(ss))
}

func TestIsStatefulSetReady_NotAllReady(t *testing.T) {
	ss := &appsv1.StatefulSet{
		Spec: appsv1.StatefulSetSpec{
			Replicas: int32Ptr(3),
		},
		Status: appsv1.StatefulSetStatus{
			ReadyReplicas: 1,
		},
	}
	assert.False(t, isStatefulSetReady(ss))
}

func TestIsStatefulSetReady_NilReplicas(t *testing.T) {
	ss := &appsv1.StatefulSet{
		Spec: appsv1.StatefulSetSpec{},
		Status: appsv1.StatefulSetStatus{
			ReadyReplicas: 1,
		},
	}
	assert.True(t, isStatefulSetReady(ss))
}

func TestIsDaemonSetReady_Ready(t *testing.T) {
	ds := &appsv1.DaemonSet{
		Status: appsv1.DaemonSetStatus{
			DesiredNumberScheduled: 3,
			NumberReady:            3,
		},
	}
	assert.True(t, isDaemonSetReady(ds))
}

func TestIsDaemonSetReady_NotReady(t *testing.T) {
	ds := &appsv1.DaemonSet{
		Status: appsv1.DaemonSetStatus{
			DesiredNumberScheduled: 3,
			NumberReady:            2,
		},
	}
	assert.False(t, isDaemonSetReady(ds))
}

func TestIsDaemonSetReady_ZeroDesired(t *testing.T) {
	ds := &appsv1.DaemonSet{
		Status: appsv1.DaemonSetStatus{
			DesiredNumberScheduled: 0,
			NumberReady:            0,
		},
	}
	assert.False(t, isDaemonSetReady(ds))
}

func TestCheckConditions_ReadyTrue(t *testing.T) {
	ctx := &Context{}
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{
						"type":   "Ready",
						"status": "True",
					},
				},
			},
		},
	}
	assert.True(t, ctx.checkConditions(obj))
}

func TestCheckConditions_AcceptedTrue(t *testing.T) {
	ctx := &Context{}
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{
						"type":   "Accepted",
						"status": "True",
					},
				},
			},
		},
	}
	assert.True(t, ctx.checkConditions(obj))
}

func TestCheckConditions_ProgrammedTrue(t *testing.T) {
	ctx := &Context{}
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{
						"type":   "Programmed",
						"status": "True",
					},
				},
			},
		},
	}
	assert.True(t, ctx.checkConditions(obj))
}

func TestCheckConditions_AvailableTrue(t *testing.T) {
	ctx := &Context{}
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{
						"type":   "Available",
						"status": "True",
					},
				},
			},
		},
	}
	assert.True(t, ctx.checkConditions(obj))
}

func TestCheckConditions_ReadyFalse(t *testing.T) {
	ctx := &Context{}
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{
						"type":   "Ready",
						"status": "False",
					},
				},
			},
		},
	}
	assert.False(t, ctx.checkConditions(obj))
}

func TestCheckConditions_NoConditions(t *testing.T) {
	ctx := &Context{}
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"status": map[string]interface{}{},
		},
	}
	assert.False(t, ctx.checkConditions(obj))
}

func TestCheckConditions_NoStatus(t *testing.T) {
	ctx := &Context{}
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{},
	}
	assert.False(t, ctx.checkConditions(obj))
}

func TestCheckConditions_NotUnstructured(t *testing.T) {
	ctx := &Context{}
	pod := &corev1.Pod{}
	assert.False(t, ctx.checkConditions(pod))
}

func TestCheckConditions_UnknownConditionType(t *testing.T) {
	ctx := &Context{}
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{
						"type":   "CustomCondition",
						"status": "True",
					},
				},
			},
		},
	}
	assert.False(t, ctx.checkConditions(obj))
}

func TestToUnstructured(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	}
	// Set GVK so toUnstructured can convert
	pod.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Pod"))

	u, err := toUnstructured(pod)
	assert.NoError(t, err)
	assert.Equal(t, "test-pod", u.GetName())
	assert.Equal(t, "default", u.GetNamespace())
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	assert.True(t, cfg.KeepOnFailure)
	// KeepAlways depends on env var, just check it doesn't panic
	_ = cfg.KeepAlways
}

// ============================================================================
// podHints tests
// ============================================================================

func TestPodHints_ImagePullBackOff(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Image: "myapp:v1"}},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}}},
			},
		},
	}
	hints := podHints(pod)
	assert.Len(t, hints, 1)
	assert.Contains(t, hints[0], "myapp:v1")
	assert.Contains(t, hints[0], "failed to pull")
}

func TestPodHints_ErrImagePull(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Image: "bad:image"}},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ErrImagePull"}}},
			},
		},
	}
	hints := podHints(pod)
	assert.Len(t, hints, 1)
	assert.Contains(t, hints[0], "bad:image")
}

func TestPodHints_CrashLoopBackOff(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "crash-pod"},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}},
			},
		},
	}
	hints := podHints(pod)
	assert.Len(t, hints, 1)
	assert.Contains(t, hints[0], "crash-looping")
	assert.Contains(t, hints[0], "crash-pod")
}

func TestPodHints_CreateContainerConfigError(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CreateContainerConfigError"}}},
			},
		},
	}
	hints := podHints(pod)
	assert.Len(t, hints, 1)
	assert.Contains(t, hints[0], "configuration error")
}

func TestPodHints_OOMKilled(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled"}}},
			},
		},
	}
	hints := podHints(pod)
	assert.Len(t, hints, 1)
	assert.Contains(t, hints[0], "Out of Memory")
}

func TestPodHints_NonZeroExitCode(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "exit-pod"},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
					Reason:   "Error",
					ExitCode: 137,
				}}},
			},
		},
	}
	hints := podHints(pod)
	assert.Len(t, hints, 1)
	assert.Contains(t, hints[0], "137")
	assert.Contains(t, hints[0], "exit-pod")
}

func TestPodHints_PendingUnschedulable(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			Conditions: []corev1.PodCondition{
				{
					Type:    corev1.PodScheduled,
					Status:  corev1.ConditionFalse,
					Message: "0/3 nodes available: insufficient memory",
				},
			},
		},
	}
	hints := podHints(pod)
	assert.Len(t, hints, 1)
	assert.Contains(t, hints[0], "cannot be scheduled")
	assert.Contains(t, hints[0], "insufficient memory")
}

func TestPodHints_RunningHealthy_NoHints(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
			},
		},
	}
	hints := podHints(pod)
	assert.Empty(t, hints)
}

func TestPodHints_ContainerCreating(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}}},
			},
		},
	}
	hints := podHints(pod)
	assert.Len(t, hints, 1)
	assert.Contains(t, hints[0], "still being created")
}

func TestPodHints_PodInitializing(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "PodInitializing"}}},
			},
		},
	}
	hints := podHints(pod)
	assert.Len(t, hints, 1)
	assert.Contains(t, hints[0], "Init containers")
}

// ============================================================================
// Timeout configuration tests
// ============================================================================

func TestDefaultTimeoutConstants(t *testing.T) {
	assert.Equal(t, 60*time.Second, DefaultWaitTimeout)
	assert.Equal(t, 1*time.Second, DefaultPollInterval)
}

func TestContext_DefaultTimeouts(t *testing.T) {
	ctx := &Context{
		waitTimeout:  DefaultWaitTimeout,
		pollInterval: DefaultPollInterval,
	}
	assert.Equal(t, DefaultWaitTimeout, ctx.waitTimeout)
	assert.Equal(t, DefaultPollInterval, ctx.pollInterval)
}

func TestWithWaitTimeout_Option(t *testing.T) {
	opts := &contextOptions{}
	opt := WithWaitTimeout(30 * time.Second)
	opt(opts)
	assert.Equal(t, 30*time.Second, opts.waitTimeout)
}

func TestWithPollInterval_Option(t *testing.T) {
	opts := &contextOptions{}
	opt := WithPollInterval(500 * time.Millisecond)
	opt(opts)
	assert.Equal(t, 500*time.Millisecond, opts.pollInterval)
}
