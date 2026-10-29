package tasks

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func infisicalPAMPreflightReferences(plan pro_interfaces.ExecutionPreflightPlan) []pro_interfaces.ExecutionPreflightReference {
	references := make([]pro_interfaces.ExecutionPreflightReference, 0)
	for _, reference := range plan.References {
		if reference.Kind == pro_interfaces.ExecutionReferenceSecretStorage {
			references = append(references, reference)
		}
	}
	return references
}

func infisicalPAMPreflightFinding(plan pro_interfaces.ExecutionPreflightPlan, code pro_interfaces.ExecutionPreflightReasonCode) *pro_interfaces.ExecutionPreflightFinding {
	for index := range plan.Findings {
		if plan.Findings[index].Code == code {
			return &plan.Findings[index]
		}
	}
	return nil
}

func TestTaskExecutionPreflightListsInfisicalPAMRoutes(t *testing.T) {
	store, pool, actor, template, _ := createTaskPreflightFixture(t)
	storage := createInfisicalPAMStorage(t, store, template.ProjectID, []any{
		map[string]any{"host": "web-1.example.test", "account": "servers/web-1"},
		map[string]any{"host": "db-1.example.test", "account": "servers/db-1"},
	})
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

	snapshot, err := pool.BuildTaskExecutionPreflight(db.Task{TemplateID: template.ID}, &actor, template.ProjectID, now)

	require.NoError(t, err)
	assert.Equal(t, []pro_interfaces.ExecutionPreflightReference{{
		Kind: pro_interfaces.ExecutionReferenceSecretStorage, ID: storage.ID, Name: "pam",
		BindingTarget: "ssh: web-1.example.test → servers/web-1, db-1.example.test → servers/db-1", Visible: true,
	}}, infisicalPAMPreflightReferences(snapshot.Plan))
	assert.Nil(t, infisicalPAMPreflightFinding(snapshot.Plan, pro_interfaces.ExecutionReasonInfisicalPAMInvalid))
	encoded, err := json.Marshal(snapshot.Plan)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "client-1", "the machine identity stays out of the plan")
}

func TestTaskExecutionPreflightInfisicalPAMRouteChangeMakesReviewStale(t *testing.T) {
	store, pool, actor, template, _ := createTaskPreflightFixture(t)
	storage := createInfisicalPAMStorage(t, store, template.ProjectID, []any{map[string]any{"host": "web-1", "account": "servers/web-1"}})
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	before, err := pool.BuildTaskExecutionPreflight(db.Task{TemplateID: template.ID}, &actor, template.ProjectID, now)
	require.NoError(t, err)

	storage.Params["pam_routes"] = []any{map[string]any{"host": "web-1", "account": "servers/other"}}
	require.NoError(t, store.UpdateSecretStorage(storage))
	after, err := pool.BuildTaskExecutionPreflight(db.Task{TemplateID: template.ID}, &actor, template.ProjectID, now)

	require.NoError(t, err)
	assert.Equal(t, []pro_interfaces.ExecutionPreflightChangeCode{pro_interfaces.ExecutionChangeReference}, componentChanges(before.Components, after.Components))
}

func TestTaskExecutionPreflightDeniesInfisicalPAMTheRunnerWouldReject(t *testing.T) {
	store, pool, actor, template, _ := createTaskPreflightFixture(t)
	route := []any{map[string]any{"host": "web-1", "account": "servers/web-1"}}
	createInfisicalPAMStorage(t, store, template.ProjectID, route)
	createInfisicalPAMStorage(t, store, template.ProjectID, route)

	snapshot, err := pool.BuildTaskExecutionPreflight(db.Task{TemplateID: template.ID}, &actor, template.ProjectID, time.Now().UTC())

	require.NoError(t, err)
	finding := infisicalPAMPreflightFinding(snapshot.Plan, pro_interfaces.ExecutionReasonInfisicalPAMInvalid)
	require.NotNil(t, finding)
	assert.Equal(t, pro_interfaces.ExecutionFindingDenial, finding.Severity)
	assert.Equal(t, `Infisical PAM routes cannot be applied: host "web-1" is routed by more than one Infisical storage`, finding.Message)
	assert.Empty(t, infisicalPAMPreflightReferences(snapshot.Plan))
}

func TestTaskExecutionPreflightWarnsThatContainerTasksSkipInfisicalPAM(t *testing.T) {
	store, pool, actor, template, _ := createTaskPreflightFixture(t)
	createInfisicalPAMStorage(t, store, template.ProjectID, []any{map[string]any{"host": "web-1", "account": "servers/web-1"}})
	image := "ghcr.io/example/ansible:1"
	template.ExecutorImage = &image

	snapshot, _, err := pool.buildTaskExecutionPreflight(db.Task{TemplateID: template.ID}, template, &actor, template.ProjectID, time.Now().UTC())

	require.NoError(t, err)
	finding := infisicalPAMPreflightFinding(snapshot.Plan, pro_interfaces.ExecutionReasonInfisicalPAMSkipped)
	require.NotNil(t, finding)
	assert.Equal(t, pro_interfaces.ExecutionFindingWarning, finding.Severity)
}

func TestTaskExecutionPreflightWithoutInfisicalPAMIsUnchanged(t *testing.T) {
	_, pool, actor, template, _ := createTaskPreflightFixture(t)

	snapshot, err := pool.BuildTaskExecutionPreflight(db.Task{TemplateID: template.ID}, &actor, template.ProjectID, time.Now().UTC())

	require.NoError(t, err)
	assert.Empty(t, infisicalPAMPreflightReferences(snapshot.Plan))
	assert.Nil(t, infisicalPAMPreflightFinding(snapshot.Plan, pro_interfaces.ExecutionReasonInfisicalPAMSkipped))
}

func TestTaskExecutionPreflightSummarizesManyInfisicalPAMRoutesInOneReference(t *testing.T) {
	store, pool, actor, template, _ := createTaskPreflightFixture(t)
	routes := make([]any, 0, 300)
	for index := 0; index < 300; index++ {
		routes = append(routes, map[string]any{"host": fmt.Sprintf("web-%03d.example.test", index), "account": fmt.Sprintf("servers/web-%03d", index)})
	}
	createInfisicalPAMStorage(t, store, template.ProjectID, routes)

	snapshot, err := pool.BuildTaskExecutionPreflight(db.Task{TemplateID: template.ID}, &actor, template.ProjectID, time.Now().UTC())

	require.NoError(t, err)
	references := infisicalPAMPreflightReferences(snapshot.Plan)
	require.Len(t, references, 1)
	assert.Equal(t, "ssh: 300 hosts", references[0].BindingTarget)
	assert.Nil(t, infisicalPAMPreflightFinding(snapshot.Plan, pro_interfaces.ExecutionReasonPlanLimitExceeded))
}
