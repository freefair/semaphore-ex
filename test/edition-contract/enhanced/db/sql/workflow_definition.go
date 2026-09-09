package sql

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
)

func sqlBool(value bool) int {
	if value {
		return 1
	}
	return 0
}

func decodeWorkflowParameterDefinitions(workflow *db.WorkflowTemplate) error {
	if workflow.ParameterDefinitionsJSON == "" {
		workflow.ParameterDefinitionsJSON = "[]"
	}
	if err := json.Unmarshal([]byte(workflow.ParameterDefinitionsJSON), &workflow.ParameterDefinitions); err != nil {
		return fmt.Errorf("decode workflow parameter definitions: %w", err)
	}
	return nil
}

func decodeWorkflowPolicies(workflow *db.WorkflowTemplate) error {
	if workflow.AccessPolicyJSON == "" {
		workflow.AccessPolicyJSON = "{}"
	}
	if err := json.Unmarshal([]byte(workflow.AccessPolicyJSON), &workflow.AccessPolicy); err != nil {
		return fmt.Errorf("decode workflow access policy: %w", err)
	}
	workflow.AccessPolicy.Revision = workflow.AccessPolicyRevision
	if err := workflow.AccessPolicy.Validate(); err != nil {
		return err
	}
	for index := range workflow.Nodes {
		node := &workflow.Nodes[index]
		if node.ApprovalRolePolicyJSON == "" || node.ApprovalRolePolicyJSON == "{}" {
			continue
		}
		if err := json.Unmarshal([]byte(node.ApprovalRolePolicyJSON), &node.ApprovalRolePolicy); err != nil {
			return fmt.Errorf("decode workflow approval role policy: %w", err)
		}
		node.ApprovalRolePolicy.Revision = node.ApprovalRolePolicyRevision
		if err := node.ApprovalRolePolicy.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func encodeWorkflowPolicies(workflow *db.WorkflowTemplate) error {
	if workflow.AccessPolicy.Revision <= 0 {
		workflow.AccessPolicy.Revision = 1
	}
	if err := workflow.AccessPolicy.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(workflow.AccessPolicy)
	if err != nil {
		return fmt.Errorf("encode workflow access policy: %w", err)
	}
	workflow.AccessPolicyJSON = string(payload)
	workflow.AccessPolicyRevision = workflow.AccessPolicy.Revision
	for index := range workflow.Nodes {
		node := &workflow.Nodes[index]
		if node.EffectiveKind() != db.WorkflowNodeApprovalKind || len(node.ApprovalRolePolicy.RoleIDs) == 0 {
			node.ApprovalRolePolicyJSON = "{}"
			if node.ApprovalRolePolicyRevision <= 0 {
				node.ApprovalRolePolicyRevision = 1
			}
			continue
		}
		if node.ApprovalRolePolicy.Revision <= 0 {
			node.ApprovalRolePolicy.Revision = 1
		}
		if err = node.ApprovalRolePolicy.Validate(); err != nil {
			return err
		}
		payload, err = json.Marshal(node.ApprovalRolePolicy)
		if err != nil {
			return fmt.Errorf("encode workflow approval role policy: %w", err)
		}
		node.ApprovalRolePolicyJSON = string(payload)
		node.ApprovalRolePolicyRevision = node.ApprovalRolePolicy.Revision
	}
	return nil
}

func (d *WorkflowStoreImpl) GetWorkflowTemplates(projectID int, params db.RetrieveQueryParams) ([]db.WorkflowTemplate, error) {
	if d.connection == nil {
		return nil, db.ErrNotFound
	}
	query := "select * from project__workflow_template where project_id=?"
	args := []any{projectID}
	if params.Filter != "" {
		query += " and lower(name) like lower(?)"
		args = append(args, "%"+params.Filter+"%")
	}
	if params.BeforeID > 0 {
		query += " and id < ?"
		args = append(args, params.BeforeID)
	}
	sortColumn := "id"
	if params.SortBy == "name" {
		sortColumn = "name"
	}
	direction := " asc"
	if params.SortInverted || params.SortBy == "" {
		direction = " desc"
	}
	query += " order by " + sortColumn + direction
	if params.Count > 0 {
		query += " limit ?"
		args = append(args, params.Count)
		if params.Offset > 0 {
			query += " offset ?"
			args = append(args, params.Offset)
		}
	}
	var workflows []db.WorkflowTemplate
	if _, err := d.connection.SelectAll(&workflows, query, args...); err != nil {
		return nil, err
	}
	for index := range workflows {
		if err := decodeWorkflowParameterDefinitions(&workflows[index]); err != nil {
			return nil, err
		}
		if err := d.loadWorkflowGraph(nil, &workflows[index]); err != nil {
			return nil, err
		}
		if err := decodeWorkflowPolicies(&workflows[index]); err != nil {
			return nil, err
		}
	}
	return workflows, nil
}

func (d *WorkflowStoreImpl) GetWorkflowTemplate(projectID int, workflowID int) (db.WorkflowTemplate, error) {
	if d.connection == nil {
		return db.WorkflowTemplate{}, db.ErrNotFound
	}
	var workflow db.WorkflowTemplate
	if err := d.connection.SelectOne(&workflow,
		"select * from project__workflow_template where project_id=? and id=?", projectID, workflowID); err != nil {
		return db.WorkflowTemplate{}, err
	}
	if err := decodeWorkflowParameterDefinitions(&workflow); err != nil {
		return db.WorkflowTemplate{}, err
	}
	if err := d.loadWorkflowGraph(nil, &workflow); err != nil {
		return db.WorkflowTemplate{}, err
	}
	if err := decodeWorkflowPolicies(&workflow); err != nil {
		return db.WorkflowTemplate{}, err
	}
	return workflow, nil
}

func (d *WorkflowStoreImpl) CreateWorkflowTemplate(workflow db.WorkflowTemplate) (db.WorkflowTemplate, error) {
	created, _, err := d.createWorkflowTemplate(workflow, nil, false)
	return created, err
}

func (d *WorkflowStoreImpl) CreateWorkflowTemplateVersioned(
	workflow db.WorkflowTemplate,
	mutation db.WorkflowVersionMutation,
) (db.WorkflowTemplate, db.WorkflowVersion, error) {
	if err := validateWorkflowVersionMutation(mutation); err != nil {
		return db.WorkflowTemplate{}, db.WorkflowVersion{}, err
	}
	return d.createWorkflowTemplate(workflow, &mutation, false)
}

func (d *WorkflowStoreImpl) CreateWorkflowTemplateVersionedWithCrossProjectReferences(workflow db.WorkflowTemplate, mutation db.WorkflowVersionMutation) (db.WorkflowTemplate, db.WorkflowVersion, error) {
	if err := validateWorkflowVersionMutation(mutation); err != nil {
		return db.WorkflowTemplate{}, db.WorkflowVersion{}, err
	}
	return d.createWorkflowTemplate(workflow, &mutation, true)
}

func (d *WorkflowStoreImpl) createWorkflowTemplate(
	workflow db.WorkflowTemplate,
	mutation *db.WorkflowVersionMutation,
	crossProjectReferences bool,
) (db.WorkflowTemplate, db.WorkflowVersion, error) {
	if d.connection == nil {
		return db.WorkflowTemplate{}, db.WorkflowVersion{}, errors.New("workflow database connection is unavailable")
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return db.WorkflowTemplate{}, db.WorkflowVersion{}, err
	}
	defer func() { _ = tx.Rollback() }()
	workflow.DefinitionVersion = db.WorkflowDefinitionVersion
	workflow.Revision = 1
	if err = encodeWorkflowPolicies(&workflow); err != nil {
		return db.WorkflowTemplate{}, db.WorkflowVersion{}, err
	}
	workflow.ID, err = d.insertTx(tx,
		"insert into project__workflow_template(project_id, name, description, start_version, definition_version, revision, max_parallel_tasks, parameter_definitions, access_policy, access_policy_revision) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		workflow.ProjectID, workflow.Name, workflow.Description, workflow.StartVersion,
		workflow.DefinitionVersion, workflow.Revision, workflow.MaxParallelTasks, workflow.ParameterDefinitionsJSON, workflow.AccessPolicyJSON, workflow.AccessPolicyRevision,
	)
	if err != nil {
		return db.WorkflowTemplate{}, db.WorkflowVersion{}, err
	}
	if crossProjectReferences {
		if err = d.normalizeWorkflowCrossProjectReferencesTx(tx, &workflow, db.CrossProjectTemplateGrantReference); err != nil {
			return db.WorkflowTemplate{}, db.WorkflowVersion{}, err
		}
	}
	if err = d.replaceWorkflowGraph(tx, &workflow, nil); err != nil {
		return db.WorkflowTemplate{}, db.WorkflowVersion{}, err
	}
	if crossProjectReferences {
		if err = d.recheckWorkflowCrossProjectReferencesTx(tx, workflow, db.CrossProjectTemplateGrantReference); err != nil {
			return db.WorkflowTemplate{}, db.WorkflowVersion{}, err
		}
	}
	var version db.WorkflowVersion
	if mutation != nil {
		version, err = d.insertWorkflowVersionTx(tx, workflow, nil, *mutation)
		if err != nil {
			return db.WorkflowTemplate{}, db.WorkflowVersion{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return db.WorkflowTemplate{}, db.WorkflowVersion{}, err
	}
	return workflow, version, nil
}

func (d *WorkflowStoreImpl) UpdateWorkflowTemplate(workflow db.WorkflowTemplate) (db.WorkflowTemplate, error) {
	updated, _, err := d.updateWorkflowTemplate(workflow, nil, false)
	return updated, err
}

func (d *WorkflowStoreImpl) UpdateWorkflowTemplateVersioned(
	workflow db.WorkflowTemplate,
	mutation db.WorkflowVersionMutation,
) (db.WorkflowTemplate, db.WorkflowVersion, error) {
	if err := validateWorkflowVersionMutation(mutation); err != nil {
		return db.WorkflowTemplate{}, db.WorkflowVersion{}, err
	}
	return d.updateWorkflowTemplate(workflow, &mutation, false)
}

func (d *WorkflowStoreImpl) UpdateWorkflowTemplateVersionedWithCrossProjectReferences(workflow db.WorkflowTemplate, mutation db.WorkflowVersionMutation) (db.WorkflowTemplate, db.WorkflowVersion, error) {
	if err := validateWorkflowVersionMutation(mutation); err != nil {
		return db.WorkflowTemplate{}, db.WorkflowVersion{}, err
	}
	return d.updateWorkflowTemplate(workflow, &mutation, true)
}

func (d *WorkflowStoreImpl) updateWorkflowTemplate(
	workflow db.WorkflowTemplate,
	mutation *db.WorkflowVersionMutation,
	crossProjectReferences bool,
) (db.WorkflowTemplate, db.WorkflowVersion, error) {
	if d.connection == nil {
		return db.WorkflowTemplate{}, db.WorkflowVersion{}, errors.New("workflow database connection is unavailable")
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return db.WorkflowTemplate{}, db.WorkflowVersion{}, err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := d.selectWorkflowTemplateTx(tx, workflow.ProjectID, workflow.ID)
	if err != nil {
		return db.WorkflowTemplate{}, db.WorkflowVersion{}, err
	}
	if err = d.loadWorkflowGraph(tx, &current); err != nil {
		return db.WorkflowTemplate{}, db.WorkflowVersion{}, err
	}
	if err = decodeWorkflowPolicies(&current); err != nil {
		return db.WorkflowTemplate{}, db.WorkflowVersion{}, err
	}
	if current.Revision != workflow.Revision {
		return db.WorkflowTemplate{}, db.WorkflowVersion{}, pro_interfaces.ErrWorkflowRevisionConflict
	}
	if err = encodeWorkflowPolicies(&workflow); err != nil {
		return db.WorkflowTemplate{}, db.WorkflowVersion{}, err
	}
	result, err := tx.Exec(d.connection.PrepareQuery(
		"update project__workflow_template set name=?, description=?, start_version=?, definition_version=?, max_parallel_tasks=?, parameter_definitions=?, access_policy=?, access_policy_revision=?, revision=revision+1 where project_id=? and id=? and revision=?"),
		workflow.Name, workflow.Description, workflow.StartVersion, workflow.DefinitionVersion, workflow.MaxParallelTasks, workflow.ParameterDefinitionsJSON,
		workflow.AccessPolicyJSON, workflow.AccessPolicyRevision, workflow.ProjectID, workflow.ID, workflow.Revision,
	)
	if err != nil {
		return db.WorkflowTemplate{}, db.WorkflowVersion{}, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return db.WorkflowTemplate{}, db.WorkflowVersion{}, err
	}
	if updated != 1 {
		return db.WorkflowTemplate{}, db.WorkflowVersion{}, pro_interfaces.ErrWorkflowRevisionConflict
	}
	if crossProjectReferences {
		if err = d.normalizeWorkflowCrossProjectReferencesTx(tx, &workflow, db.CrossProjectTemplateGrantReference); err != nil {
			return db.WorkflowTemplate{}, db.WorkflowVersion{}, err
		}
	}
	if err = d.replaceWorkflowGraph(tx, &workflow, &current); err != nil {
		return db.WorkflowTemplate{}, db.WorkflowVersion{}, err
	}
	if crossProjectReferences {
		if err = d.recheckWorkflowCrossProjectReferencesTx(tx, workflow, db.CrossProjectTemplateGrantReference); err != nil {
			return db.WorkflowTemplate{}, db.WorkflowVersion{}, err
		}
	}
	workflow.Revision++
	var version db.WorkflowVersion
	if mutation != nil {
		parent, parentErr := d.latestWorkflowVersionTx(tx, workflow.ProjectID, workflow.ID)
		if errors.Is(parentErr, db.ErrNotFound) {
			baselineMutation := db.WorkflowVersionMutation{AuthorUserID: 0, Message: "Baseline imported before versioned mutation", Created: mutation.Created}
			parent, parentErr = d.insertWorkflowVersionTx(tx, current, nil, baselineMutation)
		}
		if parentErr != nil {
			return db.WorkflowTemplate{}, db.WorkflowVersion{}, parentErr
		}
		version, err = d.insertWorkflowVersionTx(tx, workflow, &parent.ID, *mutation)
		if err != nil {
			return db.WorkflowTemplate{}, db.WorkflowVersion{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return db.WorkflowTemplate{}, db.WorkflowVersion{}, err
	}
	return workflow, version, nil
}

func (d *WorkflowStoreImpl) recheckWorkflowCrossProjectReferencesTx(tx *gorp.Transaction, workflow db.WorkflowTemplate, operation db.CrossProjectTemplateGrantOperation) error {
	for _, node := range workflow.Nodes {
		if node.CrossProjectTemplateReference == nil {
			continue
		}
		normalized, _, err := d.resolveActiveCrossProjectTemplateGrantTx(tx, workflow.ProjectID, *node.CrossProjectTemplateReference, operation)
		if err != nil {
			return err
		}
		if normalized != *node.CrossProjectTemplateReference {
			return db.ErrNotFound
		}
	}
	return nil
}

func (d *WorkflowStoreImpl) normalizeWorkflowCrossProjectReferencesTx(tx *gorp.Transaction, workflow *db.WorkflowTemplate, operation db.CrossProjectTemplateGrantOperation) error {
	for index := range workflow.Nodes {
		node := &workflow.Nodes[index]
		if node.CrossProjectTemplateReference == nil {
			continue
		}
		normalized, _, err := d.resolveActiveCrossProjectTemplateGrantTx(tx, workflow.ProjectID, *node.CrossProjectTemplateReference, operation)
		if err != nil {
			return err
		}
		node.TemplateID = normalized.TemplateID
		node.CrossProjectTemplateReference = &normalized
	}
	return nil
}

func validateWorkflowVersionMutation(mutation db.WorkflowVersionMutation) error {
	if mutation.AuthorUserID <= 0 {
		return errors.New("workflow version author is required")
	}
	mutation.Message = strings.TrimSpace(mutation.Message)
	if len([]byte(mutation.Message)) > db.MaxWorkflowVersionMessageBytes {
		return fmt.Errorf("workflow version message exceeds %d bytes", db.MaxWorkflowVersionMessageBytes)
	}
	return nil
}

func (d *WorkflowStoreImpl) insertWorkflowVersionTx(
	tx *gorp.Transaction,
	workflow db.WorkflowTemplate,
	parentVersionID *int,
	mutation db.WorkflowVersionMutation,
) (db.WorkflowVersion, error) {
	workflow.CurrentVersionID = 0
	workflow.VersionMessage = ""
	fingerprint, err := pro_interfaces.WorkflowDefinitionFingerprint(workflow)
	if err != nil {
		return db.WorkflowVersion{}, err
	}
	snapshot, err := json.Marshal(workflow)
	if err != nil {
		return db.WorkflowVersion{}, fmt.Errorf("encode workflow version snapshot: %w", err)
	}
	created := mutation.Created.UTC()
	if created.IsZero() {
		created = time.Now().UTC()
	}
	version := db.WorkflowVersion{
		ProjectID: workflow.ProjectID, WorkflowTemplateID: workflow.ID, VersionNumber: workflow.Revision,
		ParentVersionID: parentVersionID, RestoredFromVersionID: mutation.RestoredFromVersionID,
		AuthorUserID: mutation.AuthorUserID, Message: strings.TrimSpace(mutation.Message), Created: created,
		ContentFingerprint: fingerprint, DefinitionSnapshotJSON: string(snapshot), DefinitionSnapshot: workflow,
	}
	version.ID, err = d.insertTx(tx,
		"insert into project__workflow_version(project_id,workflow_template_id,version_number,parent_version_id,restored_from_version_id,author_user_id,message,created,content_fingerprint,definition_snapshot) values (?,?,?,?,?,?,?,?,?,?)",
		version.ProjectID, version.WorkflowTemplateID, version.VersionNumber, version.ParentVersionID,
		version.RestoredFromVersionID, version.AuthorUserID, version.Message, version.Created,
		version.ContentFingerprint, version.DefinitionSnapshotJSON,
	)
	return version, err
}

func (d *WorkflowStoreImpl) latestWorkflowVersionTx(
	tx *gorp.Transaction,
	projectID int,
	workflowID int,
) (db.WorkflowVersion, error) {
	var version db.WorkflowVersion
	err := tx.SelectOne(&version, d.connection.PrepareQuery(
		"select * from project__workflow_version where project_id=? and workflow_template_id=? order by version_number desc limit 1"),
		projectID, workflowID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return db.WorkflowVersion{}, db.ErrNotFound
	}
	if err != nil {
		return db.WorkflowVersion{}, err
	}
	return version, nil
}

func (d *WorkflowStoreImpl) GetWorkflowVersions(
	projectID int,
	workflowID int,
	params db.RetrieveQueryParams,
) ([]db.WorkflowVersion, error) {
	if d.connection == nil {
		return nil, db.ErrNotFound
	}
	query := "select * from project__workflow_version where project_id=? and workflow_template_id=?"
	args := []any{projectID, workflowID}
	if params.BeforeID > 0 {
		query += " and id < ?"
		args = append(args, params.BeforeID)
	}
	query += " order by version_number desc"
	if params.Count > 0 {
		query += " limit ?"
		args = append(args, params.Count)
		if params.Offset > 0 {
			query += " offset ?"
			args = append(args, params.Offset)
		}
	}
	var versions []db.WorkflowVersion
	if _, err := d.connection.SelectAll(&versions, d.connection.PrepareQuery(query), args...); err != nil {
		return nil, err
	}
	for index := range versions {
		if err := decodeWorkflowVersion(&versions[index]); err != nil {
			return nil, err
		}
	}
	return versions, nil
}

func (d *WorkflowStoreImpl) GetWorkflowVersion(
	projectID int,
	workflowID int,
	versionNumber int,
) (db.WorkflowVersion, error) {
	if d.connection == nil {
		return db.WorkflowVersion{}, db.ErrNotFound
	}
	var version db.WorkflowVersion
	err := d.connection.SelectOne(&version,
		"select * from project__workflow_version where project_id=? and workflow_template_id=? and version_number=?",
		projectID, workflowID, versionNumber)
	if errors.Is(err, sql.ErrNoRows) {
		return db.WorkflowVersion{}, db.ErrNotFound
	}
	if err != nil {
		return db.WorkflowVersion{}, err
	}
	if err = decodeWorkflowVersion(&version); err != nil {
		return db.WorkflowVersion{}, err
	}
	return version, nil
}

func (d *WorkflowStoreImpl) EnsureCurrentWorkflowVersion(
	projectID int,
	workflowID int,
) (db.WorkflowVersion, error) {
	if d.connection == nil {
		return db.WorkflowVersion{}, db.ErrNotFound
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return db.WorkflowVersion{}, err
	}
	defer func() { _ = tx.Rollback() }()
	workflow, err := d.selectWorkflowTemplateTx(tx, projectID, workflowID)
	if err != nil {
		return db.WorkflowVersion{}, err
	}
	if err = d.loadWorkflowGraph(tx, &workflow); err != nil {
		return db.WorkflowVersion{}, err
	}
	if err = decodeWorkflowPolicies(&workflow); err != nil {
		return db.WorkflowVersion{}, err
	}
	latest, err := d.latestWorkflowVersionTx(tx, projectID, workflowID)
	if err == nil && latest.VersionNumber == workflow.Revision {
		if err = decodeWorkflowVersion(&latest); err != nil {
			return db.WorkflowVersion{}, err
		}
		return latest, nil
	}
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return db.WorkflowVersion{}, err
	}
	if latest.VersionNumber > workflow.Revision {
		return db.WorkflowVersion{}, errors.New("workflow version timeline is ahead of the live definition")
	}
	var parentID *int
	if latest.ID > 0 {
		parentID = &latest.ID
	}
	version, err := d.insertWorkflowVersionTx(tx, workflow, parentID, db.WorkflowVersionMutation{
		AuthorUserID: 0, Message: "Baseline imported for existing workflow",
	})
	if err != nil {
		_ = tx.Rollback()
		concurrent, concurrentErr := d.GetWorkflowVersion(projectID, workflowID, workflow.Revision)
		if concurrentErr == nil {
			return concurrent, nil
		}
		return db.WorkflowVersion{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.WorkflowVersion{}, err
	}
	return version, nil
}

func decodeWorkflowVersion(version *db.WorkflowVersion) error {
	if err := json.Unmarshal([]byte(version.DefinitionSnapshotJSON), &version.DefinitionSnapshot); err != nil {
		return fmt.Errorf("decode workflow version snapshot: %w", err)
	}
	fingerprint, err := pro_interfaces.WorkflowDefinitionFingerprint(version.DefinitionSnapshot)
	if err != nil {
		return err
	}
	if fingerprint != version.ContentFingerprint {
		return errors.New("workflow version fingerprint does not match its snapshot")
	}
	return nil
}

func (d *WorkflowStoreImpl) DeleteWorkflowTemplate(projectID int, workflowID int) error {
	if d.connection == nil {
		return db.ErrNotFound
	}
	result, err := d.connection.Exec(
		"delete from project__workflow_template where project_id=? and id=?", projectID, workflowID,
	)
	if err != nil {
		return err
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if deleted == 0 {
		return db.ErrNotFound
	}
	return nil
}

func (d *WorkflowStoreImpl) selectWorkflowTemplateTx(tx *gorp.Transaction, projectID, workflowID int) (db.WorkflowTemplate, error) {
	var workflow db.WorkflowTemplate
	err := tx.SelectOne(&workflow, d.connection.PrepareQuery(
		"select * from project__workflow_template where project_id=? and id=?"), projectID, workflowID)
	if errors.Is(err, sql.ErrNoRows) {
		err = db.ErrNotFound
	}
	if err == nil {
		err = decodeWorkflowParameterDefinitions(&workflow)
	}
	return workflow, err
}

func (d *WorkflowStoreImpl) loadWorkflowGraph(tx *gorp.Transaction, workflow *db.WorkflowTemplate) error {
	nodeQuery := d.connection.PrepareQuery(
		"select id, workflow_template_id, template_id, cross_project_template_reference, kind, convergence_mode, join_mode, approval_timeout, approval_message, approval_permission, approval_timeout_outcome, approval_separation_of_duties, delay_seconds, approval_role_policy, approval_role_policy_revision, task_params_id, note, position_x, position_y, display_name, artifact_outputs, artifact_inputs, override_policy from project__workflow_node where workflow_template_id=? order by id")
	edgeQuery := d.connection.PrepareQuery(
		"select * from project__workflow_edge where workflow_template_id=? order by id")
	var err error
	if tx == nil {
		_, err = d.connection.SelectAll(&workflow.Nodes, nodeQuery, workflow.ID)
	} else {
		_, err = tx.Select(&workflow.Nodes, nodeQuery, workflow.ID)
	}
	if err != nil {
		return err
	}
	if tx == nil {
		_, err = d.connection.SelectAll(&workflow.Edges, edgeQuery, workflow.ID)
	} else {
		_, err = tx.Select(&workflow.Edges, edgeQuery, workflow.ID)
	}
	if err != nil {
		return err
	}
	for index := range workflow.Edges {
		if workflow.Edges[index].ConditionProgramJSON == "" {
			continue
		}
		if err = json.Unmarshal([]byte(workflow.Edges[index].ConditionProgramJSON), &workflow.Edges[index].ConditionProgram); err != nil {
			return fmt.Errorf("decode workflow edge condition program: %w", err)
		}
	}
	for index := range workflow.Nodes {
		reference, decodeErr := db.DecodeCrossProjectTemplateReference(workflow.Nodes[index].CrossProjectTemplateReferenceJSON)
		if decodeErr != nil {
			return fmt.Errorf("decode workflow cross-project template reference: %w", decodeErr)
		}
		workflow.Nodes[index].CrossProjectTemplateReference = reference
		if err = decodeWorkflowArtifactDefinition(&workflow.Nodes[index]); err != nil {
			return err
		}
		if workflow.Nodes[index].OverridePolicyJSON != "" {
			if err = json.Unmarshal([]byte(workflow.Nodes[index].OverridePolicyJSON), &workflow.Nodes[index].OverridePolicy); err != nil {
				return fmt.Errorf("decode workflow node override policy: %w", err)
			}
		}
		if workflow.Nodes[index].TaskParamsID == nil {
			continue
		}
		var params db.TaskParams
		query := d.connection.PrepareQuery(
			"select * from project__task_params where project_id=? and id=?")
		if tx == nil {
			err = d.connection.SelectOne(&params, query, workflow.ProjectID, *workflow.Nodes[index].TaskParamsID)
		} else {
			err = tx.SelectOne(&params, query, workflow.ProjectID, *workflow.Nodes[index].TaskParamsID)
		}
		if err != nil {
			return err
		}
		workflow.Nodes[index].TaskParams = &params
	}
	return nil
}

func (d *WorkflowStoreImpl) replaceWorkflowGraph(tx *gorp.Transaction, workflow *db.WorkflowTemplate, current *db.WorkflowTemplate) error {
	existingNodes := make(map[int]db.WorkflowNode)
	existingEdges := make(map[int]db.WorkflowEdge)
	if current != nil {
		for _, node := range current.Nodes {
			existingNodes[node.ID] = node
		}
		for _, edge := range current.Edges {
			existingEdges[edge.ID] = edge
		}
	}

	nodeIDMap := make(map[int]int, len(workflow.Nodes))
	keptNodes := make(map[int]struct{}, len(workflow.Nodes))
	for index := range workflow.Nodes {
		node := &workflow.Nodes[index]
		clientID := node.ID
		node.WorkflowTemplateID = workflow.ID
		var removedParamsID *int
		if node.TaskParams != nil {
			paramsID, err := d.saveTaskParamsTx(tx, workflow.ProjectID, node.TaskParams, existingNodes[clientID].TaskParamsID)
			if err != nil {
				return err
			}
			node.TaskParamsID = &paramsID
		} else {
			node.TaskParamsID = nil
			if existing := existingNodes[clientID].TaskParamsID; existing != nil {
				removed := *existing
				removedParamsID = &removed
			}
		}
		if err := encodeCrossProjectTemplateReference(node); err != nil {
			return err
		}
		if _, exists := existingNodes[clientID]; exists && clientID > 0 {
			if _, err := tx.Exec(d.connection.PrepareQuery(
				"update project__workflow_node set template_id=?, cross_project_template_reference=?, kind=?, convergence_mode=?, join_mode=?, approval_timeout=?, approval_message=?, approval_permission=?, approval_timeout_outcome=?, approval_separation_of_duties=?, delay_seconds=?, approval_role_policy=?, approval_role_policy_revision=?, task_params_id=?, note=?, position_x=?, position_y=?, display_name=?, override_policy=? where workflow_template_id=? and id=?"),
				node.TemplateID, node.CrossProjectTemplateReferenceJSON, node.Kind, node.ConvergenceMode, node.JoinMode, node.ApprovalTimeout, node.ApprovalMessage,
				node.ApprovalPermission, node.ApprovalTimeoutOutcome, sqlBool(node.ApprovalSeparationOfDuties), node.DelaySeconds,
				node.ApprovalRolePolicyJSON, node.ApprovalRolePolicyRevision, node.TaskParamsID, node.Note, node.PositionX, node.PositionY, node.DisplayName, node.OverridePolicyJSON,
				workflow.ID, clientID,
			); err != nil {
				return err
			}
			if removedParamsID != nil {
				if _, err := tx.Exec(d.connection.PrepareQuery(
					"delete from project__task_params where project_id=? and id=?"), workflow.ProjectID, *removedParamsID); err != nil {
					return err
				}
			}
			nodeIDMap[clientID] = clientID
			keptNodes[clientID] = struct{}{}
			continue
		}
		if clientID > 0 && current != nil {
			return fmt.Errorf("workflow node %d does not belong to workflow %d", clientID, workflow.ID)
		}
		newID, err := d.insertTx(tx,
			"insert into project__workflow_node(workflow_template_id, template_id, cross_project_template_reference, kind, convergence_mode, join_mode, approval_timeout, approval_message, approval_permission, approval_timeout_outcome, approval_separation_of_duties, delay_seconds, approval_role_policy, approval_role_policy_revision, task_params_id, note, position_x, position_y, display_name, artifact_outputs, artifact_inputs, override_policy) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			workflow.ID, node.TemplateID, node.CrossProjectTemplateReferenceJSON, node.Kind, node.ConvergenceMode, node.JoinMode, node.ApprovalTimeout,
			node.ApprovalMessage, node.ApprovalPermission, node.ApprovalTimeoutOutcome, sqlBool(node.ApprovalSeparationOfDuties), node.DelaySeconds,
			node.ApprovalRolePolicyJSON, node.ApprovalRolePolicyRevision, node.TaskParamsID, node.Note, node.PositionX, node.PositionY, node.DisplayName, "[]", "[]", node.OverridePolicyJSON,
		)
		if err != nil {
			return err
		}
		nodeIDMap[clientID] = newID
		node.ID = newID
		keptNodes[newID] = struct{}{}
	}
	for index := range workflow.Nodes {
		node := &workflow.Nodes[index]
		for inputIndex := range node.ArtifactInputs {
			if mapped, exists := nodeIDMap[node.ArtifactInputs[inputIndex].SourceNodeID]; exists {
				node.ArtifactInputs[inputIndex].SourceNodeID = mapped
			}
		}
		outputsJSON, inputsJSON, err := encodeWorkflowArtifactDefinition(*node)
		if err != nil {
			return err
		}
		node.ArtifactOutputsJSON = outputsJSON
		node.ArtifactInputsJSON = inputsJSON
		if _, err = tx.Exec(d.connection.PrepareQuery(
			"update project__workflow_node set artifact_outputs=?, artifact_inputs=? where workflow_template_id=? and id=?"),
			outputsJSON, inputsJSON, workflow.ID, node.ID,
		); err != nil {
			return err
		}
	}

	keptEdges := make(map[int]struct{}, len(workflow.Edges))
	for index := range workflow.Edges {
		edge := &workflow.Edges[index]
		clientID := edge.ID
		edge.WorkflowTemplateID = workflow.ID
		if mapped, exists := nodeIDMap[edge.SourceNodeID]; exists {
			edge.SourceNodeID = mapped
		}
		if mapped, exists := nodeIDMap[edge.DestinationNodeID]; exists {
			edge.DestinationNodeID = mapped
		}
		if _, exists := existingEdges[clientID]; exists && clientID > 0 {
			if _, err := tx.Exec(d.connection.PrepareQuery(
				"update project__workflow_edge set source_node_id=?, destination_node_id=?, `condition`=?, label=?, condition_expression=?, condition_program=? where workflow_template_id=? and id=?"),
				edge.SourceNodeID, edge.DestinationNodeID, edge.Condition, edge.Label, edge.Expression, edge.ConditionProgramJSON, workflow.ID, clientID,
			); err != nil {
				return err
			}
			keptEdges[clientID] = struct{}{}
			continue
		}
		if clientID > 0 && current != nil {
			return fmt.Errorf("workflow edge %d does not belong to workflow %d", clientID, workflow.ID)
		}
		newID, err := d.insertTx(tx,
			"insert into project__workflow_edge(workflow_template_id, source_node_id, destination_node_id, `condition`, label, condition_expression, condition_program) values (?, ?, ?, ?, ?, ?, ?)",
			workflow.ID, edge.SourceNodeID, edge.DestinationNodeID, edge.Condition, edge.Label, edge.Expression, edge.ConditionProgramJSON,
		)
		if err != nil {
			return err
		}
		edge.ID = newID
		keptEdges[newID] = struct{}{}
	}

	for id := range existingEdges {
		if _, keep := keptEdges[id]; keep {
			continue
		}
		if _, err := tx.Exec(d.connection.PrepareQuery(
			"delete from project__workflow_edge where workflow_template_id=? and id=?"), workflow.ID, id); err != nil {
			return err
		}
	}
	for id, node := range existingNodes {
		if _, keep := keptNodes[id]; keep {
			continue
		}
		if _, err := tx.Exec(d.connection.PrepareQuery(
			"delete from project__workflow_node where workflow_template_id=? and id=?"), workflow.ID, id); err != nil {
			return err
		}
		if node.TaskParamsID != nil {
			if _, err := tx.Exec(d.connection.PrepareQuery(
				"delete from project__task_params where project_id=? and id=?"), workflow.ProjectID, *node.TaskParamsID); err != nil {
				return err
			}
		}
	}
	return nil
}

func encodeCrossProjectTemplateReference(node *db.WorkflowNode) error {
	if node.CrossProjectTemplateReference == nil {
		node.CrossProjectTemplateReferenceJSON = ""
		return nil
	}
	if err := node.CrossProjectTemplateReference.ValidateNormalized(); err != nil {
		return fmt.Errorf("workflow cross-project template reference: %w", err)
	}
	payload, err := json.Marshal(node.CrossProjectTemplateReference)
	if err != nil {
		return fmt.Errorf("encode workflow cross-project template reference: %w", err)
	}
	node.CrossProjectTemplateReferenceJSON = string(payload)
	return nil
}

func encodeWorkflowArtifactDefinition(node db.WorkflowNode) (string, string, error) {
	outputs, err := json.Marshal(node.ArtifactOutputs)
	if err != nil {
		return "", "", fmt.Errorf("encode workflow artifact outputs: %w", err)
	}
	inputs, err := json.Marshal(node.ArtifactInputs)
	if err != nil {
		return "", "", fmt.Errorf("encode workflow artifact inputs: %w", err)
	}
	return string(outputs), string(inputs), nil
}

func decodeWorkflowArtifactDefinition(node *db.WorkflowNode) error {
	if node.ArtifactOutputsJSON != "" {
		if err := json.Unmarshal([]byte(node.ArtifactOutputsJSON), &node.ArtifactOutputs); err != nil {
			return fmt.Errorf("decode workflow artifact outputs: %w", err)
		}
	}
	if node.ArtifactInputsJSON != "" {
		if err := json.Unmarshal([]byte(node.ArtifactInputsJSON), &node.ArtifactInputs); err != nil {
			return fmt.Errorf("decode workflow artifact inputs: %w", err)
		}
	}
	return nil
}

func (d *WorkflowStoreImpl) saveTaskParamsTx(tx *gorp.Transaction, projectID int, params *db.TaskParams, existingID *int) (int, error) {
	copy := *params
	copy.ProjectID = projectID
	if existingID == nil {
		copy.ID = 0
		if err := tx.Insert(&copy); err != nil {
			return 0, err
		}
		params.ID = copy.ID
		params.ProjectID = projectID
		return copy.ID, nil
	}
	copy.ID = *existingID
	if _, err := tx.Update(&copy); err != nil {
		return 0, err
	}
	params.ID = copy.ID
	params.ProjectID = projectID
	return copy.ID, nil
}

func (d *WorkflowStoreImpl) insertTx(tx *gorp.Transaction, query string, args ...any) (int, error) {
	prepared := d.connection.PrepareQuery(query)
	if d.connection.GetDialect() == util.DbDriverPostgres {
		id, err := tx.SelectInt(prepared+" returning id", args...)
		return int(id), err
	}
	result, err := tx.Exec(prepared, args...)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	return int(id), err
}
