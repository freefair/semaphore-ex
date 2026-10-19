package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	prosql "github.com/semaphoreui/semaphore/pro/db/sql"
	"github.com/semaphoreui/semaphore/services/audit"
	"github.com/semaphoreui/semaphore/services/audit/audittest"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoleMappingsControllerPostResolvesBuiltinAndScopedSlug(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	previous := util.Config
	t.Cleanup(func() { util.Config = previous })
	util.Config = &util.ConfigType{OidcProviders: map[string]util.OidcProvider{"qa": {GroupsClaim: "groups"}}}
	first, err := store.CreateProject(db.Project{Name: "first"})
	require.NoError(t, err)
	second, err := store.CreateProject(db.Project{Name: "second"})
	require.NoError(t, err)
	_, err = store.CreateProjectRole(db.Role{ID: "second-role", Slug: "operator", Name: "Second operator", ProjectID: &second.ID, Permissions: db.CanRunProjectTasks, Revision: 1})
	require.NoError(t, err)
	controller := NewRoleMappingsController(prosql.NewRoleMappingStore(store), store)
	request := httptest.NewRequest(http.MethodPost, "/role_mappings", bytes.NewBufferString(`{"provider":"qa","group":"owners","project_id":`+strconv.Itoa(first.ID)+`,"role":"manager"}`))
	response := httptest.NewRecorder()
	controller.AddMapping(response, request)
	assert.Equal(t, http.StatusCreated, response.Code)
	request = httptest.NewRequest(http.MethodPost, "/role_mappings", bytes.NewBufferString(`{"provider":"qa","group":"operators","project_id":`+strconv.Itoa(first.ID)+`,"role":"operator"}`))
	response = httptest.NewRecorder()
	controller.AddMapping(response, request)
	assert.NotEqual(t, http.StatusCreated, response.Code)
}

func TestRoleMappingsControllerPostMapsDuplicateToRevisionConflict(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	previous := util.Config
	t.Cleanup(func() { util.Config = previous })
	util.Config = &util.ConfigType{OidcProviders: map[string]util.OidcProvider{"qa": {GroupsClaim: "groups"}}}
	project, err := store.CreateProject(db.Project{Name: "duplicate role mapping"})
	require.NoError(t, err)

	controller := NewRoleMappingsController(prosql.NewRoleMappingStore(store), store)
	actor := db.User{ID: 81, Username: "audit-admin"}
	recorder := &audittest.Recorder{}
	request := func() *http.Request {
		return withRoleMappingAudit(httptest.NewRequest(http.MethodPost, "/role_mappings", bytes.NewBufferString(
			`{"provider":"qa","group":"operators","project_id":`+strconv.Itoa(project.ID)+`,"role":"manager"}`)), actor, recorder)
	}

	createdResponse := httptest.NewRecorder()
	controller.AddMapping(createdResponse, request())
	require.Equal(t, http.StatusCreated, createdResponse.Code, createdResponse.Body.String())

	duplicateResponse := httptest.NewRecorder()
	controller.AddMapping(duplicateResponse, request())
	require.Equal(t, http.StatusConflict, duplicateResponse.Code, duplicateResponse.Body.String())
	assert.Contains(t, duplicateResponse.Body.String(), "ROLE_MAPPING_REVISION_CONFLICT")

	mappings, err := prosql.NewRoleMappingStore(store).GetRoleMappings()
	require.NoError(t, err)
	require.Len(t, mappings, 1)
	kinds, err := recorder.Kinds()
	require.NoError(t, err)
	assert.Equal(t, []audit.Kind{audit.IAMRoleMappingCreate}, kinds)
}

func TestRoleMappingsControllerListsAndDeletesMappingsForRemovedProviders(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	previous := util.Config
	t.Cleanup(func() { util.Config = previous })
	util.Config = &util.ConfigType{OidcProviders: map[string]util.OidcProvider{
		"corp":  {GroupClaimPath: "groups"},
		"alias": {GroupsClaim: "groups"},
	}}
	project, err := store.CreateProject(db.Project{Name: "provider projection"})
	require.NoError(t, err)
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "provider-projection", Name: "Provider projection", Email: "provider-projection@example.test", External: true,
	})
	require.NoError(t, err)
	_, err = store.CreateExternalIdentity(db.UserExternalIdentity{
		UserID: user.ID, Type: db.IdentityTypeOidc, Provider: "corp", ExternalUID: "provider-projection-subject",
	})
	require.NoError(t, err)

	mappingStore := prosql.NewRoleMappingStore(store)
	corpMapping, err := mappingStore.CreateRoleMapping(db.RoleMapping{
		Provider: "corp", Group: "readers", ProjectID: project.ID, RoleID: "builtin:task_runner",
	})
	require.NoError(t, err)
	aliasMapping, err := mappingStore.CreateRoleMapping(db.RoleMapping{
		Provider: "alias", Group: "operators", ProjectID: project.ID, RoleID: "builtin:task_runner",
	})
	require.NoError(t, err)
	oidcMappings, err := store.GetOIDCGroupMappings("corp")
	require.NoError(t, err)
	require.Len(t, oidcMappings, 1)
	revision, err := store.GetOIDCGroupMappingRevision("corp")
	require.NoError(t, err)
	_, err = store.ApplyOIDCGroupReconciliation(db.OIDCGroupReconciliation{
		ProviderID: "corp", UserID: user.ID, Source: "test", Status: "applied", Token: "provider-removal",
		MappingRevision: revision, ClaimRevision: "claim", PreviewJSON: "{}", Created: time.Now().UTC(),
	}, []db.OIDCGroupAssignmentChange{{
		MappingID: oidcMappings[0].ID, UserID: user.ID, TargetScope: "project", ProjectID: &project.ID, RoleID: "builtin:task_runner",
	}}, nil)
	require.NoError(t, err)

	controller := NewRoleMappingsController(mappingStore, store)
	getMappings := func() ([]struct {
		ID                 int    `json:"id"`
		Provider           string `json:"provider"`
		ProviderConfigured bool   `json:"provider_configured"`
	}, string) {
		response := httptest.NewRecorder()
		controller.GetMappings(response, httptest.NewRequest(http.MethodGet, "/role_mappings", nil))
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var mappings []struct {
			ID                 int    `json:"id"`
			Provider           string `json:"provider"`
			ProviderConfigured bool   `json:"provider_configured"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &mappings))
		return mappings, response.Body.String()
	}

	configured, configuredBody := getMappings()
	require.Len(t, configured, 2)
	assert.Contains(t, configuredBody, `"provider_configured":true`)
	configuredByID := make(map[int]bool, len(configured))
	for _, mapping := range configured {
		configuredByID[mapping.ID] = mapping.ProviderConfigured
	}
	assert.True(t, configuredByID[corpMapping.ID])
	assert.True(t, configuredByID[aliasMapping.ID])

	delete(util.Config.OidcProviders, "corp")
	unconfigured, unconfiguredBody := getMappings()
	require.Len(t, unconfigured, 2)
	assert.Contains(t, unconfiguredBody, `"provider_configured":false`)
	configuredByID = make(map[int]bool, len(unconfigured))
	for _, mapping := range unconfigured {
		configuredByID[mapping.ID] = mapping.ProviderConfigured
	}
	assert.False(t, configuredByID[corpMapping.ID])
	assert.True(t, configuredByID[aliasMapping.ID])

	actor := db.User{ID: 81, Username: "audit-admin"}
	request := withRoleMappingAudit(mux.SetURLVars(httptest.NewRequest(http.MethodDelete,
		"/role_mappings/"+strconv.Itoa(corpMapping.ID), nil), map[string]string{"mapping_id": strconv.Itoa(corpMapping.ID)}), actor, &audittest.Recorder{})
	response := httptest.NewRecorder()
	controller.DeleteMapping(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assignments, err := store.GetOIDCGroupRoleAssignments("corp", user.ID)
	require.NoError(t, err)
	assert.Empty(t, assignments)
	remaining, err := mappingStore.GetRoleMappings()
	require.NoError(t, err)
	assert.Equal(t, []db.RoleMapping{aliasMapping}, remaining)
}

func TestRoleMappingsControllerAuditsCreationAndRevokedAssignments(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	previous := util.Config
	t.Cleanup(func() { util.Config = previous })
	util.Config = &util.ConfigType{OidcProviders: map[string]util.OidcProvider{"qa": {GroupsClaim: "groups"}}}
	project, err := store.CreateProject(db.Project{Name: "audit mapping"})
	require.NoError(t, err)
	controller := NewRoleMappingsController(prosql.NewRoleMappingStore(store), store)
	actor := db.User{ID: 81, Username: "audit-admin"}
	recorder := &audittest.Recorder{}
	request := withRoleMappingAudit(httptest.NewRequest(http.MethodPost, "/role_mappings", bytes.NewBufferString(
		`{"provider":"qa","group":"operators","project_id":`+strconv.Itoa(project.ID)+`,"role":"manager"}`)), actor, recorder)
	response := httptest.NewRecorder()
	controller.AddMapping(response, request)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	var created db.RoleMapping
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &created))

	mappings, err := store.GetOIDCGroupMappings("qa")
	require.NoError(t, err)
	require.Len(t, mappings, 1)
	user, err := store.CreateUserWithoutPassword(db.User{Username: "mapped-audit", Name: "Mapped audit", Email: "mapped-audit@example.test", External: true})
	require.NoError(t, err)
	_, err = store.CreateExternalIdentity(db.UserExternalIdentity{UserID: user.ID, Type: db.IdentityTypeOidc, Provider: "qa", ExternalUID: "mapped-audit-subject"})
	require.NoError(t, err)
	revision, err := store.GetOIDCGroupMappingRevision("qa")
	require.NoError(t, err)
	now := time.Now().UTC()
	_, err = store.ApplyOIDCGroupReconciliation(db.OIDCGroupReconciliation{
		ProviderID: "qa", UserID: user.ID, Source: "test", Status: "applied", Token: "test", MappingRevision: revision, ClaimRevision: "test", PreviewJSON: "{}", Created: now,
	}, []db.OIDCGroupAssignmentChange{{MappingID: mappings[0].ID, UserID: user.ID, TargetScope: "project", ProjectID: &project.ID, RoleID: mappings[0].RoleID}}, nil)
	require.NoError(t, err)
	assignments, err := store.GetOIDCGroupRoleAssignments("qa", 0)
	require.NoError(t, err)
	require.Len(t, assignments, 1)

	deleteRequest := withRoleMappingAudit(mux.SetURLVars(httptest.NewRequest(http.MethodDelete,
		"/role_mappings/"+strconv.Itoa(created.ID), nil), map[string]string{"mapping_id": strconv.Itoa(created.ID)}), actor, recorder)
	deleteResponse := httptest.NewRecorder()
	controller.DeleteMapping(deleteResponse, deleteRequest)
	require.Equal(t, http.StatusOK, deleteResponse.Code, deleteResponse.Body.String())

	kinds, err := recorder.Kinds()
	require.NoError(t, err)
	require.Equal(t, []audit.Kind{audit.IAMRoleMappingCreate, audit.IAMRoleMappingDelete, audit.IAMMappedRoleRemove}, kinds)
	recorded := recorder.All()
	assert.Equal(t, audit.UserActor(actor.ID, actor.Username, audit.AuthSession, ""), recorded[0].Actor)
	assert.Equal(t, audit.RoleMappingMetadata{Provider: "qa", Group: "operators", Role: audit.RoleRef{ID: "builtin:manager", Slug: "manager"}}, recorded[0].Event.Metadata)
	assert.Equal(t, audit.MappedRoleCauseMappingDeleted, recorded[2].Event.Metadata.(audit.MappedRoleMetadata).Cause)
}

func TestRoleMappingsControllerDeleteProtectsLastMappedAdministrator(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	previous := util.Config
	t.Cleanup(func() { util.Config = previous })
	util.Config = &util.ConfigType{OidcProviders: map[string]util.OidcProvider{"qa": {GroupsClaim: "groups"}}}

	project, err := store.CreateProject(db.Project{Name: "protected mapped administrator"})
	require.NoError(t, err)
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "mapped-administrator", Name: "Mapped administrator", Email: "mapped-administrator@example.test", External: true,
	})
	require.NoError(t, err)
	_, err = store.CreateExternalIdentity(db.UserExternalIdentity{
		UserID: user.ID, Type: db.IdentityTypeOidc, Provider: "qa", ExternalUID: "mapped-administrator-subject",
	})
	require.NoError(t, err)

	mappingStore := prosql.NewRoleMappingStore(store)
	created, err := mappingStore.CreateRoleMapping(db.RoleMapping{
		Provider: "qa", Group: "owners", ProjectID: project.ID, RoleID: db.ProjectRoleID(db.BuiltinProjectRoleReferenceOwner),
	})
	require.NoError(t, err)
	oidcMappings, err := store.GetOIDCGroupMappings("qa")
	require.NoError(t, err)
	require.Len(t, oidcMappings, 1)
	revision, err := store.GetOIDCGroupMappingRevision("qa")
	require.NoError(t, err)
	_, err = store.ApplyOIDCGroupReconciliation(db.OIDCGroupReconciliation{
		ProviderID: "qa", UserID: user.ID, Source: "test", Status: "applied", Token: "protected-delete",
		MappingRevision: revision, ClaimRevision: "claim", PreviewJSON: "{}", Created: time.Now().UTC(),
	}, []db.OIDCGroupAssignmentChange{{
		MappingID: oidcMappings[0].ID, UserID: user.ID, TargetScope: "project", ProjectID: &project.ID,
		RoleID: string(db.BuiltinProjectRoleReferenceOwner),
	}}, nil)
	require.NoError(t, err)

	controller := NewRoleMappingsController(mappingStore, store)
	actor := db.User{ID: 81, Username: "audit-admin"}
	request := withRoleMappingAudit(mux.SetURLVars(httptest.NewRequest(http.MethodDelete,
		"/role_mappings/"+strconv.Itoa(created.ID), nil), map[string]string{"mapping_id": strconv.Itoa(created.ID)}), actor, &audittest.Recorder{})
	response := httptest.NewRecorder()

	controller.DeleteMapping(response, request)

	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), "ROLE_MAPPING_PROTECTED_ADMINISTRATOR")
	mappingsAfter, err := mappingStore.GetRoleMappings()
	require.NoError(t, err)
	assert.Equal(t, []db.RoleMapping{created}, mappingsAfter)
	assignments, err := store.GetOIDCGroupRoleAssignments("qa", user.ID)
	require.NoError(t, err)
	require.Len(t, assignments, 1)
	assert.Equal(t, oidcMappings[0].ID, assignments[0].ManagedByMappingID)
	assert.Equal(t, string(db.BuiltinProjectRoleReferenceOwner), assignments[0].RoleID)
}

func withRoleMappingAudit(request *http.Request, actor db.User, recorder *audittest.Recorder) *http.Request {
	request = helpers.SetContextValue(request, "audit", recorder)
	return request.WithContext(audit.WithActor(request.Context(), audit.UserActor(actor.ID, actor.Username, audit.AuthSession, "")))
}
