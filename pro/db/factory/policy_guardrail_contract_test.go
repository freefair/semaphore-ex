package factory

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCommunityPolicyGuardrailStoreIsUnavailable(t *testing.T) {
	assert.Nil(t, NewPolicyGuardrailStore(nil))
	assert.Nil(t, NewWorkflowFileArtifactStore(nil))
}
