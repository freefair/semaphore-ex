package db

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskGlobalCredentialBindingsAreCanonicalAndPrivate(t *testing.T) {
	task := Task{GlobalCredentialBindings: map[string]int{"z_token": 9, "a_token": 4}}
	require.NoError(t, task.PreInsert(nil))
	assert.JSONEq(t, `{"a_token":4,"z_token":9}`, task.GlobalCredentialBindingsJSON)

	loaded := Task{GlobalCredentialBindingsJSON: task.GlobalCredentialBindingsJSON}
	require.NoError(t, loaded.DecodeGlobalCredentialBindings())
	assert.Equal(t, task.GlobalCredentialBindings, loaded.GlobalCredentialBindings)

	payload, err := json.Marshal(task)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "global_credential")
}

func TestTaskGlobalCredentialBindingsRejectInvalidShape(t *testing.T) {
	for _, bindings := range []map[string]int{
		{"bad name": 1},
		{"token": 0},
	} {
		task := Task{GlobalCredentialBindings: bindings}
		require.Error(t, task.PreInsert(nil))
	}

	loaded := Task{GlobalCredentialBindingsJSON: `{"token":1,"extra":2} trailing`}
	require.Error(t, loaded.DecodeGlobalCredentialBindings())
}
