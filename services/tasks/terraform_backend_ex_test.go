package tasks

import (
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	proFactory "github.com/semaphoreui/semaphore/pro/db/factory"
	"github.com/stretchr/testify/assert"
)

type terraformBackendTestEncryption struct {
	EncryptionServiceMock
	credentials map[int]db.LoginPassword
}

func (e *terraformBackendTestEncryption) DeserializeSecret(key *db.AccessKey) error {
	if value, ok := e.credentials[key.ID]; ok {
		key.LoginPassword = value
	}
	return nil
}

func TestTaskRunnerTerraformBackendSkipsRandomTaskAliasWithoutOverride(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "external backend"})
	assert.NoError(t, err)
	inventory, err := store.CreateInventory(db.Inventory{ProjectID: project.ID, Type: db.InventoryTerraformWorkspace, Inventory: "external"})
	assert.NoError(t, err)

	for _, app := range []db.TemplateApp{db.AppTerraform, db.AppTofu, db.AppTerragrunt} {
		t.Run(string(app), func(t *testing.T) {
			runner := &TaskRunner{
				Task:      db.Task{ProjectID: project.ID},
				Template:  db.Template{App: app, TaskParams: db.MapStringAnyField{"override_backend": false}},
				Inventory: inventory,
				Alias:     "task-lifecycle-alias-not-persisted",
				pool:      &TaskPool{store: store, encryptionService: &terraformBackendTestEncryption{}},
			}
			local := &LocalExecutor{}
			assert.NoError(t, runner.configureTerraformBackend(local))
			assert.Empty(t, local.TerraformBackendEnvironment)
		})
	}
}

func TestTaskRunnerTerraformBackendOverrideUsesScopedInventoryAlias(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "internal backend"})
	assert.NoError(t, err)
	inventory, err := store.CreateInventory(db.Inventory{ProjectID: project.ID, Type: db.InventoryTerraformWorkspace, Inventory: "workspace"})
	assert.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyLoginPassword, LoginPassword: db.LoginPassword{Login: "backend-user", Password: "synthetic-backend-password"}})
	assert.NoError(t, err)
	aliases := proFactory.NewTerraformStore(store)
	_, err = aliases.CreateTerraformInventoryAlias(db.TerraformInventoryAlias{ProjectID: project.ID, InventoryID: inventory.ID, AuthKeyID: key.ID, Alias: "zeta"})
	assert.NoError(t, err)
	_, err = aliases.CreateTerraformInventoryAlias(db.TerraformInventoryAlias{ProjectID: project.ID, InventoryID: inventory.ID, AuthKeyID: key.ID, Alias: "alpha"})
	assert.NoError(t, err)

	runner := &TaskRunner{
		Task:      db.Task{ProjectID: project.ID},
		Template:  db.Template{App: db.AppTerraform, TaskParams: db.MapStringAnyField{"override_backend": true}},
		Inventory: inventory,
		Alias:     "task-lifecycle-alias-not-persisted",
		pool:      &TaskPool{store: store, encryptionService: &terraformBackendTestEncryption{credentials: map[int]db.LoginPassword{key.ID: {Login: "backend-user", Password: "synthetic-backend-password"}}}},
	}
	local := &LocalExecutor{}
	assert.NoError(t, runner.configureTerraformBackend(local))
	assert.NotEmpty(t, local.TerraformBackendEnvironment)
	assert.Contains(t, strings.Join(local.TerraformBackendEnvironment, "\n"), "/terraform/alpha")
	assert.NotContains(t, strings.Join(local.TerraformBackendEnvironment, "\n"), runner.Alias)
}

func TestTerraformBackendOverrideRejectsDifferentScopedCredentialAliases(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "ambiguous backend"})
	assert.NoError(t, err)
	inventory, err := store.CreateInventory(db.Inventory{ProjectID: project.ID, Type: db.InventoryTerraformWorkspace, Inventory: "workspace"})
	assert.NoError(t, err)
	first, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyLoginPassword})
	assert.NoError(t, err)
	second, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyLoginPassword})
	assert.NoError(t, err)
	aliases := proFactory.NewTerraformStore(store)
	_, err = aliases.CreateTerraformInventoryAlias(db.TerraformInventoryAlias{ProjectID: project.ID, InventoryID: inventory.ID, AuthKeyID: first.ID, Alias: "first"})
	assert.NoError(t, err)
	_, err = aliases.CreateTerraformInventoryAlias(db.TerraformInventoryAlias{ProjectID: project.ID, InventoryID: inventory.ID, AuthKeyID: second.ID, Alias: "second"})
	assert.NoError(t, err)

	_, err = TerraformBackendOverrideEnvironment(store, &terraformBackendTestEncryption{}, project.ID, db.Template{App: db.AppTerraform, TaskParams: db.MapStringAnyField{"override_backend": true}}, inventory)
	assert.EqualError(t, err, "terraform backend aliases are ambiguous")
}

func TestTerraformBackendProcessEnvironmentInjectsScopedHTTPSettings(t *testing.T) {
	backend := []string{
		"TF_HTTP_ADDRESS=https://semaphore.example.test/api/terraform/alias",
		"TF_HTTP_LOCK_ADDRESS=https://semaphore.example.test/api/terraform/alias",
		"TF_HTTP_UNLOCK_ADDRESS=https://semaphore.example.test/api/terraform/alias",
		"TF_HTTP_USERNAME=backend-user",
		"TF_HTTP_PASSWORD=synthetic-password",
	}
	result := terraformBackendProcessEnvironment(true, []string{"VISIBLE=value"}, backend)
	assert.Equal(t, append([]string{"VISIBLE=value"}, backend...), result)
	assert.Equal(t, 1, countEnvironmentKey(result, "TF_HTTP_ADDRESS"))
	assert.Equal(t, 1, countEnvironmentKey(result, "TF_HTTP_LOCK_ADDRESS"))
	assert.Equal(t, 1, countEnvironmentKey(result, "TF_HTTP_UNLOCK_ADDRESS"))
	assert.Equal(t, []string{"VISIBLE=value"}, terraformBackendProcessEnvironment(false, []string{"VISIBLE=value"}, backend))
	assert.Equal(t, []string{"VISIBLE=value"}, terraformBackendProcessEnvironment(true, []string{"VISIBLE=value"}, nil))
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
