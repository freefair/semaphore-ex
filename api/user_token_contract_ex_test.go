package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTokenContractTestStore(t *testing.T) *sql.SqlDb {
	t.Helper()
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	return store
}

func createTokenContractUser(t *testing.T, store db.Store, username string) db.User {
	t.Helper()
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: username,
		Name:     username,
		Email:    username + "@example.test",
	})
	require.NoError(t, err)
	return user
}

func createTokenContractToken(t *testing.T, store db.Store, userID int, id string) db.APIToken {
	t.Helper()
	token, err := store.CreateAPIToken(db.APIToken{ID: id, UserID: userID, Name: "automation"})
	require.NoError(t, err)
	return token
}

func newTokenContractRequest(store db.Store, user *db.User, method, path string) *http.Request {
	request := httptest.NewRequest(method, path, nil)
	request = helpers.SetContextValue(request, "store", store)
	return helpers.SetContextValue(request, "user", user)
}

func TestGetAPITokens_StableReferencesDoNotExposeCredentials(t *testing.T) {
	store := newTokenContractTestStore(t)
	user := createTokenContractUser(t, store, "token-list-owner")
	first := createTokenContractToken(t, store, user.ID, "list-credential-one-0123456789")
	second := createTokenContractToken(t, store, user.ID, "list-credential-two-0123456789")

	response := httptest.NewRecorder()
	getAPITokens(response, newTokenContractRequest(store, &user, http.MethodGet, "/api/user/tokens"))

	require.Equal(t, http.StatusOK, response.Code)
	assert.NotContains(t, response.Body.String(), first.ID)
	assert.NotContains(t, response.Body.String(), second.ID)

	var listed []apiTokenResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &listed))
	require.Len(t, listed, 2)
	assert.Equal(t, first.ID[:8], listed[0].ID)
	assert.Equal(t, second.ID[:8], listed[1].ID)
	assert.Equal(t, first.StableID(), listed[0].TokenRef)
	assert.Equal(t, second.StableID(), listed[1].TokenRef)
	assert.NotEqual(t, listed[0].TokenRef, listed[1].TokenRef)

	var payload []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.Len(t, payload, 2)
	assert.NotContains(t, payload[0], "token_id")
	assert.NotContains(t, payload[1], "token_id")
	assert.Contains(t, payload[0], "token_ref")
	assert.Contains(t, payload[1], "token_ref")
}

func TestCreateAPIToken_ReturnsCredentialOnceWithStableReference(t *testing.T) {
	store := newTokenContractTestStore(t)
	user := createTokenContractUser(t, store, "token-create-owner")

	response := httptest.NewRecorder()
	createAPIToken(response, newTokenContractRequest(store, &user, http.MethodPost, "/api/user/tokens"))

	require.Equal(t, http.StatusCreated, response.Code)
	var created apiTokenResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &created))
	assert.NotEmpty(t, created.ID)
	assert.Equal(t, created.APIToken.StableID(), created.TokenRef)
	assert.True(t, db.IsAPITokenStableID(created.TokenRef))

	tokens, err := store.GetAPITokens(user.ID)
	require.NoError(t, err)
	require.Len(t, tokens, 1)
	assert.Equal(t, created.ID, tokens[0].ID)
	assert.Equal(t, created.TokenRef, tokens[0].StableID())
}

func TestDeleteAPIToken_StableReferenceDeletesOnlyExactOwnedToken(t *testing.T) {
	store := newTokenContractTestStore(t)
	user := createTokenContractUser(t, store, "token-delete-owner")
	target := createTokenContractToken(t, store, user.ID, "delete-target-credential-0123456789")
	other := createTokenContractToken(t, store, user.ID, "delete-other-credential-0123456789")

	request := newTokenContractRequest(store, &user, http.MethodDelete, "/api/user/tokens/"+target.StableID())
	request = mux.SetURLVars(request, map[string]string{"token_id": target.StableID()})
	response := httptest.NewRecorder()
	deleteAPIToken(response, request)

	require.Equal(t, http.StatusNoContent, response.Code)
	_, err := store.GetAPIToken(target.ID)
	assert.ErrorIs(t, err, db.ErrNotFound)
	persistedOther, err := store.GetAPIToken(other.ID)
	require.NoError(t, err)
	assert.Equal(t, other.ID, persistedOther.ID)
}

func TestDeleteAPIToken_StableReferenceCannotRevokeAnotherOwnersToken(t *testing.T) {
	store := newTokenContractTestStore(t)
	owner := createTokenContractUser(t, store, "token-owner")
	otherUser := createTokenContractUser(t, store, "token-other-owner")
	token := createTokenContractToken(t, store, owner.ID, "other-owner-credential-0123456789")

	request := newTokenContractRequest(store, &otherUser, http.MethodDelete, "/api/user/tokens/"+token.StableID())
	request = mux.SetURLVars(request, map[string]string{"token_id": token.StableID()})
	response := httptest.NewRecorder()
	deleteAPIToken(response, request)

	assert.Equal(t, http.StatusNoContent, response.Code)
	persisted, err := store.GetAPIToken(token.ID)
	require.NoError(t, err)
	assert.Equal(t, owner.ID, persisted.UserID)
}

func TestDeleteAPIToken_LegacyCredentialPrefixRemainsSupported(t *testing.T) {
	store := newTokenContractTestStore(t)
	user := createTokenContractUser(t, store, "token-legacy-owner")
	token := createTokenContractToken(t, store, user.ID, "legacy-token-credential-0123456789")

	request := newTokenContractRequest(store, &user, http.MethodDelete, "/api/user/tokens/"+token.ID[:8])
	request = mux.SetURLVars(request, map[string]string{"token_id": token.ID[:8]})
	response := httptest.NewRecorder()
	deleteAPIToken(response, request)

	require.Equal(t, http.StatusNoContent, response.Code)
	_, err := store.GetAPIToken(token.ID)
	assert.True(t, errors.Is(err, db.ErrNotFound))
}
