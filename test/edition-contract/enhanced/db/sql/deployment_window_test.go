package sql

import (
	"encoding/json"
	"testing"
	"time"

	coreDB "github.com/semaphoreui/semaphore/db"
	coreSQL "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/deployment_windows"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeploymentWindowStoreStartsWithLazyDefaultAndCASPersistsTenantScopedRules(t *testing.T) {
	store := coreSQL.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	repository := NewDeploymentWindowStore(store.GetConnection())

	project, err := store.CreateProject(coreDB.Project{Name: "window project"})
	require.NoError(t, err)
	otherProject, err := store.CreateProject(coreDB.Project{Name: "other window project"})
	require.NoError(t, err)

	defaultPolicy, err := repository.GetDeploymentWindowPolicy(project.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, defaultPolicy.Revision)
	assert.Equal(t, "UTC", defaultPolicy.Timezone)
	assert.Equal(t, coreDB.DeploymentWindowDefaultAllow, defaultPolicy.Default)

	template := deploymentWindowTemplate(t, store, project.ID, "deploy")

	created, err := repository.SaveDeploymentWindowPolicy(coreDB.DeploymentWindowPolicy{
		ProjectID: project.ID, Revision: 1, Timezone: "UTC", Default: coreDB.DeploymentWindowDefaultDeny,
		Rules: []coreDB.DeploymentWindowRule{{
			Name: "production", Active: true, Kind: coreDB.DeploymentWindowAllow, Scope: coreDB.DeploymentWindowTemplateScope,
			TemplateID: intPointer(template.ID), Recurrence: "0 9 * * *", DurationMinutes: 60,
		}},
	}, 1)
	require.NoError(t, err)
	assert.Equal(t, 2, created.Revision)
	require.Len(t, created.Rules, 1)
	assert.Positive(t, created.Rules[0].ID)

	_, err = repository.SaveDeploymentWindowPolicy(created, 1)
	assert.ErrorIs(t, err, coreDB.ErrDeploymentWindowRevisionConflict)

	foreignTemplate := deploymentWindowTemplate(t, store, otherProject.ID, "foreign")
	created.Rules[0].TemplateID = intPointer(foreignTemplate.ID)
	_, err = repository.SaveDeploymentWindowPolicy(created, created.Revision)
	assert.ErrorIs(t, err, coreDB.ErrDeploymentWindowTenantMismatch)
}

func TestDeploymentWindowAdmissionUsesDatabaseTimeAndPersistsOneImmutableDecision(t *testing.T) {
	store := coreSQL.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	repository := NewDeploymentWindowStore(store.GetConnection())
	project, err := store.CreateProject(coreDB.Project{Name: "admission project"})
	require.NoError(t, err)
	template := deploymentWindowTemplate(t, store, project.ID, "admission")
	policy, err := repository.SaveDeploymentWindowPolicy(coreDB.DeploymentWindowPolicy{
		ProjectID: project.ID, Revision: 1, Timezone: "UTC", Default: coreDB.DeploymentWindowDefaultDeny,
		Rules: []coreDB.DeploymentWindowRule{{Name: "allow", Active: true, Kind: coreDB.DeploymentWindowAllow, Scope: coreDB.DeploymentWindowTemplateScope, TemplateID: intPointer(template.ID), Recurrence: "* * * * *", DurationMinutes: 2}},
	}, 1)
	require.NoError(t, err)

	evaluator := deployment_windows.NewEvaluator(deployment_windows.WithTimezoneValidator(func(value string) error { _, err := time.LoadLocation(value); return err }))
	claim, err := repository.ClaimDeploymentWindowAdmission(pro_interfaces.DeploymentWindowAdmissionRequest{
		ProjectID: project.ID, DecisionKey: "manual-request-1", Source: pro_interfaces.DeploymentWindowSourceManual, Origin: pro_interfaces.DeploymentWindowOriginUser, TemplateID: intPointer(template.ID),
	}, evaluator.Evaluate)
	require.NoError(t, err)
	assert.True(t, claim.Inserted)
	assert.Equal(t, policy.Revision, claim.Decision.PolicyRevision)
	assert.False(t, claim.Decision.EvaluatedAt.IsZero())
	var matched []pro_interfaces.DeploymentWindowMatchedRule
	require.NoError(t, json.Unmarshal([]byte(claim.Decision.MatchedRulesJSON), &matched))
	require.Len(t, matched, 1)
	assert.Equal(t, policy.Rules[0].ID, matched[0].ID)
	assert.Equal(t, policy.Rules[0].Revision, matched[0].Revision)
	assert.Equal(t, policy.Rules[0].Kind, matched[0].Kind)

	duplicate, err := repository.ClaimDeploymentWindowAdmission(pro_interfaces.DeploymentWindowAdmissionRequest{
		ProjectID: project.ID, DecisionKey: "manual-request-1", Source: pro_interfaces.DeploymentWindowSourceManual, Origin: pro_interfaces.DeploymentWindowOriginUser, TemplateID: intPointer(template.ID),
	}, evaluator.Evaluate)
	require.NoError(t, err)
	assert.False(t, duplicate.Inserted)
	assert.Equal(t, claim.Decision.ID, duplicate.Decision.ID)

	otherTemplate := deploymentWindowTemplate(t, store, project.ID, "different-admission-target")
	_, err = repository.ClaimDeploymentWindowAdmission(pro_interfaces.DeploymentWindowAdmissionRequest{
		ProjectID: project.ID, DecisionKey: "manual-request-1", Source: pro_interfaces.DeploymentWindowSourceManual, Origin: pro_interfaces.DeploymentWindowOriginUser, TemplateID: intPointer(otherTemplate.ID),
	}, evaluator.Evaluate)
	assert.ErrorIs(t, err, coreDB.ErrInvalidOperation)
}

func TestDeploymentWindowHistoryIsTenantScopedAndDatabaseTimed(t *testing.T) {
	store := coreSQL.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	repository := NewDeploymentWindowStore(store.GetConnection())
	project, err := store.CreateProject(coreDB.Project{Name: "window history project"})
	require.NoError(t, err)
	otherProject, err := store.CreateProject(coreDB.Project{Name: "other window history project"})
	require.NoError(t, err)
	template := deploymentWindowTemplate(t, store, project.ID, "history")
	otherTemplate := deploymentWindowTemplate(t, store, otherProject.ID, "other-history")

	for _, request := range []pro_interfaces.DeploymentWindowAdmissionRequest{
		{ProjectID: project.ID, DecisionKey: "history-first", Source: pro_interfaces.DeploymentWindowSourceManual, Origin: pro_interfaces.DeploymentWindowOriginUser, TemplateID: intPointer(template.ID)},
		{ProjectID: project.ID, DecisionKey: "history-second", Source: pro_interfaces.DeploymentWindowSourceManual, Origin: pro_interfaces.DeploymentWindowOriginUser, TemplateID: intPointer(template.ID)},
		{ProjectID: otherProject.ID, DecisionKey: "history-other", Source: pro_interfaces.DeploymentWindowSourceManual, Origin: pro_interfaces.DeploymentWindowOriginUser, TemplateID: intPointer(otherTemplate.ID)},
	} {
		_, claimErr := repository.ClaimDeploymentWindowAdmission(request, deploymentWindowEvaluator().Evaluate)
		require.NoError(t, claimErr)
	}

	before := time.Now().UTC().Add(-time.Second)
	databaseTime, err := repository.GetDeploymentWindowDatabaseTime()
	require.NoError(t, err)
	after := time.Now().UTC().Add(time.Second)
	assert.False(t, databaseTime.Before(before) || databaseTime.After(after), "database time must be backend-authoritative and UTC")

	firstPage, err := repository.GetDeploymentWindowDecisionHistory(project.ID, coreDB.RetrieveQueryParams{Count: 1})
	require.NoError(t, err)
	require.Len(t, firstPage, 1)
	assert.Equal(t, project.ID, firstPage[0].ProjectID)
	assert.NotEmpty(t, firstPage[0].MatchedRulesJSON)

	secondPage, err := repository.GetDeploymentWindowDecisionHistory(project.ID, coreDB.RetrieveQueryParams{Count: 1, BeforeID: firstPage[0].ID})
	require.NoError(t, err)
	require.Len(t, secondPage, 1)
	assert.Equal(t, project.ID, secondPage[0].ProjectID)
	assert.Less(t, secondPage[0].ID, firstPage[0].ID)

	status, err := repository.EvaluateDeploymentWindowStatus(pro_interfaces.DeploymentWindowStatusRequest{
		ProjectID: project.ID, TemplateID: intPointer(template.ID),
	}, deploymentWindowEvaluator().Evaluate)
	require.NoError(t, err)
	assert.False(t, status.Provenance.EvaluatedAt.IsZero())
	assert.False(t, status.Provenance.EvaluatedAt.Before(before) || status.Provenance.EvaluatedAt.After(after), "status must use the repository database clock")

	_, err = repository.EvaluateDeploymentWindowStatus(pro_interfaces.DeploymentWindowStatusRequest{
		ProjectID: project.ID, TemplateID: intPointer(otherTemplate.ID),
	}, deploymentWindowEvaluator().Evaluate)
	assert.ErrorIs(t, err, coreDB.ErrDeploymentWindowTenantMismatch)
}

func TestDeploymentWindowPreviewRejectsForeignTargetAndDraftRuleTarget(t *testing.T) {
	store := coreSQL.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	repository := NewDeploymentWindowStore(store.GetConnection())
	project, err := store.CreateProject(coreDB.Project{Name: "preview tenant project"})
	require.NoError(t, err)
	otherProject, err := store.CreateProject(coreDB.Project{Name: "preview foreign project"})
	require.NoError(t, err)
	template := deploymentWindowTemplate(t, store, project.ID, "preview-owned")
	foreignTemplate := deploymentWindowTemplate(t, store, otherProject.ID, "preview-foreign")

	request := pro_interfaces.DeploymentWindowStatusRequest{ProjectID: project.ID, TemplateID: intPointer(template.ID)}
	draft := coreDB.DeploymentWindowPolicy{
		ProjectID: project.ID, Revision: 1, Timezone: "UTC", Default: coreDB.DeploymentWindowDefaultAllow,
		Rules: []coreDB.DeploymentWindowRule{{
			Name: "foreign target", Active: true, Kind: coreDB.DeploymentWindowAllow, Scope: coreDB.DeploymentWindowTemplateScope,
			TemplateID: intPointer(foreignTemplate.ID), Recurrence: "* * * * *", DurationMinutes: 1,
		}},
	}

	_, err = repository.PreviewDeploymentWindowPolicy(draft, request, deploymentWindowEvaluator().Evaluate)
	assert.ErrorIs(t, err, coreDB.ErrDeploymentWindowTenantMismatch)

	draft.Rules = nil
	request.TemplateID = intPointer(foreignTemplate.ID)
	_, err = repository.PreviewDeploymentWindowPolicy(draft, request, deploymentWindowEvaluator().Evaluate)
	assert.ErrorIs(t, err, coreDB.ErrDeploymentWindowTenantMismatch)

	request.TemplateID = intPointer(template.ID)
	request.WorkflowID = intPointer(1)
	_, err = repository.PreviewDeploymentWindowPolicy(draft, request, deploymentWindowEvaluator().Evaluate)
	assert.ErrorIs(t, err, coreDB.ErrInvalidOperation)
}

func TestDeploymentWindowDecisionBindsExactlyOnceWithTaskCreation(t *testing.T) {
	store := coreSQL.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	repository := NewDeploymentWindowStore(store.GetConnection())
	project, err := store.CreateProject(coreDB.Project{Name: "task decision binding project"})
	require.NoError(t, err)
	template := deploymentWindowTemplate(t, store, project.ID, "task-decision-binding")
	claim, err := repository.ClaimDeploymentWindowAdmission(pro_interfaces.DeploymentWindowAdmissionRequest{
		ProjectID: project.ID, DecisionKey: "task-binding", Source: pro_interfaces.DeploymentWindowSourceManual,
		Origin: pro_interfaces.DeploymentWindowOriginUser, TemplateID: intPointer(template.ID),
	}, deploymentWindowEvaluator().Evaluate)
	require.NoError(t, err)
	decisionID := claim.Decision.ID
	created, err := store.CreateTask(coreDB.Task{ProjectID: project.ID, TemplateID: template.ID, DeploymentWindowDecisionID: &decisionID}, 0)
	require.NoError(t, err)
	var taskID int
	require.NoError(t, store.Sql().SelectOne(&taskID, "select task_id from project__deployment_window_decision where id=?", decisionID))
	assert.Equal(t, created.ID, taskID)

	_, err = store.CreateTask(coreDB.Task{ProjectID: project.ID, TemplateID: template.ID, DeploymentWindowDecisionID: &decisionID}, 0)
	assert.Error(t, err)
}

func TestDeploymentWindowOverriddenClaimRejectsKeyReuseWithoutTheOverride(t *testing.T) {
	store := coreSQL.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	repository := NewDeploymentWindowStore(store.GetConnection())
	project, err := store.CreateProject(coreDB.Project{Name: "overridden key reuse project"})
	require.NoError(t, err)
	template := deploymentWindowTemplate(t, store, project.ID, "overridden-key")
	_, err = repository.SaveDeploymentWindowPolicy(coreDB.DeploymentWindowPolicy{
		ProjectID: project.ID, Revision: 1, Timezone: "UTC", Default: coreDB.DeploymentWindowDefaultDeny,
	}, 1)
	require.NoError(t, err)
	manager := deploymentWindowTestUser(t, store, "overridden-key-manager", false)
	_, err = store.CreateProjectUser(coreDB.ProjectUser{ProjectID: project.ID, UserID: manager.ID, Role: coreDB.ProjectManager})
	require.NoError(t, err)

	request := deploymentWindowOverrideAdmission(project.ID, template.ID, manager.ID, "overridden-key")
	claim, err := repository.ClaimDeploymentWindowAdmission(request, deploymentWindowEvaluator().Evaluate)
	require.NoError(t, err)
	assert.Equal(t, string(pro_interfaces.DeploymentWindowDecisionOverridden), claim.Decision.State)

	request.Override = nil
	_, err = repository.ClaimDeploymentWindowAdmission(request, deploymentWindowEvaluator().Evaluate)
	assert.ErrorIs(t, err, pro_interfaces.ErrDeploymentWindowOverrideForbidden)
}

func TestDeploymentWindowAdmissionDerivesOverridePermissionAndRejectsAutorunOverride(t *testing.T) {
	store := coreSQL.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	repository := NewDeploymentWindowStore(store.GetConnection())
	project, err := store.CreateProject(coreDB.Project{Name: "override project"})
	require.NoError(t, err)
	template := deploymentWindowTemplate(t, store, project.ID, "override")
	_, err = repository.SaveDeploymentWindowPolicy(coreDB.DeploymentWindowPolicy{ProjectID: project.ID, Revision: 1, Timezone: "UTC", Default: coreDB.DeploymentWindowDefaultDeny}, 1)
	require.NoError(t, err)
	manager, err := store.CreateUserWithoutPassword(coreDB.User{Username: "window-manager", Name: "Window Manager", Email: "manager@example.test"})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(coreDB.ProjectUser{ProjectID: project.ID, UserID: manager.ID, Role: coreDB.ProjectManager})
	require.NoError(t, err)
	evaluator := deployment_windows.NewEvaluator(deployment_windows.WithTimezoneValidator(func(value string) error { _, err := time.LoadLocation(value); return err }))
	actorID := manager.ID
	override := &pro_interfaces.DeploymentWindowOverrideRequest{ActorID: actorID, Category: pro_interfaces.DeploymentWindowOverrideIncident, Reference: "INC-71"}
	claim, err := repository.ClaimDeploymentWindowAdmission(pro_interfaces.DeploymentWindowAdmissionRequest{ProjectID: project.ID, DecisionKey: "override-1", Source: pro_interfaces.DeploymentWindowSourceManual, Origin: pro_interfaces.DeploymentWindowOriginUser, TemplateID: intPointer(template.ID), ActorUserID: &actorID, Override: override}, evaluator.Evaluate)
	require.NoError(t, err)
	assert.Equal(t, pro_interfaces.DeploymentWindowDecisionOverridden, pro_interfaces.DeploymentWindowDecisionState(claim.Decision.State))
	assert.Equal(t, manager.ID, *claim.Decision.OverrideActorID)

	autorun := pro_interfaces.DeploymentWindowAdmissionRequest{ProjectID: project.ID, DecisionKey: "autorun-1", Source: pro_interfaces.DeploymentWindowSourceAutorun, Origin: pro_interfaces.DeploymentWindowOriginAutorun, TemplateID: intPointer(template.ID), ActorUserID: &actorID, Override: override}
	_, err = repository.ClaimDeploymentWindowAdmission(autorun, evaluator.Evaluate)
	assert.ErrorIs(t, err, pro_interfaces.ErrDeploymentWindowOverrideForbidden)
}

func TestDeploymentWindowOverrideAuthorizationLocksRevocableRowsInOrder(t *testing.T) {
	const query = "select id from example where id=?"
	for _, dialect := range []string{util.DbDriverMySQL, util.DbDriverPostgres} {
		t.Run(dialect, func(t *testing.T) {
			assert.Equal(t, query+" for update", deploymentWindowLockAuthorizationQuery(dialect, query))
		})
	}
	assert.Equal(t, query, deploymentWindowLockAuthorizationQuery(util.DbDriverSQLite, query))
}

func TestDeploymentWindowOverrideRejectsRevokedMembershipAndAllowsGlobalAdminWithoutMembership(t *testing.T) {
	store := coreSQL.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	repository := NewDeploymentWindowStore(store.GetConnection())
	project, err := store.CreateProject(coreDB.Project{Name: "override revocation project"})
	require.NoError(t, err)
	template := deploymentWindowTemplate(t, store, project.ID, "override-revocation")
	_, err = repository.SaveDeploymentWindowPolicy(coreDB.DeploymentWindowPolicy{
		ProjectID: project.ID, Revision: 1, Timezone: "UTC", Default: coreDB.DeploymentWindowDefaultDeny,
	}, 1)
	require.NoError(t, err)

	owner, err := store.CreateUserWithoutPassword(coreDB.User{Username: "override-owner", Name: "Override Owner", Email: "owner@example.test"})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(coreDB.ProjectUser{ProjectID: project.ID, UserID: owner.ID, Role: coreDB.ProjectOwner})
	require.NoError(t, err)
	manager, err := store.CreateUserWithoutPassword(coreDB.User{Username: "override-manager", Name: "Override Manager", Email: "manager@example.test"})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(coreDB.ProjectUser{ProjectID: project.ID, UserID: manager.ID, Role: coreDB.ProjectManager})
	require.NoError(t, err)
	evaluator := deploymentWindowEvaluator()

	claim, err := repository.ClaimDeploymentWindowAdmission(deploymentWindowOverrideAdmission(project.ID, template.ID, manager.ID, "before-revocation"), evaluator.Evaluate)
	require.NoError(t, err)
	assert.Equal(t, string(pro_interfaces.DeploymentWindowDecisionOverridden), claim.Decision.State)

	require.NoError(t, store.DeleteProjectUser(project.ID, manager.ID))
	_, err = repository.ClaimDeploymentWindowAdmission(deploymentWindowOverrideAdmission(project.ID, template.ID, manager.ID, "before-revocation"), evaluator.Evaluate)
	assert.ErrorIs(t, err, pro_interfaces.ErrDeploymentWindowOverrideForbidden, "an unbound decision must recheck a revoked permission before first task/run persistence")
	_, err = repository.ClaimDeploymentWindowAdmission(deploymentWindowOverrideAdmission(project.ID, template.ID, manager.ID, "after-revocation"), evaluator.Evaluate)
	assert.ErrorIs(t, err, pro_interfaces.ErrDeploymentWindowOverrideForbidden)

	globalAdmin, err := store.CreateUserWithoutPassword(coreDB.User{Username: "override-global-admin", Name: "Override Global Admin", Email: "admin@example.test", Admin: true})
	require.NoError(t, err)
	claim, err = repository.ClaimDeploymentWindowAdmission(deploymentWindowOverrideAdmission(project.ID, template.ID, globalAdmin.ID, "global-admin"), evaluator.Evaluate)
	require.NoError(t, err)
	assert.Equal(t, string(pro_interfaces.DeploymentWindowDecisionOverridden), claim.Decision.State)

	customRole, err := store.CreateProjectRole(coreDB.Role{
		ID:          "deployment-window-override",
		Name:        "Deployment window override",
		Permissions: coreDB.CanOverrideDeploymentWindow,
		ProjectID:   intPointer(project.ID),
		Revision:    1,
	})
	require.NoError(t, err)
	customRoleUser, err := store.CreateUserWithoutPassword(coreDB.User{Username: "override-custom-role", Name: "Override Custom Role", Email: "custom-role@example.test"})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(coreDB.ProjectUser{ProjectID: project.ID, UserID: customRoleUser.ID, RoleID: &customRole.ID, Revision: 1})
	require.NoError(t, err)
	claim, err = repository.ClaimDeploymentWindowAdmission(deploymentWindowOverrideAdmission(project.ID, template.ID, customRoleUser.ID, "custom-role-before-revocation"), evaluator.Evaluate)
	require.NoError(t, err)
	assert.Equal(t, string(pro_interfaces.DeploymentWindowDecisionOverridden), claim.Decision.State)

	customRole.Permissions = 0
	customRole, err = store.UpdateProjectRole(project.ID, customRole, customRole.Revision)
	require.NoError(t, err)
	_, err = repository.ClaimDeploymentWindowAdmission(deploymentWindowOverrideAdmission(project.ID, template.ID, customRoleUser.ID, "custom-role-before-revocation"), evaluator.Evaluate)
	assert.ErrorIs(t, err, pro_interfaces.ErrDeploymentWindowOverrideForbidden, "custom-role permission is rechecked before an unbound decision can be bound")
	_, err = repository.ClaimDeploymentWindowAdmission(deploymentWindowOverrideAdmission(project.ID, template.ID, customRoleUser.ID, "custom-role-after-revocation"), evaluator.Evaluate)
	assert.ErrorIs(t, err, pro_interfaces.ErrDeploymentWindowOverrideForbidden)
}

func TestDeploymentWindowClaimOrdersConcurrentAuthorizationRevocations(t *testing.T) {
	t.Run("membership", func(t *testing.T) {
		store, repository, project, template := deploymentWindowOverrideTestStore(t)
		owner := deploymentWindowTestUser(t, store, "concurrent-membership-owner", false)
		_, err := store.CreateProjectUser(coreDB.ProjectUser{ProjectID: project.ID, UserID: owner.ID, Role: coreDB.ProjectOwner})
		require.NoError(t, err)
		manager := deploymentWindowTestUser(t, store, "concurrent-membership-manager", false)
		_, err = store.CreateProjectUser(coreDB.ProjectUser{ProjectID: project.ID, UserID: manager.ID, Role: coreDB.ProjectManager})
		require.NoError(t, err)

		deploymentWindowAssertClaimPrecedesRevocation(t, store, repository, deploymentWindowOverrideAdmission(project.ID, template.ID, manager.ID, "concurrent-membership"), func() error {
			return store.DeleteProjectUser(project.ID, manager.ID)
		})
		_, err = repository.ClaimDeploymentWindowAdmission(deploymentWindowOverrideAdmission(project.ID, template.ID, manager.ID, "membership-revoked"), deploymentWindowEvaluator().Evaluate)
		assert.ErrorIs(t, err, pro_interfaces.ErrDeploymentWindowOverrideForbidden)
	})

	t.Run("custom role", func(t *testing.T) {
		store, repository, project, template := deploymentWindowOverrideTestStore(t)
		customRole, err := store.CreateProjectRole(coreDB.Role{
			ID:          "concurrent-deployment-window-override",
			Name:        "Concurrent deployment window override",
			Permissions: coreDB.CanOverrideDeploymentWindow,
			ProjectID:   intPointer(project.ID),
			Revision:    1,
		})
		require.NoError(t, err)
		actor := deploymentWindowTestUser(t, store, "concurrent-custom-role", false)
		_, err = store.CreateProjectUser(coreDB.ProjectUser{ProjectID: project.ID, UserID: actor.ID, RoleID: &customRole.ID, Revision: 1})
		require.NoError(t, err)

		deploymentWindowAssertClaimPrecedesRevocation(t, store, repository, deploymentWindowOverrideAdmission(project.ID, template.ID, actor.ID, "concurrent-custom-role"), func() error {
			customRole.Permissions = 0
			_, updateErr := store.UpdateProjectRole(project.ID, customRole, customRole.Revision)
			return updateErr
		})
		_, err = repository.ClaimDeploymentWindowAdmission(deploymentWindowOverrideAdmission(project.ID, template.ID, actor.ID, "custom-role-revoked"), deploymentWindowEvaluator().Evaluate)
		assert.ErrorIs(t, err, pro_interfaces.ErrDeploymentWindowOverrideForbidden)
	})

	t.Run("administrator", func(t *testing.T) {
		store, repository, project, template := deploymentWindowOverrideTestStore(t)
		actor := deploymentWindowTestUser(t, store, "concurrent-global-admin", true)
		_ = deploymentWindowTestUser(t, store, "concurrent-global-admin-backup", true)

		deploymentWindowAssertClaimPrecedesRevocation(t, store, repository, deploymentWindowOverrideAdmission(project.ID, template.ID, actor.ID, "concurrent-admin"), func() error {
			revoked := coreDB.UserWithPwd{User: actor}
			revoked.Admin = false
			return store.UpdateUser(revoked)
		})
		_, err := repository.ClaimDeploymentWindowAdmission(deploymentWindowOverrideAdmission(project.ID, template.ID, actor.ID, "admin-revoked"), deploymentWindowEvaluator().Evaluate)
		assert.ErrorIs(t, err, pro_interfaces.ErrDeploymentWindowOverrideForbidden)
	})
}

func deploymentWindowAssertClaimPrecedesRevocation(
	t *testing.T,
	store *coreSQL.SqlDb,
	repository *DeploymentWindowStore,
	request pro_interfaces.DeploymentWindowAdmissionRequest,
	revoke func() error,
) {
	t.Helper()
	evaluator := deploymentWindowEvaluator()
	enteredEvaluation := make(chan struct{})
	releaseClaim := make(chan struct{})
	claimResult := make(chan error, 1)
	go func() {
		_, err := repository.ClaimDeploymentWindowAdmission(request, func(policy coreDB.DeploymentWindowPolicy, evaluationRequest pro_interfaces.DeploymentWindowEvaluationRequest) (pro_interfaces.DeploymentWindowDecision, error) {
			close(enteredEvaluation)
			<-releaseClaim
			return evaluator.Evaluate(policy, evaluationRequest)
		})
		claimResult <- err
	}()
	<-enteredEvaluation

	startedRevocation := make(chan struct{})
	revocationResult := make(chan error, 1)
	waitCount := store.Sql().Db.Stats().WaitCount
	go func() {
		close(startedRevocation)
		revocationResult <- revoke()
	}()
	<-startedRevocation
	require.Eventually(t, func() bool {
		return store.Sql().Db.Stats().WaitCount > waitCount
	}, time.Second, 5*time.Millisecond, "revocation must be queued behind the claim transaction")
	select {
	case err := <-revocationResult:
		t.Fatalf("revocation completed before the blocked claim: %v", err)
	default:
	}

	close(releaseClaim)
	require.NoError(t, <-claimResult)
	require.NoError(t, <-revocationResult)
}

func deploymentWindowOverrideTestStore(t *testing.T) (*coreSQL.SqlDb, *DeploymentWindowStore, coreDB.Project, coreDB.Template) {
	t.Helper()
	store := coreSQL.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	repository := NewDeploymentWindowStore(store.GetConnection())
	project, err := store.CreateProject(coreDB.Project{Name: "concurrent override project"})
	require.NoError(t, err)
	template := deploymentWindowTemplate(t, store, project.ID, "concurrent-override")
	_, err = repository.SaveDeploymentWindowPolicy(coreDB.DeploymentWindowPolicy{
		ProjectID: project.ID, Revision: 1, Timezone: "UTC", Default: coreDB.DeploymentWindowDefaultDeny,
	}, 1)
	require.NoError(t, err)
	return store, repository, project, template
}

func deploymentWindowTestUser(t *testing.T, store *coreSQL.SqlDb, username string, admin bool) coreDB.User {
	t.Helper()
	user, err := store.CreateUserWithoutPassword(coreDB.User{Username: username, Name: username, Email: username + "@example.test", Admin: admin})
	require.NoError(t, err)
	return user
}

func TestDeploymentWindowProjectDeletionCascadesPolicyRulesAndDecisions(t *testing.T) {
	store := coreSQL.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	repository := NewDeploymentWindowStore(store.GetConnection())
	project, err := store.CreateProject(coreDB.Project{Name: "window cascade project"})
	require.NoError(t, err)
	template := deploymentWindowTemplate(t, store, project.ID, "window-cascade")
	_, err = repository.SaveDeploymentWindowPolicy(coreDB.DeploymentWindowPolicy{
		ProjectID: project.ID, Revision: 1, Timezone: "UTC", Default: coreDB.DeploymentWindowDefaultDeny,
		Rules: []coreDB.DeploymentWindowRule{{
			Name: "project allow", Active: true, Kind: coreDB.DeploymentWindowAllow, Scope: coreDB.DeploymentWindowProjectScope,
			Recurrence: "* * * * *", DurationMinutes: 1,
		}},
	}, 1)
	require.NoError(t, err)
	_, err = repository.ClaimDeploymentWindowAdmission(pro_interfaces.DeploymentWindowAdmissionRequest{
		ProjectID: project.ID, DecisionKey: "cascade-decision", Source: pro_interfaces.DeploymentWindowSourceManual, Origin: pro_interfaces.DeploymentWindowOriginUser,
		TemplateID: intPointer(template.ID),
	}, deploymentWindowEvaluator().Evaluate)
	require.NoError(t, err)

	require.NoError(t, store.DeleteProject(project.ID))
	for _, table := range []string{
		"project__deployment_window_policy", "project__deployment_window_rule", "project__deployment_window_decision",
	} {
		count, countErr := store.Sql().SelectInt("select count(*) from "+table+" where project_id=?", project.ID)
		require.NoError(t, countErr)
		assert.Zero(t, count, table)
	}
}

func deploymentWindowEvaluator() *deployment_windows.Evaluator {
	return deployment_windows.NewEvaluator(deployment_windows.WithTimezoneValidator(func(value string) error {
		_, err := time.LoadLocation(value)
		return err
	}))
}

func deploymentWindowOverrideAdmission(projectID, templateID, actorID int, decisionKey string) pro_interfaces.DeploymentWindowAdmissionRequest {
	override := &pro_interfaces.DeploymentWindowOverrideRequest{
		ActorID: actorID, Category: pro_interfaces.DeploymentWindowOverrideIncident, Reference: "INC-71",
	}
	return pro_interfaces.DeploymentWindowAdmissionRequest{
		ProjectID: projectID, DecisionKey: decisionKey, Source: pro_interfaces.DeploymentWindowSourceManual,
		Origin: pro_interfaces.DeploymentWindowOriginUser, TemplateID: intPointer(templateID), ActorUserID: intPointer(actorID), Override: override,
	}
}

func intPointer(value int) *int { return &value }

func deploymentWindowTemplate(t *testing.T, store *coreSQL.SqlDb, projectID int, name string) coreDB.Template {
	t.Helper()
	key, err := store.CreateAccessKey(coreDB.AccessKey{ProjectID: &projectID, Type: coreDB.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(coreDB.Repository{ProjectID: projectID, Name: name + " repository", GitURL: "https://example.test/" + name + ".git", GitBranch: "main", SSHKeyID: key.ID})
	require.NoError(t, err)
	template, err := store.CreateTemplate(coreDB.Template{ProjectID: projectID, RepositoryID: repository.ID, Name: name, Playbook: "deploy.yml", App: coreDB.AppBash})
	require.NoError(t, err)
	return template
}
