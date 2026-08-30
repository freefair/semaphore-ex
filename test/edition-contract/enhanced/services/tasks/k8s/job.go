package k8s

import (
	"fmt"
	"strconv"

	"github.com/semaphoreui/semaphore/db"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	bundleArchiveKey  = "bundle.tar"
	bundleInputPath   = "/semaphore/input"
	bundlePath        = "/semaphore/bundle"
	workspacePath     = "/workspace"
	taskContainerName = "task"
)

func buildJob(cfg config, task db.Task, runnerID int, bundleSecret string, image string, command []string) *batchv1.Job {
	labels := taskLabels(task, runnerID)
	backoffLimit := int32(0)
	automountToken := false
	enableServiceLinks := false
	secretMode := int32(0o400)
	terminationGraceSeconds := int64(cfg.cleanupGrace.Seconds())
	pullSecrets := make([]corev1.LocalObjectReference, 0, len(cfg.pullSecrets))
	for _, name := range cfg.pullSecrets {
		pullSecrets = append(pullSecrets, corev1.LocalObjectReference{Name: name})
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      taskObjectName("semaphore-task", task),
			Namespace: cfg.namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:          &backoffLimit,
			ActiveDeadlineSeconds: &cfg.activeDeadlineSeconds,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy:                 corev1.RestartPolicyNever,
					TerminationGracePeriodSeconds: &terminationGraceSeconds,
					ServiceAccountName:            cfg.serviceAccount,
					AutomountServiceAccountToken:  &automountToken,
					EnableServiceLinks:            &enableServiceLinks,
					ImagePullSecrets:              pullSecrets,
					InitContainers: []corev1.Container{{
						Name:    "bundle",
						Image:   cfg.helperImage,
						Command: []string{"/bin/sh", "-c", "tar -xf /semaphore/input/bundle.tar -C /semaphore/bundle"},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "bundle-input", MountPath: bundleInputPath, ReadOnly: true},
							{Name: "bundle", MountPath: bundlePath},
						},
					}},
					Containers: []corev1.Container{{
						Name:       taskContainerName,
						Image:      image,
						Command:    append([]string(nil), command...),
						WorkingDir: workspacePath,
						VolumeMounts: []corev1.VolumeMount{
							{Name: "bundle", MountPath: bundlePath, ReadOnly: true},
							{Name: "workspace", MountPath: workspacePath},
						},
					}},
					Volumes: []corev1.Volume{
						{Name: "bundle-input", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: bundleSecret, Items: []corev1.KeyToPath{{Key: bundleArchiveKey, Path: bundleArchiveKey}}, DefaultMode: &secretMode}}},
						{Name: "bundle", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
						{Name: "workspace", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
					},
				},
			},
		},
	}
}

func taskLabels(task db.Task, runnerID int) map[string]string {
	return map[string]string{
		"app.kubernetes.io/managed-by":       "semaphore",
		"io.semaphore.executor":              "k8s",
		"io.semaphore.project-id":            strconv.Itoa(task.ProjectID),
		"io.semaphore.task-id":               strconv.Itoa(task.ID),
		"io.semaphore.assignment-generation": strconv.Itoa(task.AssignmentGeneration),
		"io.semaphore.runner-id":             strconv.Itoa(runnerID),
	}
}

func taskObjectName(prefix string, task db.Task) string {
	return fmt.Sprintf("%s-%d-%d", prefix, task.ID, task.AssignmentGeneration)
}
