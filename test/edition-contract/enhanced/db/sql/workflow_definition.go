package sql

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
)

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
		if err := d.loadWorkflowGraph(nil, &workflows[index]); err != nil {
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
	if err := d.loadWorkflowGraph(nil, &workflow); err != nil {
		return db.WorkflowTemplate{}, err
	}
	return workflow, nil
}

func (d *WorkflowStoreImpl) CreateWorkflowTemplate(workflow db.WorkflowTemplate) (db.WorkflowTemplate, error) {
	if d.connection == nil {
		return db.WorkflowTemplate{}, errors.New("workflow database connection is unavailable")
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return db.WorkflowTemplate{}, err
	}
	defer func() { _ = tx.Rollback() }()
	workflow.DefinitionVersion = db.WorkflowDefinitionVersion
	workflow.Revision = 1
	workflow.ID, err = d.insertTx(tx,
		"insert into project__workflow_template(project_id, name, description, start_version, definition_version, revision, max_parallel_tasks) values (?, ?, ?, ?, ?, ?, ?)",
		workflow.ProjectID, workflow.Name, workflow.Description, workflow.StartVersion,
		workflow.DefinitionVersion, workflow.Revision, workflow.MaxParallelTasks,
	)
	if err != nil {
		return db.WorkflowTemplate{}, err
	}
	if err = d.replaceWorkflowGraph(tx, &workflow, nil); err != nil {
		return db.WorkflowTemplate{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.WorkflowTemplate{}, err
	}
	return workflow, nil
}

func (d *WorkflowStoreImpl) UpdateWorkflowTemplate(workflow db.WorkflowTemplate) (db.WorkflowTemplate, error) {
	if d.connection == nil {
		return db.WorkflowTemplate{}, errors.New("workflow database connection is unavailable")
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return db.WorkflowTemplate{}, err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := d.selectWorkflowTemplateTx(tx, workflow.ProjectID, workflow.ID)
	if err != nil {
		return db.WorkflowTemplate{}, err
	}
	if current.Revision != workflow.Revision {
		return db.WorkflowTemplate{}, pro_interfaces.ErrWorkflowRevisionConflict
	}
	result, err := tx.Exec(d.connection.PrepareQuery(
		"update project__workflow_template set name=?, description=?, start_version=?, definition_version=?, max_parallel_tasks=?, revision=revision+1 where project_id=? and id=? and revision=?"),
		workflow.Name, workflow.Description, workflow.StartVersion, workflow.DefinitionVersion, workflow.MaxParallelTasks,
		workflow.ProjectID, workflow.ID, workflow.Revision,
	)
	if err != nil {
		return db.WorkflowTemplate{}, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return db.WorkflowTemplate{}, err
	}
	if updated != 1 {
		return db.WorkflowTemplate{}, pro_interfaces.ErrWorkflowRevisionConflict
	}
	if err = d.loadWorkflowGraph(tx, &current); err != nil {
		return db.WorkflowTemplate{}, err
	}
	if err = d.replaceWorkflowGraph(tx, &workflow, &current); err != nil {
		return db.WorkflowTemplate{}, err
	}
	workflow.Revision++
	if err = tx.Commit(); err != nil {
		return db.WorkflowTemplate{}, err
	}
	return workflow, nil
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
	return workflow, err
}

func (d *WorkflowStoreImpl) loadWorkflowGraph(tx *gorp.Transaction, workflow *db.WorkflowTemplate) error {
	nodeQuery := d.connection.PrepareQuery(
		"select id, workflow_template_id, template_id, kind, convergence_mode, join_mode, approval_timeout, approval_message, task_params_id, note, position_x, position_y, display_name, artifact_outputs, artifact_inputs from project__workflow_node where workflow_template_id=? order by id")
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
		if err = decodeWorkflowArtifactDefinition(&workflow.Nodes[index]); err != nil {
			return err
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
		if _, exists := existingNodes[clientID]; exists && clientID > 0 {
			if _, err := tx.Exec(d.connection.PrepareQuery(
				"update project__workflow_node set template_id=?, kind=?, convergence_mode=?, join_mode=?, approval_timeout=?, approval_message=?, task_params_id=?, note=?, position_x=?, position_y=?, display_name=? where workflow_template_id=? and id=?"),
				node.TemplateID, node.Kind, node.ConvergenceMode, node.JoinMode, node.ApprovalTimeout, node.ApprovalMessage,
				node.TaskParamsID, node.Note, node.PositionX, node.PositionY, node.DisplayName,
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
			"insert into project__workflow_node(workflow_template_id, template_id, kind, convergence_mode, join_mode, approval_timeout, approval_message, task_params_id, note, position_x, position_y, display_name) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			workflow.ID, node.TemplateID, node.Kind, node.ConvergenceMode, node.JoinMode, node.ApprovalTimeout,
			node.ApprovalMessage, node.TaskParamsID, node.Note, node.PositionX, node.PositionY, node.DisplayName,
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
				"update project__workflow_edge set source_node_id=?, destination_node_id=?, condition=?, label=?, condition_expression=?, condition_program=? where workflow_template_id=? and id=?"),
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
			"insert into project__workflow_edge(workflow_template_id, source_node_id, destination_node_id, condition, label, condition_expression, condition_program) values (?, ?, ?, ?, ?, ?, ?)",
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
