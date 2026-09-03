package pro_interfaces

import (
	"context"
	"errors"
	"net/http"

	"github.com/semaphoreui/semaphore/db"
)

// WorkflowArtifactRetentionUpdate contains only administrator-selected limits.
// Scope, tenant, revision, actor, and timestamps are derived by the server.
type WorkflowArtifactRetentionUpdate struct {
	ExpectedRevision int   `json:"expected_revision"`
	RetentionSeconds int64 `json:"retention_seconds"`
	MaxArtifactBytes int64 `json:"max_artifact_bytes"`
	MaxRunBytes      int64 `json:"max_run_bytes"`
}

func (update WorkflowArtifactRetentionUpdate) Validate() error {
	if update.ExpectedRevision < 0 || update.ExpectedRevision == int(^uint(0)>>1) ||
		update.RetentionSeconds < db.MinWorkflowArtifactRetentionSeconds ||
		update.RetentionSeconds > db.MaxWorkflowArtifactRetentionSeconds ||
		update.MaxArtifactBytes < 1 || update.MaxArtifactBytes > db.MaxWorkflowFileArtifactBytes ||
		update.MaxRunBytes < update.MaxArtifactBytes || update.MaxRunBytes > db.MaxWorkflowFileArtifactRunBytes {
		return errors.New("workflow artifact retention update is invalid")
	}
	return nil
}

// WorkflowArtifactRetentionState exposes the current published policies and
// the effective limits. A nil GlobalPolicy means immutable built-in revision 0.
type WorkflowArtifactRetentionState struct {
	GlobalPolicy  *db.WorkflowArtifactRetentionPolicy  `json:"global_policy,omitempty"`
	ProjectPolicy *db.WorkflowArtifactRetentionPolicy  `json:"project_policy,omitempty"`
	Effective     db.WorkflowArtifactRetentionSnapshot `json:"effective"`
}

func (state WorkflowArtifactRetentionState) Validate(scope db.WorkflowArtifactRetentionScope, projectID *int) error {
	if state.Effective.Validate() != nil {
		return errors.New("workflow artifact retention state is invalid")
	}
	expectedRetentionSeconds := int64(db.DefaultWorkflowArtifactRetentionSeconds)
	expectedMaxArtifactBytes := db.MaxWorkflowFileArtifactBytes
	expectedMaxRunBytes := db.MaxWorkflowFileArtifactRunBytes
	if state.GlobalPolicy == nil {
		if state.Effective.GlobalRevision != 0 {
			return errors.New("workflow artifact retention state has invalid global provenance")
		}
	} else if state.GlobalPolicy.Validate() != nil || state.GlobalPolicy.Scope != db.WorkflowArtifactRetentionGlobal ||
		state.GlobalPolicy.ProjectID != nil || state.Effective.GlobalRevision != state.GlobalPolicy.Revision {
		return errors.New("workflow artifact retention state has invalid global policy")
	} else {
		expectedRetentionSeconds = state.GlobalPolicy.RetentionSeconds
		expectedMaxArtifactBytes = state.GlobalPolicy.MaxArtifactBytes
		expectedMaxRunBytes = state.GlobalPolicy.MaxRunBytes
	}
	switch scope {
	case db.WorkflowArtifactRetentionGlobal:
		if projectID != nil || state.ProjectPolicy != nil || state.Effective.ProjectRevision != 0 {
			return errors.New("global workflow artifact retention state names a project")
		}
	case db.WorkflowArtifactRetentionProject:
		if projectID == nil || *projectID < 1 {
			return errors.New("project workflow artifact retention state has no project")
		}
		if state.ProjectPolicy == nil {
			if state.Effective.ProjectRevision != 0 {
				return errors.New("workflow artifact retention state has invalid project provenance")
			}
		} else if state.ProjectPolicy.Validate() != nil || state.ProjectPolicy.Scope != db.WorkflowArtifactRetentionProject ||
			state.ProjectPolicy.ProjectID == nil || *state.ProjectPolicy.ProjectID != *projectID ||
			state.Effective.ProjectRevision != state.ProjectPolicy.Revision {
			return errors.New("workflow artifact retention state has invalid project policy")
		} else {
			expectedRetentionSeconds = state.ProjectPolicy.RetentionSeconds
			expectedMaxArtifactBytes = state.ProjectPolicy.MaxArtifactBytes
			expectedMaxRunBytes = state.ProjectPolicy.MaxRunBytes
		}
	default:
		return errors.New("workflow artifact retention state has invalid scope")
	}
	if state.Effective.RetentionSeconds != expectedRetentionSeconds || state.Effective.MaxArtifactBytes != expectedMaxArtifactBytes || state.Effective.MaxRunBytes != expectedMaxRunBytes {
		return errors.New("workflow artifact retention state has inconsistent effective limits")
	}
	return nil
}

type WorkflowArtifactRetentionGovernanceServiceFacade interface {
	GetWorkflowArtifactRetention(context.Context, db.WorkflowArtifactRetentionScope, *int) (WorkflowArtifactRetentionState, error)
	PublishWorkflowArtifactRetention(context.Context, db.WorkflowArtifactRetentionScope, *int, WorkflowArtifactRetentionUpdate, int) (WorkflowArtifactRetentionState, error)
}

type WorkflowArtifactRetentionController interface {
	GetGlobalWorkflowArtifactRetention(http.ResponseWriter, *http.Request)
	PublishGlobalWorkflowArtifactRetention(http.ResponseWriter, *http.Request)
	GetProjectWorkflowArtifactRetention(http.ResponseWriter, *http.Request)
	PublishProjectWorkflowArtifactRetention(http.ResponseWriter, *http.Request)
}

type WorkflowArtifactRetentionAuditConfigurer interface {
	ConfigureWorkflowArtifactRetentionAudit(AuditServiceFacade)
}
