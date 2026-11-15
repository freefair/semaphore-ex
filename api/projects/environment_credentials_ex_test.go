package projects

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateEnvironmentCredentialChanges(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	owner, err := store.CreateUserWithoutPassword(db.User{Username: "env-ref-owner", Name: "Owner", Email: "env-ref-owner@example.test"})
	require.NoError(t, err)
	project, err := store.CreateProject(db.Project{Name: "env references"})
	require.NoError(t, err)
	other, err := store.CreateProject(db.Project{Name: "other project"})
	require.NoError(t, err)
	now := time.Now().UTC()
	createCredential := func(name string, grantProject int, operations db.GlobalCredentialGrantOperation) db.GlobalCredential {
		credential, _, err := store.CreateGlobalCredential(
			db.GlobalCredential{Type: db.GlobalCredentialTypeString, DisplayName: name, OwnerUserID: owner.ID, Enabled: true, Created: now},
			db.GlobalCredentialVersion{MaterialKind: db.GlobalCredentialMaterialLocalEncrypted, EncryptedMaterial: "envelope", CreatedByUserID: owner.ID})
		require.NoError(t, err)
		_, err = store.CreateGlobalCredentialGrant(db.GlobalCredentialGrant{
			CredentialID: credential.ID, ProjectID: grantProject, Operations: operations,
			Status: db.GlobalCredentialGrantStatusActive, Revision: 1, CreatedByUserID: owner.ID, Created: now,
		})
		require.NoError(t, err)
		return credential
	}
	granted := createCredential("granted", project.ID, db.GlobalCredentialGrantOperationReference|db.GlobalCredentialGrantOperationConsume)
	consumeOnly := createCredential("consume only", project.ID, db.GlobalCredentialGrantOperationConsume)
	foreign := createCredential("foreign", other.ID, db.GlobalCredentialGrantOperationReference)

	request := func(permissions db.ProjectUserPermission) *http.Request {
		r := httptest.NewRequest(http.MethodPut, "/environment", nil)
		r = helpers.SetContextValue(r, "user", &db.User{ID: 5})
		r = helpers.SetContextValue(r, "permissions", permissions)
		r = helpers.SetContextValue(r, "store", store)
		return r
	}
	reference := func(credentialID int) []db.EnvironmentCredential {
		return []db.EnvironmentCredential{{Name: "license_key", Target: db.EnvironmentCredentialTargetVar, CredentialID: credentialID}}
	}

	tests := []struct {
		name        string
		permissions db.ProjectUserPermission
		references  []db.EnvironmentCredential
		previous    []db.EnvironmentCredential
		status      int
	}{
		{"granted credential", db.CanListGrantedCredentials, reference(granted.ID), nil, http.StatusOK},
		{"missing permission", 0, reference(granted.ID), nil, http.StatusForbidden},
		{"grant without reference", db.CanListGrantedCredentials, reference(consumeOnly.ID), nil, http.StatusBadRequest},
		{"credential of another project", db.CanListGrantedCredentials, reference(foreign.ID), nil, http.StatusBadRequest},
		{"unknown credential", db.CanListGrantedCredentials, reference(999_999), nil, http.StatusBadRequest},
		{"invalid name", db.CanListGrantedCredentials, []db.EnvironmentCredential{{Name: "Bad-Name", Target: db.EnvironmentCredentialTargetVar, CredentialID: granted.ID}}, nil, http.StatusBadRequest},
		{"unchanged reference without permission", 0, reference(foreign.ID), reference(foreign.ID), http.StatusOK},
		{"removing references without permission", 0, nil, reference(granted.ID), http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := db.Environment{ProjectID: project.ID, Credentials: tt.references}
			status, err := validateEnvironmentCredentialChanges(request(tt.permissions), env, tt.previous)
			assert.Equal(t, tt.status, status)
			if tt.status == http.StatusOK {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}
