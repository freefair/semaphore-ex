package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGlobalCredentialProviderCredentialEnvIsDeterministicAndValidatesID(t *testing.T) {
	envName, err := GlobalCredentialProviderCredentialEnv("primary-vault")
	assert.NoError(t, err)
	assert.Equal(t, "SEMAPHORE_GLOBAL_CREDENTIAL_PROVIDER_PRIMARY_VAULT_CREDENTIAL", envName)
	_, err = GlobalCredentialProviderCredentialEnv("Primary")
	assert.Error(t, err)
}
