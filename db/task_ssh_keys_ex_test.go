package db

import (
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestSSHKeyBindingsNilAndEmptyPersistence(t *testing.T) {
	var nilBindings SSHKeyBindings
	value, err := nilBindings.Value()
	require.NoError(t, err)
	assert.Nil(t, value)
	empty := SSHKeyBindings{}
	value, err = empty.Value()
	require.NoError(t, err)
	assert.Equal(t, "[]", value)
	var decoded SSHKeyBindings
	require.NoError(t, decoded.Scan("[]"))
	assert.NotNil(t, decoded)
	assert.Empty(t, decoded)
}
func TestResolveTaskSSHKeysInheritanceAndConflicts(t *testing.T) {
	defaults := SSHKeyBindings{{1, []string{"git.example.com"}}}
	always := SSHKeyBindings{{2, []string{"10.0.0.1"}}}
	resolved, err := ResolveTaskSSHKeys(defaults, always, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, SSHKeyBindings{{1, []string{"git.example.com"}}, {2, []string{"10.0.0.1"}}}, resolved)
	_, err = ResolveTaskSSHKeys(defaults, SSHKeyBindings{{2, []string{"git.example.com"}}}, nil, nil)
	assert.Error(t, err)
}
func TestSSHKeyBindingsValidation(t *testing.T) {
	assert.NoError(t, ValidateSSHKeyBindings(SSHKeyBindings{{AccessKeyID: 1}}))
	assert.NoError(t, ValidateSSHKeyBindings(SSHKeyBindings{{AccessKeyID: 1, Hosts: []string{}}}))
	for _, host := range []string{"Git.example.com", "*.example.com", "git.example.com:22", "user@git.example.com", "git/example"} {
		t.Run(host, func(t *testing.T) { assert.Error(t, ValidateSSHKeyBindings(SSHKeyBindings{{1, []string{host}}})) })
	}
	raw, _ := json.Marshal(SSHKeyBindings{{1, []string{"git.example.com"}}})
	var bindings SSHKeyBindings
	require.NoError(t, bindings.Scan(raw))
	assert.Equal(t, SSHKeyBindings{{1, []string{"git.example.com"}}}, bindings)
}

func TestSSHKeyBindingsPreserveOptionalHostLists(t *testing.T) {
	for _, bindings := range []SSHKeyBindings{
		{{AccessKeyID: 1}},
		{{AccessKeyID: 1, Hosts: []string{}}},
	} {
		value, err := bindings.Value()
		require.NoError(t, err)
		var restored SSHKeyBindings
		require.NoError(t, restored.Scan(value))
		require.Len(t, restored, 1)
		assert.Empty(t, restored[0].Hosts)
	}
}

func TestSSHKeyBindingsAcceptIPv6AndRejectOversizedEncodedValues(t *testing.T) {
	bindings := SSHKeyBindings{{AccessKeyID: 1, Hosts: []string{"2001:db8::1"}}}
	assert.NoError(t, ValidateSSHKeyBindings(bindings))

	oversized := make(SSHKeyBindings, 16)
	for bindingIndex := range oversized {
		hosts := make([]string, maxSSHKeyHosts)
		for hostIndex := range hosts {
			hosts[hostIndex] = fmt.Sprintf("h%02d-%02d.%s.%s.example", bindingIndex, hostIndex, strings.Repeat("a", 50), strings.Repeat("b", 20))
		}
		oversized[bindingIndex] = SSHKeyBinding{AccessKeyID: bindingIndex + 1, Hosts: hosts}
	}
	require.NoError(t, ValidateSSHKeyBindings(oversized))
	_, err := oversized.Value()
	assert.Error(t, err)
	raw, err := json.Marshal(oversized)
	require.NoError(t, err)
	var decoded SSHKeyBindings
	assert.Error(t, decoded.Scan(raw))
}

func TestResolveTaskSSHKeysPrecedenceAndInputIsolation(t *testing.T) {
	defaults := SSHKeyBindings{{AccessKeyID: 1, Hosts: []string{"default.example.test"}}}
	always := SSHKeyBindings{{AccessKeyID: 2, Hosts: []string{"always.example.test"}}}
	template := SSHKeyBindings{{AccessKeyID: 3, Hosts: []string{"template.example.test"}}}
	task := SSHKeyBindings{{AccessKeyID: 4, Hosts: []string{"task.example.test"}}}

	tests := []struct {
		name     string
		template SSHKeyBindings
		task     SSHKeyBindings
		expected SSHKeyBindings
	}{
		{"project default plus always", nil, nil, SSHKeyBindings{{1, []string{"default.example.test"}}, {2, []string{"always.example.test"}}}},
		{"template override plus always", template, nil, SSHKeyBindings{{2, []string{"always.example.test"}}, {3, []string{"template.example.test"}}}},
		{"task override plus always", template, task, SSHKeyBindings{{2, []string{"always.example.test"}}, {4, []string{"task.example.test"}}}},
		{"empty template clears default", SSHKeyBindings{}, nil, SSHKeyBindings{{2, []string{"always.example.test"}}}},
		{"empty task clears template", template, SSHKeyBindings{}, SSHKeyBindings{{2, []string{"always.example.test"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := ResolveTaskSSHKeys(defaults, always, tt.template, tt.task)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, resolved)
		})
	}
	_, err := ResolveTaskSSHKeys(
		SSHKeyBindings{{AccessKeyID: 1, Hosts: []string{"2001:0db8:0:0:0:0:0:1"}}},
		SSHKeyBindings{{AccessKeyID: 2, Hosts: []string{"2001:db8::1"}}}, nil, nil,
	)
	assert.Error(t, err)

	_, err = ResolveTaskSSHKeys(defaults, always, template, task)
	require.NoError(t, err)
	assert.Equal(t, []string{"default.example.test"}, defaults[0].Hosts)
	assert.Equal(t, []string{"always.example.test"}, always[0].Hosts)
	assert.Equal(t, []string{"template.example.test"}, template[0].Hosts)
	assert.Equal(t, []string{"task.example.test"}, task[0].Hosts)
}

func TestTemplateVersionSnapshotPreservesEmptySSHKeyOverride(t *testing.T) {
	snapshot, err := NewTemplateVersionSnapshot(Template{RepositoryID: 1, SSHKeys: SSHKeyBindings{}})
	require.NoError(t, err)
	reconstructed, err := snapshot.ReconstructTemplate(1, 1)
	require.NoError(t, err)
	assert.NotNil(t, reconstructed.SSHKeys)
	assert.Empty(t, reconstructed.SSHKeys)
}
