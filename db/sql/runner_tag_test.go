package sql

import (
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunnerTagsAreNormalizedAndListedAcrossEligibleScopes(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "tag scope"})
	require.NoError(t, err)
	projectRunner, err := store.CreateRunner(db.Runner{
		ProjectID: &project.ID, Name: "project",
		Tags: []string{" GPU ", "linux", "gpu"}, Token: db.GenerateRunnerToken(), Active: true,
	})
	require.NoError(t, err)
	globalRunner, err := store.CreateRunner(db.Runner{
		Name: "global", Tags: []string{"LINUX", "arm64"},
		Token: db.GenerateRunnerToken(), Active: true,
	})
	require.NoError(t, err)

	projectRunner, err = store.GetRunner(project.ID, projectRunner.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"gpu", "linux"}, projectRunner.Tags)
	globalRunner, err = store.GetGlobalRunner(globalRunner.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"arm64", "linux"}, globalRunner.Tags)
	tags, err := store.GetRunnerTags(project.ID)
	require.NoError(t, err)
	assert.Equal(t, []db.RunnerTag{
		{Tag: "arm64", NumberOfRunners: 1},
		{Tag: "gpu", NumberOfRunners: 1},
		{Tag: "linux", NumberOfRunners: 2},
	}, tags)
}

func TestRunnerTagValidationHappensBeforeCreateOrUpdate(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "tag validation"})
	require.NoError(t, err)

	tooLong := strings.Repeat("x", db.MaxRunnerTagLength+1)
	_, err = store.CreateRunner(db.Runner{
		ProjectID: &project.ID,
		Name:      "invalid",
		Tags:      []string{tooLong},
	})
	require.ErrorContains(t, err, "runner tag must contain at most")
	runners, err := store.GetRunners(project.ID, false, db.RunnerFilterIgnoreTags, nil)
	require.NoError(t, err)
	assert.Empty(t, runners, "invalid tags must not leave a partially-created runner")

	runner, err := store.CreateRunner(db.Runner{
		ProjectID: &project.ID,
		Name:      "unchanged",
		Tags:      []string{"linux"},
	})
	require.NoError(t, err)
	runner.Name = "partially-updated"
	runner.Tags = []string{tooLong}
	require.ErrorContains(t, store.UpdateRunner(runner), "runner tag must contain at most")

	loaded, err := store.GetRunner(project.ID, runner.ID)
	require.NoError(t, err)
	assert.Equal(t, "unchanged", loaded.Name)
	assert.Equal(t, []string{"linux"}, loaded.Tags)
}
