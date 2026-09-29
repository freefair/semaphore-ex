package sql

import (
	"database/sql"
	"errors"
	"time"

	"github.com/semaphoreui/semaphore/db"
)

// GetWorkflowRevisions exposes the upstream revision contract as a projection
// of EX's append-only workflow versions. The authoritative graph remains the
// immutable version snapshot; no mutable copy of nodes or edges is created.
func (d *WorkflowStoreImpl) GetWorkflowRevisions(projectID int, workflowID int) ([]db.WorkflowRevision, error) {
	if d.connection == nil {
		return nil, db.ErrNotFound
	}

	type revisionProjection struct {
		ID                 int       `db:"id"`
		ProjectID          int       `db:"project_id"`
		WorkflowTemplateID int       `db:"workflow_template_id"`
		Number             int       `db:"number"`
		Created            time.Time `db:"created"`
		CreatedByUserID    *int      `db:"created_by_user_id"`
		HasRuns            bool      `db:"has_runs"`
	}
	var projections []revisionProjection
	_, err := d.connection.SelectAll(&projections, `select v.id, v.project_id,
		v.workflow_template_id, v.version_number as number, v.created,
		case when v.author_user_id > 0 then v.author_user_id else null end as created_by_user_id,
		exists(select 1 from project__workflow_run r where r.project_id=v.project_id
			and r.workflow_template_id=v.workflow_template_id and r.workflow_version_id=v.id) as has_runs
		from project__workflow_version v where v.project_id=? and v.workflow_template_id=?
		order by v.version_number desc`, projectID, workflowID)
	if err != nil {
		return nil, err
	}
	revisions := make([]db.WorkflowRevision, len(projections))
	for index, projection := range projections {
		revisions[index] = db.WorkflowRevision{
			ID: projection.ID, ProjectID: projection.ProjectID, WorkflowTemplateID: projection.WorkflowTemplateID,
			Number: projection.Number, Created: projection.Created, CreatedByUserID: projection.CreatedByUserID,
			HasRuns: projection.HasRuns,
		}
	}
	return revisions, nil
}

// GetWorkflowRevisionGraph returns the immutable EX definition snapshot for
// an upstream-compatible revision ID. API handlers must authorize through
// WorkflowDefinitionService before calling this repository projection.
func (d *WorkflowStoreImpl) GetWorkflowRevisionGraph(projectID int, revisionID int) (db.WorkflowTemplate, error) {
	if d.connection == nil || revisionID <= 0 {
		return db.WorkflowTemplate{}, db.ErrNotFound
	}

	var version db.WorkflowVersion
	err := d.connection.SelectOne(&version,
		"select * from project__workflow_version where project_id=? and id=?",
		projectID, revisionID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return db.WorkflowTemplate{}, db.ErrNotFound
	}
	if err != nil {
		return db.WorkflowTemplate{}, err
	}
	if err = decodeWorkflowVersion(&version); err != nil {
		return db.WorkflowTemplate{}, err
	}

	workflow := version.DefinitionSnapshot
	workflow.ID = version.WorkflowTemplateID
	workflow.ProjectID = version.ProjectID
	workflow.RevisionID = version.ID
	workflow.Revision = version.VersionNumber
	workflow.CurrentVersionID = version.ID
	return workflow, nil
}
