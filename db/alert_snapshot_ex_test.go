package db_test

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestTemplateVersionSnapshotPreservesErrorAlertSuppression(t *testing.T) {
	source := db.Template{RepositoryID: 9, Name: "Deploy", Playbook: "deploy.yml", SuppressErrorAlerts: true}
	snapshot, err := db.NewTemplateVersionSnapshot(source)
	require.NoError(t, err)
	fingerprint, err := db.TemplateVersionFingerprint(snapshot)
	require.NoError(t, err)
	restored, err := snapshot.ReconstructTemplate(4, 12)
	require.NoError(t, err)
	assert.True(t, restored.SuppressErrorAlerts)
	source.SuppressErrorAlerts = false
	changed, err := db.NewTemplateVersionSnapshot(source)
	require.NoError(t, err)
	changedFingerprint, err := db.TemplateVersionFingerprint(changed)
	require.NoError(t, err)
	assert.NotEqual(t, fingerprint, changedFingerprint)
}
