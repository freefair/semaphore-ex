package db

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrationRegistriesAreIndependent(t *testing.T) {
	for _, m := range GetMigrations(util.DbDriverSQLite) {
		assert.False(t, strings.Contains(m.Version, "-ex"))
		assert.NotEqual(t, "2.20.56", m.Version)
	}
	ex := GetEXMigrations()
	require.NotEmpty(t, ex)
	assert.Equal(t, "2.20.5-ex1.5", ex[len(ex)-1].Version)
	for i, m := range ex {
		assert.True(t, strings.Contains(m.Version, "-ex"))
		_, err := m.ParseVersion()
		require.NoError(t, err)
		if i > 0 {
			assert.Less(t, ex[i-1].Compare(m), 0)
		}
	}
	assert.Equal(t, "upstream/2.20.7;ex/2.20.5-ex1.5", CurrentSchemaVersion(util.DbDriverSQLite))
}

type migrationRecordingStore struct {
	Store
	applied map[string]bool
	events  []string
	fail    string
}

func (s *migrationRecordingStore) GetDialect() string { return util.DbDriverSQLite }
func (s *migrationRecordingStore) IsMigrationApplied(m Migration) (bool, error) {
	return s.applied[m.Version], nil
}
func (s *migrationRecordingStore) ApplyMigration(m Migration) error {
	s.events = append(s.events, "apply "+m.Version)
	if m.Version == s.fail {
		return errors.New("migration failed")
	}
	s.applied[m.Version] = true
	return nil
}
func (s *migrationRecordingStore) TryRollbackMigration(m Migration) error {
	s.events = append(s.events, "undo "+m.Version)
	delete(s.applied, m.Version)
	return nil
}

func TestMigrationSequencesFollowUpstreamAnchorsAndRollbackInReverse(t *testing.T) {
	s := &migrationRecordingStore{applied: map[string]bool{}}
	require.NoError(t, Migrate(s, nil))
	before := slices.Index(s.events, "apply 2.20.1")
	assert.Equal(t, "apply 2.20.1-ex1.1", s.events[before+1])
	assert.Equal(t, "apply 2.20.2", s.events[before+65])
	assert.Equal(t, "apply 2.20.2-ex1.1", s.events[before+66])
	assert.Equal(t, "apply 2.20.3", s.events[before+67])
	applied := slices.Clone(s.events)
	s.events = nil
	require.NoError(t, Migrate(s, nil))
	assert.Empty(t, s.events)
	require.NoError(t, Rollback(s, "2.20.1"))
	expected := applied[before+1:]
	slices.Reverse(expected)
	for i, step := range expected {
		assert.Equal(t, strings.Replace(step, "apply ", "undo ", 1), s.events[i])
	}
	assert.True(t, s.applied["2.20.1"])
}

func TestMigrationFailureStopsDependentStepsWithoutAutomaticUndo(t *testing.T) {
	s := &migrationRecordingStore{applied: map[string]bool{}, fail: "2.20.2"}
	require.ErrorContains(t, Migrate(s, nil), "migration failed")
	assert.False(t, s.applied["2.20.2-ex1.1"])
	assert.False(t, s.applied["2.20.3"])
	for _, event := range s.events {
		assert.NotContains(t, event, "undo ")
	}
}

func TestMigrationPlanUsesNumericEXSuffixesAndRejectsMissingAnchors(t *testing.T) {
	upstream := []Migration{{Version: "2.20.2"}, {Version: "2.20.3"}}
	ex := []Migration{{Version: "2.20.2-ex2.2.10"}, {Version: "2.20.2-ex2.2.1"}, {Version: "2.20.2-ex2.2.2"}}
	plan, err := orderedMigrations(upstream, ex)
	require.NoError(t, err)
	assert.Equal(t, []Migration{{Version: "2.20.2"}, {Version: "2.20.2-ex2.2.1"}, {Version: "2.20.2-ex2.2.2"}, {Version: "2.20.2-ex2.2.10"}, {Version: "2.20.3"}}, plan)
	_, err = orderedMigrations(upstream, []Migration{{Version: "2.20.4-ex1"}})
	require.ErrorContains(t, err, "no registered upstream anchor")
	_, err = orderedMigrations(upstream, []Migration{ex[0], ex[0]})
	require.ErrorContains(t, err, "duplicate EX migration")
}

func TestMigrationTargetsRespectDependencyBoundaries(t *testing.T) {
	s := &migrationRecordingStore{applied: map[string]bool{}}
	target := "2.20.2"
	require.NoError(t, Migrate(s, &target))
	assert.True(t, s.applied["2.20.1-ex1.64"])
	assert.True(t, s.applied["2.20.2"])
	assert.False(t, s.applied["2.20.2-ex1.1"])
	target = "2.20.2-ex1.1"
	require.NoError(t, Migrate(s, &target))
	assert.True(t, s.applied[target])
	assert.False(t, s.applied["2.20.3"])
	require.NoError(t, Rollback(s, "2.20.2"))
	assert.False(t, s.applied[target])
	assert.True(t, s.applied["2.20.2"])
	for _, bad := range []string{"2.20.2-ex", "2.20.2-ex-1", "2.20.2-ex01", "2.20.2-ex1/../2", "2.20.2-ex1.sql", "2.20.2-ex999"} {
		require.Error(t, Migrate(s, &bad))
		require.Error(t, Rollback(s, bad))
	}
}
