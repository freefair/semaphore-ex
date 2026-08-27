package db

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeExecutorImage(t *testing.T) {
	valid := map[string]string{
		"trimmed":       "registry.example.com/team/job:1.2",
		"digest":        "registry.example.com/team/job@sha256:" + strings.Repeat("a", 64),
		"short":         "ubuntu:latest",
		"registry port": "localhost:5000/team/job:v1",
	}
	for name, expected := range valid {
		t.Run(name, func(t *testing.T) {
			image, err := NormalizeExecutorImage("  " + expected + "  ")
			require.NoError(t, err)
			require.NotNil(t, image)
			assert.Equal(t, expected, *image)
		})
	}
	for name, value := range map[string]string{
		"url":            "https://registry.example.com/team/job:latest",
		"credentials":    "user:secret@registry.example.com/team/job:latest",
		"uppercase path": "Registry.Example.com/Team/Job:latest",
		"space":          "registry.example.com/team/my job:latest",
		"too long":       strings.Repeat("a", MaxExecutorImageLength+1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NormalizeExecutorImage(value)
			assert.Error(t, err)
		})
	}
	image, err := NormalizeExecutorImage("   ")
	require.NoError(t, err)
	assert.Nil(t, image)
}

func TestTemplateValidateNormalizesExecutorImage(t *testing.T) {
	template := Template{Name: "image", Playbook: "site.yml", ExecutorImage: new(" registry.example.com/team/job:v1 ")}
	require.NoError(t, template.Validate())
	require.NotNil(t, template.ExecutorImage)
	assert.Equal(t, "registry.example.com/team/job:v1", *template.ExecutorImage)
}
