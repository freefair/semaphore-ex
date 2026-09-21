package server

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccessKeyServiceRejectsGenericGeneratedSSHKeyRequests(t *testing.T) {
	projectID := 1
	created := false
	updated := false
	repo := &mockAccessKeyRepo{
		CreateAccessKeyFn: func(key db.AccessKey) (db.AccessKey, error) {
			created = true
			return key, nil
		},
		UpdateAccessKeyFn: func(db.AccessKey) error {
			updated = true
			return nil
		},
	}
	service := NewAccessKeyService(repo, NewAccessKeyEncryptionService(repo, nil, nil, nil), nil)

	_, err := service.Create(db.AccessKey{
		ProjectID:      &projectID,
		Name:           "generated",
		Type:           db.AccessKeySSH,
		GenerateSSHKey: true,
	})
	require.Error(t, err)
	assert.ErrorContains(t, err, "dedicated generation or rotation action")
	assert.False(t, created)

	err = service.Update(db.AccessKey{
		ID:             7,
		ProjectID:      &projectID,
		Name:           "generated",
		Type:           db.AccessKeySSH,
		GenerateSSHKey: true,
	})
	require.Error(t, err)
	assert.ErrorContains(t, err, "dedicated generation or rotation action")
	assert.False(t, updated)
}

func TestAccessKeyServiceCreateDiscardsCallerSuppliedPlain(t *testing.T) {
	previousConfig := util.Config
	util.Config = &util.ConfigType{}
	t.Cleanup(func() { util.Config = previousConfig })

	projectID := 1
	callerPlain := `{"public_key":"forged"}`
	repo := &mockAccessKeyRepo{}
	service := NewAccessKeyService(repo, NewAccessKeyEncryptionService(repo, nil, nil, nil), nil)

	created, err := service.Create(db.AccessKey{
		ProjectID: &projectID,
		Name:      "manual",
		Type:      db.AccessKeySSH,
		Plain:     &callerPlain,
		SshKey:    db.SshKey{PrivateKey: "private-key"},
	})

	require.NoError(t, err)
	assert.Nil(t, created.Plain)
	assert.True(t, created.IgnorePlain)
}

func TestAccessKeyServiceRenamePreservesGeneratedSSHKeyMetadata(t *testing.T) {
	projectID := 1
	var updated db.AccessKey
	repo := &mockAccessKeyRepo{
		UpdateAccessKeyFn: func(key db.AccessKey) error {
			updated = key
			return nil
		},
	}
	service := NewAccessKeyService(repo, NewAccessKeyEncryptionService(repo, nil, nil, nil), nil)

	err := service.Update(db.AccessKey{
		ID:        7,
		ProjectID: &projectID,
		Name:      "renamed",
		Type:      db.AccessKeySSH,
	})

	require.NoError(t, err)
	assert.True(t, updated.IgnorePlain)
	assert.Nil(t, updated.Plain)
}
