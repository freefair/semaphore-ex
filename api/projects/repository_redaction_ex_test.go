package projects

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type repositoryBrowseDecryptor struct {
	repositoryKeyID int
	hostKeyID       int
	calls           []int
}

func (d *repositoryBrowseDecryptor) DeserializeSecret(key *db.AccessKey) error {
	d.calls = append(d.calls, key.ID)
	switch key.ID {
	case d.repositoryKeyID:
		key.LoginPassword.Password = "repository-password=synthetic"
	case d.hostKeyID:
		key.LoginPassword.Password = "host-password=synthetic"
	}
	return nil
}

func TestRepositoryBrowseGitConfigurationRedactsHydratedCredentials(t *testing.T) {
	f := newResourceFixture(t)
	repositoryKey, err := f.store.CreateAccessKey(db.AccessKey{ProjectID: &f.project.ID, Name: "repository", Type: db.AccessKeyLoginPassword})
	require.NoError(t, err)
	hostKey, err := f.store.CreateAccessKey(db.AccessKey{ProjectID: &f.project.ID, Name: "host", Type: db.AccessKeyLoginPassword})
	require.NoError(t, err)
	repository, err := f.store.CreateRepository(db.Repository{
		ProjectID: f.project.ID,
		Name:      "repository",
		GitURL:    "https://url-token%3Dsynthetic@git.example/repository.git",
		GitBranch: "main",
		SSHKeyID:  repositoryKey.ID,
	})
	require.NoError(t, err)
	repository, err = f.store.GetRepository(f.project.ID, repository.ID)
	require.NoError(t, err)
	_, err = f.store.CreateHostConfig(db.HostConfig{ProjectID: f.project.ID, Type: db.HostConfigURL, Name: "https://git.example/", SSHKeyID: hostKey.ID})
	require.NoError(t, err)

	req, _ := f.request("GET", "", nil, nil)
	decryptor := &repositoryBrowseDecryptor{repositoryKeyID: repositoryKey.ID, hostKeyID: hostKey.ID}
	controller := NewRepositoryController(nil, decryptor)
	configured, installation, logger, err := controller.browseGitConfiguration(req, repository)
	require.NoError(t, err)
	defer installation.Destroy()

	assert.Equal(t, []int{repositoryKey.ID, hostKey.ID}, decryptor.calls)
	assert.Equal(t, "repository-password=synthetic", configured.SSHKey.LoginPassword.Password)
	output := logger.Redactor.Redact("repository-password%3Dsynthetic host-password%3Dsynthetic url-token%3Dsynthetic")
	assert.NotContains(t, output, "repository-password%3Dsynthetic")
	assert.NotContains(t, output, "host-password%3Dsynthetic")
	assert.NotContains(t, output, "url-token%3Dsynthetic")
	assert.Contains(t, output, "[REDACTED]")
}

func TestRepositoryURLUserinfoValues(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected []string
	}{
		{"HTTPS token-only userinfo", "https://token%3Dsynthetic@git.example/repository.git", []string{"token=synthetic"}},
		{"HTTP token-only userinfo", "http://token%3Dsynthetic@git.example/repository.git", []string{"token=synthetic"}},
		{"HTTPS token with explicit empty password", "https://token%3Dsynthetic:@git.example/repository.git", []string{"token=synthetic"}},
		{"HTTPS login and password", "https://user:password%3Dsynthetic@git.example/repository.git", []string{"password=synthetic"}},
		{"SSH userinfo remains outside the HTTP credential boundary", "git@git.example:repository.git", nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, repositoryURLUserinfoValues(test.url))
		})
	}
}
