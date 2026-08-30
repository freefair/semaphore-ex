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

	job := buildJob(cfg, task, 19, "semaphore-bundle-41-3", testImage, []string{"/bin/sh", "/semaphore/bundle/run.sh", "run"})

	require.NotNil(t, job)
	assert.Equal(t, "semaphore-jobs", job.Namespace)
	assert.Equal(t, "semaphore-task-41-3", job.Name)
	assert.Equal(t, map[string]string{
		"app.kubernetes.io/managed-by":       "semaphore",
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
	assert.Equal(t, []corev1.LocalObjectReference{{Name: "registry-a"}, {Name: "registry-b"}}, job.Spec.Template.Spec.ImagePullSecrets)
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
	job := buildJob(config{namespace: "semaphore", serviceAccount: "semaphore-task", helperImage: testImage, activeDeadlineSeconds: 1}, db.Task{
		ID:                   2147483647,
		ProjectID:            2147483647,
		AssignmentGeneration: 2147483647,
	}, 2147483647, "semaphore-bundle-2147483647-2147483647", testImage, []string{"/bin/true"})

	assert.LessOrEqual(t, len(job.Name), 63)
	assert.Equal(t, job.Labels, job.Spec.Template.Labels)
}
