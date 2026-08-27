package main

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"testing"
)

func writeSpec(t *testing.T, directory, name, content string) string {
	t.Helper()
	target := filepath.Join(directory, name)
	require.NoError(t, os.WriteFile(target, []byte(content), 0600))
	return target
}

func TestBundlePreservesSchemaAndEscapedPathReferences(t *testing.T) {
	directory := t.TempDir()
	input := writeSpec(t, directory, "api-docs.yml", `swagger: "2.0"
definitions:
  Existing: {type: string}
  Added: {$ref: './api-docs-ex.yml#/definitions/Added'}
paths:
  /x/{id}: {$ref: './api-docs-ex.yml#/paths/~1x~1{id}'}
`)
	writeSpec(t, directory, "api-docs-ex.yml", `definitions:
  Added:
    type: object
    properties:
      existing: {$ref: './api-docs.yml#/definitions/Existing'}
      child: {$ref: './api-docs.yml#/definitions/Added'}
paths:
  /x/{id}:
    get:
      responses:
        200:
          schema: {$ref: './api-docs.yml#/definitions/Added'}
`)
	result, err := bundle(input)
	require.NoError(t, err)
	var actual any
	require.NoError(t, yaml.Unmarshal(result, &actual))
	var expected any
	require.NoError(t, yaml.Unmarshal([]byte(`swagger: "2.0"
definitions:
  Existing: {type: string}
  Added:
    type: object
    properties:
      existing: {$ref: '#/definitions/Existing'}
      child: {$ref: '#/definitions/Added'}
paths:
  /x/{id}:
    get:
      responses:
        200:
          schema: {$ref: '#/definitions/Added'}
`), &expected))
	assert.Equal(t, expected, actual)
}

func TestBundleRejectsAmbiguousAndUnsupportedReferences(t *testing.T) {
	for _, test := range []struct{ name, source, extra, message string }{
		{"outside file", "definitions: {X: {$ref: '../private.yml#/X'}}", "", "unsupported external"},
		{"network", "definitions: {X: {$ref: 'https://example.invalid/spec.yml#/X'}}", "", "unsupported external"},
		{"missing target", "definitions: {X: {$ref: './api-docs-ex.yml#/definitions/Missing'}}", "definitions: {}", "missing reference"},
		{"siblings", "definitions: {X: {$ref: './api-docs-ex.yml#/definitions/X', type: string}}", "definitions: {X: {type: string}}", "siblings"},
		{"cycle", "definitions: {X: {$ref: './api-docs-ex.yml#/definitions/X'}}", "definitions: {X: {$ref: './api-docs-ex.yml#/definitions/X'}}", "cycle"},
		{"duplicate", "definitions: {X: {type: string}, X: {type: integer}}", "", "duplicate"},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			input := writeSpec(t, directory, "api-docs.yml", test.source)
			if test.extra != "" {
				writeSpec(t, directory, "api-docs-ex.yml", test.extra)
			}
			_, err := bundle(input)
			assert.ErrorContains(t, err, test.message)
		})
	}
}

func TestOutputDirectorySymlinkCannotReplaceAuthoredSource(t *testing.T) {
	directory := t.TempDir()
	source := writeSpec(t, directory, "api-docs.yml", "swagger: '2.0'\n")
	link := filepath.Join(t.TempDir(), "linked")
	require.NoError(t, os.Symlink(directory, link))
	err := run([]string{"-input", source, "-output", filepath.Join(link, "api-docs.yml")})
	assert.ErrorContains(t, err, "authored API source")
	content, err := os.ReadFile(source)
	require.NoError(t, err)
	assert.Equal(t, "swagger: '2.0'\n", string(content))
}

func TestInputSymlinkCannotReplaceAuthoredTarget(t *testing.T) {
	directory := t.TempDir()
	source := writeSpec(t, directory, "api-docs.yml", "swagger: '2.0'\n")
	link := filepath.Join(t.TempDir(), "linked.yml")
	require.NoError(t, os.Symlink(source, link))
	err := run([]string{"-input", link, "-output", source})
	assert.ErrorContains(t, err, "authored API source")
	content, err := os.ReadFile(source)
	require.NoError(t, err)
	assert.Equal(t, "swagger: '2.0'\n", string(content))
}
