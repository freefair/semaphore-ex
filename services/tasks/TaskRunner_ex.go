package tasks

import (
	"encoding/json"
	"github.com/semaphoreui/semaphore/api/sockets"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
)

func (t *TaskRunner) publishStatus() {
	for _, user := range t.users {
		b, err := json.Marshal(&map[string]any{
			"type":                     "update",
			"start":                    t.Task.Start,
			"end":                      t.Task.End,
			"status":                   t.Task.Status,
			"task_id":                  t.Task.ID,
			"template_id":              t.Task.TemplateID,
			"project_id":               t.Task.ProjectID,
			"version":                  t.Task.Version,
			"assignment_generation":    t.Task.AssignmentGeneration,
			"runner_assigned_at":       t.Task.RunnerAssignedAt,
			"recovery_reason":          t.Task.RecoveryReason,
			"placement_decision":       t.Task.PlacementDecision,
			"requested_executor_image": t.Task.RequestedExecutorImage,
			"resolved_executor_image":  t.Task.ResolvedExecutorImage,
		})

		util.LogPanic(err)

		sockets.Message(user, b)
	}
}

// resolveCrossProjectTemplateVaults fills password vault values only in the
// in-memory runner template. The persisted provenance contains descriptors
// and IDs, never the resolved AccessKey material.
func (t *TaskRunner) resolveCrossProjectTemplateVaults(ownerProjectID int) error {
	for index := range t.Template.Vaults {
		vault := &t.Template.Vaults[index]
		if vault.Type != db.TemplateVaultPassword || vault.VaultKeyID == nil {
			continue
		}
		key, err := t.pool.store.GetAccessKey(ownerProjectID, *vault.VaultKeyID)
		if err != nil {
			return err
		}
		if err = t.pool.encryptionService.DeserializeSecret(&key); err != nil {
			return err
		}
		vault.Vault = &key
	}
	return nil
}
