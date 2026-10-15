package tasks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type monorepoCommitLogger struct {
	task_logger.NopLogger
	hash    string
	message string
}

func (l *monorepoCommitLogger) SetCommit(hash, message string) { l.hash, l.message = hash, message }

func TestCheckoutMonorepoIgnoresRequestedCommitAndReportsBranchHead(t *testing.T) {
	setupExecutorConfig(t)
	util.Config.TmpPath = t.TempDir()
	util.Config.GitClientId = "cmd_git"
	for _, shared := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "shared"}[shared], func(t *testing.T) {
			repository := db.Repository{ID: 1, ProjectID: 1, GitURL: "https://example.test/repo.git", GitBranch: "main", Monorepo: shared}
			checkout := repository.GetFullPath(7)
			require.NoError(t, os.MkdirAll(checkout, 0700))
			git := func(args ...string) string {
				cmd := exec.Command("git", append([]string{"-c", "commit.gpgsign=false", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test"}, args...)...)
				cmd.Dir = checkout
				output, err := cmd.CombinedOutput()
				require.NoError(t, err, "%s", output)
				return strings.TrimSpace(string(output))
			}
			git("init", "-b", "main")
			require.NoError(t, os.WriteFile(filepath.Join(checkout, "task.sh"), []byte("echo old\n"), 0600))
			git("add", "task.sh")
			git("commit", "-m", "old revision")
			old := git("rev-parse", "HEAD")
			require.NoError(t, os.WriteFile(filepath.Join(checkout, "task.sh"), []byte("echo current\n"), 0600))
			git("commit", "-am", "current revision")
			current := git("rev-parse", "HEAD")
			logger := &monorepoCommitLogger{}
			executor := LocalExecutor{Template: db.Template{ID: 7}, Repository: repository, Task: db.Task{CommitHash: &old}, Logger: logger, KeyInstaller: &KeyInstallerMock{}}
			require.NoError(t, executor.checkoutRepository())
			if shared {
				assert.Equal(t, current, git("rev-parse", "HEAD"))
				require.NotNil(t, executor.Task.CommitHash)
				assert.Equal(t, current, *executor.Task.CommitHash)
				assert.Equal(t, current, logger.hash)
				assert.Contains(t, logger.message, "current revision")
			} else {
				assert.Equal(t, old, git("rev-parse", "HEAD"))
			}
		})
	}
}
