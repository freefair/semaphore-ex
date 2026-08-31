package server

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type unavailableWorkflowDefinitionService struct{}

func NewWorkflowDefinitionService(_ db.WorkflowManager, _ db.WorkflowTemplateValidationStore) pro_interfaces.WorkflowDefinitionService {
	return &unavailableWorkflowDefinitionService{}
}

func (s *unavailableWorkflowDefinitionService) List(_ int, _ db.RetrieveQueryParams, _ ...*db.User) ([]db.WorkflowTemplate, error) {
	return []db.WorkflowTemplate{}, nil
}

func (s *unavailableWorkflowDefinitionService) Get(_ int, _ int, _ ...*db.User) (db.WorkflowTemplate, error) {
	return db.WorkflowTemplate{}, db.ErrNotFound
}

func (s *unavailableWorkflowDefinitionService) Validate(_ int, _ db.WorkflowTemplate, _ ...*db.User) (db.WorkflowValidationResult, error) {
	return db.WorkflowValidationResult{Valid: false, Issues: []db.WorkflowValidationIssue{{
		Code: "WORKFLOWS_UNAVAILABLE", Message: "Workflow definitions are unavailable in this edition.",
	}}}, nil
}

func (s *unavailableWorkflowDefinitionService) Create(_ int, workflow db.WorkflowTemplate, _ ...*db.User) (db.WorkflowTemplate, db.WorkflowValidationResult, error) {
	validation, _ := s.Validate(0, workflow)
	return db.WorkflowTemplate{}, validation, nil
}

func (s *unavailableWorkflowDefinitionService) Update(_ int, _ int, workflow db.WorkflowTemplate, _ ...*db.User) (db.WorkflowTemplate, db.WorkflowValidationResult, error) {
	validation, _ := s.Validate(0, workflow)
	return db.WorkflowTemplate{}, validation, nil
}

func (s *unavailableWorkflowDefinitionService) Delete(_ int, _ int, _ ...*db.User) error {
	return db.ErrNotFound
}
