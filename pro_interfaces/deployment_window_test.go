package pro_interfaces

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeploymentWindowOverrideInputRejectsPrivilegedTransportFields(t *testing.T) {
	for _, payload := range []string{
		`{"category":"incident","reference":"INC-41","actor_id":99}`,
		`{"category":"incident","reference":"INC-41","authenticated":true}`,
		`{"category":"incident","reference":"INC-41","authorized":true}`,
	} {
		var input DeploymentWindowOverrideInput
		err := json.Unmarshal([]byte(payload), &input)
		assert.ErrorIs(t, err, ErrDeploymentWindowOverrideInvalid, payload)
	}
}

func TestNewManualDeploymentWindowOverrideDerivesTrustedFields(t *testing.T) {
	override, err := NewManualDeploymentWindowOverride(&DeploymentWindowOverrideInput{
		Category: DeploymentWindowOverrideIncident, Reference: "INC-41",
	}, 12)
	require.NoError(t, err)
	require.NotNil(t, override)
	assert.Equal(t, 12, override.ActorID)
	assert.True(t, override.Authenticated)
	assert.True(t, override.Authorized)
	assert.Equal(t, DeploymentWindowOverrideIncident, override.Category)
	assert.Equal(t, "INC-41", override.Reference)
}
