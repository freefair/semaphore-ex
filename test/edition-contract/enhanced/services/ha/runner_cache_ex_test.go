package ha

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRunnerCache_SingleNodeRetainsStoreDefault(t *testing.T) {
	previous := util.Config
	t.Cleanup(func() { util.Config = previous })
	util.Config = nil
	assert.Nil(t, NewRunnerCache(nil))
	util.Config = &util.ConfigType{HA: &util.HAConfig{Enabled: false}}
	assert.Nil(t, NewRunnerCache(nil))
}

func TestNewRunnerCache_HAAlwaysMisses(t *testing.T) {
	previous := util.Config
	t.Cleanup(func() { util.Config = previous })
	util.Config = &util.ConfigType{HA: &util.HAConfig{Enabled: true}}
	cache := NewRunnerCache(nil)
	require.NotNil(t, cache)
	_, generation, ok := cache.Get("token")
	assert.False(t, ok)
	cache.Put("token", db.Runner{ID: 1, Token: "token"}, generation)
	_, _, ok = cache.Get("token")
	assert.False(t, ok)
	cache.Forget("token")
	cache.Invalidate()
	_, _, ok = cache.Get("token")
	assert.False(t, ok)
}
