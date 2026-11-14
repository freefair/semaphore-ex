package db_lib

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type logLineRecorder struct {
	task_logger.NopLogger
	lines []string
}

func (l *logLineRecorder) Log(msg string) {
	l.lines = append(l.lines, msg)
}

// A repository without any requirements.yml used to log one notice per
// candidate path (eight lines) before every run; it now logs one summary.
func TestInstallRequirementsLogsOneLineWithoutGalaxyRequirements(t *testing.T) {
	previousConfig := util.Config
	util.Config = &util.ConfigType{}
	t.Cleanup(func() { util.Config = previousConfig })

	repositoryDir := t.TempDir()
	logger := &logLineRecorder{}
	app := &AnsibleApp{
		Logger:     logger,
		Playbook:   &AnsiblePlaybook{Logger: logger},
		Template:   db.Template{ID: 1, Playbook: "site.yml"},
		Repository: db.Repository{GitURL: repositoryDir},
	}

	err := app.InstallRequirements(LocalAppInstallingArgs{})

	require.NoError(t, err)
	assert.Equal(t, []string{"No Galaxy requirements found; skipping galaxy install.\n"}, logger.lines)
}
