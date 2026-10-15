package db_lib

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoGitShallowBrowsePreservesCheckoutAcrossUpdates(t *testing.T) {
	setupGitClientTest(t)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "commit.gpgsign")
	t.Setenv("GIT_CONFIG_VALUE_0", "false")
	upstream := t.TempDir()
	gitInit(t, upstream)
	gitCommitFile(t, upstream, "second", "second")

	repo := newTestGitRepo(t, "file://"+upstream, "main")
	repo.TmpDirName = "go-git-browse"
	repo.ShallowClone = true
	repo.Client = GoGitClient{keyInstaller: nopKeyInstaller{}}
	require.NoError(t, repo.CloneOrPull())
	assert.Equal(t, "1", gitOut(t, repo.GetFullPath(), "rev-list", "--count", "HEAD"))
	marker := filepath.Join(repo.GetFullPath(), ".git", "checkout-retained")
	require.NoError(t, os.WriteFile(marker, []byte("retained"), 0o600))

	t.Run("unchanged branch", func(t *testing.T) {
		require.NoError(t, repo.CloneOrPull())
		assert.FileExists(t, marker, "an unchanged browse checkout must not be cloned again")
	})
	t.Run("advancing branch", func(t *testing.T) {
		latest := gitCommitFile(t, upstream, "third", "third")
		require.NoError(t, repo.CloneOrPull())
		assert.FileExists(t, marker, "a fast-forward browse update must retain its checkout")
		assert.Equal(t, latest, gitOut(t, repo.GetFullPath(), "rev-parse", "HEAD"))
		assert.FileExists(t, filepath.Join(repo.GetFullPath(), ".git", "shallow"))
	})
}
