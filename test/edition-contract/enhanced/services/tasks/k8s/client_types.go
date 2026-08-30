package k8s

import (
	"context"
	"io"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/types"
)

type BundleSecret struct {
	Name      string
	Labels    map[string]string
	Data      []byte
	Immutable bool
}

type ObjectIdentity struct {
	Name string
	UID  types.UID
}

type PodIdentity struct {
	ObjectIdentity
	RestartCount int32
}

type JobResult struct {
	Succeeded bool
	Lifecycle string
	Reason    string
}

type KubernetesClient interface {
	CreateBundleSecret(context.Context, BundleSecret) (ObjectIdentity, error)
	CreateJob(context.Context, *batchv1.Job) (ObjectIdentity, error)
	WaitForTaskPod(context.Context, ObjectIdentity, map[string]string) (PodIdentity, error)
	OpenPodLogs(context.Context, PodIdentity, *time.Time, bool) (io.ReadCloser, error)
	WaitForJob(context.Context, ObjectIdentity, PodIdentity) (JobResult, error)
	DeleteJobForeground(context.Context, ObjectIdentity, PodIdentity, time.Duration) error
	DeleteBundleSecret(context.Context, ObjectIdentity) error
}
