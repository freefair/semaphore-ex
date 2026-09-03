package server

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommunityLogWriterHasNoSideEffects(t *testing.T) {
	service := NewLogWriteService()

	assert.NoError(t, service.WriteEventLog(pro_interfaces.EventLogRecord{Action: "tripwire"}))
	assert.NoError(t, service.WriteTaskLog(pro_interfaces.TaskLogRecord{TaskID: 42}))
	assert.NoError(t, service.WriteResult(map[string]string{"secret": "tripwire"}))
	assert.Equal(t, pro_interfaces.StructuredLogDisabled, service.Diagnostics().State)
	assert.False(t, service.Diagnostics().Enabled)
	assert.NoError(t, service.Close())
}

func TestCommunityWorkflowServiceReturnsEmptyResults(t *testing.T) {
	service := NewWorkflowService(nil, nil, nil, nil)

	run, err := service.StartWorkflow(db.WorkflowTemplate{}, nil, "")
	require.NoError(t, err)
	assert.Equal(t, db.WorkflowRun{}, run)
	artifacts, err := service.GetWorkflowRunArtifacts(1, 2, nil)
	require.NoError(t, err)
	assert.Empty(t, artifacts)
}

func TestCommunitySecretStorageCollectionIsEmpty(t *testing.T) {
	storages, err := GetSecretStorages(nil, 42)
	require.NoError(t, err)
	assert.Empty(t, storages)
}

func TestCommunityPolicyGuardrailServicesAreUnavailable(t *testing.T) {
	assert.Nil(t, NewPolicyGuardrailAdmissionService(nil))
	assert.Nil(t, NewPolicyGuardrailGovernanceService(nil))
}
