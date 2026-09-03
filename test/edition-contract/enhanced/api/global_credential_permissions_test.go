package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	rootapi "github.com/semaphoreui/semaphore/api"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/metrics"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type globalCredentialPermissionLogWriter struct{}

func (globalCredentialPermissionLogWriter) WriteEventLog(pro_interfaces.EventLogRecord) error {
	return nil
}
func (globalCredentialPermissionLogWriter) WriteTaskLog(pro_interfaces.TaskLogRecord) error {
	return nil
}
func (globalCredentialPermissionLogWriter) WriteResult(any) error { return nil }

func TestGlobalCredentialRoutesEnforceSeparatedPermissions(t *testing.T) {
	previousConfig := util.Config
	t.Cleanup(func() { util.Config = previousConfig })
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	util.Config.Debugging = &util.DebuggingConfig{}

	project, err := store.CreateProject(db.Project{Name: "credential permission project"})
	require.NoError(t, err)
	metadataUser := createGlobalCredentialPermissionUser(t, store, "metadata", db.CanManageGlobalCredentialsMetadata)
	rotateUser := createGlobalCredentialPermissionUser(t, store, "rotate", db.CanManageGlobalCredentialsRotate)
	grantUser := createGlobalCredentialPermissionUser(t, store, "grant", db.CanManageGlobalCredentialsGrant)
	combinedUser := createGlobalCredentialPermissionUser(t, store, "combined", db.CanManageGlobalCredentialsMetadata|db.CanManageGlobalCredentialsRotate)
	guestUser := createProjectCredentialPermissionUser(t, store, project.ID, "guest", db.ProjectGuest, 0)
	listUser := createProjectCredentialPermissionUser(t, store, project.ID, "list", db.ProjectNone, db.CanListGrantedCredentials)
	consumeUser := createProjectCredentialPermissionUser(t, store, project.ID, "consume", db.ProjectNone, db.CanConsumeGrantedCredentials)

	metadataCredential := createPermissionCredential(t, store, metadataUser.ID, "Metadata")
	rotateCredential := createPermissionCredential(t, store, rotateUser.ID, "Rotate")
	grantCredential := createPermissionCredential(t, store, grantUser.ID, "Grant")

	router := rootapi.Route(store,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		nil, nil, nil, nil, globalCredentialPermissionLogWriter{}, nil, metrics.NewMetrics(), nil, nil,
	)
	serve := func(token, method, target, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, target, bytes.NewBufferString(body))
		request.Header.Set("Authorization", "Bearer "+token)
		request = helpers.SetContextValue(request, "store", store)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}

	metadataToken := "credential-metadata-token"
	rotateToken := "credential-rotate-token"
	grantToken := "credential-grant-token"
	combinedToken := "credential-combined-token"
	guestToken := "credential-guest-token"
	listToken := "credential-list-token"
	consumeToken := "credential-consume-token"
	for token, userID := range map[string]int{
		metadataToken: metadataUser.ID, rotateToken: rotateUser.ID, grantToken: grantUser.ID,
		combinedToken: combinedUser.ID, guestToken: guestUser.ID, listToken: listUser.ID, consumeToken: consumeUser.ID,
	} {
		_, createTokenErr := store.CreateAPIToken(db.APIToken{ID: token, UserID: userID, Name: token})
		require.NoError(t, createTokenErr)
	}

	credentialPath := func(id int) string { return "/api/global-credentials/" + strconv.Itoa(id) }
	externalMaterial := `{"external_reference":{"provider":"vault","provider_id":"provider","mount":"kv","path":"team/deploy","version":1,"field":"token"}}`
	createBody := `{"type":"string","display_name":"Created","material":` + externalMaterial + `}`

	// Metadata administration reaches only metadata/state/delete handlers.
	require.Equal(t, http.StatusOK, serve(metadataToken, http.MethodGet, "/api/global-credentials", "").Code)
	require.Equal(t, http.StatusOK, serve(metadataToken, http.MethodGet, credentialPath(metadataCredential.ID), "").Code)
	require.Equal(t, http.StatusOK, serve(metadataToken, http.MethodPut, credentialPath(metadataCredential.ID), `{"display_name":"Metadata updated","revision":1}`).Code)
	require.Equal(t, http.StatusOK, serve(metadataToken, http.MethodPost, credentialPath(metadataCredential.ID)+"/enabled", `{"enabled":false,"revision":2}`).Code)
	require.Equal(t, http.StatusNoContent, serve(metadataToken, http.MethodDelete, credentialPath(metadataCredential.ID)+"?expected_revision=3", "").Code)
	assert.Equal(t, http.StatusForbidden, serve(metadataToken, http.MethodPost, credentialPath(rotateCredential.ID)+"/rotate", `{"revision":1,"material":`+externalMaterial+`}`).Code)
	assert.Equal(t, http.StatusForbidden, serve(metadataToken, http.MethodGet, credentialPath(grantCredential.ID)+"/grants", "").Code)
	assert.Equal(t, http.StatusForbidden, serve(metadataToken, http.MethodPost, "/api/global-credentials", createBody).Code)

	// Rotation is independent of metadata and grant administration, but may list
	// summaries for choosing a credential to rotate.
	require.Equal(t, http.StatusOK, serve(rotateToken, http.MethodGet, "/api/global-credentials", "").Code)
	require.Equal(t, http.StatusOK, serve(rotateToken, http.MethodPost, credentialPath(rotateCredential.ID)+"/rotate", `{"revision":1,"material":`+externalMaterial+`}`).Code)
	assert.Equal(t, http.StatusForbidden, serve(rotateToken, http.MethodGet, credentialPath(grantCredential.ID), "").Code)
	assert.Equal(t, http.StatusForbidden, serve(rotateToken, http.MethodGet, credentialPath(grantCredential.ID)+"/grants", "").Code)
	assert.Equal(t, http.StatusForbidden, serve(rotateToken, http.MethodPost, "/api/global-credentials", createBody).Code)

	// Grant administration can list eligible projects and execute the complete
	// grant state machine, without obtaining metadata or material authority.
	require.Equal(t, http.StatusOK, serve(grantToken, http.MethodGet, "/api/global-credentials", "").Code)
	require.Equal(t, http.StatusOK, serve(grantToken, http.MethodGet, "/api/global-credentials/grant-projects", "").Code)
	require.Equal(t, http.StatusOK, serve(grantToken, http.MethodGet, credentialPath(grantCredential.ID)+"/grants", "").Code)
	grantCreate := serve(grantToken, http.MethodPost, credentialPath(grantCredential.ID)+"/grants", `{"project_id":`+strconv.Itoa(project.ID)+`,"operations":1}`)
	require.Equal(t, http.StatusCreated, grantCreate.Code, grantCreate.Body.String())
	var grant db.GlobalCredentialGrant
	require.NoError(t, json.Unmarshal(grantCreate.Body.Bytes(), &grant))
	grantPath := credentialPath(grantCredential.ID) + "/grants/" + strconv.Itoa(grant.ID)
	require.Equal(t, http.StatusOK, serve(grantToken, http.MethodPut, grantPath, `{"project_id":`+strconv.Itoa(project.ID)+`,"operations":1,"revision":1}`).Code)
	require.Equal(t, http.StatusOK, serve(grantToken, http.MethodPost, grantPath+"/revoke", `{"revision":2}`).Code)
	require.Equal(t, http.StatusOK, serve(grantToken, http.MethodPost, grantPath+"/restore", `{"revision":3}`).Code)
	require.Equal(t, http.StatusOK, serve(grantToken, http.MethodPost, grantPath+"/revoke", `{"revision":4}`).Code)
	require.Equal(t, http.StatusNoContent, serve(grantToken, http.MethodDelete, grantPath+"?expected_revision=5", "").Code)
	assert.Equal(t, http.StatusForbidden, serve(grantToken, http.MethodGet, credentialPath(rotateCredential.ID), "").Code)
	assert.Equal(t, http.StatusForbidden, serve(grantToken, http.MethodPost, credentialPath(rotateCredential.ID)+"/rotate", `{"revision":2,"material":`+externalMaterial+`}`).Code)
	assert.Equal(t, http.StatusForbidden, serve(grantToken, http.MethodPost, "/api/global-credentials", createBody).Code)

	// Initial material creation has both independent privileges as prerequisites.
	assert.Equal(t, http.StatusForbidden, serve(metadataToken, http.MethodPost, "/api/global-credentials", createBody).Code)
	assert.Equal(t, http.StatusForbidden, serve(rotateToken, http.MethodPost, "/api/global-credentials", createBody).Code)
	assert.Equal(t, http.StatusForbidden, serve(grantToken, http.MethodPost, "/api/global-credentials", createBody).Code)
	require.Equal(t, http.StatusCreated, serve(combinedToken, http.MethodPost, "/api/global-credentials", createBody).Code)

	projectListPath := "/api/project/" + strconv.Itoa(project.ID) + "/granted-credentials"
	assert.Equal(t, http.StatusForbidden, serve(guestToken, http.MethodGet, projectListPath, "").Code)
	require.Equal(t, http.StatusOK, serve(listToken, http.MethodGet, projectListPath, "").Code)
	assert.Equal(t, http.StatusForbidden, serve(consumeToken, http.MethodGet, projectListPath, "").Code)
}

func createGlobalCredentialPermissionUser(t *testing.T, store *coresql.SqlDb, label string, permissions db.GlobalPermission) db.User {
	t.Helper()
	user, err := store.CreateUserWithoutPassword(db.User{Username: "credential-" + label, Name: label, Email: label + "@example.test"})
	require.NoError(t, err)
	role, err := store.CreateGlobalRole(db.Role{ID: db.ProjectRoleID("credential_" + label), Slug: "credential_" + label, Name: label, GlobalPermissions: permissions, Revision: 1})
	require.NoError(t, err)
	_, err = store.CreateGlobalRoleAssignment(db.GlobalRoleAssignment{UserID: user.ID, RoleID: role.ID, Revision: 1})
	require.NoError(t, err)
	return user
}

func createProjectCredentialPermissionUser(t *testing.T, store *coresql.SqlDb, projectID int, label string, builtin db.ProjectUserRole, permissions db.ProjectUserPermission) db.User {
	t.Helper()
	user, err := store.CreateUserWithoutPassword(db.User{Username: "credential-project-" + label, Name: label, Email: "project-" + label + "@example.test"})
	require.NoError(t, err)
	membership := db.ProjectUser{ProjectID: projectID, UserID: user.ID, Role: builtin, Revision: 1}
	if permissions != 0 {
		role, roleErr := store.CreateProjectRole(db.Role{ID: db.ProjectRoleID("credential_project_" + label), Name: label, ProjectID: &projectID, Permissions: permissions, Revision: 1})
		require.NoError(t, roleErr)
		membership.RoleID = &role.ID
	}
	_, err = store.CreateProjectUser(membership)
	require.NoError(t, err)
	return user
}

func createPermissionCredential(t *testing.T, store *coresql.SqlDb, ownerID int, name string) db.GlobalCredential {
	t.Helper()
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	credential, _, err := store.CreateGlobalCredential(
		db.GlobalCredential{Type: db.GlobalCredentialTypeString, DisplayName: name, OwnerUserID: ownerID, Enabled: true, Created: now},
		db.GlobalCredentialVersion{MaterialKind: db.GlobalCredentialMaterialLocalEncrypted, EncryptedMaterial: "permission-envelope", CreatedByUserID: ownerID},
	)
	require.NoError(t, err)
	return credential
}
