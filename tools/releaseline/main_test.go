package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFinal(t *testing.T) {
	tests := []struct {
		tag  string
		want version
		ok   bool
	}{
		{"v2.20.24", version{2, 20, 24}, true},
		{"v10.0.1", version{10, 0, 1}, true},
		{"v2.20.21-rc1", version{}, false},
		{"v2.20.19-beta1", version{}, false},
		{"v2.20.16-ex.2.1.7", version{}, false},
		{"2.20.24", version{}, false},
		{"v2.20", version{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.tag, func(t *testing.T) {
			got, ok := parseFinal(tt.tag)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseEX(t *testing.T) {
	tests := []struct {
		tag  string
		want []int
		ok   bool
	}{
		{"v2.20.0-ex.1", []int{1}, true},
		{"v2.20.0-ex.2.1", []int{2, 1}, true},
		{"v2.20.16-ex.2.1.7", []int{2, 1, 7}, true},
		{"v2.20.0-ex.1-rc1", nil, false},
		{"v2.20.0-ex.1.2.3.4", nil, false},
		{"v2.20.24", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.tag, func(t *testing.T) {
			got, ok := parseEX(tt.tag)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLatestFinalIgnoresPreReleasesAndOlderLines(t *testing.T) {
	tags := []string{"v2.19.22", "v2.20.9", "v2.20.24", "v2.20.21-rc1", "v2.21.0-beta1", "v2.20.16-ex.2.1.7", "v2.20.23"}

	got, ok := latestFinal(tags)

	require.True(t, ok)
	assert.Equal(t, "v2.20.24", got)
}

func TestLatestFinalWithoutReleases(t *testing.T) {
	_, ok := latestFinal([]string{"v2.20.21-rc1", "v2.20.16-ex.2.1.7"})
	assert.False(t, ok)
}

func TestNextEX(t *testing.T) {
	tests := []struct {
		name string
		base string
		tags []string
		want string
	}{
		{"continues across upstream lines", "v2.20.24", []string{"v2.20.0-ex.2.1.6", "v2.20.16-ex.2.1.7", "v2.20.0-ex.2"}, "v2.20.24-ex.2.1.8"},
		{"numeric not lexical order", "v2.21.0", []string{"v2.20.0-ex.2.1.9", "v2.20.0-ex.2.1.10"}, "v2.21.0-ex.2.1.11"},
		{"longer tuple after its prefix", "v2.20.0", []string{"v2.20.0-ex.2", "v2.20.0-ex.2.1"}, "v2.20.0-ex.2.2"},
		{"release candidates do not count", "v2.20.24", []string{"v2.20.16-ex.2.1.7", "v2.20.24-ex.2.1.8-rc1"}, "v2.20.24-ex.2.1.8"},
		{"first release", "v2.20.0", nil, "v2.20.0-ex.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := nextEX(tt.base, tt.tags)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNextEXRejectsNonFinalBase(t *testing.T) {
	_, err := nextEX("v2.20.21-rc1", nil)
	assert.ErrorContains(t, err, "not a final upstream release")
}

func TestRunAgainstRepository(t *testing.T) {
	dir := newRepository(t)
	commit(t, dir, "base")
	git(t, dir, "tag", "v2.20.23")
	git(t, dir, "tag", "v2.20.16-ex.2.1.7")
	commit(t, dir, "fork work")
	git(t, dir, "branch", "develop")
	git(t, dir, "checkout", "-q", "-b", "stable", "v2.20.23")
	commit(t, dir, "upstream fix")
	git(t, dir, "tag", "v2.20.24")
	git(t, dir, "tag", "v2.20.25-rc1")

	t.Run("latest reports an unmerged release", func(t *testing.T) {
		out := run(t, dir, "-mode", "latest", "-head", "develop")
		assert.Contains(t, out, "tag: v2.20.24\n")
		assert.Contains(t, out, "merged: false\n")
	})
	t.Run("base and next use the newest merged release", func(t *testing.T) {
		assert.Equal(t, "v2.20.23\n", run(t, dir, "-mode", "base", "-head", "develop"))
		assert.Equal(t, "v2.20.23-ex.2.1.8\n", run(t, dir, "-mode", "next", "-head", "develop"))
	})
	t.Run("after merging the release", func(t *testing.T) {
		git(t, dir, "checkout", "-q", "develop")
		git(t, dir, "merge", "-q", "--no-ff", "--no-edit", "v2.20.24")
		assert.Contains(t, run(t, dir, "-mode", "latest", "-head", "develop"), "merged: true\n")
		assert.Equal(t, "v2.20.24-ex.2.1.8\n", run(t, dir, "-mode", "next", "-head", "develop"))
	})
}

func TestRunRejectsUnknownMode(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run1([]string{"-mode", "other"}, &out, &errOut)
	assert.Equal(t, 2, code)
	assert.Contains(t, errOut.String(), "unknown mode")
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run1(append([]string{"-repo", dir}, args...), &out, &errOut)
	require.Equal(t, 0, code, errOut.String())
	return out.String()
}

func newRepository(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	return dir
}

func commit(t *testing.T, dir, message string) {
	t.Helper()
	name := strings.ReplaceAll(message, " ", "-") + ".txt"
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(message), 0o644))
	git(t, dir, "add", name)
	git(t, dir, "commit", "-q", "-m", message)
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}
