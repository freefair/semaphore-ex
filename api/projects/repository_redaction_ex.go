package projects

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/ssh"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pkg/taskredaction"
)

func (c *RepositoryController) browseGitConfiguration(
	r *http.Request,
	repository db.Repository,
) (db.Repository, *ssh.HostConfigInstallation, task_logger.DebugLogger, error) {
	if err := c.encryptionService.DeserializeSecret(&repository.SSHKey); err != nil {
		return db.Repository{}, nil, task_logger.DebugLogger{}, fmt.Errorf("decrypt repository credential: %w", err)
	}

	hostConfigs, err := helpers.Store(r).GetHostConfigs(repository.ProjectID, db.RetrieveQueryParams{})
	if err != nil {
		return db.Repository{}, nil, task_logger.DebugLogger{}, err
	}
	for i := range hostConfigs {
		hostConfigs[i].SSHKey, err = helpers.Store(r).GetAccessKey(repository.ProjectID, hostConfigs[i].SSHKeyID)
		if err != nil {
			return db.Repository{}, nil, task_logger.DebugLogger{}, err
		}
		if err = c.encryptionService.DeserializeSecret(&hostConfigs[i].SSHKey); err != nil {
			return db.Repository{}, nil, task_logger.DebugLogger{}, fmt.Errorf("decrypt host mapping credential: %w", err)
		}
	}

	logger := task_logger.DebugLogger{
		Prefix:   fmt.Sprintf("repository_%d_browse", repository.ID),
		Redactor: repositoryBrowseRedactor(repository, hostConfigs),
	}
	installation, err := ssh.InstallHostConfigs(repository.ProjectID, hostConfigs, logger)
	if err != nil {
		return db.Repository{}, nil, task_logger.DebugLogger{}, err
	}
	return repository, installation, logger, nil
}

func repositoryBrowseRedactor(repository db.Repository, hostConfigs []db.HostConfig) taskredaction.Redactor {
	values := accessKeyRedactionValues(repository.SSHKey)
	values = append(values, repositoryURLUserinfoValues(repository.GitURL)...)
	for _, hostConfig := range hostConfigs {
		values = append(values, accessKeyRedactionValues(hostConfig.SSHKey)...)
	}
	return taskredaction.NewFromTaskSecretAndValues("", nil, values)
}

func repositoryURLUserinfoValues(rawURL string) []string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.User == nil ||
		(!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) {
		return nil
	}
	password, ok := parsed.User.Password()
	if ok && password != "" {
		return []string{password}
	}
	return []string{parsed.User.Username()}
}

func accessKeyRedactionValues(key db.AccessKey) []string {
	return []string{
		key.SshKey.PrivateKey,
		key.SshKey.Passphrase,
		key.LoginPassword.Password,
	}
}
