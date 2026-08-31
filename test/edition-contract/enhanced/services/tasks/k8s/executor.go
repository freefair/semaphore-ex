package k8s

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/services/tasks"
)

const (
	maxBundleBytes   = 768 * 1024
	maxLogLineBytes  = 64 * 1024
	maxLogReconnects = 5
)

type KubernetesExecutor struct {
	client   KubernetesClient
	config   config
	policy   db.KubernetesExecutionPolicy
	runnerID int
	task     db.Task
	template db.Template
	local    *tasks.LocalExecutor
	logger   task_logger.Logger
	image    string

	mu                sync.Mutex
	createMu          sync.Mutex
	cleanupMu         sync.Mutex
	killed            bool
	runCancel         context.CancelFunc
	plan              *tasks.ContainerTaskPlan
	secret            ObjectIdentity
	networkPolicy     ObjectIdentity
	job               ObjectIdentity
	pod               PodIdentity
	lifecycle         string
	terminalReason    string
	retentionDeadline *time.Time
	cleanupCompleted  bool
}

func newExecutorForPlan(client KubernetesClient, cfg config, policy db.KubernetesExecutionPolicy, runnerID int, task db.Task, template db.Template, logger task_logger.Logger) *KubernetesExecutor {
	if logger == nil {
		logger = task_logger.NopLogger{}
	}
	image, _ := cfg.taskImage(template)
	return &KubernetesExecutor{
		client: client, config: cfg, policy: policy, runnerID: runnerID, task: task, template: template,
		logger: logger, image: image, lifecycle: "starting",
	}
}

func (e *KubernetesExecutor) Async() bool { return false }

func (e *KubernetesExecutor) IsKilled() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.killed
}

func (e *KubernetesExecutor) SetLogger(logger task_logger.Logger) {
	if logger == nil {
		logger = task_logger.NopLogger{}
	}
	e.logger = logger
	if e.local != nil {
		e.local.SetLogger(logger)
		e.logger = e.local.Logger
	}
}

func (e *KubernetesExecutor) SetStatus(status task_logger.TaskStatus) {
	e.logger.SetStatus(status)
}

func (e *KubernetesExecutor) Prepare(username string, incomingVersion *string, alias string) error {
	if e.plan != nil {
		return nil
	}
	if e.local == nil {
		return fmt.Errorf("Kubernetes executor has no task materializer")
	}
	plan, err := e.local.PrepareContainerTask(username, incomingVersion, alias)
	if err != nil {
		return err
	}
	e.plan = plan
	return nil
}

func (e *KubernetesExecutor) Run(username string, incomingVersion *string, alias string) error {
	defer e.Cleanup()
	if err := e.Prepare(username, incomingVersion, alias); err != nil {
		return err
	}
	return e.runPlan(context.Background(), e.plan)
}

func (e *KubernetesExecutor) runPlan(parent context.Context, plan *tasks.ContainerTaskPlan) (runErr error) {
	if plan == nil || plan.Bundle == nil {
		return fmt.Errorf("Kubernetes executor has no task bundle")
	}
	defer plan.Bundle.Close() //nolint:errcheck

	bundle, err := readBoundedBundle(plan.Bundle)
	if err != nil {
		return err
	}
	image := e.image
	if image == "" {
		image, err = e.config.taskImage(e.template)
		if err != nil {
			return err
		}
	}
	command, err := kubernetesTaskCommand(plan)
	if err != nil {
		return err
	}

	runCtx, cancel := context.WithCancel(parent)
	e.mu.Lock()
	if e.killed {
		e.mu.Unlock()
		cancel()
		return nil
	}
	e.runCancel = cancel
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.runCancel = nil
		e.mu.Unlock()
		cancel()
	}()
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), e.config.cleanupGrace+10*time.Second)
		defer cleanupCancel()
		if cleanupErr := e.cleanupTaskObjects(cleanupCtx); cleanupErr != nil {
			e.logger.Log("Unable to clean up Kubernetes task objects")
			if runErr == nil {
				e.mu.Lock()
				e.lifecycle = "failed"
				e.terminalReason = "CleanupFailed"
				e.mu.Unlock()
				runErr = fmt.Errorf("cleaning Kubernetes task objects: %w", cleanupErr)
			}
		}
	}()

	labels := taskLabels(e.task, e.runnerID)
	job, err := e.createTaskObjects(runCtx, labels, bundle, image, command)
	if err != nil {
		return err
	}
	if job.Name == "" {
		return nil
	}

	pod, err := e.client.WaitForTaskPod(runCtx, job, labels)
	if err != nil {
		if e.IsKilled() || errors.Is(err, context.Canceled) {
			return nil
		}
		return fmt.Errorf("waiting for Kubernetes task Pod: %w", err)
	}
	e.mu.Lock()
	e.pod = pod
	e.lifecycle = "running"
	e.mu.Unlock()

	logCtx, stopLogs := context.WithCancel(runCtx)
	logDone := make(chan struct{})
	tracker := newLogTracker()
	go func() {
		defer close(logDone)
		e.streamLogs(logCtx, pod, tracker)
	}()

	result, err := e.client.WaitForJob(runCtx, job, pod)
	stopLogs()
	<-logDone
	finalCtx, finalCancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = e.streamLogsOnce(finalCtx, pod, tracker, false)
	finalCancel()
	if err != nil {
		if e.IsKilled() || errors.Is(err, context.Canceled) {
			return nil
		}
		return fmt.Errorf("waiting for Kubernetes Job: %w", err)
	}
	e.mu.Lock()
	e.lifecycle = result.Lifecycle
	e.terminalReason = safeTerminalReason(result.Reason)
	e.mu.Unlock()
	if !result.Succeeded {
		return fmt.Errorf("Kubernetes Job failed: %s", fallbackReason(e.terminalReason))
	}
	return nil
}

func (e *KubernetesExecutor) createTaskObjects(ctx context.Context, labels map[string]string, bundle []byte, image string, command []string) (ObjectIdentity, error) {
	e.createMu.Lock()
	defer e.createMu.Unlock()
	secret, err := e.client.CreateBundleSecret(ctx, BundleSecret{
		Name: taskObjectName("semaphore-bundle", e.task), Labels: labels,
		Data: bundle, Immutable: true,
	})
	if err != nil {
		return ObjectIdentity{}, fmt.Errorf("creating Kubernetes task bundle: %w", err)
	}
	e.mu.Lock()
	e.secret = secret
	killed := e.killed
	e.mu.Unlock()
	if killed {
		return ObjectIdentity{}, nil
	}
	networkManifest, err := buildNetworkPolicy(e.config, e.policy, e.task, e.runnerID)
	if err != nil {
		return ObjectIdentity{}, err
	}
	if err := validateNetworkPolicy(networkManifest, e.config, e.task, e.runnerID); err != nil {
		return ObjectIdentity{}, err
	}
	networkPolicy, err := e.client.CreateNetworkPolicy(ctx, networkManifest)
	if err != nil {
		return ObjectIdentity{}, fmt.Errorf("creating Kubernetes task NetworkPolicy: %w", err)
	}
	e.mu.Lock()
	e.networkPolicy = networkPolicy
	e.mu.Unlock()
	jobManifest := buildJob(e.config, e.policy, e.task, e.runnerID, secret.Name, image, command)
	if err := validateGeneratedJob(jobManifest, e.config, e.policy, image); err != nil {
		return ObjectIdentity{}, err
	}
	job, err := e.client.CreateJob(ctx, jobManifest)
	if err != nil {
		return ObjectIdentity{}, fmt.Errorf("creating Kubernetes Job: %w", err)
	}
	e.mu.Lock()
	e.job = job
	e.lifecycle = "pending"
	e.mu.Unlock()
	return job, nil
}

func readBoundedBundle(reader io.Reader) ([]byte, error) {
	bundle, err := io.ReadAll(io.LimitReader(reader, maxBundleBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading Kubernetes task bundle: %w", err)
	}
	if len(bundle) > maxBundleBytes {
		return nil, fmt.Errorf("Kubernetes task bundle exceeds %d bytes", maxBundleBytes)
	}
	return bundle, nil
}

func kubernetesTaskCommand(plan *tasks.ContainerTaskPlan) ([]string, error) {
	bootstrap := "/bin/sh /semaphore/bundle/run.sh bootstrap"
	if !plan.App.IsTerraform() {
		return []string{"/bin/sh", "-c", bootstrap + " && /bin/sh /semaphore/bundle/run.sh run"}, nil
	}
	if !plan.Terraform.PlanOnly && !plan.Terraform.AutoApprove {
		return nil, fmt.Errorf("Kubernetes executor requires Terraform plan-only or auto-approve mode")
	}
	command := bootstrap + " && if /bin/sh /semaphore/bundle/run.sh plan; then exit 0; else code=$?; " +
		"if [ \"$code\" -ne 2 ]; then exit \"$code\"; fi; "
	if plan.Terraform.PlanOnly {
		command += "exit 0; fi"
	} else {
		command += "exec /bin/sh /semaphore/bundle/run.sh apply; fi"
	}
	return []string{"/bin/sh", "-c", command}, nil
}

func (e *KubernetesExecutor) streamLogs(ctx context.Context, pod PodIdentity, tracker *logTracker) {
	for reconnect := 0; reconnect < maxLogReconnects && ctx.Err() == nil; reconnect++ {
		_ = e.streamLogsOnce(ctx, pod, tracker, true)
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(e.config.pollInterval):
		}
	}
}

func (e *KubernetesExecutor) streamLogsOnce(ctx context.Context, pod PodIdentity, tracker *logTracker, follow bool) error {
	stream, err := e.client.OpenPodLogs(ctx, pod, tracker.since(), follow)
	if err != nil {
		return err
	}
	defer stream.Close() //nolint:errcheck
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 4*1024), maxLogLineBytes)
	for scanner.Scan() {
		timestamp, message, ok := parseTimestampedLog(scanner.Text())
		if !ok {
			continue
		}
		tracker.observe(timestamp)
		e.logger.LogWithTime(timestamp, message)
	}
	return scanner.Err()
}

func parseTimestampedLog(line string) (time.Time, string, bool) {
	timestampText, message, ok := strings.Cut(line, " ")
	if !ok {
		return time.Time{}, "", false
	}
	timestamp, err := time.Parse(time.RFC3339Nano, timestampText)
	if err != nil {
		return time.Time{}, "", false
	}
	return timestamp, message, true
}

type logTracker struct {
	mu     sync.Mutex
	latest time.Time
}

func newLogTracker() *logTracker {
	return &logTracker{}
}

func (t *logTracker) since() *time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.latest.IsZero() {
		return nil
	}
	value := t.latest
	return &value
}

func (t *logTracker) observe(timestamp time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if timestamp.After(t.latest) {
		t.latest = timestamp
	}
}

func (e *KubernetesExecutor) Kill() {
	ctx, cancel := context.WithTimeout(context.Background(), e.config.cleanupGrace+10*time.Second)
	defer cancel()
	_ = e.ConfirmStop(ctx)
}

func (e *KubernetesExecutor) ConfirmStop(ctx context.Context) tasks.StopConfirmation {
	e.mu.Lock()
	e.killed = true
	if e.job.Name != "" && e.pod.Name != "" {
		e.lifecycle = "canceling"
	}
	cancel := e.runCancel
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	e.createMu.Lock()
	e.createMu.Unlock()
	if err := e.cleanupTaskObjects(ctx); err != nil {
		return tasks.StopPending
	}
	e.mu.Lock()
	e.lifecycle = "stopped"
	e.terminalReason = "Canceled"
	e.mu.Unlock()
	return tasks.StopConfirmed
}

func (e *KubernetesExecutor) Cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), e.config.cleanupGrace+10*time.Second)
	defer cancel()
	if err := e.cleanupTaskObjects(ctx); err != nil {
		e.logger.Log("Unable to clean up Kubernetes task objects")
	}
	if e.plan != nil && e.plan.Bundle != nil {
		_ = e.plan.Bundle.Close()
	}
	if e.local != nil {
		e.local.Cleanup()
	}
}

func (e *KubernetesExecutor) cleanupTaskObjects(ctx context.Context) error {
	e.cleanupMu.Lock()
	defer e.cleanupMu.Unlock()
	if e.cleanupCompleted {
		return nil
	}
	e.mu.Lock()
	job, pod, networkPolicy, secret := e.job, e.pod, e.networkPolicy, e.secret
	e.mu.Unlock()
	if job.Name != "" {
		if err := e.client.DeleteJobForeground(ctx, job, pod, e.config.cleanupGrace); err != nil {
			return err
		}
	}
	if networkPolicy.Name != "" {
		if err := e.client.DeleteNetworkPolicy(ctx, networkPolicy, taskLabels(e.task, e.runnerID)); err != nil {
			return err
		}
	}
	if secret.Name != "" {
		if err := e.client.DeleteBundleSecret(ctx, secret); err != nil {
			return err
		}
	}
	e.cleanupCompleted = true
	return nil
}

func (e *KubernetesExecutor) ExecutorMetadata() db.RunnerExecutorMetadata {
	e.mu.Lock()
	defer e.mu.Unlock()
	metadata := db.RunnerExecutorMetadata{
		ExecutorType:          db.RunnerExecutorK8s,
		RequestedImage:        e.image,
		ResolvedImage:         e.image,
		K8sClusterAlias:       e.config.clusterAlias,
		K8sNamespace:          e.config.namespace,
		K8sJobName:            e.job.Name,
		K8sJobUID:             string(e.job.UID),
		K8sPodName:            e.pod.Name,
		K8sPodUID:             string(e.pod.UID),
		K8sContainerName:      taskContainerName,
		K8sLifecycle:          e.lifecycle,
		K8sTerminalReason:     e.terminalReason,
		K8sPolicyRevision:     e.policy.Revision,
		K8sPolicyHash:         e.policy.Hash,
		K8sServiceAccount:     e.config.serviceAccount,
		K8sRuntimeClass:       e.policy.RuntimeClass,
		K8sResourcePolicyID:   strconv.Itoa(e.policy.Revision),
		K8sResourcePolicyHash: e.policy.Hash,
		K8sNetworkProfile:     e.policy.NetworkProfile,
		K8sNetworkEnforcement: string(e.policy.NetworkPolicyEnforcement),
		K8sSecretName:         e.secret.Name,
		K8sSecretUID:          string(e.secret.UID),
		K8sNetworkPolicyName:  e.networkPolicy.Name,
		K8sNetworkPolicyUID:   string(e.networkPolicy.UID),
		K8sRetentionState:     "active",
	}
	if e.lifecycle == "succeeded" || e.lifecycle == "failed" || e.lifecycle == "stopped" {
		if e.retentionDeadline == nil {
			deadline := time.Now().UTC().Add(e.policy.TerminalRetentionDuration())
			e.retentionDeadline = &deadline
		}
		deadline := *e.retentionDeadline
		metadata.K8sRetentionDeadline = &deadline
		metadata.K8sRetentionState = "terminal"
	}
	return metadata
}

func safeTerminalReason(value string) string {
	value = strings.TrimSpace(strings.Map(func(character rune) rune {
		if character < 0x20 || character == 0x7f {
			return -1
		}
		return character
	}, value))
	if len(value) > 128 {
		value = value[:128]
	}
	return value
}

func fallbackReason(value string) string {
	if value == "" {
		return "Unknown"
	}
	return value
}
