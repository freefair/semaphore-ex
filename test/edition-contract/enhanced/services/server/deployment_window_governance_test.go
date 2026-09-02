package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeploymentWindowGovernanceProjectsPrivateHistorySafely(t *testing.T) {
	repository := &deploymentWindowGovernanceRepositoryStub{
		policy: db.DeploymentWindowPolicy{ProjectID: 7, Revision: 2, Timezone: "UTC", Default: db.DeploymentWindowDefaultDeny},
		history: []db.DeploymentWindowDecisionRecord{{
			ID: 11, ProjectID: 7, Source: string(pro_interfaces.DeploymentWindowSourceManual), Origin: string(pro_interfaces.DeploymentWindowOriginUser),
			PolicyRevision: 2, EffectiveTimezone: "UTC", EvaluatedAt: time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC),
			State: string(pro_interfaces.DeploymentWindowDecisionOverridden), Reason: string(pro_interfaces.DeploymentWindowReasonOverride), NextEligibleKnown: true,
			OverrideActorID: intPointerGovernance(4), OverrideReference: stringPointerGovernance("INC-123"),
			MatchedRulesJSON: `[{"id":3,"revision":2,"kind":"freeze"}]`, Created: time.Date(2026, 9, 3, 10, 0, 1, 0, time.UTC),
		}},
	}
	service := NewDeploymentWindowGovernanceService(repository)

	history, err := service.DecisionHistory(context.Background(), 7, db.RetrieveQueryParams{Count: 10})
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, 11, history[0].ID)
	assert.Equal(t, 2, history[0].Decision.Provenance.PolicyRevision)
	assert.Equal(t, 4, history[0].Decision.Provenance.OverrideActorID)
	assert.Equal(t, 3, history[0].Decision.Provenance.MatchedRules[0].ID)
	encoded, err := json.Marshal(history[0])
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "INC-123")
	assert.NotContains(t, string(encoded), "override_reference")
}

func TestDeploymentWindowGovernanceEvaluatesStatusAtRepositoryTime(t *testing.T) {
	repository := &deploymentWindowGovernanceRepositoryStub{policy: db.DeploymentWindowPolicy{ProjectID: 7, Revision: 2, Timezone: "UTC", Default: db.DeploymentWindowDefaultAllow}}
	service := NewDeploymentWindowGovernanceService(repository)

	status, err := service.CurrentStatus(context.Background(), pro_interfaces.DeploymentWindowStatusRequest{ProjectID: 7, TemplateID: intPointerGovernance(9)})
	require.NoError(t, err)
	assert.Equal(t, pro_interfaces.DeploymentWindowDecisionAllowed, status.State)
	assert.Equal(t, pro_interfaces.DeploymentWindowReasonDefaultAllow, status.Reason)
	assert.Equal(t, repository.databaseTime, repository.observedEvaluationAt)
}

type deploymentWindowGovernanceRepositoryStub struct {
	policy               db.DeploymentWindowPolicy
	history              []db.DeploymentWindowDecisionRecord
	databaseTime         time.Time
	observedEvaluationAt time.Time
}

func (s *deploymentWindowGovernanceRepositoryStub) GetDeploymentWindowPolicy(projectID int) (db.DeploymentWindowPolicy, error) {
	return s.policy, nil
}

func (s *deploymentWindowGovernanceRepositoryStub) SaveDeploymentWindowPolicy(policy db.DeploymentWindowPolicy, expectedRevision int) (db.DeploymentWindowPolicy, error) {
	return policy, nil
}

func (s *deploymentWindowGovernanceRepositoryStub) DeleteDeploymentWindowPolicy(projectID int, expectedRevision int) error {
	return nil
}

func (s *deploymentWindowGovernanceRepositoryStub) ClaimDeploymentWindowAdmission(request pro_interfaces.DeploymentWindowAdmissionRequest, evaluate func(db.DeploymentWindowPolicy, pro_interfaces.DeploymentWindowEvaluationRequest) (pro_interfaces.DeploymentWindowDecision, error)) (pro_interfaces.DeploymentWindowAdmissionClaim, error) {
	return pro_interfaces.DeploymentWindowAdmissionClaim{}, nil
}

func (s *deploymentWindowGovernanceRepositoryStub) GetDeploymentWindowDecisionHistory(projectID int, params db.RetrieveQueryParams) ([]db.DeploymentWindowDecisionRecord, error) {
	return s.history, nil
}

func (s *deploymentWindowGovernanceRepositoryStub) GetDeploymentWindowDatabaseTime() (time.Time, error) {
	return s.databaseTime, nil
}

func (s *deploymentWindowGovernanceRepositoryStub) PreviewDeploymentWindowPolicy(policy db.DeploymentWindowPolicy, request pro_interfaces.DeploymentWindowStatusRequest, evaluate func(db.DeploymentWindowPolicy, pro_interfaces.DeploymentWindowEvaluationRequest) (pro_interfaces.DeploymentWindowDecision, error)) (pro_interfaces.DeploymentWindowDecision, error) {
	if s.databaseTime.IsZero() {
		s.databaseTime = time.Date(2026, 9, 3, 11, 0, 0, 0, time.UTC)
	}
	decision, err := evaluate(policy, pro_interfaces.DeploymentWindowEvaluationRequest{ProjectID: request.ProjectID, TemplateID: governanceDeref(request.TemplateID), WorkflowID: governanceDeref(request.WorkflowID), Source: pro_interfaces.DeploymentWindowSourceManual, At: s.databaseTime})
	s.observedEvaluationAt = s.databaseTime
	return decision, err
}

func (s *deploymentWindowGovernanceRepositoryStub) EvaluateDeploymentWindowStatus(request pro_interfaces.DeploymentWindowStatusRequest, evaluate func(db.DeploymentWindowPolicy, pro_interfaces.DeploymentWindowEvaluationRequest) (pro_interfaces.DeploymentWindowDecision, error)) (pro_interfaces.DeploymentWindowDecision, error) {
	if s.databaseTime.IsZero() {
		s.databaseTime = time.Date(2026, 9, 3, 11, 0, 0, 0, time.UTC)
	}
	decision, err := evaluate(s.policy, pro_interfaces.DeploymentWindowEvaluationRequest{ProjectID: request.ProjectID, TemplateID: governanceDeref(request.TemplateID), WorkflowID: governanceDeref(request.WorkflowID), Source: pro_interfaces.DeploymentWindowSourceManual, At: s.databaseTime})
	s.observedEvaluationAt = s.databaseTime
	return decision, err
}

func governanceDeref(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func intPointerGovernance(value int) *int          { return &value }
func stringPointerGovernance(value string) *string { return &value }
