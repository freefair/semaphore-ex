package sql

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	workflowDB "github.com/semaphoreui/semaphore/pro/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowRunRepositoryPersistsImmutableSnapshotAndConditionalNodeState(t *testing.T) {
	store, repository, projectID := workflowRepositoryFixture(t)
	defer store.Close()
	user, templateOne, templateTwo := workflowRunResources(t, store, projectID)
	now := time.Date(2026, 8, 28, 15, 0, 0, 0, time.UTC)
	workflow, err := repository.CreateWorkflowTemplate(linearRepositoryWorkflow(projectID, templateOne.ID, templateTwo.ID))
	require.NoError(t, err)
	rootNodeID := workflow.Nodes[0].ID
	run, err := workflowDB.BuildWorkflowRunSnapshot(workflow, map[int]db.Template{
		templateOne.ID: templateOne, templateTwo.ID: templateTwo,
	}, user.ID, "run-once", now)
	require.NoError(t, err)

	created, err := repository.CreateWorkflowRun(run)
	require.NoError(t, err)
	require.Positive(t, created.ID)
	require.Len(t, created.Nodes, 2)

	workflow.Name = "Edited later"
	templateOne.Playbook = "edited.yml"
	reloaded, err := repository.GetWorkflowRun(projectID, workflow.ID, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "Linear", reloaded.DefinitionSnapshot.Name)
	assert.Equal(t, "first.yml", reloaded.Nodes[0].TemplateSnapshot.Playbook)
	assert.Equal(t, user.ID, reloaded.ActorUserID)
	assert.Equal(t, "run-once", reloaded.CorrelationID)

	claimed, err := repository.ClaimWorkflowRunNode(projectID, created.ID, rootNodeID, now.Add(time.Second))
	require.NoError(t, err)
	assert.True(t, claimed)
	claimed, err = repository.ClaimWorkflowRunNode(projectID, created.ID, rootNodeID, now.Add(2*time.Second))
	require.NoError(t, err)
	assert.False(t, claimed)

	task, err := store.CreateTask(db.Task{
		ProjectID: projectID, TemplateID: templateOne.ID, Status: task_logger.TaskWaitingStatus,
		WorkflowRunID: &created.ID, WorkflowNodeID: &rootNodeID,
		WorkflowTemplateSnapshot: &created.Nodes[0].TemplateSnapshotJSON,
	}, 0)
	require.NoError(t, err)
	attached, err := repository.AttachWorkflowRunNodeTask(projectID, created.ID, rootNodeID, task.ID)
	require.NoError(t, err)
	assert.True(t, attached)
	attached, err = repository.AttachWorkflowRunNodeTask(projectID, created.ID, rootNodeID, task.ID)
	require.NoError(t, err)
	assert.False(t, attached)

	_, err = store.CreateTask(db.Task{
		ProjectID: projectID, TemplateID: templateOne.ID, Status: task_logger.TaskWaitingStatus,
		WorkflowRunID: &created.ID, WorkflowNodeID: &rootNodeID,
	}, 0)
	require.Error(t, err, "one run attempt must not create two tasks for one node")

	updated, err := repository.UpdateWorkflowRunNodeFromTask(projectID, created.ID, rootNodeID, task.ID, db.WorkflowRunNodeRunning, "", "{}", now.Add(3*time.Second))
	require.NoError(t, err)
	assert.True(t, updated)
	updated, err = repository.UpdateWorkflowRunNodeFromTask(projectID, created.ID, rootNodeID, task.ID, db.WorkflowRunNodeSucceeded, "", `{"status":"succeeded","successful":true}`, now.Add(4*time.Second))
	require.NoError(t, err)
	assert.True(t, updated)
	updated, err = repository.UpdateWorkflowRunNodeFromTask(projectID, created.ID, rootNodeID, task.ID, db.WorkflowRunNodeFailed, "late duplicate", `{"status":"failed","successful":false}`, now.Add(5*time.Second))
	require.NoError(t, err)
	assert.False(t, updated)

	node, err := repository.GetWorkflowRunNode(projectID, created.ID, rootNodeID)
	require.NoError(t, err)
	assert.Equal(t, db.WorkflowRunNodeSucceeded, node.Status)
	assert.Equal(t, db.WorkflowRunNodeSucceeded, node.Result.Status)
	assert.True(t, node.Result.Successful)
	require.NotNil(t, node.Start)
	require.NotNil(t, node.End)

	_, err = repository.GetWorkflowRun(projectID+1, workflow.ID, created.ID)
	assert.ErrorIs(t, err, db.ErrNotFound)
	_, err = repository.GetWorkflowRunNode(projectID+1, created.ID, rootNodeID)
	assert.ErrorIs(t, err, db.ErrNotFound)

	dependentNodeID := workflow.Nodes[1].ID
	finalized, err := repository.FinalizeWorkflowRunNode(
		projectID, created.ID, dependentNodeID, db.WorkflowRunNodeSkipped,
		"condition did not match", `{"status":"skipped","successful":false}`, now.Add(6*time.Second),
	)
	require.NoError(t, err)
	assert.True(t, finalized)
	finalized, err = repository.FinalizeWorkflowRunNode(
		projectID, created.ID, dependentNodeID, db.WorkflowRunNodeBlocked,
		"late duplicate", `{"status":"blocked","successful":false}`, now.Add(7*time.Second),
	)
	require.NoError(t, err)
	assert.False(t, finalized)
	dependent, err := repository.GetWorkflowRunNode(projectID, created.ID, dependentNodeID)
	require.NoError(t, err)
	assert.Equal(t, db.WorkflowRunNodeSkipped, dependent.Status)
	assert.Equal(t, db.WorkflowRunNodeSkipped, dependent.Result.Status)
}

func TestConfiguredWorkflowRepositoryRejectsUnboundRun(t *testing.T) {
	store, repository, projectID := workflowRepositoryFixture(t)
	defer store.Close()
	user, first, second := workflowRunResources(t, store, projectID)
	workflow, err := repository.CreateWorkflowTemplate(linearRepositoryWorkflow(projectID, first.ID, second.ID))
	require.NoError(t, err)
	run, err := workflowDB.BuildWorkflowRunSnapshot(workflow, map[int]db.Template{first.ID: first, second.ID: second}, user.ID, "unbound-admission", time.Now().UTC())
	require.NoError(t, err)
	repository.ConfigureDeploymentWindowAdmission()
	_, err = repository.CreateWorkflowRun(run)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deployment window decision is required")
}

func TestCrossProjectWorkflowTaskFencePersistsConsumerTaskAndPreservesAuditAfterRevoke(t *testing.T) {
	store, ownerProjectID, ownerTemplate := templateVersionGrantFixture(t, "dispatch-owner")
	defer store.Close()
	consumer, err := store.CreateProject(db.Project{Name: "dispatch-consumer"})
	require.NoError(t, err)
	repository := NewWorkflowStore(store.GetConnection())
	versionStore := any(repository).(db.TemplateVersionStore)
	grantStore := any(repository).(db.CrossProjectTemplateGrantStore)
	published, _, err := versionStore.PublishTemplateVersion(ownerTemplate, 1, time.Now().UTC())
	require.NoError(t, err)
	grant, err := grantStore.CreateCrossProjectTemplateGrant(db.CrossProjectTemplateGrant{
		OwnerProjectID: ownerProjectID, ConsumerProjectID: consumer.ID, TemplateID: ownerTemplate.ID,
		MinTemplateVersion: published.VersionNumber, MaxTemplateVersion: published.VersionNumber,
		Operations: db.CrossProjectTemplateGrantReference | db.CrossProjectTemplateGrantRun,
		Status:     db.CrossProjectTemplateGrantPending, Revision: 1, CreatedByUserID: 1,
		Created: time.Now().UTC(), Reason: "fenced dispatch",
	})
	require.NoError(t, err)
	grant, err = grantStore.AcceptCrossProjectTemplateGrant(consumer.ID, grant.ID, 1, grant.Revision, time.Now().UTC())
	require.NoError(t, err)
	reference, version, err := grantStore.ResolveActiveCrossProjectTemplateGrant(
		consumer.ID,
		db.CrossProjectTemplateReference{GrantID: grant.ID, TemplateVersionNumber: published.VersionNumber},
		db.CrossProjectTemplateGrantRun,
	)
	require.NoError(t, err)
	workflow, validation, err := workflowDB.PrepareWorkflowTemplate(store, db.WorkflowTemplate{
		ProjectID: consumer.ID, Name: "external dispatch", Nodes: []db.WorkflowNode{{
			ID: -1, Kind: db.WorkflowNodeTaskKind, TemplateID: reference.TemplateID,
			CrossProjectTemplateReference: &reference,
		}},
	})
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	workflow, workflowVersion, err := repository.CreateWorkflowTemplateVersionedWithCrossProjectReferences(
		workflow, db.WorkflowVersionMutation{AuthorUserID: 1, Created: time.Now().UTC()},
	)
	require.NoError(t, err)
	workflow.CurrentVersionID = workflowVersion.ID
	provenance := db.CrossProjectTemplateProvenance{Reference: reference, TemplateSnapshot: version.Snapshot}
	require.NoError(t, provenance.Validate())

	firstRun := createCrossProjectDispatchRun(t, repository, workflow, provenance, 1, "dispatch-before-revoke")
	firstNodeID := firstRun.Nodes[0].WorkflowNodeID
	ownership := NewWorkflowReconciliationStore(store.GetConnection())
	lease, owned, err := ownership.ClaimWorkflowReconciliation(consumer.ID, firstRun.ID, "dispatch-owner", time.Minute)
	require.NoError(t, err)
	require.True(t, owned)
	claimed, err := repository.ClaimWorkflowRunNodeFenced(lease, firstNodeID, time.Now().UTC())
	require.NoError(t, err)
	require.True(t, claimed)
	firstTask := crossProjectDispatchTask(t, provenance, consumer.ID, firstRun.ID, firstNodeID)
	admissions := NewDeploymentWindowStore(store.GetConnection())
	workflowID, runID, runNodeID := workflow.ID, firstRun.ID, firstRun.Nodes[0].ID
	claim, err := admissions.ClaimDeploymentWindowAdmission(pro_interfaces.DeploymentWindowAdmissionRequest{
		ProjectID: consumer.ID, DecisionKey: "cross-project-workflow-node", Source: pro_interfaces.DeploymentWindowSourceWorkflowNode,
		Origin: pro_interfaces.DeploymentWindowOriginWorkflowNode, WorkflowID: &workflowID, WorkflowRunID: &runID, WorkflowRunNodeID: &runNodeID,
	}, deploymentWindowEvaluator().Evaluate)
	require.NoError(t, err)
	decisionID := claim.Decision.ID
	firstTask.DeploymentWindowDecisionID = &decisionID
	created, err := repository.CreateCrossProjectWorkflowTaskFenced(firstTask, provenance, &lease)
	require.NoError(t, err)
	assert.Equal(t, consumer.ID, created.ProjectID)
	assert.Equal(t, ownerTemplate.ID, created.TemplateID)
	require.NotNil(t, created.WorkflowTemplateProvenance)
	require.NotNil(t, created.WorkflowTemplateProvenance.CrossProject)
	assert.Equal(t, reference, created.WorkflowTemplateProvenance.CrossProject.Reference)
	var decision struct {
		TemplateID         *int `db:"template_id"`
		WorkflowTemplateID int  `db:"workflow_template_id"`
		TaskID             int  `db:"task_id"`
	}
	require.NoError(t, store.Sql().SelectOne(&decision, "select template_id, workflow_template_id, task_id from project__deployment_window_decision where id=?", decisionID))
	assert.Nil(t, decision.TemplateID, "cross-project work is admitted against the consumer workflow, never the owner template")
	assert.Equal(t, workflow.ID, decision.WorkflowTemplateID)
	assert.Equal(t, created.ID, decision.TaskID)
	firstNode, err := repository.GetWorkflowRunNode(consumer.ID, firstRun.ID, firstNodeID)
	require.NoError(t, err)
	require.NotNil(t, firstNode.TaskID)
	assert.Equal(t, created.ID, *firstNode.TaskID)

	secondRun := createCrossProjectDispatchRun(t, repository, workflow, provenance, 1, "dispatch-after-revoke")
	secondNodeID := secondRun.Nodes[0].WorkflowNodeID
	claimed, err = repository.ClaimWorkflowRunNode(consumer.ID, secondRun.ID, secondNodeID, time.Now().UTC())
	require.NoError(t, err)
	require.True(t, claimed)
	wrongTask := crossProjectDispatchTask(t, provenance, consumer.ID, secondRun.ID, secondNodeID)
	wrongTask.DeploymentWindowDecisionID = &decisionID
	_, err = repository.CreateCrossProjectWorkflowTaskFenced(wrongTask, provenance, nil)
	require.Error(t, err, "an already-bound workflow-only decision cannot be moved to another run node")
	_, err = repository.GetWorkflowRunNodeTask(consumer.ID, secondRun.ID, secondNodeID)
	assert.ErrorIs(t, err, db.ErrNotFound, "the failed binding must roll back without a task or node attachment")
	grant, err = grantStore.RevokeCrossProjectTemplateGrant(ownerProjectID, grant.ID, 1, grant.Revision, "withdrawn", time.Now().UTC())
	require.NoError(t, err)
	_, err = repository.CreateCrossProjectWorkflowTaskFenced(
		crossProjectDispatchTask(t, provenance, consumer.ID, secondRun.ID, secondNodeID), provenance, nil,
	)
	assert.ErrorIs(t, err, db.ErrNotFound)
	_, err = repository.GetWorkflowRunNodeTask(consumer.ID, secondRun.ID, secondNodeID)
	assert.ErrorIs(t, err, db.ErrNotFound)

	// A retry for the already-inserted task remains idempotent after revocation;
	// historical work keeps immutable provenance instead of being corrupted.
	replayed, err := repository.CreateCrossProjectWorkflowTaskFenced(firstTask, provenance, nil)
	require.NoError(t, err)
	assert.Equal(t, created.ID, replayed.ID)
}

func TestCrossProjectWorkflowTaskFenceSerializesDispatchWithRevocation(t *testing.T) {
	store, ownerProjectID, ownerTemplate := templateVersionGrantFixture(t, "dispatch-revoke-owner")
	defer store.Close()
	consumer, err := store.CreateProject(db.Project{Name: "dispatch-revoke-consumer"})
	require.NoError(t, err)
	repository := NewWorkflowStore(store.GetConnection())
	versionStore := any(repository).(db.TemplateVersionStore)
	grantStore := any(repository).(db.CrossProjectTemplateGrantStore)
	published, _, err := versionStore.PublishTemplateVersion(ownerTemplate, 1, time.Now().UTC())
	require.NoError(t, err)
	grant, err := grantStore.CreateCrossProjectTemplateGrant(db.CrossProjectTemplateGrant{
		OwnerProjectID: ownerProjectID, ConsumerProjectID: consumer.ID, TemplateID: ownerTemplate.ID,
		MinTemplateVersion: published.VersionNumber, MaxTemplateVersion: published.VersionNumber,
		Operations: db.CrossProjectTemplateGrantReference | db.CrossProjectTemplateGrantRun,
		Status:     db.CrossProjectTemplateGrantPending, Revision: 1, CreatedByUserID: 1,
		Created: time.Now().UTC(), Reason: "dispatch revoke race",
	})
	require.NoError(t, err)
	grant, err = grantStore.AcceptCrossProjectTemplateGrant(consumer.ID, grant.ID, 1, grant.Revision, time.Now().UTC())
	require.NoError(t, err)
	reference, version, err := grantStore.ResolveActiveCrossProjectTemplateGrant(
		consumer.ID,
		db.CrossProjectTemplateReference{GrantID: grant.ID, TemplateVersionNumber: published.VersionNumber},
		db.CrossProjectTemplateGrantRun,
	)
	require.NoError(t, err)
	workflow, validation, err := workflowDB.PrepareWorkflowTemplate(store, db.WorkflowTemplate{
		ProjectID: consumer.ID, Name: "dispatch revoke", Nodes: []db.WorkflowNode{{
			ID: -1, Kind: db.WorkflowNodeTaskKind, TemplateID: reference.TemplateID,
			CrossProjectTemplateReference: &reference,
		}},
	})
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	workflow, workflowVersion, err := repository.CreateWorkflowTemplateVersionedWithCrossProjectReferences(
		workflow, db.WorkflowVersionMutation{AuthorUserID: 1, Created: time.Now().UTC()},
	)
	require.NoError(t, err)
	workflow.CurrentVersionID = workflowVersion.ID
	provenance := db.CrossProjectTemplateProvenance{Reference: reference, TemplateSnapshot: version.Snapshot}
	run := createCrossProjectDispatchRun(t, repository, workflow, provenance, 1, "dispatch-revoke-race")
	nodeID := run.Nodes[0].WorkflowNodeID
	claimed, err := repository.ClaimWorkflowRunNode(consumer.ID, run.ID, nodeID, time.Now().UTC())
	require.NoError(t, err)
	require.True(t, claimed)
	task := crossProjectDispatchTask(t, provenance, consumer.ID, run.ID, nodeID)

	type dispatchResult struct {
		task db.Task
		err  error
	}
	start := make(chan struct{})
	dispatchDone := make(chan dispatchResult, 1)
	revokeDone := make(chan error, 1)
	go func() {
		<-start
		created, dispatchErr := repository.CreateCrossProjectWorkflowTaskFenced(task, provenance, nil)
		dispatchDone <- dispatchResult{task: created, err: dispatchErr}
	}()
	go func() {
		<-start
		_, revokeErr := grantStore.RevokeCrossProjectTemplateGrant(
			ownerProjectID, grant.ID, 1, grant.Revision, "withdrawn", time.Now().UTC(),
		)
		revokeDone <- revokeErr
	}()
	close(start)
	dispatched := <-dispatchDone
	require.NoError(t, <-revokeDone)
	if dispatched.err == nil {
		assert.Positive(t, dispatched.task.ID)
		persisted, getErr := repository.GetWorkflowRunNodeTask(consumer.ID, run.ID, nodeID)
		require.NoError(t, getErr)
		assert.Equal(t, dispatched.task.ID, persisted.ID)
		require.NotNil(t, persisted.WorkflowTemplateProvenance)
		assert.Equal(t, provenance.Reference, persisted.WorkflowTemplateProvenance.CrossProject.Reference)
	} else {
		assert.ErrorIs(t, dispatched.err, db.ErrNotFound)
		_, getErr := repository.GetWorkflowRunNodeTask(consumer.ID, run.ID, nodeID)
		assert.ErrorIs(t, getErr, db.ErrNotFound)
	}
}

func TestCrossProjectWorkflowTaskFencePreservesStorageFailureWithoutIdempotentTask(t *testing.T) {
	store, repository, projectID := workflowRepositoryFixture(t)
	defer store.Close()
	runID, nodeID := 41, 73
	cause := errors.New("task insert failed")
	_, err := repository.resolveExistingCrossProjectWorkflowTask(
		db.Task{ProjectID: projectID, WorkflowRunID: &runID, WorkflowNodeID: &nodeID},
		db.CrossProjectTemplateProvenance{}, cause,
	)
	assert.ErrorIs(t, err, cause)
}

func createCrossProjectDispatchRun(
	t *testing.T,
	repository *WorkflowStoreImpl,
	workflow db.WorkflowTemplate,
	provenance db.CrossProjectTemplateProvenance,
	actorID int,
	correlationID string,
) db.WorkflowRun {
	t.Helper()
	run, err := workflowDB.BuildWorkflowRunSnapshotWithCrossProjectProvenance(
		workflow, nil, map[int]db.CrossProjectTemplateProvenance{workflow.Nodes[0].ID: provenance},
		actorID, correlationID, time.Now().UTC(),
	)
	require.NoError(t, err)
	created, err := repository.CreateWorkflowRunWithCrossProjectReferences(run)
	require.NoError(t, err)
	return created
}

func crossProjectDispatchTask(
	t *testing.T,
	provenance db.CrossProjectTemplateProvenance,
	consumerProjectID int,
	runID int,
	nodeID int,
) db.Task {
	t.Helper()
	template, err := provenance.TemplateSnapshot.ReconstructTemplate(
		provenance.Reference.OwnerProjectID, provenance.Reference.TemplateID,
	)
	require.NoError(t, err)
	templateJSON, err := json.Marshal(template)
	require.NoError(t, err)
	snapshotJSON := string(templateJSON)
	taskProvenance := db.WorkflowTemplateProvenance{CrossProject: &provenance}
	provenanceJSON, err := taskProvenance.CanonicalJSON()
	require.NoError(t, err)
	return db.Task{
		ProjectID: consumerProjectID, TemplateID: provenance.Reference.TemplateID,
		Status: task_logger.TaskWaitingStatus, WorkflowRunID: &runID, WorkflowNodeID: &nodeID,
		WorkflowTemplateSnapshot: &snapshotJSON, WorkflowTemplateProvenance: &taskProvenance,
		WorkflowTemplateProvenanceJSON: &provenanceJSON, Created: time.Now().UTC(),
	}
}

func TestWorkflowRunRepositoryPersistsExactWorkflowVersionReference(t *testing.T) {
	store, repository, projectID := workflowRepositoryFixture(t)
	defer store.Close()
	user, templateOne, templateTwo := workflowRunResources(t, store, projectID)
	versionStore := any(repository).(db.WorkflowVersionStore)
	workflow, version, err := versionStore.CreateWorkflowTemplateVersioned(
		linearRepositoryWorkflow(projectID, templateOne.ID, templateTwo.ID),
		db.WorkflowVersionMutation{AuthorUserID: user.ID, Message: "Initial definition"},
	)
	require.NoError(t, err)
	workflow.CurrentVersionID = version.ID
	run, err := workflowDB.BuildWorkflowRunSnapshot(
		workflow,
		map[int]db.Template{templateOne.ID: templateOne, templateTwo.ID: templateTwo},
		user.ID,
		"exact-version-reference",
		time.Date(2026, 8, 31, 8, 0, 0, 0, time.UTC),
	)
	require.NoError(t, err)

	created, err := repository.CreateWorkflowRun(run)
	require.NoError(t, err)
	reloaded, err := repository.GetWorkflowRun(projectID, workflow.ID, created.ID)
	require.NoError(t, err)
	assert.Equal(t, version.ID, reloaded.WorkflowVersionID)
	assert.Equal(t, version.VersionNumber, reloaded.DefinitionRevision)
}

func TestWorkflowRunRepositoryDoesNotClaimNodesAfterDurableStopRequest(t *testing.T) {
	store, repository, projectID := workflowRepositoryFixture(t)
	defer store.Close()
	user, templateOne, templateTwo := workflowRunResources(t, store, projectID)
	workflow, err := repository.CreateWorkflowTemplate(linearRepositoryWorkflow(projectID, templateOne.ID, templateTwo.ID))
	require.NoError(t, err)
	run, err := workflowDB.BuildWorkflowRunSnapshot(workflow, map[int]db.Template{
		templateOne.ID: templateOne, templateTwo.ID: templateTwo,
	}, user.ID, "stop-before-claim", time.Now())
	require.NoError(t, err)
	run, err = repository.CreateWorkflowRun(run)
	require.NoError(t, err)

	requested, err := repository.RequestWorkflowRunStop(projectID, run.ID)
	require.NoError(t, err)
	assert.True(t, requested)
	claimed, err := repository.ClaimWorkflowRunNode(projectID, run.ID, workflow.Nodes[0].ID, time.Now())
	require.NoError(t, err)
	assert.False(t, claimed)
}

func TestWorkflowRunRepositoryPersistsImmutableParameterAndOverrideSnapshots(t *testing.T) {
	store, repository, projectID := workflowRepositoryFixture(t)
	defer store.Close()
	user, templateOne, templateTwo := workflowRunResources(t, store, projectID)
	now := time.Date(2026, 8, 28, 17, 0, 0, 0, time.UTC)
	definition := linearRepositoryWorkflow(projectID, templateOne.ID, templateTwo.ID)
	definition.ParameterDefinitions = []db.WorkflowParameterDeclaration{{
		Name: "region", Type: db.WorkflowParameterString, Default: json.RawMessage(`"eu"`),
	}}
	parameterJSON, err := json.Marshal(definition.ParameterDefinitions)
	require.NoError(t, err)
	definition.ParameterDefinitionsJSON = string(parameterJSON)
	definition.Nodes[0].OverridePolicy = db.WorkflowNodeOverridePolicy{AllowArguments: true}
	policyJSON, err := json.Marshal(definition.Nodes[0].OverridePolicy)
	require.NoError(t, err)
	definition.Nodes[0].OverridePolicyJSON = string(policyJSON)
	workflow, err := repository.CreateWorkflowTemplate(definition)
	require.NoError(t, err)
	arguments := `["--check"]`
	run, err := workflowDB.BuildWorkflowRunSnapshot(workflow, map[int]db.Template{
		templateOne.ID: templateOne, templateTwo.ID: templateTwo,
	}, user.ID, "parameter-snapshot", now, db.WorkflowRunInput{
		UserValues: map[string]json.RawMessage{"region": json.RawMessage(`"us"`)},
		NodeOverrides: map[int]db.WorkflowNodeOverride{
			workflow.Nodes[0].ID: {Arguments: &arguments},
		},
	})
	require.NoError(t, err)
	created, err := repository.CreateWorkflowRun(run)
	require.NoError(t, err)

	workflow.ParameterDefinitions[0].Default = json.RawMessage(`"changed"`)
	updatedDefinitions, err := json.Marshal(workflow.ParameterDefinitions)
	require.NoError(t, err)
	workflow.ParameterDefinitionsJSON = string(updatedDefinitions)
	_, err = repository.UpdateWorkflowTemplate(workflow)
	require.NoError(t, err)

	reloaded, err := repository.GetWorkflowRun(projectID, workflow.ID, created.ID)
	require.NoError(t, err)
	assert.JSONEq(t, `"us"`, string(reloaded.ParameterSnapshot["region"].Value))
	assert.Equal(t, db.WorkflowParameterSourceUser, reloaded.ParameterSnapshot["region"].Source)
	require.NotNil(t, reloaded.Nodes[0].OverrideSnapshot.Arguments)
	assert.Equal(t, arguments, *reloaded.Nodes[0].OverrideSnapshot.Arguments)
	assert.JSONEq(t, `"eu"`, string(reloaded.DefinitionSnapshot.ParameterDefinitions[0].Default))
}

func TestWorkflowRunRepositoryPersistsTriggerSnapshot(t *testing.T) {
	store, repository, projectID := workflowRepositoryFixture(t)
	defer store.Close()
	user, templateOne, templateTwo := workflowRunResources(t, store, projectID)
	now := time.Date(2026, 8, 28, 18, 0, 0, 0, time.UTC)
	workflow, err := repository.CreateWorkflowTemplate(linearRepositoryWorkflow(
		projectID, templateOne.ID, templateTwo.ID,
	))
	require.NoError(t, err)
	scheduledAt := now.Add(-time.Minute)
	trigger := db.WorkflowTriggerSnapshot{
		ID: 17, Revision: 3, CredentialGeneration: 2,
		Name: "Nightly deploy", Type: db.WorkflowTriggerSchedule,
		OwnerUserID: user.ID, InvocationID: 29,
		ScheduledAt: &scheduledAt, TriggeredAt: now,
	}
	run, err := workflowDB.BuildWorkflowRunSnapshot(workflow, map[int]db.Template{
		templateOne.ID: templateOne, templateTwo.ID: templateTwo,
	}, user.ID, "scheduled-occurrence", now, db.WorkflowRunInput{TriggerSnapshot: &trigger})
	require.NoError(t, err)
	created, err := repository.CreateWorkflowRun(run)
	require.NoError(t, err)

	reloaded, err := repository.GetWorkflowRun(projectID, workflow.ID, created.ID)
	require.NoError(t, err)
	assert.Equal(t, trigger, reloaded.TriggerSnapshot)
	assert.NotContains(t, reloaded.TriggerSnapshotJSON, "credential_hash")
}

func TestWorkflowRunRepositoryDeduplicatesCorrelationAndRollsBackNodes(t *testing.T) {
	store, repository, projectID := workflowRepositoryFixture(t)
	defer store.Close()
	user, templateOne, templateTwo := workflowRunResources(t, store, projectID)
	now := time.Date(2026, 8, 28, 15, 0, 0, 0, time.UTC)
	workflow, err := repository.CreateWorkflowTemplate(linearRepositoryWorkflow(projectID, templateOne.ID, templateTwo.ID))
	require.NoError(t, err)
	run, err := workflowDB.BuildWorkflowRunSnapshot(workflow, map[int]db.Template{
		templateOne.ID: templateOne, templateTwo.ID: templateTwo,
	}, user.ID, "deduplicated", now)
	require.NoError(t, err)
	first, err := repository.CreateWorkflowRun(run)
	require.NoError(t, err)
	second, err := repository.CreateWorkflowRun(run)
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID)

	broken := run
	broken.ID = 0
	broken.CorrelationID = "rollback"
	broken.Nodes = append([]db.WorkflowRunNode(nil), run.Nodes...)
	broken.Nodes[1].WorkflowNodeID = broken.Nodes[0].WorkflowNodeID
	_, err = repository.CreateWorkflowRun(broken)
	require.Error(t, err)
	_, err = repository.GetWorkflowRunByCorrelationID(projectID, workflow.ID, "rollback")
	assert.True(t, errors.Is(err, db.ErrNotFound))
}

func TestWorkflowRunRepositorySkipsQuarantinedRunsUntilManualRetry(t *testing.T) {
	store, repository, projectID := workflowRepositoryFixture(t)
	defer store.Close()
	user, templateOne, templateTwo := workflowRunResources(t, store, projectID)
	workflow, err := repository.CreateWorkflowTemplate(linearRepositoryWorkflow(projectID, templateOne.ID, templateTwo.ID))
	require.NoError(t, err)
	run, err := workflowDB.BuildWorkflowRunSnapshot(workflow, map[int]db.Template{
		templateOne.ID: templateOne, templateTwo.ID: templateTwo,
	}, user.ID, "quarantined-run", time.Now().UTC())
	require.NoError(t, err)
	created, err := repository.CreateWorkflowRun(run)
	require.NoError(t, err)
	created.ReconciliationState = db.WorkflowRunReconciliationQuarantined
	created.ReconciliationAttempts = 3
	now := time.Now().UTC()
	created.ReconciliationQuarantinedAt = &now
	require.NoError(t, repository.UpdateWorkflowRun(created))

	active, err := repository.GetActiveWorkflowRuns()
	require.NoError(t, err)
	assert.Empty(t, active)
	created.ReconciliationState = db.WorkflowRunReconciliationRecovering
	created.ReconciliationAttempts = 0
	created.ReconciliationQuarantinedAt = nil
	require.NoError(t, repository.UpdateWorkflowRun(created))
	active, err = repository.GetActiveWorkflowRuns()
	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.Equal(t, created.ID, active[0].ID)
}

func TestDecodeWorkflowRunNodeLoadsResultWithoutTemplateSnapshot(t *testing.T) {
	node := db.WorkflowRunNode{ResultJSON: `{"status":"skipped","successful":false}`}
	require.NoError(t, decodeWorkflowRunNode(&node))
	assert.Equal(t, db.WorkflowRunNodeSkipped, node.Result.Status)
	assert.False(t, node.Result.Successful)
}

func TestWorkflowArtifactRepositoryBindsValuesToRunTaskAndAttempt(t *testing.T) {
	store, repository, projectID := workflowRepositoryFixture(t)
	defer store.Close()
	user, templateOne, templateTwo := workflowRunResources(t, store, projectID)
	now := time.Date(2026, 8, 28, 16, 0, 0, 0, time.UTC)
	workflowDefinition := linearRepositoryWorkflow(projectID, templateOne.ID, templateTwo.ID)
	workflowDefinition.Nodes[0].ArtifactOutputs = []db.WorkflowArtifactDeclaration{
		{Name: "release", Schema: db.WorkflowArtifactSchema{Type: db.WorkflowArtifactString}, MaxBytes: 128},
		{Name: "token", Schema: db.WorkflowArtifactSchema{Type: db.WorkflowArtifactString}, Sensitive: true, MaxBytes: 128},
	}
	workflowDefinition.Nodes[1].ArtifactInputs = []db.WorkflowArtifactReference{
		{Name: "release_name", SourceNodeID: -1, Output: "release", Required: true},
	}
	workflow, err := repository.CreateWorkflowTemplate(workflowDefinition)
	require.NoError(t, err)
	run, err := workflowDB.BuildWorkflowRunSnapshot(workflow, map[int]db.Template{
		templateOne.ID: templateOne, templateTwo.ID: templateTwo,
	}, user.ID, "artifacts", now)
	require.NoError(t, err)
	run, err = repository.CreateWorkflowRun(run)
	require.NoError(t, err)
	producerNodeID := workflow.Nodes[0].ID
	consumerNodeID := workflow.Nodes[1].ID

	snapshot := []db.WorkflowArtifactInputSnapshot{{
		Name: "release_name", SourceNodeID: producerNodeID, Output: "release", Required: true,
		Availability: db.WorkflowArtifactAvailable, ReferenceFingerprint: "sha256:metadata-only",
	}}
	snapshotJSON, err := json.Marshal(snapshot)
	require.NoError(t, err)
	updated, err := repository.UpdateWorkflowRunNodeArtifactInputs(projectID, run.ID, consumerNodeID, string(snapshotJSON))
	require.NoError(t, err)
	assert.True(t, updated)
	consumer, err := repository.GetWorkflowRunNode(projectID, run.ID, consumerNodeID)
	require.NoError(t, err)
	require.Len(t, consumer.ArtifactInputs, 1)
	assert.Equal(t, "sha256:metadata-only", consumer.ArtifactInputs[0].ReferenceFingerprint)

	task, err := store.CreateTask(db.Task{
		ProjectID: projectID, TemplateID: templateOne.ID, Status: task_logger.TaskWaitingStatus,
		WorkflowRunID: &run.ID, WorkflowNodeID: &producerNodeID,
	}, 0)
	require.NoError(t, err)
	artifacts := []db.WorkflowArtifact{
		{
			Name: "release", Schema: db.WorkflowArtifactSchema{Type: db.WorkflowArtifactString},
			Availability: db.WorkflowArtifactAvailable, SizeBytes: len(`"release-1"`),
			Fingerprint: "sha256:release-attempt-0", ValueJSON: `"release-1"`,
		},
		{
			Name: "token", Schema: db.WorkflowArtifactSchema{Type: db.WorkflowArtifactString}, Sensitive: true,
			Availability: db.WorkflowArtifactAvailable, SizeBytes: 12,
			Fingerprint: "sha256:token-attempt-0", EncryptedValue: "encrypted-token",
		},
	}
	require.NoError(t, repository.ReplaceWorkflowTaskArtifacts(projectID, run.ID, producerNodeID, task.ID, 0, artifacts))
	stored, err := repository.GetWorkflowRunArtifacts(projectID, run.ID)
	require.NoError(t, err)
	require.Len(t, stored, 2)
	assert.Equal(t, `"release-1"`, stored[0].ValueJSON)
	assert.Empty(t, stored[1].ValueJSON)
	assert.Equal(t, "encrypted-token", stored[1].EncryptedValue)

	_, err = store.Sql().Exec("update task set assignment_generation=1 where id=?", task.ID)
	require.NoError(t, err)
	retry := artifacts[:1]
	retry[0].ValueJSON = `"release-2"`
	retry[0].SizeBytes = len(retry[0].ValueJSON)
	retry[0].Fingerprint = "sha256:release-attempt-1"
	require.NoError(t, repository.ReplaceWorkflowTaskArtifacts(projectID, run.ID, producerNodeID, task.ID, 1, retry))
	stored, err = repository.GetWorkflowRunArtifacts(projectID, run.ID)
	require.NoError(t, err)
	require.Len(t, stored, 3)
	assert.Equal(t, 0, stored[0].Attempt)
	assert.Equal(t, 0, stored[1].Attempt)
	assert.Equal(t, 1, stored[2].Attempt)
	assert.Equal(t, `"release-2"`, stored[2].ValueJSON)

	invalid := retry[0]
	invalid.Sensitive = true
	invalid.EncryptedValue = ""
	err = repository.ReplaceWorkflowTaskArtifacts(projectID, run.ID, producerNodeID, task.ID, 1, []db.WorkflowArtifact{invalid})
	require.ErrorContains(t, err, "ciphertext only")
	stored, err = repository.GetWorkflowRunArtifacts(projectID, run.ID)
	require.NoError(t, err)
	require.Len(t, stored, 3, "validation failure must not erase the previous attempt")

	foreign, err := repository.GetWorkflowRunArtifacts(projectID+1, run.ID)
	require.NoError(t, err)
	assert.Empty(t, foreign)
	assert.ErrorIs(t,
		repository.ReplaceWorkflowTaskArtifacts(projectID+1, run.ID, producerNodeID, task.ID, 1, retry),
		db.ErrNotFound,
	)
}

func workflowRunResources(t *testing.T, store interface {
	CreateUserWithoutPassword(db.User) (db.User, error)
	CreateAccessKey(db.AccessKey) (db.AccessKey, error)
	CreateRepository(db.Repository) (db.Repository, error)
	CreateTemplate(db.Template) (db.Template, error)
}, projectID int) (db.User, db.Template, db.Template) {
	t.Helper()
	user, err := store.CreateUserWithoutPassword(db.User{Username: "workflow-actor", Name: "Workflow Actor", Email: "workflow@example.invalid"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &projectID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{ProjectID: projectID, SSHKeyID: key.ID, Name: "repo", GitURL: "https://example.invalid/repo.git", GitBranch: "main"})
	require.NoError(t, err)
	first, err := store.CreateTemplate(db.Template{ProjectID: projectID, RepositoryID: repository.ID, Name: "First", Playbook: "first.yml"})
	require.NoError(t, err)
	second, err := store.CreateTemplate(db.Template{ProjectID: projectID, RepositoryID: repository.ID, Name: "Second", Playbook: "second.yml"})
	require.NoError(t, err)
	return user, first, second
}

func linearRepositoryWorkflow(projectID, firstTemplateID, secondTemplateID int) db.WorkflowTemplate {
	return db.WorkflowTemplate{
		ProjectID: projectID, Name: "Linear", DefinitionVersion: 1,
		Nodes: []db.WorkflowNode{{ID: -1, TemplateID: firstTemplateID}, {ID: -2, TemplateID: secondTemplateID}},
		Edges: []db.WorkflowEdge{{ID: -1, SourceNodeID: -1, DestinationNodeID: -2, Condition: db.WorkflowEdgeOnSuccess}},
	}
}
