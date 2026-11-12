//go:build !windows

package db_lib

import (
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInventoryResolverUsesServiceProcessOwnership(t *testing.T) {
	path, err := installInventoryResolver(t.TempDir())
	require.NoError(t, err)
	info, err := os.Stat(path)
	require.NoError(t, err)
	stat := info.Sys().(*syscall.Stat_t)
	assert.Equal(t, uint32(os.Geteuid()), stat.Uid)
	assert.Equal(t, uint32(os.Getegid()), stat.Gid)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
