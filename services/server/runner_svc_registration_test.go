package server

import (
	"strings"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateProjectRunnerIssuesOneTimeRegistrationMaterial(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "runner project"})
	require.NoError(t, err)
	service := NewRunnerService(store)

	runner, token, err := service.CreateProjectRunner(db.Runner{
		Name:      "isolated",
		ProjectID: &project.ID,
		Active:    true,
	})

	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(token, RunnerRegistrationTokenPrefix))
	assert.Empty(t, runner.Token)
	assert.False(t, runner.Active)
	stored, err := store.GetRunner(project.ID, runner.ID)
	require.NoError(t, err)
	require.NotNil(t, stored.RegistrationTokenHash)
	assert.Equal(t, HashRunnerRegistrationToken(token), *stored.RegistrationTokenHash)
	require.NotNil(t, stored.RegistrationTokenExpiresAt)
	assert.WithinDuration(t, time.Now().Add(time.Hour), *stored.RegistrationTokenExpiresAt, 5*time.Second)
}

func TestCreateProjectRunnerRejectsGlobalScope(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	service := NewRunnerService(store)
	invalidProjectID := 0
	for name, projectID := range map[string]*int{
		"missing project": nil,
		"invalid project": &invalidProjectID,
	} {
		t.Run(name, func(t *testing.T) {
			_, token, err := service.CreateProjectRunner(db.Runner{Name: "not project-bound", ProjectID: projectID})

			assert.ErrorIs(t, err, ErrProjectRunnerRequiresProject)
			assert.Empty(t, token)
		})
	}
}

func TestRegenerateProjectRunnerRegistrationDeactivatesRunner(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "registration reset"})
	require.NoError(t, err)
	runner, err := store.CreateRunner(db.Runner{
		Name: "registered", ProjectID: &project.ID, Token: db.GenerateRunnerToken(), Active: true,
	})
	require.NoError(t, err)

	token, err := NewRunnerService(store).RegenerateRegistrationToken(runner)

	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(token, RunnerRegistrationTokenPrefix))
	stored, err := store.GetRunner(project.ID, runner.ID)
	require.NoError(t, err)
	assert.False(t, stored.Active)
	assert.Empty(t, stored.Token)
	require.NotNil(t, stored.RegistrationTokenHash)
	assert.Equal(t, HashRunnerRegistrationToken(token), *stored.RegistrationTokenHash)
}
