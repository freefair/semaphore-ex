package server

import (
	"context"
	"errors"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type workflowArtifactRetentionGovernanceService struct {
	repository pro_interfaces.WorkflowFileArtifactRepository
}

var _ pro_interfaces.WorkflowArtifactRetentionGovernanceServiceFacade = (*workflowArtifactRetentionGovernanceService)(nil)

func NewWorkflowArtifactRetentionGovernanceService(repository pro_interfaces.WorkflowFileArtifactRepository) pro_interfaces.WorkflowArtifactRetentionGovernanceServiceFacade {
	if repository == nil {
		return nil
	}
	return &workflowArtifactRetentionGovernanceService{repository: repository}
}

func (service *workflowArtifactRetentionGovernanceService) GetWorkflowArtifactRetention(
	ctx context.Context,
	scope db.WorkflowArtifactRetentionScope,
	projectID *int,
) (pro_interfaces.WorkflowArtifactRetentionState, error) {
	if service == nil || service.repository == nil || ctx == nil || ctx.Err() != nil || !validWorkflowArtifactRetentionGovernanceScope(scope, projectID) {
		return pro_interfaces.WorkflowArtifactRetentionState{}, db.ErrInvalidOperation
	}
	global, globalFound, err := service.repository.GetWorkflowArtifactRetentionPolicy(db.WorkflowArtifactRetentionGlobal, nil)
	if err != nil {
		return pro_interfaces.WorkflowArtifactRetentionState{}, err
	}
	state := pro_interfaces.WorkflowArtifactRetentionState{Effective: db.DefaultWorkflowArtifactRetentionSnapshot()}
	if globalFound {
		state.GlobalPolicy = &global
		state.Effective, err = db.ResolveWorkflowArtifactRetention(global, nil)
		if err != nil {
			return pro_interfaces.WorkflowArtifactRetentionState{}, err
		}
	}
	if scope == db.WorkflowArtifactRetentionProject {
		project, projectFound, projectErr := service.repository.GetWorkflowArtifactRetentionPolicy(scope, projectID)
		if projectErr != nil {
			return pro_interfaces.WorkflowArtifactRetentionState{}, projectErr
		}
		if projectFound {
			state.ProjectPolicy = &project
			if globalFound {
				state.Effective, err = db.ResolveWorkflowArtifactRetention(global, &project)
			} else {
				state.Effective, err = resolveWorkflowArtifactRetentionAgainstBuiltin(project)
			}
			if err != nil {
				return pro_interfaces.WorkflowArtifactRetentionState{}, pro_interfaces.ErrWorkflowArtifactRetentionConflict
			}
		}
	}
	if err = state.Validate(scope, projectID); err != nil {
		return pro_interfaces.WorkflowArtifactRetentionState{}, err
	}
	return state, nil
}

func (service *workflowArtifactRetentionGovernanceService) PublishWorkflowArtifactRetention(
	ctx context.Context,
	scope db.WorkflowArtifactRetentionScope,
	projectID *int,
	update pro_interfaces.WorkflowArtifactRetentionUpdate,
	actorUserID int,
) (pro_interfaces.WorkflowArtifactRetentionState, error) {
	if service == nil || service.repository == nil || ctx == nil || ctx.Err() != nil || actorUserID < 1 ||
		update.Validate() != nil || !validWorkflowArtifactRetentionGovernanceScope(scope, projectID) {
		return pro_interfaces.WorkflowArtifactRetentionState{}, db.ErrInvalidOperation
	}
	current, err := service.GetWorkflowArtifactRetention(ctx, scope, projectID)
	if err != nil {
		return pro_interfaces.WorkflowArtifactRetentionState{}, err
	}
	currentRevision := current.Effective.GlobalRevision
	if scope == db.WorkflowArtifactRetentionProject {
		currentRevision = current.Effective.ProjectRevision
		globalRetentionSeconds := int64(db.DefaultWorkflowArtifactRetentionSeconds)
		globalMaxArtifactBytes := int64(db.MaxWorkflowFileArtifactBytes)
		globalMaxRunBytes := int64(db.MaxWorkflowFileArtifactRunBytes)
		if current.GlobalPolicy != nil {
			globalRetentionSeconds = current.GlobalPolicy.RetentionSeconds
			globalMaxArtifactBytes = current.GlobalPolicy.MaxArtifactBytes
			globalMaxRunBytes = current.GlobalPolicy.MaxRunBytes
		}
		if update.RetentionSeconds > globalRetentionSeconds || update.MaxArtifactBytes > globalMaxArtifactBytes || update.MaxRunBytes > globalMaxRunBytes {
			return pro_interfaces.WorkflowArtifactRetentionState{}, pro_interfaces.ErrWorkflowArtifactRetentionConflict
		}
	}
	if currentRevision != update.ExpectedRevision {
		return pro_interfaces.WorkflowArtifactRetentionState{}, pro_interfaces.ErrWorkflowArtifactRetentionConflict
	}
	policyProjectID := copyWorkflowArtifactRetentionProjectID(projectID)
	policy := db.WorkflowArtifactRetentionPolicy{
		Scope: scope, ProjectID: policyProjectID, Revision: update.ExpectedRevision + 1,
		RetentionSeconds: update.RetentionSeconds, MaxArtifactBytes: update.MaxArtifactBytes, MaxRunBytes: update.MaxRunBytes,
		CreatedByUserID: actorUserID, CreatedAt: time.Unix(1, 0).UTC(),
	}
	if policy.Validate() != nil {
		return pro_interfaces.WorkflowArtifactRetentionState{}, db.ErrInvalidOperation
	}
	if _, err = service.repository.PublishWorkflowArtifactRetentionPolicy(policy, update.ExpectedRevision); err != nil {
		return pro_interfaces.WorkflowArtifactRetentionState{}, err
	}
	return service.GetWorkflowArtifactRetention(ctx, scope, projectID)
}

func validWorkflowArtifactRetentionGovernanceScope(scope db.WorkflowArtifactRetentionScope, projectID *int) bool {
	return scope == db.WorkflowArtifactRetentionGlobal && projectID == nil ||
		scope == db.WorkflowArtifactRetentionProject && projectID != nil && *projectID > 0
}

func copyWorkflowArtifactRetentionProjectID(projectID *int) *int {
	if projectID == nil {
		return nil
	}
	value := *projectID
	return &value
}

func resolveWorkflowArtifactRetentionAgainstBuiltin(project db.WorkflowArtifactRetentionPolicy) (db.WorkflowArtifactRetentionSnapshot, error) {
	defaults := db.DefaultWorkflowArtifactRetentionSnapshot()
	if project.Validate() != nil || project.Scope != db.WorkflowArtifactRetentionProject ||
		project.RetentionSeconds > defaults.RetentionSeconds || project.MaxArtifactBytes > defaults.MaxArtifactBytes || project.MaxRunBytes > defaults.MaxRunBytes {
		return db.WorkflowArtifactRetentionSnapshot{}, errors.New("project workflow artifact retention policy widens the built-in global policy")
	}
	defaults.ProjectRevision = project.Revision
	defaults.RetentionSeconds = project.RetentionSeconds
	defaults.MaxArtifactBytes = project.MaxArtifactBytes
	defaults.MaxRunBytes = project.MaxRunBytes
	return defaults, defaults.Validate()
}
