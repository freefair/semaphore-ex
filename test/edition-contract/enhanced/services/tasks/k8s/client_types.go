package k8s

import (
	"context"
	"io"
	"time"

	"github.com/semaphoreui/semaphore/db"
	batchv1 "k8s.io/api/batch/v1"
	networkingv1 "k8s.io/api/networking/v1"
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
	CreateNetworkPolicy(context.Context, *networkingv1.NetworkPolicy) (ObjectIdentity, error)
	CreateJob(context.Context, *batchv1.Job) (ObjectIdentity, error)
	WaitForTaskPod(context.Context, ObjectIdentity, map[string]string) (PodIdentity, error)
	OpenPodLogs(context.Context, PodIdentity, *time.Time, bool) (io.ReadCloser, error)
	WaitForJob(context.Context, ObjectIdentity, PodIdentity) (JobResult, error)
	DeleteJobForeground(context.Context, ObjectIdentity, PodIdentity, time.Duration) error
	DeleteNetworkPolicy(context.Context, ObjectIdentity, map[string]string) error
	DeleteBundleSecret(context.Context, ObjectIdentity) error
}

// kubernetesReconciliationScanner is intentionally narrower than the executor
// client: it has no cluster-scoped capability and returns only bounded state.
type kubernetesReconciliationScanner interface {
	ScanKubernetesReconciliation(context.Context, db.KubernetesReconciliationSession, int) (db.KubernetesReconciliationScan, error)
}

// kubernetesReconciliationRemediator is intentionally separate from normal
// execution. Only a server-issued, session-fenced command can reach its
// narrowly typed namespaced GC path.
type kubernetesReconciliationRemediator interface {
	GarbageCollectKubernetesReconciliation(context.Context, db.KubernetesReconciliationRemediationCommand, int) db.KubernetesReconciliationRemediationResult
}
