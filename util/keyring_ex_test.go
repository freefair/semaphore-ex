package util

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestKeyset_OptionEncryptionEnabledRequiresAnActiveKey(t *testing.T) {
	Config = mustKeyset(t, nil, "", "")
	assert.False(t, Config.OptionEncryptionEnabled())

	keyA := genKey(0x01)
	Config = mustKeyset(t, keysCfg(map[string]string{"a": keyA}, "a", ""), "", "")
	assert.True(t, Config.OptionEncryptionEnabled(), "the access-key fallback protects option values")

	keyB := genKey(0x02)
	Config = mustKeyset(t, keysCfg(map[string]string{"b": keyB}, "", "b"), "", "")
	assert.True(t, Config.OptionEncryptionEnabled(), "a dedicated option key protects option values")
}
