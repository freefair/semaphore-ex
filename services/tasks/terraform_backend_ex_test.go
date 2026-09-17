package tasks

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
)

func TestTerraformBackendProcessEnvironmentInjectsScopedHTTPSettings(t *testing.T) {
	backend := []string{
		"TF_HTTP_ADDRESS=https://semaphore.example.test/api/terraform/alias",
		"TF_HTTP_LOCK_ADDRESS=https://semaphore.example.test/api/terraform/alias",
		"TF_HTTP_UNLOCK_ADDRESS=https://semaphore.example.test/api/terraform/alias",
		"TF_HTTP_USERNAME=backend-user",
		"TF_HTTP_PASSWORD=synthetic-password",
	}
	result := terraformBackendProcessEnvironment(true, "alias", []string{"VISIBLE=value"}, backend)
	assert.Equal(t, append([]string{"VISIBLE=value"}, backend...), result)
	assert.Equal(t, 1, countEnvironmentKey(result, "TF_HTTP_ADDRESS"))
	assert.Equal(t, 1, countEnvironmentKey(result, "TF_HTTP_LOCK_ADDRESS"))
	assert.Equal(t, 1, countEnvironmentKey(result, "TF_HTTP_UNLOCK_ADDRESS"))
	assert.Equal(t, []string{"VISIBLE=value"}, terraformBackendProcessEnvironment(false, "alias", []string{"VISIBLE=value"}, backend))
}

func TestTerraformBackendEnvironmentValuesRejectsCrossProjectAndInvalidKeys(t *testing.T) {
	projectID, otherProjectID, keyID := 7, 8, 12
	alias := db.TerraformInventoryAlias{ProjectID: projectID, AuthKeyID: keyID}
	key := db.AccessKey{ID: keyID, ProjectID: &projectID, Type: db.AccessKeyLoginPassword, LoginPassword: db.LoginPassword{Login: "backend-user", Password: "synthetic-password"}}
	for name, mutate := range map[string]func(*db.TerraformInventoryAlias, *db.AccessKey){
		"foreign alias":    func(a *db.TerraformInventoryAlias, _ *db.AccessKey) { a.ProjectID = otherProjectID },
		"foreign key":      func(_ *db.TerraformInventoryAlias, k *db.AccessKey) { k.ProjectID = &otherProjectID },
		"wrong key type":   func(_ *db.TerraformInventoryAlias, k *db.AccessKey) { k.Type = db.AccessKeyString },
		"missing password": func(_ *db.TerraformInventoryAlias, k *db.AccessKey) { k.LoginPassword.Password = "" },
	} {
		t.Run(name, func(t *testing.T) {
			a, k := alias, key
			mutate(&a, &k)
			_, err := terraformBackendEnvironmentValues(projectID, "alias", a, k)
			assert.Error(t, err)
		})
	}
}

func countEnvironmentKey(values []string, key string) int {
	count := 0
	for _, value := range values {
		if len(value) > len(key) && value[:len(key)] == key && value[len(key)] == '=' {
			count++
		}
	}
	return count
}
