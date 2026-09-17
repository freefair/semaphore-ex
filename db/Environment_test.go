package db

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_EnvironmentValidate_EmptyName_ReturnsError(t *testing.T) {
	env := &Environment{
		Name: "",
		JSON: "{}",
		ENV:  nil,
	}
	err := env.Validate()
	assert.Error(t, err)
	assert.Equal(t, "Environment name can not be empty", err.Error())
}

func TestEnvironmentUnmarshalTracksSecretStorageFieldPresence(t *testing.T) {
	var omitted Environment
	assert.NoError(t, json.Unmarshal([]byte(`{"name":"environment"}`), &omitted))
	assert.False(t, omitted.SecretStorageIDSet)
	assert.False(t, omitted.SecretStorageKeyPrefixSet)

	var cleared Environment
	assert.NoError(t, json.Unmarshal([]byte(`{"secret_storage_id":null,"secret_storage_key_prefix":null}`), &cleared))
	assert.True(t, cleared.SecretStorageIDSet)
	assert.True(t, cleared.SecretStorageKeyPrefixSet)
	assert.Nil(t, cleared.SecretStorageID)
	assert.Nil(t, cleared.SecretStorageKeyPrefix)
}

func Test_EnvironmentValidate_InvalidJSON_ReturnsError(t *testing.T) {
	env := &Environment{
		Name: "TestEnv",
		JSON: "{invalid_json}",
		ENV:  nil,
	}
	err := env.Validate()
	assert.Error(t, err)
	assert.Equal(t, "Extra variables must be valid JSON", err.Error())
}

func Test_EnvironmentValidate_ValidJSON_ReturnsNoError(t *testing.T) {
	env := &Environment{
		Name: "TestEnv",
		JSON: `{"key": "value"}`,
		ENV:  nil,
	}
	err := env.Validate()
	assert.NoError(t, err)
}

func Test_EnvironmentValidate_InvalidEnvJSON_ReturnsError(t *testing.T) {
	envVar := "{invalid_json}"
	env := &Environment{
		Name: "TestEnv",
		JSON: `{"key": "value"}`,
		ENV:  &envVar,
	}
	err := env.Validate()
	assert.Error(t, err)
	assert.Equal(t, "Environment variables must be valid JSON", err.Error())
}

func Test_EnvironmentValidate_EmptyJsonName_ReturnsError(t *testing.T) {
	env := &Environment{
		Name: "TestEnv",
		JSON: `{"": "value"}`,
		ENV:  nil,
	}
	err := env.Validate()
	assert.Error(t, err)
	assert.Equal(t, "Extra variables key can not be empty", err.Error())
}

func Test_EnvironmentValidate_NonScalarEnvValues_ReturnsError(t *testing.T) {
	envVar := `{"key": {"nested": "value"}}`
	env := &Environment{
		Name: "TestEnv",
		JSON: `{"key": "value"}`,
		ENV:  &envVar,
	}
	err := env.Validate()
	assert.Error(t, err)
	assert.Equal(t, "Environment variables values must be scalar", err.Error())
}

func Test_EnvironmentValidate_ValidEnvJSON_ReturnsNoError(t *testing.T) {
	envVar := `{"key": "value"}`
	env := &Environment{
		Name: "TestEnv",
		JSON: `{"key": "value"}`,
		ENV:  &envVar,
	}
	err := env.Validate()
	assert.NoError(t, err)
}
