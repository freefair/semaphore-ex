package server

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	workflowSQL "github.com/semaphoreui/semaphore/pro/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowDefinitionServicePreventsInvalidPersistence(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	defer store.Close()
	project, err := store.CreateProject(db.Project{Name: "Service test"})
	require.NoError(t, err)
	repository := workflowSQL.NewWorkflowStore(store.GetConnection())
	service := NewWorkflowDefinitionService(repository, store)
	actor := &db.User{ID: 1, Admin: true}

	_, validation, err := service.Create(project.ID, db.WorkflowTemplate{
		Name: "Invalid",
		Nodes: []db.WorkflowNode{{
			ID: -1, Kind: db.WorkflowNodeTaskKind, TemplateID: 987654,
		}},
	}, actor)

	require.NoError(t, err)
	assert.False(t, validation.Valid)
	assert.Contains(t, workflowServiceIssueCodes(validation), "WORKFLOW_TEMPLATE_NOT_IN_PROJECT")
	stored, err := repository.GetWorkflowTemplates(project.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	assert.Empty(t, stored)
}

func TestWorkflowDefinitionServiceNormalizesBeforeCreate(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	defer store.Close()
	project, err := store.CreateProject(db.Project{Name: "Service test"})
	require.NoError(t, err)
	templateID := insertWorkflowTestTemplate(t, store, project.ID)
	repository := workflowSQL.NewWorkflowStore(store.GetConnection())
	service := NewWorkflowDefinitionService(repository, store)
	actor := &db.User{ID: 1, Admin: true}

	created, validation, err := service.Create(project.ID, db.WorkflowTemplate{
		Name: "  Build  ",
		Nodes: []db.WorkflowNode{{
			ID: -1, TemplateID: templateID,
		}},
	}, actor)

	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	assert.Equal(t, "Build", created.Name)
	assert.Equal(t, db.WorkflowDefinitionVersion, created.DefinitionVersion)
	assert.Equal(t, db.WorkflowNodeTaskKind, created.Nodes[0].Kind)
	assert.Equal(t, db.WorkflowConvergenceAll, created.Nodes[0].ConvergenceMode)
	assert.Equal(t, 4, created.MaxParallelTasks)
}

func TestWorkflowDefinitionServiceCompilesConditionsBeforePersistence(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	defer store.Close()
	project, err := store.CreateProject(db.Project{Name: "Condition service test"})
	require.NoError(t, err)
	templateID := insertWorkflowTestTemplate(t, store, project.ID)
	repository := workflowSQL.NewWorkflowStore(store.GetConnection())
	service := NewWorkflowDefinitionService(repository, store)
	actor := &db.User{ID: 1, Admin: true}
	workflow := db.WorkflowTemplate{
		Name: "Conditional",
		Nodes: []db.WorkflowNode{
			{ID: -1, TemplateID: templateID},
			{ID: -2, TemplateID: templateID, JoinMode: db.WorkflowJoinAnySuccessful},
		},
		Edges: []db.WorkflowEdge{{
			ID: -1, SourceNodeID: -1, DestinationNodeID: -2,
			Condition: db.WorkflowEdgeExpression, Expression: `result.summary.failed_hosts == 0`,
		}},
	}

	created, validation, err := service.Create(project.ID, workflow, actor)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	require.NotEmpty(t, created.Edges[0].ConditionProgramJSON)
	assert.Equal(t, 1, created.Edges[0].ConditionProgram.Version)
	reloaded, err := repository.GetWorkflowTemplate(project.ID, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.Edges[0].ConditionProgram, reloaded.Edges[0].ConditionProgram)
	assert.Equal(t, db.WorkflowJoinAnySuccessful, reloaded.Nodes[1].JoinMode)
}

func TestWorkflowDefinitionServiceRejectsMalformedConditionAndParallelismLimit(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	defer store.Close()
	project, err := store.CreateProject(db.Project{Name: "Invalid condition service test"})
	require.NoError(t, err)
	templateID := insertWorkflowTestTemplate(t, store, project.ID)
	repository := workflowSQL.NewWorkflowStore(store.GetConnection())
	service := NewWorkflowDefinitionService(repository, store)
	actor := &db.User{ID: 1, Admin: true}

	_, validation, err := service.Create(project.ID, db.WorkflowTemplate{
		Name: "Unsafe", MaxParallelTasks: 33,
		Nodes: []db.WorkflowNode{{ID: -1, TemplateID: templateID}, {ID: -2, TemplateID: templateID}},
		Edges: []db.WorkflowEdge{{
			ID: -1, SourceNodeID: -1, DestinationNodeID: -2,
			Condition: db.WorkflowEdgeExpression, Expression: `result.secret == "value"`,
		}},
	}, actor)

	require.NoError(t, err)
	assert.False(t, validation.Valid)
	assert.Contains(t, workflowServiceIssueCodes(validation), "WORKFLOW_EDGE_EXPRESSION_INVALID")
	assert.Contains(t, workflowServiceIssueCodes(validation), "WORKFLOW_PARALLELISM_INVALID")
	stored, err := repository.GetWorkflowTemplates(project.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	assert.Empty(t, stored)
}

func TestWorkflowDefinitionServiceRejectsUnapprovedParameterAndOverrideResources(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	defer store.Close()
	project, err := store.CreateProject(db.Project{Name: "Resource validation"})
	require.NoError(t, err)
	otherProject, err := store.CreateProject(db.Project{Name: "Other project"})
	require.NoError(t, err)
	templateID := insertWorkflowTestTemplate(t, store, project.ID)
	foreignKey, err := store.CreateAccessKey(db.AccessKey{
		ProjectID: &otherProject.ID, Name: "Foreign secret", Type: db.AccessKeyString, Owner: db.AccessKeyShared,
	})
	require.NoError(t, err)
	foreignInventory, err := store.CreateInventory(db.Inventory{
		ProjectID: otherProject.ID, Name: "Foreign inventory", Type: db.InventoryStatic, Inventory: "localhost",
	})
	require.NoError(t, err)
	foreignEnvironment, err := store.CreateEnvironment(db.Environment{
		ProjectID: otherProject.ID, Name: "Foreign environment", JSON: `{}`,
	})
	require.NoError(t, err)
	repository := workflowSQL.NewWorkflowStore(store.GetConnection())
	service := NewWorkflowDefinitionService(repository, store)
	actor := &db.User{ID: 1, Admin: true}

	_, validation, err := service.Create(project.ID, db.WorkflowTemplate{
		Name: "Invalid resources",
		ParameterDefinitions: []db.WorkflowParameterDeclaration{{
			Name: "token", Type: db.WorkflowParameterSecretReference,
			SecretOptions: []db.WorkflowSecretOption{{AccessKeyID: foreignKey.ID}},
		}},
		Nodes: []db.WorkflowNode{{
			ID: -1, TemplateID: templateID,
			OverridePolicy: db.WorkflowNodeOverridePolicy{
				InventoryIDs: []int{foreignInventory.ID}, EnvironmentIDs: []int{foreignEnvironment.ID},
				CredentialParameters: []string{"missing_token"}, AllowArguments: true, AllowBranch: true,
			},
		}},
	}, actor)

	require.NoError(t, err)
	assert.False(t, validation.Valid)
	codes := workflowServiceIssueCodes(validation)
	assert.Contains(t, codes, "WORKFLOW_PARAMETER_SECRET_NOT_APPROVED")
	assert.Contains(t, codes, "WORKFLOW_NODE_INVENTORY_OVERRIDE_FORBIDDEN")
	assert.Contains(t, codes, "WORKFLOW_NODE_ARGUMENTS_OVERRIDE_FORBIDDEN")
	assert.Contains(t, codes, "WORKFLOW_NODE_BRANCH_OVERRIDE_FORBIDDEN")
	assert.Contains(t, codes, "WORKFLOW_NODE_INVENTORY_NOT_IN_PROJECT")
	assert.Contains(t, codes, "WORKFLOW_NODE_ENVIRONMENT_NOT_IN_PROJECT")
	assert.Contains(t, codes, "WORKFLOW_NODE_CREDENTIAL_PARAMETER_INVALID")
}

func insertWorkflowTestTemplate(t *testing.T, store *coresql.SqlDb, projectID int) int {
	t.Helper()
	keyResult, err := store.GetConnection().Exec(
		"insert into access_key(name, type, project_id) values (?, ?, ?)",
		"Test key", "none", projectID,
	)
	require.NoError(t, err)
	keyID, err := keyResult.LastInsertId()
	require.NoError(t, err)
	repositoryResult, err := store.GetConnection().Exec(
		"insert into project__repository(project_id, git_url, ssh_key_id, name) values (?, ?, ?, ?)",
		projectID, "https://example.invalid/repository.git", keyID, "Repository",
	)
	require.NoError(t, err)
	repositoryID, err := repositoryResult.LastInsertId()
	require.NoError(t, err)
	result, err := store.GetConnection().Exec(
		"insert into project__template(project_id, repository_id, name, playbook, type, app) values (?, ?, ?, ?, ?, ?)",
		projectID, repositoryID, "Build", "site.yml", "", "ansible",
	)
	require.NoError(t, err)
	id, err := result.LastInsertId()
	require.NoError(t, err)
	return int(id)
}

func workflowServiceIssueCodes(result db.WorkflowValidationResult) []string {
	codes := make([]string, 0, len(result.Issues))
	for _, issue := range result.Issues {
		codes = append(codes, issue.Code)
	}
	return codes
}
