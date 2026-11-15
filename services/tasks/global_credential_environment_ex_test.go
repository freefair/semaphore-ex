package tasks

import (
	"context"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type taskCredentialBindingRecorder struct {
	bindings []pro_interfaces.GlobalCredentialTaskBinding
}

func (s *taskCredentialBindingRecorder) ResolveAndInject(ctx context.Context, request pro_interfaces.GlobalCredentialResolutionRequest, injector pro_interfaces.GlobalCredentialInjector) (pro_interfaces.GlobalCredentialResolutionSnapshot, error) {
	s.bindings = append(s.bindings, request.Binding)
	if err := injector.InjectGlobalCredential(ctx, request.Binding.Target, []byte("value-of-"+request.Binding.Target)); err != nil {
		return pro_interfaces.GlobalCredentialResolutionSnapshot{}, err
	}
	return pro_interfaces.GlobalCredentialResolutionSnapshot{Outcome: pro_interfaces.GlobalCredentialResolutionAllowed}, nil
}

func TestResolveTaskGlobalCredentialsIncludesVariableGroupReferences(t *testing.T) {
	resolver := &taskCredentialBindingRecorder{}
	pool := &TaskPool{globalCredentialResolver: resolver, logger: make(chan logRecord, 4)}
	runner := &TaskRunner{
		Task: db.Task{ID: 3, ProjectID: 4, TemplateID: 5, GlobalCredentialBindings: map[string]int{"workflow_token": 9}},
		Environment: db.Environment{Credentials: []db.EnvironmentCredential{
			{Name: "license_key", Target: db.EnvironmentCredentialTargetVar, CredentialID: 7},
			{Name: "TF_VAR_token", Target: db.EnvironmentCredentialTargetEnv, CredentialID: 8},
		}},
		pool: pool,
	}

	merged, err := pool.ResolveTaskGlobalCredentials(context.Background(), runner, nil, `{"survey":"kept"}`)

	require.NoError(t, err)
	assert.Equal(t, []pro_interfaces.GlobalCredentialTaskBinding{
		{CredentialID: 8, Target: "TF_VAR_token", Env: true},
		{CredentialID: 7, Target: "license_key"},
		{CredentialID: 9, Target: "workflow_token"},
	}, resolver.bindings)
	assert.JSONEq(t, `{"survey":"kept","TF_VAR_token":"value-of-TF_VAR_token","license_key":"value-of-license_key","workflow_token":"value-of-workflow_token"}`, merged)
	assert.Equal(t, []string{"TF_VAR_token", "license_key", "workflow_token"}, runner.GlobalCredentialBindingTargets())

	runner.SetTaskCredentialRedaction(merged)
	runner.Log("output contains value-of-TF_VAR_token")
	record := <-pool.logger
	assert.NotContains(t, record.output, "value-of-TF_VAR_token")
}

func TestResolveTaskGlobalCredentialsBlocksConflictingReferences(t *testing.T) {
	reference := []db.EnvironmentCredential{{Name: "shared_name", Target: db.EnvironmentCredentialTargetVar, CredentialID: 2}}
	tests := []struct {
		name   string
		runner *TaskRunner
		survey string
	}{
		{"workflow binding of the same name", &TaskRunner{
			Task:        db.Task{GlobalCredentialBindings: map[string]int{"shared_name": 1}},
			Environment: db.Environment{Credentials: reference},
		}, ""},
		{"variable group secret of the same name", &TaskRunner{
			Environment: db.Environment{
				Secrets:     []db.EnvironmentSecret{{Name: "shared_name", Type: db.EnvironmentSecretVar}},
				Credentials: reference,
			},
		}, ""},
		{"survey answer of the same name", &TaskRunner{
			Environment: db.Environment{Credentials: reference},
		}, `{"shared_name":"survey"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := &TaskPool{globalCredentialResolver: &taskCredentialBindingRecorder{}, logger: make(chan logRecord, 4)}
			tt.runner.pool = pool
			tt.runner.Task.ID, tt.runner.Task.ProjectID, tt.runner.Task.TemplateID = 3, 4, 5
			_, err := pool.ResolveTaskGlobalCredentials(context.Background(), tt.runner, nil, tt.survey)
			assert.Error(t, err)
		})
	}
}

func TestSortedEnvironmentCredentialsOrdersByName(t *testing.T) {
	sorted := sortedEnvironmentCredentials(map[string]db.EnvironmentCredential{
		"b": {Name: "b", Target: db.EnvironmentCredentialTargetVar, CredentialID: 2},
		"A": {Name: "A", Target: db.EnvironmentCredentialTargetEnv, CredentialID: 1},
	})
	assert.Equal(t, []db.EnvironmentCredential{
		{Name: "A", Target: db.EnvironmentCredentialTargetEnv, CredentialID: 1},
		{Name: "b", Target: db.EnvironmentCredentialTargetVar, CredentialID: 2},
	}, sorted)
	assert.Empty(t, sortedEnvironmentCredentials(nil))
}

func TestLocalExecutorDeliversEnvironmentTargetCredentials(t *testing.T) {
	setupExecutorConfig(t)
	executor := &LocalExecutor{
		Secret: `{"TF_VAR_api":"env-value","license_key":"var-value"}`,
		Environment: db.Environment{Credentials: []db.EnvironmentCredential{
			{Name: "TF_VAR_api", Target: db.EnvironmentCredentialTargetEnv, CredentialID: 1},
			{Name: "license_key", Target: db.EnvironmentCredentialTargetVar, CredentialID: 2},
		}},
	}

	envVars, err := executor.credentialEnvVars()
	require.NoError(t, err)
	assert.Equal(t, []string{"TF_VAR_api=env-value"}, envVars)

	extraVars, err := executor.getEnvironmentExtraVars("user", nil)
	require.NoError(t, err)
	assert.NotContains(t, extraVars, "TF_VAR_api", "an environment-target credential must not become an extra var")
	assert.Equal(t, "var-value", extraVars["license_key"])
}

func TestLocalExecutorFailsWhenEnvironmentCredentialWasNotResolved(t *testing.T) {
	executor := &LocalExecutor{Environment: db.Environment{Credentials: []db.EnvironmentCredential{
		{Name: "TF_VAR_api", Target: db.EnvironmentCredentialTargetEnv, CredentialID: 1},
	}}}
	_, err := executor.credentialEnvVars()
	assert.ErrorContains(t, err, "TF_VAR_api")
}

func TestLoadEnvironmentsMergesCredentialReferencesAcrossGroups(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	owner, err := store.CreateUserWithoutPassword(db.User{Username: "merge-owner", Name: "Owner", Email: "merge-owner@example.test"})
	require.NoError(t, err)
	project, err := store.CreateProject(db.Project{Name: "merge project"})
	require.NoError(t, err)
	now := time.Now().UTC()
	create := func(name string) db.GlobalCredential {
		credential, _, err := store.CreateGlobalCredential(
			db.GlobalCredential{Type: db.GlobalCredentialTypeString, DisplayName: name, OwnerUserID: owner.ID, Enabled: true, Created: now},
			db.GlobalCredentialVersion{MaterialKind: db.GlobalCredentialMaterialLocalEncrypted, EncryptedMaterial: "envelope", CreatedByUserID: owner.ID})
		require.NoError(t, err)
		return credential
	}
	first, second, third := create("first"), create("second"), create("third")
	base, err := store.CreateEnvironment(db.Environment{ProjectID: project.ID, Name: "base", JSON: "{}", Credentials: []db.EnvironmentCredential{
		{Name: "shared_name", Target: db.EnvironmentCredentialTargetVar, CredentialID: first.ID},
		{Name: "LICENSE", Target: db.EnvironmentCredentialTargetEnv, CredentialID: second.ID},
	}})
	require.NoError(t, err)
	override, err := store.CreateEnvironment(db.Environment{ProjectID: project.ID, Name: "override", JSON: "{}", Credentials: []db.EnvironmentCredential{
		{Name: "shared_name", Target: db.EnvironmentCredentialTargetVar, CredentialID: third.ID},
	}})
	require.NoError(t, err)
	runner := &TaskRunner{
		pool:     &TaskPool{store: store, encryptionService: &EncryptionServiceMock{}},
		Template: db.Template{ProjectID: project.ID, EnvironmentIDs: []int{base.ID, override.ID}},
	}

	require.NoError(t, runner.loadEnvironments())

	assert.Equal(t, []db.EnvironmentCredential{
		{Name: "LICENSE", Target: db.EnvironmentCredentialTargetEnv, CredentialID: second.ID},
		{Name: "shared_name", Target: db.EnvironmentCredentialTargetVar, CredentialID: third.ID},
	}, runner.Environment.Credentials, "a later group replaces an earlier reference of the same name")
}

func TestLocalExecutorKeepsCredentialExtraVariablesOffCommandLines(t *testing.T) {
	setupExecutorConfig(t)
	references := []db.EnvironmentCredential{
		{Name: "license_key", Target: db.EnvironmentCredentialTargetVar, CredentialID: 2},
		{Name: "API_TOKEN", Target: db.EnvironmentCredentialTargetEnv, CredentialID: 3},
	}
	tests := []struct {
		app     db.TemplateApp
		env     []string
		inExtra bool
	}{
		{db.AppAnsible, []string{"API_TOKEN=env-value"}, true},
		{db.AppTerraform, []string{"TF_VAR_license_key=var-value", "API_TOKEN=env-value"}, false},
		{db.AppTofu, []string{"TF_VAR_license_key=var-value", "API_TOKEN=env-value"}, false},
		{db.AppBash, []string{"license_key=var-value", "API_TOKEN=env-value"}, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.app), func(t *testing.T) {
			executor := &LocalExecutor{
				Template:    db.Template{App: tt.app},
				Secret:      `{"license_key":"var-value","API_TOKEN":"env-value"}`,
				Environment: db.Environment{Credentials: references},
			}
			envVars, err := executor.credentialEnvVars()
			require.NoError(t, err)
			assert.ElementsMatch(t, tt.env, envVars)
			extraVars, err := executor.getEnvironmentExtraVars("user", nil)
			require.NoError(t, err)
			_, present := extraVars["license_key"]
			assert.Equal(t, tt.inExtra, present)
			assert.NotContains(t, extraVars, "API_TOKEN")
		})
	}
}
