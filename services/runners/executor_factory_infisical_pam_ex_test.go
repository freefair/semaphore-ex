package runners

import (
	"encoding/json"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runnerInfisicalPAM() db.TaskInfisicalPAM {
	return db.TaskInfisicalPAM{
		StorageID: 3, SiteURL: "https://infisical.example.com", ClientID: "client-1",
		SessionDuration: "1h0m0s", CredentialKeyID: 12,
		Routes: []db.InfisicalPAMRoute{{Host: "web-1", Account: "servers/web-1"}},
	}
}

func TestHydrateJobInfisicalPAMAttachesCredential(t *testing.T) {
	jobData := &JobData{InfisicalPAM: []db.TaskInfisicalPAM{runnerInfisicalPAM()}}
	keys := map[int]db.AccessKey{12: {ID: 12, Type: db.AccessKeyString, String: "machine-secret"}}

	require.NoError(t, hydrateJobAccessKeys(jobData, keys))

	require.Len(t, jobData.Task.InfisicalPAM, 1)
	assert.Equal(t, "machine-secret", jobData.Task.InfisicalPAM[0].Credential.String)
	assert.Equal(t, []db.InfisicalPAMRoute{{Host: "web-1", Account: "servers/web-1"}}, jobData.Task.InfisicalPAM[0].Routes)
}

func TestHydrateJobInfisicalPAMRejectsMissingOrForeignKey(t *testing.T) {
	tests := []struct {
		name string
		keys map[int]db.AccessKey
	}{
		{"missing", map[int]db.AccessKey{}},
		{"wrong id", map[int]db.AccessKey{12: {ID: 13, Type: db.AccessKeyString, String: "x"}}},
		{"wrong type", map[int]db.AccessKey{12: {ID: 12, Type: db.AccessKeySSH}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobData := &JobData{InfisicalPAM: []db.TaskInfisicalPAM{runnerInfisicalPAM()}}
			assert.ErrorContains(t, hydrateJobAccessKeys(jobData, tt.keys), "secret is unavailable")
		})
	}
}

func TestHydrateJobInfisicalPAMRejectsInvalidRoute(t *testing.T) {
	config := runnerInfisicalPAM()
	config.Routes[0].Host = "web\n  ProxyCommand sh"
	jobData := &JobData{InfisicalPAM: []db.TaskInfisicalPAM{config}}
	keys := map[int]db.AccessKey{12: {ID: 12, Type: db.AccessKeyString, String: "x"}}

	assert.ErrorContains(t, hydrateJobAccessKeys(jobData, keys), "is invalid")
}

func TestJobDataInfisicalPAMCarriesNoSecret(t *testing.T) {
	config := runnerInfisicalPAM()
	config.Credential = db.AccessKey{ID: 12, Type: db.AccessKeyString, String: "machine-secret"}
	jobData := JobData{Task: db.Task{InfisicalPAM: []db.TaskInfisicalPAM{config}}, InfisicalPAM: []db.TaskInfisicalPAM{config}}

	encoded, err := json.Marshal(jobData)

	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "machine-secret")
	assert.Contains(t, string(encoded), `"credential_key_id":12`)
}
