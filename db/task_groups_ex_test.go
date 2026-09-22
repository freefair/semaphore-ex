package db

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeTaskGroupsCanonicalizesAndSortsBindings(t *testing.T) {
	bindings, err := NormalizeTaskGroups(TaskGroupBindings{9, 2, 17})

	require.NoError(t, err)
	assert.Equal(t, TaskGroupBindings{2, 9, 17}, bindings)
}

func TestNormalizeTaskGroupsRejectsInvalidAndDuplicateBindings(t *testing.T) {
	for _, bindings := range []TaskGroupBindings{
		{0},
		{-1},
		{3, 3},
		{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17},
	} {
		_, err := NormalizeTaskGroups(bindings)
		assert.Error(t, err)
	}
}

func TestTaskGroupKeysUseImmutableManagedIdentifiers(t *testing.T) {
	keys := TaskGroupKeys([]TaskGroup{{ID: 73}, {ID: 12}})
	assert.Equal(t, StringArrayField{"group/73", "group/12"}, keys)
}

func TestTaskGroupBindingsValueAndScanRejectInvalidPersistedState(t *testing.T) {
	bindings := TaskGroupBindings{12, 4}
	value, err := bindings.Value()
	require.NoError(t, err)

	var restored TaskGroupBindings
	require.NoError(t, restored.Scan(value))
	assert.Equal(t, TaskGroupBindings{12, 4}, restored)
	assert.Error(t, restored.Scan(`{"group":12}`))
}
