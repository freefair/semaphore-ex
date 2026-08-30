// Package k8s implements the enhanced-edition Kubernetes task executor.
package k8s

import (
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
	config       config
	client       KubernetesClient
	keyInstaller db_lib.AccessKeyInstaller
	repoLock     *tasks.KeyLock
	mu           sync.RWMutex
	runnerID     int
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
	}
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

func (p *Provider) NewExecutor(task db.Task, template db.Template, inventory db.Inventory, repository db.Repository, environment db.Environment, jwt string) (tasks.Executor, error) {
	image, err := p.config.taskImage(template)
	if err != nil {
		return nil, err
	}
	p.mu.RLock()
	runnerID := p.runnerID
	p.mu.RUnlock()
	if runnerID <= 0 {
		return nil, fmt.Errorf("Kubernetes runner identity is not installed")
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
	executor := newExecutorForPlan(p.client, p.config, runnerID, task, template, task_logger.NopLogger{})
	executor.local = local
	executor.image = image
	return executor, nil
}
