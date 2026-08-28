package server

import (
	"github.com/semaphoreui/semaphore/db"
	workflowDB "github.com/semaphoreui/semaphore/pro/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type workflowDefinitionService struct {
	repository      db.WorkflowManager
	validationStore db.WorkflowTemplateValidationStore
}

func NewWorkflowDefinitionService(
	repository db.WorkflowManager,
	validationStore db.WorkflowTemplateValidationStore,
) pro_interfaces.WorkflowDefinitionService {
	return &workflowDefinitionService{repository: repository, validationStore: validationStore}
}

func (s *workflowDefinitionService) List(projectID int, params db.RetrieveQueryParams) ([]db.WorkflowTemplate, error) {
	return s.repository.GetWorkflowTemplates(projectID, params)
}

func (s *workflowDefinitionService) Get(projectID int, workflowID int) (db.WorkflowTemplate, error) {
	return s.repository.GetWorkflowTemplate(projectID, workflowID)
}

func (s *workflowDefinitionService) Validate(projectID int, workflow db.WorkflowTemplate) (db.WorkflowValidationResult, error) {
	workflow.ProjectID = projectID
	_, result, err := workflowDB.PrepareWorkflowTemplate(s.validationStore, workflow)
	return result, err
}

func (s *workflowDefinitionService) Create(
	projectID int,
	workflow db.WorkflowTemplate,
) (db.WorkflowTemplate, db.WorkflowValidationResult, error) {
	workflow.ID = 0
	workflow.ProjectID = projectID
	workflow.Revision = 0
	workflow, result, err := workflowDB.PrepareWorkflowTemplate(s.validationStore, workflow)
	if err != nil {
		return db.WorkflowTemplate{}, db.WorkflowValidationResult{}, err
	}
	if !result.Valid {
		return db.WorkflowTemplate{}, result, nil
	}
	created, err := s.repository.CreateWorkflowTemplate(workflow)
	return created, result, err
}

func (s *workflowDefinitionService) Update(
	projectID int,
	workflowID int,
	workflow db.WorkflowTemplate,
) (db.WorkflowTemplate, db.WorkflowValidationResult, error) {
	workflow.ID = workflowID
	workflow.ProjectID = projectID
	workflow, result, err := workflowDB.PrepareWorkflowTemplate(s.validationStore, workflow)
	if err != nil {
		return db.WorkflowTemplate{}, db.WorkflowValidationResult{}, err
	}
	if !result.Valid {
		return db.WorkflowTemplate{}, result, nil
	}
	updated, err := s.repository.UpdateWorkflowTemplate(workflow)
	return updated, result, err
}

func (s *workflowDefinitionService) Delete(projectID int, workflowID int) error {
	return s.repository.DeleteWorkflowTemplate(projectID, workflowID)
}
