package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunAssociatesAndNormalizesApplicationSourceMaps(t *testing.T) {
	production := t.TempDir()
	debug := t.TempDir()
	output := filepath.Join(t.TempDir(), "maps")
	require.NoError(t, os.MkdirAll(filepath.Join(production, "js"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(debug, "js"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(debug, "swagger"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(production, "js", "app.product.js"), []byte("application"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(debug, "js", "app.debug.js"), []byte("application"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(debug, "js", "app.debug.js.map"), []byte(`{"version":3,"file":"js/app.debug.js","sources":["webpack://semaphore/src/App.vue"]}`), 0o644))
	vendor := []byte("vendor\n//# sourceMappingURL=vendor.js.map\n")
	require.NoError(t, os.MkdirAll(filepath.Join(production, "swagger"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(production, "swagger", "vendor.js"), vendor, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(production, "swagger", "vendor.js.map"), []byte(`{"version":3}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(debug, "swagger", "vendor.js"), vendor, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(debug, "swagger", "vendor.js.map"), []byte(`{"version":3,"file":"swagger/vendor.js"}`), 0o644))

	err := run(options{production: production, debug: debug, output: output})

	require.NoError(t, err)
	encoded, err := os.ReadFile(filepath.Join(output, "js", "app.product.js.map"))
	require.NoError(t, err)
	var generated map[string]any
	require.NoError(t, json.Unmarshal(encoded, &generated))
	assert.Equal(t, "js/app.product.js", generated["file"])
	_, err = os.Stat(filepath.Join(output, "swagger", "vendor.js.map"))
	assert.NoError(t, err)
	_, err = os.Stat(filepath.Join(production, "swagger", "vendor.js.map"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	cleanedVendor, err := os.ReadFile(filepath.Join(production, "swagger", "vendor.js"))
	require.NoError(t, err)
	assert.Equal(t, "vendor", string(cleanedVendor))
}

func TestRunRejectsDebugAssetWithoutProductionMatch(t *testing.T) {
	production := t.TempDir()
	debug := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(debug, "js"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(debug, "js", "app.js"), []byte("debug"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(debug, "js", "app.js.map"), []byte(`{"version":3}`), 0o644))

	err := run(options{production: production, debug: debug, output: filepath.Join(t.TempDir(), "maps")})

	assert.EqualError(t, err, "no production asset matches "+filepath.Join(debug, "js", "app.js"))
}
