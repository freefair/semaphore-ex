package pro_interfaces

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecretReferenceValidationAndRoundTrip(t *testing.T) {
	reference := SecretReference{StorageID: 7, Mount: "secret", Path: "apps/api", Version: 3, Field: "token"}
	encoded, err := reference.Encode()
	require.NoError(t, err)
	decoded, err := DecodeSecretReference(encoded)
	require.NoError(t, err)
	assert.Equal(t, reference, decoded)
	assert.Len(t, reference.Fingerprint(), 64)
}

func TestSecretReferenceRejectsUnsafeOrIncompleteMetadata(t *testing.T) {
	tests := []SecretReference{
		{StorageID: 0, Mount: "secret", Path: "apps/api", Field: "token"},
		{StorageID: 1, Mount: "secret/data", Path: "apps/api", Field: "token"},
		{StorageID: 1, Mount: "secret", Path: "../api", Field: "token"},
		{StorageID: 1, Mount: "secret", Path: "apps/api", Version: -1, Field: "token"},
		{StorageID: 1, Mount: "secret", Path: "apps/api", Field: ""},
	}
	for _, reference := range tests {
		assert.Error(t, reference.Validate())
	}
	_, err := DecodeSecretReference(`{"storage_id":1,"mount":"secret","path":"a","field":"b","plaintext":"forbidden"}`)
	assert.EqualError(t, err, "secret reference is invalid")
}

func TestSecretProviderErrorNeverIncludesProtectedProviderDetails(t *testing.T) {
	err := SecretProviderError{Category: SecretProviderErrorAuthentication, Operation: "authenticate"}
	assert.NotContains(t, err.Error(), "token")
	assert.False(t, strings.Contains(err.Error(), "http"))
}
