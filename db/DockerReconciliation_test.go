package db

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func validDockerReconciliationObservation() DockerReconciliationObservation {
	return DockerReconciliationObservation{
		Sequence: 1, RunnerID: 1, RunnerBoot: "boot-1", TaskID: 2, ProjectID: 3, Generation: 1,
		Resource: DockerReconciliationResourceTask, State: DockerReconciliationRunning, ObservedAt: time.Now().UTC(),
		QuarantineStatus: DockerReconciliationQuarantineNone, Remediation: DockerReconciliationRemediationNone,
	}
}

func TestDockerReconciliationObservationValidation(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*DockerReconciliationObservation)
	}{
		{name: "valid"},
		{name: "runner boot whitespace", mutate: func(o *DockerReconciliationObservation) { o.RunnerBoot = " boot-1" }},
		{name: "sequence is positive", mutate: func(o *DockerReconciliationObservation) { o.Sequence = 0 }},
		{name: "task attribution is required", mutate: func(o *DockerReconciliationObservation) { o.TaskID = 0 }},
		{name: "resource is typed", mutate: func(o *DockerReconciliationObservation) { o.Resource = "sidecar" }},
		{name: "container bound", mutate: func(o *DockerReconciliationObservation) {
			o.ContainerID = strings.Repeat("c", MaxRunnerContainerIdentityLength+1)
		}},
		{name: "quarantine requires timestamp and remediation", mutate: func(o *DockerReconciliationObservation) {
			o.State = DockerReconciliationQuarantine
			o.QuarantineStatus = DockerReconciliationQuarantinePending
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			observation := validDockerReconciliationObservation()
			if test.mutate != nil {
				test.mutate(&observation)
			}
			if test.name == "valid" {
				assert.NoError(t, observation.Validate())
				return
			}
			assert.Error(t, observation.Validate())
		})
	}
}

func TestDockerReconciliationQueryValidation(t *testing.T) {
	assert.NoError(t, (DockerReconciliationQuery{Limit: 1}).Validate())
	assert.Error(t, (DockerReconciliationQuery{Limit: 0}).Validate())
	assert.Error(t, (DockerReconciliationQuery{Limit: maxDockerReconciliationQueryLimit + 1}).Validate())
	assert.Error(t, (DockerReconciliationQuery{Limit: 1, AfterSequence: -1}).Validate())
}
