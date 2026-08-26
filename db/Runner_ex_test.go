package db

import (
	"encoding/json"
	"github.com/stretchr/testify/assert"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRunnerRegistrationMaterialIsExcludedFromSerializationAndBackups(t *testing.T) {
	registrationHash := strings.Repeat("a", 64)
	expiresAt := time.Now().Add(time.Hour)
	runner := Runner{
		Token:                      "runner-auth-material",
		RegistrationTokenHash:      &registrationHash,
		RegistrationTokenExpiresAt: &expiresAt,
	}

	serialized, err := json.Marshal(runner)
	assert.NoError(t, err)
	assert.NotContains(t, string(serialized), runner.Token)
	assert.NotContains(t, string(serialized), registrationHash)

	runnerType := reflect.TypeOf(Runner{})
	for _, fieldName := range []string{"Token", "RegistrationTokenHash", "RegistrationTokenExpiresAt"} {
		field, found := runnerType.FieldByName(fieldName)
		assert.True(t, found)
		assert.Equal(t, "-", field.Tag.Get("backup"), fieldName)
	}
}
