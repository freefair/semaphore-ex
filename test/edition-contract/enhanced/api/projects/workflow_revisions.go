package projects

import (
	"net/http"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
)

type workflowRevisionStore interface {
	GetWorkflowRevisions(projectID int, workflowID int) ([]db.WorkflowRevision, error)
	GetWorkflowRevisionGraph(projectID int, revisionID int) (db.WorkflowTemplate, error)
}

// GetWorkflowRevisions serves the upstream-compatible revision timeline from
// EX's immutable version history. Authorization and legacy baseline creation
// stay in DefinitionService; the repository is only used for the HasRuns
// execution-retention marker.
func (c *workflowController) GetWorkflowRevisions(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	workflow := helpers.GetFromContext(r, "workflow").(db.WorkflowTemplate)
	actor := helpers.UserFromContext(r)

	versions, err := c.definitionService.ListVersions(project.ID, workflow.ID, workflowVersionQueryParams(r), actor)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	store, ok := c.workflowManager.(workflowRevisionStore)
	if !ok {
		helpers.WriteError(w, db.ErrNotFound)
		return
	}
	revisions, err := store.GetWorkflowRevisions(project.ID, workflow.ID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	// A version may have been appended after the authorized list read. Return
	// only the already-authorized IDs, while keeping the run-retention marker.
	authorized := make(map[int]struct{}, len(versions))
	for _, version := range versions {
		authorized[version.ID] = struct{}{}
	}
	result := make([]db.WorkflowRevision, 0, len(revisions))
	for _, revision := range revisions {
		if _, allowed := authorized[revision.ID]; allowed {
			result = append(result, revision)
		}
	}
	helpers.WriteJSON(w, http.StatusOK, result)
}

// GetWorkflowRevision resolves the public revision ID through the authorized
// immutable-version service, then returns its definition with upstream field
// names. It intentionally does not fetch an arbitrary store snapshot.
func (c *workflowController) GetWorkflowRevision(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	workflow := helpers.GetFromContext(r, "workflow").(db.WorkflowTemplate)
	actor := helpers.UserFromContext(r)
	revisionID, ok := helpers.GetIntParamOrAbort("revision_id", w, r)
	if !ok {
		return
	}

	if _, err := c.definitionService.Get(project.ID, workflow.ID, actor); err != nil {
		helpers.WriteError(w, err)
		return
	}
	store, ok := c.workflowManager.(workflowRevisionStore)
	if !ok {
		helpers.WriteError(w, db.ErrNotFound)
		return
	}
	graph, err := store.GetWorkflowRevisionGraph(project.ID, revisionID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	if graph.ID != workflow.ID || graph.RevisionID != revisionID || graph.Revision <= 0 {
		helpers.WriteError(w, db.ErrNotFound)
		return
	}

	version, err := c.definitionService.GetVersion(project.ID, workflow.ID, graph.Revision, actor)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	if version.ID != revisionID || version.ProjectID != project.ID || version.WorkflowTemplateID != workflow.ID || version.VersionNumber != graph.Revision {
		helpers.WriteError(w, db.ErrNotFound)
		return
	}
	definition := version.DefinitionSnapshot
	definition.ID = version.WorkflowTemplateID
	definition.ProjectID = version.ProjectID
	definition.RevisionID = version.ID
	definition.Revision = version.VersionNumber
	definition.CurrentVersionID = version.ID
	helpers.WriteJSON(w, http.StatusOK, definition)
}
