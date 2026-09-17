package tasks

import (
	"fmt"

	"github.com/semaphoreui/semaphore/db"
	proFactory "github.com/semaphoreui/semaphore/pro/db/factory"
	"github.com/semaphoreui/semaphore/services/server"
	"github.com/semaphoreui/semaphore/util"
)

// TerraformBackendEnvironment resolves the alias-scoped login-password key at
// dispatch time. The returned values are intended only for the child process
// environment and must never be logged or persisted in task metadata.
func TerraformBackendEnvironment(store db.Store, encryption server.AccessKeyEncryptionService, projectID int, aliasID string) ([]string, error) {
	if aliasID == "" {
		return nil, nil
	}
	alias, err := proFactory.NewTerraformStore(store).GetTerraformInventoryAliasByAlias(aliasID)
	if err != nil {
		return nil, fmt.Errorf("terraform backend alias is unavailable")
	}
	key, err := store.GetAccessKey(projectID, alias.AuthKeyID)
	if err != nil {
		return nil, fmt.Errorf("terraform backend credential is unavailable")
	}
	if err = encryption.DeserializeSecret(&key); err != nil || key.LoginPassword.Login == "" || key.LoginPassword.Password == "" {
		return nil, fmt.Errorf("terraform backend credential is unavailable")
	}
	return terraformBackendEnvironmentValues(projectID, aliasID, alias, key)
}

func terraformBackendEnvironmentValues(projectID int, aliasID string, alias db.TerraformInventoryAlias, key db.AccessKey) ([]string, error) {
	if alias.ProjectID != projectID || key.ProjectID == nil || *key.ProjectID != projectID || key.ID != alias.AuthKeyID || key.Type != db.AccessKeyLoginPassword || key.LoginPassword.Login == "" || key.LoginPassword.Password == "" {
		return nil, fmt.Errorf("terraform backend credential is unavailable")
	}
	address := util.GetPublicAliasURL("terraform", aliasID)
	return []string{"TF_HTTP_ADDRESS=" + address, "TF_HTTP_LOCK_ADDRESS=" + address, "TF_HTTP_UNLOCK_ADDRESS=" + address, "TF_HTTP_USERNAME=" + key.LoginPassword.Login, "TF_HTTP_PASSWORD=" + key.LoginPassword.Password}, nil
}
