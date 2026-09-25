//go:build !windows

package db_lib

import (
	"os"
	"syscall"
	"testing"

	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInventoryResolverUsesConfiguredProcessOwnership(t *testing.T) {
	previous := util.Config
	uid, gid := uint32(os.Getuid()), uint32(os.Getgid())
	util.Config = &util.ConfigType{Process: &util.ConfigProcess{UID: &uid, GID: &gid}}
	t.Cleanup(func() { util.Config = previous })
	path, err := installInventoryResolver(t.TempDir())
	require.NoError(t, err)
	info, err := os.Stat(path)
	require.NoError(t, err)
	stat := info.Sys().(*syscall.Stat_t)
	assert.Equal(t, uid, stat.Uid)
	assert.Equal(t, gid, stat.Gid)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
