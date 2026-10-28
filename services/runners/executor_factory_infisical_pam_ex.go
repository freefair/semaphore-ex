package runners

import (
	"fmt"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/services/server"
)

// hydrateJobInfisicalPAM attaches each machine identity secret to its
// routing configuration and validates the configuration the server sent.
func hydrateJobInfisicalPAM(jobData *JobData, accessKeys map[int]db.AccessKey) error {
	configs := make([]db.TaskInfisicalPAM, 0, len(jobData.InfisicalPAM))
	for _, config := range jobData.InfisicalPAM {
		if err := server.ValidateTaskInfisicalPAM(config); err != nil {
			return fmt.Errorf("runner Infisical PAM storage #%d is invalid: %w", config.StorageID, err)
		}
		key, ok := accessKeys[config.CredentialKeyID]
		if !ok || key.ID != config.CredentialKeyID || key.Type != db.AccessKeyString {
			return fmt.Errorf("runner Infisical PAM storage #%d secret is unavailable", config.StorageID)
		}
		config.Credential = key
		configs = append(configs, config)
	}
	jobData.Task.InfisicalPAM = configs
	return nil
}
