package runners

import (
	"context"
	"fmt"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/util"
	"net/url"
	"strings"
	"time"
)

func runnerTransportTrust(webHost string, conn *util.RunnerConnectionConfig) db.RunnerTransportTrust {
	parsed, err := url.Parse(webHost)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") {
		return db.RunnerTransportPlaintext
	}
	if conn == nil {
		return db.RunnerTransportSystemCA
	}
	if conn.SkipTLSVerify {
		return db.RunnerTransportInsecure
	}
	if conn.ServerCACertFile != "" {
		return db.RunnerTransportCustomCA
	}
	return db.RunnerTransportSystemCA
}

const maxDockerReconciliationQuarantinesPerProgress = 100

func (p *JobPool) currentDockerPolicyAck() (db.DockerExecutionPolicyAck, bool) {
	p.dockerPolicyMu.Lock()
	defer p.dockerPolicyMu.Unlock()
	return p.dockerPolicyAck, p.dockerPolicyReady
}

func (p *JobPool) applyDockerPolicy(policy db.DockerExecutionPolicy) error {
	consumer, ok := p.provider.(tasks.DockerExecutionPolicyConsumer)
	if !ok {
		return fmt.Errorf("Docker policy was received by a runner without a Docker policy consumer")
	}
	if err := consumer.ApplyDockerExecutionPolicy(policy); err != nil {
		return err
	}
	ack := consumer.DockerExecutionPolicyAcknowledgement()
	if !policy.MatchesAck(ack) {
		return fmt.Errorf("Docker policy provider acknowledgement does not match the delivered policy")
	}
	p.dockerPolicyMu.Lock()
	p.dockerPolicyAck = ack
	p.dockerPolicyReady = true
	p.dockerPolicyMu.Unlock()
	return nil
}

func (p *JobPool) applyDockerRunnerIdentity(runnerID int) error {
	consumer, ok := p.provider.(tasks.DockerRunnerIdentityConsumer)
	if !ok {
		return fmt.Errorf("Docker runner does not support server identity installation")
	}
	return consumer.ApplyDockerRunnerIdentity(runnerID)
}

func (p *JobPool) dockerDispatchReady() bool {
	if resolveExecutorType(util.Config.Runner.Executor) != util.ExecutorTypeDocker {
		return true
	}
	_, ready := p.currentDockerPolicyAck()
	p.dockerPolicyMu.Lock()
	defer p.dockerPolicyMu.Unlock()
	return ready && p.dockerReconciliationReady
}

func (p *JobPool) canDispatchQueuedJob(candidate *job) bool {
	if candidate.dockerPolicyAck == nil {
		return true
	}
	acknowledged, ready := p.currentDockerPolicyAck()
	return ready && *candidate.dockerPolicyAck == acknowledged
}

func (p *JobPool) applyDockerRemediationCommands(commands []db.DockerReconciliationRemediationCommand) {
	if len(commands) == 0 {
		return
	}
	remediator, ok := p.provider.(tasks.DockerReconciliationRemediator)
	if !ok {
		return
	}
	for _, command := range commands {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		result := remediator.RemediateDockerReconciliation(ctx, command)
		cancel()
		p.dockerPolicyMu.Lock()
		duplicate := false
		for _, pending := range p.dockerRemediationResults {
			if pending.CommandID == result.CommandID && pending.Fingerprint == result.Fingerprint {
				duplicate = true
				break
			}
		}
		if !duplicate {
			p.dockerRemediationResults = append(p.dockerRemediationResults, result)
		}
		p.dockerPolicyMu.Unlock()
	}
}

// finishStoppedJob is the only runner-side stopped transition for an optional
// Docker confirmer. A completed Run call is not daemon evidence: Docker must
// confirm the named container stopped first.
func (p *JobPool) finishStoppedJob(running *runningJob) {
	stopper, ok := running.job.(tasks.ConfirmedStopper)
	if !ok {
		running.job.Kill()
		running.SetStatus(task_logger.TaskStoppedStatus)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	confirmation := stopper.ConfirmStop(ctx)
	cancel()
	if confirmation == tasks.StopConfirmed {
		running.SetStatus(task_logger.TaskStoppedStatus)
		return
	}
	running.SetStatus(task_logger.TaskStoppingStatus)
	if confirmation == tasks.StopQuarantined {
		running.Log("Docker cancellation quarantined: daemon stop state could not be confirmed")
		if reporter, ok := running.job.(tasks.DockerReconciliationQuarantineReporter); ok {
			quarantine := reporter.DockerCancellationQuarantine()
			p.dockerPolicyMu.Lock()
			alreadyQueued := false
			for _, queued := range p.dockerQuarantines {
				if queued == quarantine {
					alreadyQueued = true
					break
				}
			}
			if !alreadyQueued {
				p.dockerQuarantines = append(p.dockerQuarantines, quarantine)
			}
			p.dockerPolicyMu.Unlock()
		}
	}
}
