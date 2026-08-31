package k8s

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	klabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func TestKubernetesExecutorDisposableClusterLifecycle(t *testing.T) {
	if os.Getenv("SEMAPHORE_TEST_KUBERNETES") != "1" {
		t.Skip("set SEMAPHORE_TEST_KUBERNETES=1 for the disposable-cluster suite")
	}
	kubeconfig := os.Getenv("SEMAPHORE_TEST_KUBECONFIG")
	contextName := os.Getenv("SEMAPHORE_TEST_KUBERNETES_CONTEXT")
	image := os.Getenv("SEMAPHORE_TEST_KUBERNETES_IMAGE")
	if kubeconfig == "" || contextName == "" || image == "" {
		t.Fatal("SEMAPHORE_TEST_KUBECONFIG, SEMAPHORE_TEST_KUBERNETES_CONTEXT, and SEMAPHORE_TEST_KUBERNETES_IMAGE are required")
	}

	cfg, baseClient, admin := createRestrictedIntegrationClient(t, kubeconfig, contextName, image)
	policy := testKubernetesPolicy(t, cfg)
	t.Run("restricted manifest creates deny-all NetworkPolicy and cleans it", func(t *testing.T) {
		client := &networkPolicyInspectClient{KubernetesClient: baseClient, admin: admin, namespace: cfg.namespace}
		executor := newExecutorForPlan(client, cfg, policy, 19, db.Task{ID: 4100, ProjectID: 7, AssignmentGeneration: 1}, db.Template{App: db.AppBash}, &captureLogger{})
		plan := &tasks.ContainerTaskPlan{App: db.AppBash, Bundle: io.NopCloser(bytes.NewReader(taskBundle(t, `
case "$1" in
  bootstrap) exit 0 ;;
  run) exit 0 ;;
  *) exit 64 ;;
esac
`)))}
		require.NoError(t, executor.runPlan(context.Background(), plan))
		assert.True(t, client.checked)
		assertIntegrationObjectsGone(t, admin, cfg.namespace, executor.task)
	})

	t.Run("RBAC denial has stable category", func(t *testing.T) {
		denied := tokenClientForServiceAccount(t, kubeconfig, contextName, admin, cfg.namespace, "semaphore-denied")
		_, err := (&client{config: cfg, api: denied}).CreateNetworkPolicy(context.Background(), mustDenyAllNetworkPolicy(t, cfg, policy, 4190))
		require.ErrorIs(t, err, db.KubernetesPolicyViolationError{Rule: db.KubernetesPolicyRuleRBACDenied})
	})

	t.Run("quota denial has stable category", func(t *testing.T) {
		_, err := admin.CoreV1().ResourceQuotas(cfg.namespace).Create(context.Background(), &corev1.ResourceQuota{
			ObjectMeta: metav1.ObjectMeta{Name: "slice053-cpu-quota", Namespace: cfg.namespace},
			Spec:       corev1.ResourceQuotaSpec{Hard: corev1.ResourceList{corev1.ResourceName("count/jobs.batch"): resource.MustParse("0")}},
		}, metav1.CreateOptions{})
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			quota, quotaErr := admin.CoreV1().ResourceQuotas(cfg.namespace).Get(context.Background(), "slice053-cpu-quota", metav1.GetOptions{})
			_, ready := quota.Status.Hard[corev1.ResourceName("count/jobs.batch")]
			return quotaErr == nil && ready
		}, 15*time.Second, 100*time.Millisecond)
		_, err = baseClient.CreateJob(context.Background(), buildJob(cfg, policy, db.Task{ID: 4191, ProjectID: 7, AssignmentGeneration: 1}, 19, "bundle", cfg.image, []string{"/bin/true"}))
		require.ErrorIs(t, err, db.KubernetesPolicyViolationError{Rule: db.KubernetesPolicyRuleQuotaDenied})
		require.NoError(t, admin.CoreV1().ResourceQuotas(cfg.namespace).Delete(context.Background(), "slice053-cpu-quota", metav1.DeleteOptions{}))
	})
	t.Run("success reconnecting logs and cleanup", func(t *testing.T) {
		client := &disconnectFirstLogStreamClient{KubernetesClient: baseClient}
		logger := &captureLogger{}
		executor := newExecutorForPlan(client, cfg, policy, 19, db.Task{ID: 4101, ProjectID: 7, AssignmentGeneration: 1}, db.Template{App: db.AppBash}, logger)
		plan := &tasks.ContainerTaskPlan{App: db.AppBash, Bundle: io.NopCloser(bytes.NewReader(taskBundle(t, `
case "$1" in
  bootstrap) echo bootstrap ;;
  run) echo success ;;
  *) exit 64 ;;
esac
`)))}

		require.NoError(t, executor.runPlan(context.Background(), plan))
		assert.Contains(t, logger.records, "bootstrap")
		assert.Contains(t, logger.records, "success")
		assert.True(t, client.disconnected())
		assertIntegrationObjectsGone(t, admin, cfg.namespace, executor.task)
	})

	t.Run("nonzero result and cleanup", func(t *testing.T) {
		executor := newExecutorForPlan(baseClient, cfg, policy, 19, db.Task{ID: 4102, ProjectID: 7, AssignmentGeneration: 1}, db.Template{App: db.AppBash}, &captureLogger{})
		plan := &tasks.ContainerTaskPlan{App: db.AppBash, Bundle: io.NopCloser(bytes.NewReader(taskBundle(t, `
case "$1" in
  bootstrap) echo bootstrap ;;
  run) echo failed; exit 23 ;;
  *) exit 64 ;;
esac
`)))}

		err := executor.runPlan(context.Background(), plan)

		require.ErrorContains(t, err, "Kubernetes Job failed")
		assert.Equal(t, "failed", executor.ExecutorMetadata().K8sLifecycle)
		assertIntegrationObjectsGone(t, admin, cfg.namespace, executor.task)
	})

	t.Run("foreground cancellation confirms pod termination before cleanup", func(t *testing.T) {
		executor := newExecutorForPlan(baseClient, cfg, policy, 19, db.Task{ID: 4103, ProjectID: 7, AssignmentGeneration: 1}, db.Template{App: db.AppBash}, &captureLogger{})
		plan := &tasks.ContainerTaskPlan{App: db.AppBash, Bundle: io.NopCloser(bytes.NewReader(taskBundle(t, `
case "$1" in
  bootstrap) echo bootstrap ;;
  run) echo running; sleep 300 ;;
  *) exit 64 ;;
esac
`)))}
		runDone := make(chan error, 1)
		go func() { runDone <- executor.runPlan(context.Background(), plan) }()
		require.Eventually(t, func() bool {
			return executor.ExecutorMetadata().K8sLifecycle == "running"
		}, 45*time.Second, 100*time.Millisecond)

		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		confirmation := executor.ConfirmStop(stopCtx)
		cancel()

		assert.Equal(t, tasks.StopConfirmed, confirmation)
		require.NoError(t, <-runDone)
		assert.Equal(t, "stopped", executor.ExecutorMetadata().K8sLifecycle)
		assertIntegrationObjectsGone(t, admin, cfg.namespace, executor.task)
	})
}

type disconnectFirstLogStreamClient struct {
	KubernetesClient
	mu            sync.Mutex
	didDisconnect bool
}

func (c *disconnectFirstLogStreamClient) OpenPodLogs(ctx context.Context, pod PodIdentity, since *time.Time, follow bool) (io.ReadCloser, error) {
	stream, err := c.KubernetesClient.OpenPodLogs(ctx, pod, since, follow)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.didDisconnect {
		return stream, nil
	}
	c.didDisconnect = true
	return &disconnectingReadCloser{ReadCloser: stream, err: errors.New("simulated log disconnect")}, nil
}

func (c *disconnectFirstLogStreamClient) disconnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.didDisconnect
}

type disconnectingReadCloser struct {
	io.ReadCloser
	err    error
	failed bool
}

func (r *disconnectingReadCloser) Read(buffer []byte) (int, error) {
	if r.failed {
		return 0, r.err
	}
	count, err := r.ReadCloser.Read(buffer)
	if count > 0 {
		r.failed = true
		return count, nil
	}
	return count, err
}

func createRestrictedIntegrationClient(t *testing.T, kubeconfig string, contextName string, image string) (config, KubernetesClient, kubernetes.Interface) {
	t.Helper()
	loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig}
	overrides := &clientcmd.ConfigOverrides{CurrentContext: contextName}
	adminConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
	require.NoError(t, err)
	admin, err := kubernetes.NewForConfig(adminConfig)
	require.NoError(t, err)
	namespace := fmt.Sprintf("semaphore-s053-%d", time.Now().UnixNano()%1_000_000_000)
	_, err = admin.CoreV1().Namespaces().Create(context.Background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		policy := metav1.DeletePropagationForeground
		_ = admin.CoreV1().Namespaces().Delete(context.Background(), namespace, metav1.DeleteOptions{PropagationPolicy: &policy})
	})

	for _, name := range []string{"semaphore-runner", "semaphore-task"} {
		_, err = admin.CoreV1().ServiceAccounts(namespace).Create(context.Background(), &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}, metav1.CreateOptions{})
		require.NoError(t, err)
	}
	_, err = admin.RbacV1().Roles(namespace).Create(context.Background(), &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "semaphore-runner", Namespace: namespace},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{"batch"}, Resources: []string{"jobs"}, Verbs: []string{"create", "get", "list", "watch", "delete"}},
			{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list", "watch"}},
			{APIGroups: []string{""}, Resources: []string{"pods/log"}, Verbs: []string{"get"}},
			{APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: []string{"create", "get", "delete"}},
			{APIGroups: []string{"networking.k8s.io"}, Resources: []string{"networkpolicies"}, Verbs: []string{"create", "get", "delete"}},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = admin.RbacV1().RoleBindings(namespace).Create(context.Background(), &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "semaphore-runner", Namespace: namespace},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: "semaphore-runner"},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: "semaphore-runner", Namespace: namespace}},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	expiration := int64(3600)
	token, err := admin.CoreV1().ServiceAccounts(namespace).CreateToken(context.Background(), "semaphore-runner", &authenticationv1.TokenRequest{Spec: authenticationv1.TokenRequestSpec{ExpirationSeconds: &expiration}}, metav1.CreateOptions{})
	require.NoError(t, err)
	restrictedConfig := rest.CopyConfig(adminConfig)
	restrictedConfig.BearerToken = token.Status.Token
	restrictedConfig.BearerTokenFile = ""
	restrictedConfig.Username = ""
	restrictedConfig.Password = ""
	restrictedConfig.ExecProvider = nil
	restrictedConfig.AuthProvider = nil
	restrictedConfig.TLSClientConfig.CertData = nil
	restrictedConfig.TLSClientConfig.KeyData = nil
	restrictedConfig.TLSClientConfig.CertFile = ""
	restrictedConfig.TLSClientConfig.KeyFile = ""
	restricted, err := kubernetes.NewForConfig(restrictedConfig)
	require.NoError(t, err)

	access, err := restricted.AuthorizationV1().SelfSubjectAccessReviews().Create(context.Background(), &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authorizationv1.ResourceAttributes{Namespace: namespace, Verb: "create", Group: "batch", Resource: "jobs"}},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	assert.True(t, access.Status.Allowed)
	cfg := config{
		clusterAlias: "kind-slice053", namespace: namespace, serviceAccount: "semaphore-task",
		image: image, helperImage: image, pollInterval: 100 * time.Millisecond,
		cleanupGrace: 5 * time.Second, activeDeadlineSeconds: 60,
	}
	return cfg, &client{config: cfg, api: restricted}, admin
}

func taskBundle(t *testing.T, script string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := tar.NewWriter(&buffer)
	require.NoError(t, archive.WriteHeader(&tar.Header{Name: "run.sh", Mode: 0o500, Size: int64(len(script)), Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0)}))
	_, err := archive.Write([]byte(script))
	require.NoError(t, err)
	require.NoError(t, archive.Close())
	return buffer.Bytes()
}

func assertIntegrationObjectsGone(t *testing.T, admin kubernetes.Interface, namespace string, task db.Task) {
	t.Helper()
	selector := klabels.Set(taskLabels(task, 19)).String()
	require.Eventually(t, func() bool {
		jobs, jobErr := admin.BatchV1().Jobs(namespace).List(context.Background(), metav1.ListOptions{LabelSelector: selector})
		pods, podErr := admin.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{LabelSelector: selector})
		secrets, secretErr := admin.CoreV1().Secrets(namespace).List(context.Background(), metav1.ListOptions{LabelSelector: selector})
		networkPolicies, networkPolicyErr := admin.NetworkingV1().NetworkPolicies(namespace).List(context.Background(), metav1.ListOptions{LabelSelector: selector})
		return jobErr == nil && podErr == nil && secretErr == nil && networkPolicyErr == nil && len(jobs.Items) == 0 && len(pods.Items) == 0 && len(secrets.Items) == 0 && len(networkPolicies.Items) == 0
	}, 15*time.Second, 100*time.Millisecond)
}

type networkPolicyInspectClient struct {
	KubernetesClient
	admin     kubernetes.Interface
	namespace string
	checked   bool
}

func (c *networkPolicyInspectClient) CreateJob(ctx context.Context, job *batchv1.Job) (ObjectIdentity, error) {
	policies, err := c.admin.NetworkingV1().NetworkPolicies(c.namespace).List(ctx, metav1.ListOptions{LabelSelector: klabels.Set(job.Labels).String()})
	if err != nil || len(policies.Items) != 1 {
		return ObjectIdentity{}, fmt.Errorf("task deny-all NetworkPolicy was not present before Job admission")
	}
	policy := policies.Items[0]
	if len(policy.Spec.Ingress) != 0 || len(policy.Spec.Egress) != 0 || len(policy.Spec.PolicyTypes) != 2 ||
		policy.Spec.PolicyTypes[0] != networkingv1.PolicyTypeIngress || policy.Spec.PolicyTypes[1] != networkingv1.PolicyTypeEgress ||
		!hasRestrictedPodSecurity(job.Spec.Template.Spec.SecurityContext) || !hasRestrictedContainerSecurity(job.Spec.Template.Spec.Containers[0].SecurityContext) {
		return ObjectIdentity{}, fmt.Errorf("restricted task manifest was not admitted with exact deny-all policy")
	}
	c.checked = true
	return c.KubernetesClient.CreateJob(ctx, job)
}

func mustDenyAllNetworkPolicy(t testing.TB, cfg config, policy db.KubernetesExecutionPolicy, taskID int) *networkingv1.NetworkPolicy {
	t.Helper()
	networkPolicy, err := buildNetworkPolicy(cfg, policy, db.Task{ID: taskID, ProjectID: 7, AssignmentGeneration: 1}, 19)
	require.NoError(t, err)
	return networkPolicy
}

func tokenClientForServiceAccount(t testing.TB, kubeconfig, contextName string, admin kubernetes.Interface, namespace, name string) kubernetes.Interface {
	t.Helper()
	_, err := admin.CoreV1().ServiceAccounts(namespace).Create(context.Background(), &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}, metav1.CreateOptions{})
	require.NoError(t, err)
	loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig}
	overrides := &clientcmd.ConfigOverrides{CurrentContext: contextName}
	adminConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
	require.NoError(t, err)
	expiration := int64(3600)
	token, err := admin.CoreV1().ServiceAccounts(namespace).CreateToken(context.Background(), name, &authenticationv1.TokenRequest{Spec: authenticationv1.TokenRequestSpec{ExpirationSeconds: &expiration}}, metav1.CreateOptions{})
	require.NoError(t, err)
	restrictedConfig := rest.CopyConfig(adminConfig)
	restrictedConfig.BearerToken = token.Status.Token
	restrictedConfig.BearerTokenFile = ""
	restrictedConfig.Username = ""
	restrictedConfig.Password = ""
	restrictedConfig.ExecProvider = nil
	restrictedConfig.AuthProvider = nil
	restrictedConfig.TLSClientConfig.CertData = nil
	restrictedConfig.TLSClientConfig.KeyData = nil
	restrictedConfig.TLSClientConfig.CertFile = ""
	restrictedConfig.TLSClientConfig.KeyFile = ""
	api, err := kubernetes.NewForConfig(restrictedConfig)
	require.NoError(t, err)
	return api
}
