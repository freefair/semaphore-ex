// Package k8s implements the enhanced-edition Kubernetes task executor.
package k8s

import (
	"context"
	"fmt"
	"sync"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db_lib"
	"github.com/semaphoreui/semaphore/pkg/ssh"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/util"
)

type Provider struct {
	config         config
	client         KubernetesClient
	keyInstaller   db_lib.AccessKeyInstaller
	repoLock       *tasks.KeyLock
	mu             sync.RWMutex
	runnerID       int
	policy         db.KubernetesExecutionPolicy
	reconciliation db.KubernetesReconciliationSession
}

func NewProvider(input util.RunnerK8sConfig) (tasks.ExecutorProvider, error) {
	cfg, err := effectiveConfig(input)
	if err != nil {
		return nil, err
	}
	client, err := newKubernetesClient(cfg)
	if err != nil {
		return nil, err
	}
	return newProviderWithClient(cfg, client), nil
}

func newProviderWithClient(cfg config, client KubernetesClient) *Provider {
	return &Provider{
		config:       cfg,
		client:       client,
		keyInstaller: ssh.KeyInstaller{},
		repoLock:     &tasks.KeyLock{},
		policy:       db.DefaultKubernetesExecutionPolicy(cfg.clusterAlias),
	}
}

func (p *Provider) ApplyKubernetesExecutionPolicy(policy db.KubernetesExecutionPolicy) error {
	if err := policy.Canonicalize(); err != nil {
		return err
	}
	if policy.ClusterAlias != p.config.clusterAlias {
		return db.KubernetesPolicyViolationError{Rule: db.KubernetesPolicyRuleClusterDenied}
	}
	if err := validateProviderConfiguration(policy, p.config); err != nil {
		return err
	}
	p.mu.Lock()
	p.policy = policy
	p.mu.Unlock()
	return nil
}

func (p *Provider) KubernetesExecutionPolicyAcknowledgement() db.KubernetesExecutionPolicyAck {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.policy.Acknowledgement()
}

func (p *Provider) ApplyRunnerIdentity(runnerID int) error {
	if runnerID <= 0 {
		return fmt.Errorf("Kubernetes runner identity is required")
	}
	p.mu.Lock()
	p.runnerID = runnerID
	p.mu.Unlock()
	return nil
}

func (p *Provider) ApplyKubernetesReconciliationSession(session db.KubernetesReconciliationSession) error {
	if session.ValidateCredentials() != nil || session.ClusterAlias != p.config.clusterAlias || session.Namespace != p.config.namespace {
		return fmt.Errorf("Kubernetes reconciliation session does not match runner configuration")
	}
	p.mu.Lock()
	p.reconciliation = session
	p.mu.Unlock()
	return nil
}

func (p *Provider) ScanKubernetesReconciliation(ctx context.Context, session db.KubernetesReconciliationSession) (db.KubernetesReconciliationScan, error) {
	p.mu.RLock()
	installed := p.reconciliation
	runnerID := p.runnerID
	p.mu.RUnlock()
	if installed.SessionID != session.SessionID || installed.Fence != session.Fence || runnerID != session.RunnerID {
		return db.KubernetesReconciliationScan{}, fmt.Errorf("Kubernetes reconciliation session is not installed")
	}
	scanner, ok := p.client.(kubernetesReconciliationScanner)
	if !ok {
		return db.KubernetesReconciliationScan{}, fmt.Errorf("Kubernetes client cannot perform namespaced reconciliation")
	}
	return scanner.ScanKubernetesReconciliation(ctx, session, runnerID)
}

// RemediateKubernetesReconciliation accepts only a server-created command
// bound to the session installed by the current authenticated runner poll.
func (p *Provider) RemediateKubernetesReconciliation(ctx context.Context, command db.KubernetesReconciliationRemediationCommand) db.KubernetesReconciliationRemediationResult {
	p.mu.RLock()
	session, runnerID := p.reconciliation, p.runnerID
	p.mu.RUnlock()
	result := db.KubernetesReconciliationRemediationResult{CommandID: command.CommandID, Status: db.KubernetesReconciliationRemediationBlocked, Evidence: db.KubernetesReconciliationEvidenceUnsafeTarget}
	if command.Validate() != nil || command.Action != db.KubernetesReconciliationGarbageCollectExpired || session.SessionID != command.SessionID || runnerID <= 0 {
		return result
	}
	remediator, ok := p.client.(kubernetesReconciliationRemediator)
	if !ok {
		return result
	}
	return remediator.GarbageCollectKubernetesReconciliation(ctx, command, runnerID)
}

func (p *Provider) NewExecutor(task db.Task, template db.Template, inventory db.Inventory, repository db.Repository, environment db.Environment, jwt string) (tasks.Executor, error) {
	image, err := p.config.taskImage(template)
	if err != nil {
		return nil, err
	}
	p.mu.RLock()
	runnerID := p.runnerID
	policy := p.policy
	p.mu.RUnlock()
	if runnerID <= 0 {
		return nil, fmt.Errorf("Kubernetes runner identity is not installed")
	}
	if err := validateTaskExecution(policy, p.config, image); err != nil {
		return nil, err
	}
	local := &tasks.LocalExecutor{
		Task:         task,
		Template:     template,
		Inventory:    inventory,
		Repository:   repository,
		Environment:  environment,
		Secret:       task.Secret,
		KeyInstaller: p.keyInstaller,
		App:          db_lib.CreateApp(template, repository, inventory, nil),
		JWT:          jwt,
		RepoLock:     p.repoLock,
	}
	executor := newExecutorForPlan(p.client, p.config, policy, runnerID, task, template, task_logger.NopLogger{})
	executor.local = local
	executor.image = image
	return executor, nil
}
