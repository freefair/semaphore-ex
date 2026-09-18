package runners

import (
	"encoding/json"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestJobDataCarriesTaskSecretOutsideTheTaskDTO(t *testing.T) {
	payload, err := json.Marshal(JobData{Task: db.Task{ID: 42}, TaskSecret: `{"token":"runner-only"}`})
	require.NoError(t, err)
	assert.Contains(t, string(payload), `"task_secret":"{\"token\":\"runner-only\"}"`)
	assert.NotContains(t, string(payload), `"secret":`)

	var decoded JobData
	require.NoError(t, json.Unmarshal(payload, &decoded))
	assert.Equal(t, `{"token":"runner-only"}`, decoded.TaskSecret)
	assert.Empty(t, decoded.Task.Secret)
}

func TestRunnerPayloadNeverSerializesResolvedTaskSSHKeyMaterial(t *testing.T) {
	private := "private-key-material"
	payload, err := json.Marshal(JobData{Task: db.Task{
		ResolvedSSHKeys: []db.ResolvedTaskSSHKey{{
			Binding: db.SSHKeyBinding{AccessKeyID: 7, Hosts: []string{"git.example.test"}},
			Key:     db.AccessKey{ID: 7, Plain: &private},
		}},
	}})
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "private-key-material")
	assert.NotContains(t, string(payload), "resolved_ssh_keys")
}

func TestValidateExecutorImageCompatibility(t *testing.T) {
	image := "registry.example.com/team/job:v1"
	for _, executorType := range []util.ExecutorType{util.ExecutorTypeDocker, util.ExecutorTypeKubernetes} {
		assert.NoError(t, validateExecutorImageCompatibility(&image, executorType))
	}
	require.ErrorContains(t, validateExecutorImageCompatibility(&image, util.ExecutorTypeLocal), "Docker or Kubernetes")
	assert.NoError(t, validateExecutorImageCompatibility(nil, util.ExecutorTypeLocal))
}
