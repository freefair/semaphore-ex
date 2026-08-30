package docker

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db_lib"
	"github.com/semaphoreui/semaphore/pkg/ssh"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/util"
)

const (
	labelExecutor   = "io.semaphore.executor"
	labelTaskID     = "io.semaphore.task-id"
	labelProjectID  = "io.semaphore.project-id"
	labelRunnerBoot = "io.semaphore.runner-boot"
	labelResource   = "io.semaphore.resource"
)

var idleContainerCommand = []string{"/bin/sh", "-c", "trap 'exit 0' TERM INT; while :; do sleep 3600; done"}

type Provider struct {
	config       config
	client       DockerClient
	keyInstaller db_lib.AccessKeyInstaller
	runnerBoot   string
	repoLock     *tasks.KeyLock
}

// NewProvider builds one long-lived Docker API client for the runner. The
// optional installer preserves the public one-argument edition seam while the
// runner factory can provide its existing installer explicitly.
func NewProvider(input util.RunnerDockerConfig, installers ...db_lib.AccessKeyInstaller) (tasks.ExecutorProvider, error) {
	if len(installers) > 1 {
		return nil, fmt.Errorf("Docker provider accepts at most one access-key installer")
	}
	cfg, err := effectiveConfig(input)
	if err != nil {
		return nil, err
	}
	client, err := newMobyClient(cfg)
	if err != nil {
		return nil, err
	}
	installer := db_lib.AccessKeyInstaller(ssh.KeyInstaller{})
	if len(installers) == 1 && installers[0] != nil {
		installer = installers[0]
	}
	nonce, err := runnerBootNonce()
	if err != nil {
		return nil, err
	}
	return &Provider{config: cfg, client: client, keyInstaller: installer, runnerBoot: nonce, repoLock: &tasks.KeyLock{}}, nil
}

func runnerBootNonce() (string, error) {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("creating Docker runner boot nonce: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func (p *Provider) NewExecutor(task db.Task, template db.Template, inventory db.Inventory, repository db.Repository, environment db.Environment, jwt string) (tasks.Executor, error) {
	if _, err := p.config.taskImage(template); err != nil {
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
	return &DockerExecutor{
		client:        p.client,
		config:        p.config,
		runnerBoot:    p.runnerBoot,
		task:          task,
		template:      template,
		local:         local,
		logger:        task_logger.NopLogger{},
		status:        task_logger.TaskStartingStatus,
		containerName: dockerTaskName(task.ID, p.runnerBoot),
	}, nil
}

type DockerExecutor struct {
	client     DockerClient
	config     config
	runnerBoot string
	task       db.Task
	template   db.Template
	local      *tasks.LocalExecutor
	logger     task_logger.Logger

	mu            sync.Mutex
	condition     *sync.Cond
	status        task_logger.TaskStatus
	killed        bool
	runCancel     context.CancelFunc
	runGeneration uint64
	plan          *tasks.ContainerTaskPlan
	helperID      string
	containerID   string
	reportedID    string
	containerName string
	volumeName    string
}

func newDockerExecutorForPlan(client DockerClient, cfg config, runnerBoot string, task db.Task, template db.Template, logger task_logger.Logger) *DockerExecutor {
	return &DockerExecutor{
		client: client, config: cfg, runnerBoot: runnerBoot, task: task, template: template,
		logger: logger, status: task_logger.TaskStartingStatus,
		containerName: dockerTaskName(task.ID, runnerBoot),
	}
}

func (e *DockerExecutor) Async() bool { return false }

func (e *DockerExecutor) IsKilled() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.killed
}

func (e *DockerExecutor) SetLogger(logger task_logger.Logger) {
	if logger == nil {
		logger = task_logger.NopLogger{}
	}
	e.logger = logger
	if e.local != nil {
		e.local.SetLogger(logger)
		e.logger = e.local.Logger
	}
}

func (e *DockerExecutor) SetStatus(status task_logger.TaskStatus) {
	e.mu.Lock()
	e.status = status
	if e.condition != nil {
		e.condition.Broadcast()
	}
	e.mu.Unlock()
	e.logger.SetStatus(status)
}

func (e *DockerExecutor) Prepare(username string, incomingVersion *string, alias string) error {
	if e.plan != nil {
		return nil
	}
	if e.local == nil {
		return fmt.Errorf("Docker executor has no task materializer")
	}
	plan, err := e.local.PrepareContainerTask(username, incomingVersion, alias)
	if err != nil {
		return err
	}
	e.plan = plan
	return nil
}

func (e *DockerExecutor) Run(username string, incomingVersion *string, alias string) error {
	defer e.Cleanup()
	if err := e.Prepare(username, incomingVersion, alias); err != nil {
		return err
	}
	return e.runContainerPlan(context.Background(), e.plan)
}

func (e *DockerExecutor) runContainerPlan(ctx context.Context, plan *tasks.ContainerTaskPlan) (runErr error) {
	if plan == nil || plan.Bundle == nil {
		return fmt.Errorf("Docker executor has no task bundle")
	}
	defer plan.Bundle.Close() //nolint:errcheck
	defer func() { e.cleanupDockerResources() }()
	runCtx, runGeneration, started := e.beginRun(ctx)
	if !started {
		return nil
	}
	defer e.endRun(runGeneration)

	taskImage, err := e.config.taskImage(e.template)
	if err != nil {
		return err
	}
	if err := e.client.PrepareImage(runCtx, e.config.helperImage, e.config.pullPolicy); err != nil {
		if e.IsKilled() {
			return nil
		}
		return fmt.Errorf("preparing Docker helper image: %w", err)
	}
	if e.IsKilled() {
		return nil
	}
	if err := e.client.PrepareImage(runCtx, taskImage, e.config.pullPolicy); err != nil {
		if e.IsKilled() {
			return nil
		}
		return fmt.Errorf("preparing Docker task image: %w", err)
	}
	if e.IsKilled() {
		return nil
	}

	baseName := e.containerName
	labels := e.labels("")
	volumeName, err := e.client.CreateVolume(runCtx, baseName+"-bundle", labels)
	if err != nil {
		if e.IsKilled() {
			e.setVolumeName(baseName + "-bundle")
			return nil
		}
		return fmt.Errorf("creating Docker task bundle volume: %w", err)
	}
	e.setVolumeName(volumeName)
	if e.IsKilled() {
		return nil
	}

	helperID, err := e.client.CreateContainer(runCtx, e.containerSpec(baseName+"-helper", e.config.helperImage, volumeName, false, "helper"))
	if err != nil {
		if e.IsKilled() {
			e.setHelperID(baseName + "-helper")
			return nil
		}
		return fmt.Errorf("creating Docker task helper: %w", err)
	}
	e.setHelperID(helperID)
	if e.IsKilled() {
		return nil
	}
	if err := e.client.StartContainer(runCtx, helperID); err != nil {
		if e.IsKilled() {
			return nil
		}
		return fmt.Errorf("starting Docker task helper: %w", err)
	}
	if e.IsKilled() {
		return nil
	}
	if err := e.client.CopyArchive(runCtx, helperID, containerBundlePath, plan.Bundle); err != nil {
		if e.IsKilled() {
			return nil
		}
		return fmt.Errorf("copying Docker task bundle: %w", err)
	}
	if e.IsKilled() {
		return nil
	}
	if err := e.client.RemoveContainer(runCtx, helperID); err != nil {
		if e.IsKilled() {
			return nil
		}
		return fmt.Errorf("removing Docker task helper: %w", err)
	}
	e.setHelperID("")
	if e.IsKilled() {
		return nil
	}

	containerID, err := e.client.CreateContainer(runCtx, e.containerSpec(baseName, taskImage, volumeName, true, "task"))
	if err != nil {
		if e.IsKilled() {
			e.setCleanupContainerID(baseName)
			return nil
		}
		return fmt.Errorf("creating Docker task container: %w", err)
	}
	e.setContainerID(containerID)
	if e.IsKilled() {
		return nil
	}
	if err := e.client.StartContainer(runCtx, containerID); err != nil {
		if e.IsKilled() {
			return nil
		}
		return fmt.Errorf("starting Docker task container: %w", err)
	}
	if e.IsKilled() {
		return nil
	}

	if err := e.runStage(runCtx, plan, tasks.ContainerTaskStageBootstrap, false); err != nil {
		return err
	}
	if e.IsKilled() {
		return nil
	}
	if !plan.App.IsTerraform() {
		return e.runStage(runCtx, plan, tasks.ContainerTaskStageRun, false)
	}

	exitCode, err := e.execStage(runCtx, plan, tasks.ContainerTaskStagePlan)
	if err != nil {
		return err
	}
	switch exitCode {
	case 0:
		return nil
	case 2:
		// Terraform's detailed exit code means the plan contains changes.
	default:
		return fmt.Errorf("Docker task stage plan exited with code %d", exitCode)
	}
	if plan.Terraform.PlanOnly {
		return nil
	}
	if !plan.Terraform.AutoApprove {
		e.SetStatus(task_logger.TaskWaitingConfirmation)
		confirmed, err := e.waitForConfirmation()
		if err != nil || !confirmed {
			return err
		}
		e.SetStatus(task_logger.TaskRunningStatus)
	}
	return e.runStage(runCtx, plan, tasks.ContainerTaskStageApply, false)
}

func (e *DockerExecutor) runStage(ctx context.Context, plan *tasks.ContainerTaskPlan, stage tasks.ContainerTaskStage, allowTerraformChanges bool) error {
	exitCode, err := e.execStage(ctx, plan, stage)
	if err != nil {
		return err
	}
	if e.IsKilled() {
		return nil
	}
	if exitCode != 0 && !(allowTerraformChanges && exitCode == 2) {
		return fmt.Errorf("Docker task stage %s exited with code %d", stage, exitCode)
	}
	return nil
}

func (e *DockerExecutor) execStage(ctx context.Context, plan *tasks.ContainerTaskPlan, stage tasks.ContainerTaskStage) (int, error) {
	stdout := newLineLogger(e.logger)
	stderr := newLineLogger(e.logger)
	exitCode, err := e.client.Exec(ctx, e.currentContainerID(), plan.Command(stage), stdout, stderr)
	stdout.Flush()
	stderr.Flush()
	if err != nil {
		if e.IsKilled() {
			return 0, nil
		}
		return 0, fmt.Errorf("executing Docker task stage %s: %w", stage, err)
	}
	return exitCode, nil
}

func (e *DockerExecutor) waitForConfirmation() (bool, error) {
	e.mu.Lock()
	if e.condition == nil {
		e.condition = sync.NewCond(&e.mu)
	}
	for !e.killed && e.status == task_logger.TaskWaitingConfirmation {
		e.condition.Wait()
	}
	status := e.status
	killed := e.killed
	e.mu.Unlock()
	if killed || status == task_logger.TaskStoppingStatus || status == task_logger.TaskStoppedStatus {
		return false, nil
	}
	if status == task_logger.TaskRejected {
		e.SetStatus(task_logger.TaskFailStatus)
		return false, nil
	}
	if status != task_logger.TaskConfirmed {
		return false, fmt.Errorf("Docker Terraform confirmation ended with status %q", status)
	}
	return true, nil
}

func (e *DockerExecutor) beginRun(parent context.Context) (context.Context, uint64, bool) {
	runCtx, cancel := context.WithCancel(parent)
	e.mu.Lock()
	if e.killed {
		e.mu.Unlock()
		cancel()
		return nil, 0, false
	}
	e.runGeneration++
	generation := e.runGeneration
	e.runCancel = cancel
	e.mu.Unlock()
	return runCtx, generation, true
}

func (e *DockerExecutor) endRun(generation uint64) {
	e.mu.Lock()
	if e.runGeneration != generation {
		e.mu.Unlock()
		return
	}
	cancel := e.runCancel
	e.runCancel = nil
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (e *DockerExecutor) Kill() {
	e.mu.Lock()
	e.killed = true
	containerID := e.containerID
	cancel := e.runCancel
	if e.condition != nil {
		e.condition.Broadcast()
	}
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if containerID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), e.config.cleanupGrace+5*time.Second)
	defer cancel()
	if err := e.client.StopContainer(ctx, containerID, e.config.cleanupGrace); err != nil {
		e.logger.Log("Docker task container did not stop gracefully; forcing termination")
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		if killErr := e.client.KillContainer(killCtx, containerID); killErr != nil {
			e.logger.Log("Unable to force-stop the Docker task container")
		}
	}
}

func (e *DockerExecutor) ExecutorMetadata() db.RunnerExecutorMetadata {
	e.mu.Lock()
	defer e.mu.Unlock()
	return db.RunnerExecutorMetadata{
		ExecutorType:  db.RunnerExecutorDocker,
		ContainerID:   e.reportedID,
		ContainerName: e.containerName,
	}
}

func (e *DockerExecutor) Cleanup() {
	e.cleanupDockerResources()
	if e.plan != nil && e.plan.Bundle != nil {
		_ = e.plan.Bundle.Close()
	}
	if e.local != nil {
		e.local.Cleanup()
	}
}

func (e *DockerExecutor) cleanupDockerResources() {
	e.mu.Lock()
	helperID, containerID, volumeName := e.helperID, e.containerID, e.volumeName
	e.helperID, e.containerID, e.volumeName = "", "", ""
	e.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), e.config.cleanupGrace+10*time.Second)
	defer cancel()
	if helperID != "" {
		if err := e.client.RemoveContainer(ctx, helperID); err != nil {
			e.logger.Log("Unable to remove the Docker task helper container")
		}
	}
	if containerID != "" {
		if err := e.client.RemoveContainer(ctx, containerID); err != nil {
			e.logger.Log("Unable to remove the Docker task container")
		}
	}
	if volumeName != "" {
		if err := e.client.RemoveVolume(ctx, volumeName); err != nil {
			e.logger.Log("Unable to remove the Docker task bundle volume")
		}
	}
}

func (e *DockerExecutor) labels(resource string) map[string]string {
	labels := map[string]string{
		labelExecutor:   "docker",
		labelTaskID:     strconv.Itoa(e.task.ID),
		labelProjectID:  strconv.Itoa(e.task.ProjectID),
		labelRunnerBoot: e.runnerBoot,
	}
	if resource != "" {
		labels[labelResource] = resource
	}
	return labels
}

func (e *DockerExecutor) containerSpec(name string, image string, volumeName string, readOnly bool, resource string) ContainerSpec {
	tmpfs := []string(nil)
	network := "none"
	privileged := false
	if resource == "task" {
		tmpfs = []string{"/workspace", "/tmp", "/home/semaphore"}
		network = e.config.network
		privileged = e.config.privileged
	}
	return ContainerSpec{
		Name:         name,
		Image:        image,
		User:         "65534:0",
		Command:      append([]string(nil), idleContainerCommand...),
		Labels:       e.labels(resource),
		Network:      network,
		NanoCPUs:     e.config.nanoCPUs,
		Memory:       e.config.memory,
		Privileged:   privileged,
		VolumeMounts: []VolumeMount{{Source: volumeName, Target: containerBundlePath, ReadOnly: readOnly}},
		Tmpfs:        tmpfs,
	}
}

func dockerTaskName(taskID int, runnerBoot string) string {
	return fmt.Sprintf("semaphore-task-%d-%s", taskID, runnerBoot)
}

func (e *DockerExecutor) setHelperID(value string) {
	e.mu.Lock()
	e.helperID = value
	e.mu.Unlock()
}

func (e *DockerExecutor) setContainerID(value string) {
	e.mu.Lock()
	e.containerID = value
	if value != "" {
		e.reportedID = value
	}
	e.mu.Unlock()
}

func (e *DockerExecutor) setCleanupContainerID(value string) {
	e.mu.Lock()
	e.containerID = value
	e.mu.Unlock()
}

func (e *DockerExecutor) setVolumeName(value string) {
	e.mu.Lock()
	e.volumeName = value
	e.mu.Unlock()
}

func (e *DockerExecutor) currentContainerID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.containerID
}

type lineLogger struct {
	logger     task_logger.Logger
	pipeReader *io.PipeReader
	pipeWriter *io.PipeWriter
	done       chan struct{}
}

func newLineLogger(logger task_logger.Logger) *lineLogger {
	reader, writer := io.Pipe()
	result := &lineLogger{logger: logger, pipeReader: reader, pipeWriter: writer, done: make(chan struct{})}
	go func() {
		defer close(result.done)
		scanner := bufio.NewScanner(reader)
		buffer := make([]byte, 64*1024)
		scanner.Buffer(buffer, 10*1024*1024)
		for scanner.Scan() {
			logger.Log(scanner.Text())
		}
	}()
	return result
}

func (w *lineLogger) Write(value []byte) (int, error) { return w.pipeWriter.Write(value) }

func (w *lineLogger) Flush() {
	_ = w.pipeWriter.Close()
	<-w.done
	_ = w.pipeReader.Close()
}

var _ tasks.ExecutorProvider = (*Provider)(nil)
var _ tasks.Executor = (*DockerExecutor)(nil)
var _ tasks.ExecutorMetadataProvider = (*DockerExecutor)(nil)
