package tasks

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/require"
)

type groupPlacementStore struct {
	db.Store
	runners []db.Runner
}

func TestTaskGroupPreflightKeepsOtherRunnerRestrictions(t *testing.T) {
	previous := util.Config
	t.Cleanup(func() { util.Config = previous })
	util.Config = &util.ConfigType{Runners: &util.RunnersConfig{}}
	now := time.Now()
	old := now.Add(-24 * time.Hour)
	linux := "linux"
	windows := "windows"
	for _, test := range []struct {
		name         string
		groups       db.TaskGroupBindings
		tags         []string
		inventoryTag *string
		active       bool
		touched      *time.Time
		selected     bool
	}{
		{"matching template tag", db.TaskGroupBindings{7}, []string{linux}, nil, true, &now, true},
		{"conflicting template tag", db.TaskGroupBindings{7}, []string{windows}, nil, true, &now, false},
		{"matching inventory tag", db.TaskGroupBindings{7}, nil, &linux, true, &now, true},
		{"conflicting inventory tag", db.TaskGroupBindings{7}, nil, &windows, true, &now, false},
		{"different group runner", db.TaskGroupBindings{8}, nil, nil, true, &now, false},
		{"inactive", db.TaskGroupBindings{7}, nil, nil, false, &now, false},
		{"offline", db.TaskGroupBindings{7}, nil, nil, true, &old, false},
		{"ordinary default required", nil, nil, nil, true, &now, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := TaskPool{store: groupPlacementStore{runners: []db.Runner{{ID: 7, Name: "candidate", Active: test.active, Token: "fixture", Touched: test.touched, Tags: []string{linux}}}}, state: NewMemoryTaskStateStore()}
			placement, _, err := p.taskPreflightPlacement(db.Template{RunnerTags: test.tags}, db.Inventory{RunnerTag: test.inventoryTag}, nil, 1, now, test.groups)
			require.NoError(t, err)
			if test.selected {
				require.NotNil(t, placement.SelectedRunnerID)
				require.Equal(t, 7, *placement.SelectedRunnerID)
			} else {
				require.Nil(t, placement.SelectedRunnerID)
			}
		})
	}
}

func (s groupPlacementStore) GetRunners(int, bool, db.RunnerTagFilterMode, *string) ([]db.Runner, error) {
	return s.runners, nil
}
func (s groupPlacementStore) GetAllRunners(bool, bool, db.RunnerTagFilterMode, *string) ([]db.Runner, error) {
	return nil, nil
}
func TestTaskGroupPreflightSelectsExplicitNonDefaultRunner(t *testing.T) {
	previous := util.Config
	t.Cleanup(func() { util.Config = previous })
	util.Config = &util.ConfigType{Runners: &util.RunnersConfig{}}
	now := time.Now()
	p := TaskPool{store: groupPlacementStore{runners: []db.Runner{{ID: 7, Name: "explicit runner", Active: true, Token: "fixture", Touched: &now}}}, state: NewMemoryTaskStateStore()}
	placement, _, err := p.taskPreflightPlacement(db.Template{}, db.Inventory{}, nil, 1, now, db.TaskGroupBindings{7})
	require.NoError(t, err)
	require.NotNil(t, placement.SelectedRunnerID, "an explicit group runner must not require is_default; runtime already allows it")
	require.Equal(t, 7, *placement.SelectedRunnerID)
}
