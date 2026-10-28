package runners

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
)

func TestCollectInfisicalPAMAccessKeysStagesCredentials(t *testing.T) {
	keys := map[int]db.AccessKey{1: {ID: 1}}
	configs := []db.TaskInfisicalPAM{
		{CredentialKeyID: 7, Credential: db.AccessKey{ID: 7, Type: db.AccessKeyString, String: "a"}},
		{CredentialKeyID: 8, Credential: db.AccessKey{ID: 8, Type: db.AccessKeyString, String: "b"}},
	}

	collectInfisicalPAMAccessKeys(configs, keys)

	assert.Len(t, keys, 3)
	assert.Equal(t, "a", keys[7].String)
	assert.Equal(t, "b", keys[8].String)
}
