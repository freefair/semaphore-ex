package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunCreatesDeterministicEditionArtifacts(t *testing.T) {
	binary, err := os.Executable()
	require.NoError(t, err)
	frontend := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(frontend, "index.html"), []byte("community"), 0o644))
	frontendLock := filepath.Join(t.TempDir(), "package-lock.json")
	require.NoError(t, os.WriteFile(frontendLock, []byte(`{
  "lockfileVersion": 3,
  "packages": {
    "": {"name": "web", "version": "0.1.0"},
    "node_modules/vue": {"version": "2.7.16"},
    "node_modules/test-only": {"version": "1.2.3", "dev": true},
    "node_modules/nested/node_modules/vue": {"version": "2.7.16"}
  }
}`), 0o644))
	sourceMaps := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(sourceMaps, "js"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sourceMaps, "js", "app.js.map"), []byte("source map"), 0o644))
	output := filepath.Join(t.TempDir(), "artifacts")
	secondOutput := filepath.Join(t.TempDir(), "artifacts")

	configured := options{
		binary:             binary,
		frontend:           frontend,
		frontendLock:       frontendLock,
		sourceMaps:         sourceMaps,
		output:             output,
		edition:            string(pro_interfaces.EditionCommunity),
		coreRevision:       "0123456789012345678901234567890123456789",
		implementation:     "community-1",
		sourceDateEpochRaw: "1700000000",
	}
	err = run(configured)

	require.NoError(t, err)
	configured.output = secondOutput
	require.NoError(t, run(configured))
	assert.Equal(t, readDirectory(t, output), readDirectory(t, secondOutput))
	for _, path := range []string{
		"semaphore-server",
		"semaphore-runner",
		"web/index.html",
		"debug-source-maps/js/app.js.map",
		"manifest.json",
		"sbom.spdx.json",
		"provenance.json",
	} {
		_, statErr := os.Stat(filepath.Join(output, path))
		assert.NoError(t, statErr, path)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(output, "manifest.json"))
	require.NoError(t, err)
	var generated manifest
	require.NoError(t, json.Unmarshal(manifestBytes, &generated))
	assert.Equal(t, string(pro_interfaces.EditionCommunity), generated.Edition)
	assert.Equal(t, int64(1700000000), generated.SourceDateEpoch)
	assert.Empty(t, generated.EnhancedRevision)
	assert.NotEmpty(t, generated.Artifacts)

	sbomBytes, err := os.ReadFile(filepath.Join(output, "sbom.spdx.json"))
	require.NoError(t, err)
	var generatedSBOM spdxDocument
	require.NoError(t, json.Unmarshal(sbomBytes, &generatedSBOM))
	assert.Equal(t, "SPDX-2.3", generatedSBOM.SPDXVersion)
	assert.NotEmpty(t, generatedSBOM.Packages)
	assert.Contains(t, generatedSBOM.Packages, newSPDXPackage("npm", "vue", "2.7.16"))
	for _, currentPackage := range generatedSBOM.Packages {
		assert.NotEqual(t, "test-only", currentPackage.Name)
	}

	provenanceBytes, err := os.ReadFile(filepath.Join(output, "provenance.json"))
	require.NoError(t, err)
	var generatedProvenance provenance
	require.NoError(t, json.Unmarshal(provenanceBytes, &generatedProvenance))
	assert.Equal(t, "https://slsa.dev/provenance/v1", generatedProvenance.PredicateType)
	assert.NotEmpty(t, generatedProvenance.Subject)
}

func TestReadNPMProductionPackagesRequiresPackagesTree(t *testing.T) {
	path := filepath.Join(t.TempDir(), "package-lock.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"lockfileVersion": 1}`), 0o644))

	_, err := readNPMProductionPackages(path)

	assert.EqualError(t, err, "frontend lockfile does not contain a packages tree")
}

func TestRunRequiresEnhancedRevision(t *testing.T) {
	err := run(options{
		edition:            string(pro_interfaces.EditionEnhanced),
		coreRevision:       "core",
		implementation:     "enhanced-test",
		sourceDateEpochRaw: "1700000000",
	})

	assert.EqualError(t, err, "enhanced revision is required for enhanced artifacts")
}

func readDirectory(t *testing.T, root string) map[string][]byte {
	t.Helper()
	contents := make(map[string][]byte)
	require.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relativePath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		contents[relativePath], err = os.ReadFile(path)
		return err
	}))
	return contents
}
