package tasks

import (
	"encoding/json"
	"errors"
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

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (t *TaskRunner) resolveExecutionSnapshotInventory() error {
	if t.Inventory.SSHKeyID != nil {
		key, err := t.pool.store.GetAccessKey(t.Inventory.ProjectID, *t.Inventory.SSHKeyID)
		if err != nil {
			return errors.New("execution preflight inventory credential is unavailable")
		}
		t.Inventory.SSHKey = key
	}
	if t.Inventory.BecomeKeyID != nil {
		key, err := t.pool.store.GetAccessKey(t.Inventory.ProjectID, *t.Inventory.BecomeKeyID)
		if err != nil {
			return errors.New("execution preflight inventory credential is unavailable")
		}
		t.Inventory.BecomeKey = key
	}
	if t.Inventory.RepositoryID != nil {
		if t.Inventory.Repository == nil || t.Inventory.Repository.ID != *t.Inventory.RepositoryID ||
			t.Inventory.Repository.ProjectID != t.Inventory.ProjectID {
			return errors.New("execution preflight inventory repository snapshot is invalid")
		}
		key, err := t.pool.store.GetAccessKey(t.Inventory.Repository.ProjectID, t.Inventory.Repository.SSHKeyID)
		if err != nil {
			return errors.New("execution preflight inventory repository credential is unavailable")
		}
		t.Inventory.Repository.SSHKey = key
		if err = t.pool.encryptionService.DeserializeSecret(&t.Inventory.Repository.SSHKey); err != nil {
			return err
		}
	}
	return nil
}

func (t *TaskRunner) resolveExecutionSnapshotTemplateVaults() error {
	for index := range t.Template.Vaults {
		vault := &t.Template.Vaults[index]
		if vault.Type != db.TemplateVaultPassword || vault.VaultKeyID == nil {
			continue
		}
		key, err := t.pool.store.GetAccessKey(t.Template.ProjectID, *vault.VaultKeyID)
		if err != nil {
			return errors.New("execution preflight vault credential is unavailable")
		}
		if err = t.pool.encryptionService.DeserializeSecret(&key); err != nil {
			return err
		}
		vault.Vault = &key
	}
	return nil
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

// loadExecutionSnapshotEnvironments merges the already-persisted resource
// layers of a reviewed task. It only resolves their AccessKey-backed secrets
// through the existing live credential authority; it never re-reads mutable
// environment configuration.
func (t *TaskRunner) loadExecutionSnapshotEnvironments(environments []db.Environment) error {
	return t.mergeEnvironmentLayers(environments, true)
}

func (t *TaskRunner) mergeEnvironmentLayers(environments []db.Environment, useSnapshotBindings bool) error {
	if len(environments) == 0 {
		return nil
	}

	seen := make(map[int]bool)

	mergedJSON := make(map[string]any)
	mergedENV := make(map[string]string)
	var mergedSecrets []db.EnvironmentSecret
	secretIndex := make(map[string]int)

	var lastEnv db.Environment

	for _, env := range environments {
		if seen[env.ID] {
			continue
		}
		seen[env.ID] = true

		var err error
		if useSnapshotBindings {
			err = t.resolveExecutionSnapshotEnvironmentSecrets(&env)
		} else {
			err = t.pool.encryptionService.FillEnvironmentSecrets(&env, true)
		}
		if err != nil {
			return err
		}

		if env.JSON != "" {
			partial := make(map[string]any)
			if err := json.Unmarshal([]byte(env.JSON), &partial); err != nil {
				return err
			}
			for k, v := range partial {
				mergedJSON[k] = v
			}
		}

		if env.ENV != nil && *env.ENV != "" {
			partial := make(map[string]string)
			if err := json.Unmarshal([]byte(*env.ENV), &partial); err != nil {
				return err
			}
			for k, v := range partial {
				mergedENV[k] = v
			}
		}

		for _, s := range env.Secrets {
			key := string(s.Type) + ":" + s.Name
			if idx, ok := secretIndex[key]; ok {
				mergedSecrets[idx] = s
			} else {
				mergedSecrets = append(mergedSecrets, s)
				secretIndex[key] = len(mergedSecrets) - 1
			}
		}

		lastEnv = env
	}

	t.Environment = lastEnv

	if len(mergedJSON) > 0 {
		b, err := json.Marshal(mergedJSON)
		if err != nil {
			return err
		}
		t.Environment.JSON = string(b)
	} else {
		t.Environment.JSON = ""
	}

	if len(mergedENV) > 0 {
		b, err := json.Marshal(mergedENV)
		if err != nil {
			return err
		}
		s := string(b)
		t.Environment.ENV = &s
	} else {
		t.Environment.ENV = nil
	}

	t.Environment.Secrets = mergedSecrets

	return nil
}

func (t *TaskRunner) resolveExecutionSnapshotEnvironmentSecrets(environment *db.Environment) error {
	resolved := make([]db.EnvironmentSecret, 0, len(environment.Secrets))
	for _, descriptor := range environment.Secrets {
		if descriptor.ID <= 0 || descriptor.Name == "" || descriptor.Secret != "" ||
			(descriptor.Type != db.EnvironmentSecretVar && descriptor.Type != db.EnvironmentSecretEnv) {
			return errors.New("execution preflight environment secret snapshot is invalid")
		}
		key, err := t.pool.store.GetAccessKey(environment.ProjectID, descriptor.ID)
		if err != nil || key.EnvironmentID == nil || *key.EnvironmentID != environment.ID ||
			(descriptor.Type == db.EnvironmentSecretVar && key.Owner != db.AccessKeyVariable) ||
			(descriptor.Type == db.EnvironmentSecretEnv && key.Owner != db.AccessKeyEnvironment) {
			return errors.New("execution preflight environment secret is unavailable")
		}
		if err = t.pool.encryptionService.DeserializeSecret(&key); err != nil {
			return err
		}
		descriptor.Secret = key.String
		resolved = append(resolved, descriptor)
	}
	environment.Secrets = resolved
	return nil
}
