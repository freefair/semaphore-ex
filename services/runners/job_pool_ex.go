package runners

import (
	"fmt"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/util"
	"net/url"
	"strings"
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

func (p *JobPool) dockerDispatchReady() bool {
	if resolveExecutorType(util.Config.Runner.Executor) != util.ExecutorTypeDocker {
		return true
	}
	_, ready := p.currentDockerPolicyAck()
	return ready
}

func (p *JobPool) canDispatchQueuedJob(candidate *job) bool {
	if candidate.dockerPolicyAck == nil {
		return true
	}
	acknowledged, ready := p.currentDockerPolicyAck()
	return ready && *candidate.dockerPolicyAck == acknowledged
}
