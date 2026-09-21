package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBooleanReferenceDefaultsAndNamedEntries(t *testing.T) {
	dir := t.TempDir()
	source := "package config\n" +
		"type Switch bool\n" +
		"type ConfigType struct {\n" +
		" Enabled bool `json:\"enabled\"`\n" +
		" Tagged bool `json:\"tagged\" default:\"true\"`\n" +
		" Alias Switch `json:\"alias\"`\n" +
		" Optional *bool `json:\"optional\"`\n" +
		" Providers map[string]Provider `json:\"providers\" env:\"PROVIDERS\"`\n" +
		"}\n" +
		"type Provider struct {\n" +
		" // Active controls this named provider.\n" +
		" Active bool `json:\"active\" env:\"NOT_AN_ENTRY_VARIABLE\"`\n" +
		" Redirect bool `json:\"redirect\" default:\"true\"`\n" +
		" URL string `json:\"url\"`\n" +
		" Children map[string]*Provider `json:\"children\"`\n" +
		"}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.go"), []byte(source), 0o600))
	structs, scalars, err := parsePackage(dir)
	require.NoError(t, err)
	w := &walker{structs: structs, scalars: scalars}
	w.walk(structs["ConfigType"], "")
	opts := make(map[string]option)
	for _, opt := range w.options {
		opts[opt.Key] = opt
	}
	assert.Equal(t, "false", opts["enabled"].Default)
	assert.Equal(t, "true", opts["tagged"].Default)
	assert.Equal(t, "false", opts["alias"].Default)
	assert.Empty(t, opts["optional"].Default, "an unset pointer is not a false boolean")
	require.Contains(t, opts, "providers.<id>.active")
	assert.Equal(t, "false", opts["providers.<id>.active"].Default)
	assert.Equal(t, "true", opts["providers.<id>.redirect"].Default)
	assert.Empty(t, opts["providers.<id>.active"].Env, "map entries are supplied through the parent object")
	assert.Equal(t, "PROVIDERS", opts["providers"].Env)
	assert.Contains(t, opts, "providers.<id>.url", "the reference includes every named-map member")
	assert.NotContains(t, opts, "providers.<id>.children.<id>.active", "recursive schemas must terminate")

	page := render(w.options, overlay{})
	assert.Contains(t, page, "`providers.<id>.active`")
	assert.Contains(t, page, "| boolean | `false` |")
	assert.NotContains(t, page, "NOT_AN_ENTRY_VARIABLE")
}

func TestReferenceIncludesNamedApplicationAndProviderFlags(t *testing.T) {
	structs, scalars, err := parsePackage("../../util")
	require.NoError(t, err)
	w := &walker{structs: structs, scalars: scalars}
	w.walk(structs["ConfigType"], "")
	opts := make(map[string]option)
	for _, opt := range w.options {
		opts[opt.Key] = opt
	}
	for _, key := range []string{
		"apps.<id>.active", "ldap_providers.<id>.need_tls", "ldap_providers.<id>.tls_skip_verify",
		"oidc_providers.<id>.group_claim_case_insensitive", "oidc_providers.<id>.require_verified_email",
	} {
		t.Run(key, func(t *testing.T) {
			_, exists := opts[key]
			require.True(t, exists, "missing named-entry flag %s", key)
			assert.Equal(t, "false", opts[key].Default)
			assert.Empty(t, opts[key].Env)
		})
	}
	require.Contains(t, opts, "oidc_providers.<id>.return_via_state")
	assert.Equal(t, "true", opts["oidc_providers.<id>.return_via_state"].Default)
}
