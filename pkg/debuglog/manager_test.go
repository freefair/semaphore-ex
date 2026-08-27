package debuglog

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagerUsesExactAndTerminalPrefixMatching(t *testing.T) {
	manager := NewManager("node-a", "runner,task_*,-task_secret", time.Now())

	assert.True(t, manager.Enabled("runner"))
	assert.False(t, manager.Enabled("runner_child"))
	assert.True(t, manager.Enabled("task_pool"))
	assert.False(t, manager.Enabled("task_secret"))
}

func TestManagerRejectsInvalidEntriesWithoutBroadening(t *testing.T) {
	manager := NewManager("node-a", "runner*middle,unknown[", time.Now())
	diagnostics := manager.Diagnostics()

	assert.Empty(t, diagnostics.Effective)
	require.Len(t, diagnostics.Rejected, 2)
	assert.False(t, manager.Enabled("runner"))
}

func TestManagerEmptyConfigurationDefaultsToAll(t *testing.T) {
	manager := NewManager("node-a", "", time.Now())

	assert.True(t, manager.Enabled("runner"))
	assert.Equal(t, DebugFilterDefaultAll, manager.Diagnostics().Default)
	assert.Equal(t, []string{"*"}, manager.Diagnostics().Effective)
}

func TestManagersReloadIndependently(t *testing.T) {
	first := NewManager("node-a", "runner", time.Now())
	second := NewManager("node-b", "db", time.Now())

	first.Reload("git", time.Now())

	assert.True(t, first.Enabled("git"))
	assert.False(t, first.Enabled("runner"))
	assert.True(t, second.Enabled("db"))
	assert.False(t, second.Enabled("git"))
}

func TestManagerConcurrentReadsAndReloads(t *testing.T) {
	manager := NewManager("node-a", "runner", time.Now())
	var reads atomic.Uint64
	var wait sync.WaitGroup
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for j := 0; j < 1000; j++ {
				_ = manager.Enabled("runner")
				_ = manager.Diagnostics()
				reads.Add(1)
			}
		}()
	}
	for i := 0; i < 100; i++ {
		manager.Reload("runner,task_*", time.Now())
	}
	wait.Wait()
	assert.Equal(t, uint64(8000), reads.Load())
}

func TestManagerReloadErrorRetainsLastKnownGoodFilter(t *testing.T) {
	manager := NewManager("node-a", "runner", time.Now())
	diagnostics := manager.RecordReloadError("cannot read\nconfiguration", time.Now())

	assert.True(t, manager.Enabled("runner"))
	assert.False(t, manager.Enabled("db"))
	assert.Equal(t, "cannot read configuration", diagnostics.ReloadError)
}

func TestManagerInitialLoadErrorFailsClosed(t *testing.T) {
	manager := NewManagerWithReloadError("node-a", "cannot read configuration", time.Now())

	assert.False(t, manager.Enabled("runner"))
	assert.Empty(t, manager.Diagnostics().Effective)
	assert.Equal(t, DebugFilterDefaultConfigured, manager.Diagnostics().Default)
	assert.Equal(t, "cannot read configuration", manager.Diagnostics().ReloadError)
}

func TestZeroValueManagerFailsClosed(t *testing.T) {
	manager := &Manager{instance: "node-a"}

	assert.False(t, manager.Enabled("runner"))
	assert.Equal(t, DebugFilterDefaultConfigured, manager.Diagnostics().Default)
	assert.NotNil(t, manager.Diagnostics().Configured)
	assert.NotNil(t, manager.Diagnostics().Effective)
	assert.NotNil(t, manager.Diagnostics().Rejected)
}
