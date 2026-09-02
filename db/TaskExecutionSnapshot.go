package db

import (
	"encoding/json"
	"errors"
)

const TaskExecutionSnapshotVersion = 1

// TaskExecutionSnapshot is the private, immutable configuration envelope for
// a reviewed execution. It is persisted only on task and workflow-run-node
// rows, is never an API DTO, and deliberately contains resource descriptors
// rather than AccessKey material. Credential authority remains live at
// dispatch, where the existing resolver applies rotation and revocation.
type TaskExecutionSnapshot struct {
	Version             int           `json:"version"`
	ProjectID           int           `json:"project_id"`
	Fingerprint         string        `json:"fingerprint"`
	Template            Template      `json:"template"`
	Inventory           *Inventory    `json:"inventory,omitempty"`
	InventoryRepository *Repository   `json:"inventory_repository,omitempty"`
	Repository          Repository    `json:"repository"`
	Environments        []Environment `json:"environments"`
}

// Validate checks only the structural invariants needed before a persisted
// snapshot becomes execution input. Values remain private to the protected
// task record and never enter a review token, audit record, or API response.
func (snapshot TaskExecutionSnapshot) Validate(projectID, templateID int) error {
	if snapshot.Version != TaskExecutionSnapshotVersion || snapshot.ProjectID != projectID ||
		snapshot.Fingerprint == "" || snapshot.Template.ID != templateID || snapshot.Template.ProjectID <= 0 ||
		snapshot.Repository.ID <= 0 || snapshot.Repository.ProjectID != snapshot.Template.ProjectID {
		return errors.New("task execution snapshot is invalid")
	}
	if snapshot.Inventory != nil && (snapshot.Inventory.ID <= 0 || snapshot.Inventory.ProjectID != snapshot.Template.ProjectID) {
		return errors.New("task execution snapshot inventory is invalid")
	}
	if snapshot.Inventory != nil && snapshot.Inventory.RepositoryID != nil &&
		(snapshot.InventoryRepository == nil || snapshot.InventoryRepository.ID != *snapshot.Inventory.RepositoryID ||
			snapshot.InventoryRepository.ProjectID != snapshot.Template.ProjectID) {
		return errors.New("task execution snapshot inventory repository is invalid")
	}
	seen := make(map[int]struct{}, len(snapshot.Environments))
	for _, environment := range snapshot.Environments {
		if environment.ID <= 0 || environment.ProjectID != snapshot.Template.ProjectID {
			return errors.New("task execution snapshot environment is invalid")
		}
		if _, exists := seen[environment.ID]; exists {
			return errors.New("task execution snapshot environment is invalid")
		}
		seen[environment.ID] = struct{}{}
	}
	return nil
}

func EncodeTaskExecutionSnapshot(snapshot TaskExecutionSnapshot) (string, error) {
	if err := snapshot.Validate(snapshot.ProjectID, snapshot.Template.ID); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func DecodeTaskExecutionSnapshot(encoded string, projectID, templateID int) (*TaskExecutionSnapshot, error) {
	if encoded == "" {
		return nil, nil
	}
	var snapshot TaskExecutionSnapshot
	if err := json.Unmarshal([]byte(encoded), &snapshot); err != nil {
		return nil, errors.New("task execution snapshot is invalid")
	}
	if err := snapshot.Validate(projectID, templateID); err != nil {
		return nil, err
	}
	return &snapshot, nil
}
