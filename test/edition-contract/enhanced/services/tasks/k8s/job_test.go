package k8s

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

func TestBuildJobCreatesOneBoundedAttributableTaskPod(t *testing.T) {
	cfg := config{
		clusterAlias:          "qa-cluster",
		namespace:             "semaphore-jobs",
		serviceAccount:        "semaphore-runner",
		helperImage:           testImage,
		pullSecrets:           []string{"registry-a", "registry-b"},
		cleanupGrace:          20 * time.Second,
		activeDeadlineSeconds: 900,
	}
	task := db.Task{ID: 41, ProjectID: 7, AssignmentGeneration: 3}

	job := buildJob(cfg, testKubernetesPolicy(t, cfg), task, 19, "semaphore-bundle-41-3", testImage, []string{"/bin/sh", "/semaphore/bundle/run.sh", "run"}, false)

	require.NotNil(t, job)
	assert.Equal(t, "semaphore-jobs", job.Namespace)
	assert.Equal(t, "semaphore-task-41-3", job.Name)
	assert.Equal(t, map[string]string{
		"app.kubernetes.io/managed-by":       "semaphore",
		"io.semaphore.managed":               "v1",
		"io.semaphore.executor":              "k8s",
		"io.semaphore.project-id":            "7",
		"io.semaphore.runner-id":             "19",
		"io.semaphore.task-id":               "41",
		"io.semaphore.assignment-generation": "3",
	}, job.Labels)
	assert.Empty(t, job.Annotations)
	require.NotNil(t, job.Spec.BackoffLimit)
	assert.Zero(t, *job.Spec.BackoffLimit)
	require.NotNil(t, job.Spec.ActiveDeadlineSeconds)
	assert.Equal(t, int64(900), *job.Spec.ActiveDeadlineSeconds)
	assert.Equal(t, corev1.RestartPolicyNever, job.Spec.Template.Spec.RestartPolicy)
	assert.Equal(t, "semaphore-runner", job.Spec.Template.Spec.ServiceAccountName)
	require.NotNil(t, job.Spec.Template.Spec.TerminationGracePeriodSeconds)
	assert.Equal(t, int64(20), *job.Spec.Template.Spec.TerminationGracePeriodSeconds)
	require.NotNil(t, job.Spec.Template.Spec.AutomountServiceAccountToken)
	assert.False(t, *job.Spec.Template.Spec.AutomountServiceAccountToken)
	require.NotNil(t, job.Spec.Template.Spec.SecurityContext)
	require.NotNil(t, job.Spec.Template.Spec.SecurityContext.FSGroup)
	assert.Equal(t, restrictedWorkloadUID, *job.Spec.Template.Spec.SecurityContext.FSGroup)
	assert.Equal(t, []corev1.LocalObjectReference{{Name: "registry-a"}, {Name: "registry-b"}}, job.Spec.Template.Spec.ImagePullSecrets)
	require.NotNil(t, job.Spec.Template.Spec.Volumes[0].Secret.DefaultMode)
	assert.Equal(t, int32(0o440), *job.Spec.Template.Spec.Volumes[0].Secret.DefaultMode)
	require.Len(t, job.Spec.Template.Spec.InitContainers, 1)
	assert.Equal(t, testImage, job.Spec.Template.Spec.InitContainers[0].Image)
	require.Len(t, job.Spec.Template.Spec.Containers, 1)
	container := job.Spec.Template.Spec.Containers[0]
	assert.Equal(t, "task", container.Name)
	assert.Equal(t, testImage, container.Image)
	assert.Equal(t, []string{"/bin/sh", "/semaphore/bundle/run.sh", "run"}, container.Command)
	assert.Empty(t, container.Env)
	assert.Empty(t, container.EnvFrom)
}

func TestBuildJobNameIsStableAndWithinKubernetesLimit(t *testing.T) {
	cfg := config{clusterAlias: "qa", namespace: "semaphore", serviceAccount: "semaphore-task", image: testImage, helperImage: testImage, activeDeadlineSeconds: 1}
	job := buildJob(cfg, testKubernetesPolicy(t, cfg), db.Task{
		ID:                   2147483647,
		ProjectID:            2147483647,
		AssignmentGeneration: 2147483647,
	}, 2147483647, "semaphore-bundle-2147483647-2147483647", testImage, []string{"/bin/true"}, false)

	assert.LessOrEqual(t, len(job.Name), 63)
	assert.Equal(t, job.Labels, job.Spec.Template.Labels)
}

func TestBuildJobAddsIsolatedOutputsSidecarOnlyForWorkflowPlans(t *testing.T) {
	cfg := config{namespace: "semaphore", serviceAccount: "semaphore-task", helperImage: testImage, cleanupGrace: time.Second, activeDeadlineSeconds: 60}
	policy := testKubernetesPolicy(t, cfg)
	task := db.Task{ID: 4, ProjectID: 5, AssignmentGeneration: 6}
	command := []string{"/bin/sh", "/semaphore/bundle/run.sh", "run"}

	job := buildJob(cfg, policy, task, 7, "bundle", testImage, command, true)
	require.Len(t, job.Spec.Template.Spec.Containers, 2)
	taskContainer := job.Spec.Template.Spec.Containers[0]
	outputs := job.Spec.Template.Spec.Containers[1]
	assert.Equal(t, command, taskContainer.Command)
	assert.Equal(t, outputsContainerName, outputs.Name)
	assert.Equal(t, cfg.helperImage, outputs.Image)
	assert.Contains(t, outputs.Command[2], "outputs-ack")
	assert.True(t, hasMount(outputs.VolumeMounts, "workspace", workspacePath, true))
	assert.True(t, hasMount(outputs.VolumeMounts, "bundle", bundlePath, true))
	assert.True(t, hasMount(outputs.VolumeMounts, "outputs-control", outputsControlPath, false))
	assert.False(t, hasMount(taskContainer.VolumeMounts, "outputs-control", outputsControlPath, false))
	assert.True(t, hasRestrictedContainerSecurity(outputs.SecurityContext))
	assert.Equal(t, outputsSidecarResources(policy.Resources), outputs.Resources)
	assert.Less(t, outputs.Resources.Requests.Cpu().MilliValue(), taskContainer.Resources.Requests.Cpu().MilliValue())
	assert.Less(t, outputs.Resources.Requests.Memory().Value(), taskContainer.Resources.Requests.Memory().Value())
	require.NotNil(t, job.Spec.Template.Spec.AutomountServiceAccountToken)
	assert.False(t, *job.Spec.Template.Spec.AutomountServiceAccountToken)

	nonWorkflow := buildJob(cfg, policy, task, 7, "bundle", testImage, command, false)
	assert.Len(t, nonWorkflow.Spec.Template.Spec.Containers, 1)
	assert.NotContains(t, nonWorkflow.Spec.Template.Spec.Volumes, corev1.Volume{Name: "outputs-control"})
}

func hasMount(mounts []corev1.VolumeMount, name string, path string, readOnly bool) bool {
	for _, mount := range mounts {
		if mount.Name == name && mount.MountPath == path && mount.ReadOnly == readOnly {
			return true
		}
	}
	return false
}
