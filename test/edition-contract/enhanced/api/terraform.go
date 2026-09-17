package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/services/server"
	"github.com/semaphoreui/semaphore/util"
)

const terraformStateBodyLimit int64 = 16 << 20

var errTerraformLocked = errors.New("terraform state is locked")

type terraformBackendStore interface {
	db.TerraformStore
	GetLatestTerraformState(projectID, inventoryID int) (db.TerraformInventoryState, error)
	PutTerraformState(projectID, inventoryID int, ciphertext, lockID string) error
	DeleteLatestTerraformState(projectID, inventoryID int, lockID string) error
	AcquireTerraformStateLock(projectID, inventoryID int, lock db.TerraformStateLock) (db.TerraformStateLock, error)
	ReleaseTerraformStateLock(projectID, inventoryID int, lockID string) error
}

type TerraformController struct {
	encryption server.AccessKeyEncryptionService
	aliases    db.TerraformStore
	keys       db.AccessKeyManager
}

func NewTerraformController(encryption server.AccessKeyEncryptionService, aliases db.TerraformStore, keys db.AccessKeyManager) *TerraformController {
	return &TerraformController{encryption: encryption, aliases: aliases, keys: keys}
}

func (c *TerraformController) TerraformInventoryAliasMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aliasID, ok := helpers.GetStrParamOrAbort("alias", w, r)
		if !ok || c.aliases == nil || c.keys == nil || c.encryption == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		alias, err := c.aliases.GetTerraformInventoryAliasByAlias(aliasID)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		key, err := c.keys.GetAccessKey(alias.ProjectID, alias.AuthKeyID)
		if err != nil || key.Type != db.AccessKeyLoginPassword || key.ProjectID == nil || *key.ProjectID != alias.ProjectID {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err = c.encryption.DeserializeSecret(&key); err != nil || key.LoginPassword.Login == "" || key.LoginPassword.Password == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		username, password, authenticated := r.BasicAuth()
		providedUser, expectedUser := sha256.Sum256([]byte(username)), sha256.Sum256([]byte(key.LoginPassword.Login))
		providedPassword, expectedPassword := sha256.Sum256([]byte(password)), sha256.Sum256([]byte(key.LoginPassword.Password))
		userMatch := subtle.ConstantTimeCompare(providedUser[:], expectedUser[:])
		passwordMatch := subtle.ConstantTimeCompare(providedPassword[:], expectedPassword[:])
		if !authenticated || userMatch&passwordMatch != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="terraform-state"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		r = helpers.SetContextValue(r, "terraform_alias", alias)
		next.ServeHTTP(w, r)
	})
}

func (c *TerraformController) GetTerraformState(w http.ResponseWriter, r *http.Request) {
	store, alias, ok := c.backendContext(w, r)
	if !ok {
		return
	}
	state, err := store.GetLatestTerraformState(alias.ProjectID, alias.InventoryID)
	if errors.Is(err, db.ErrNotFound) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if util.SecretKeyID(state.State) == "" {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	plain, err := util.Config.DecryptAccessSecret(state.State)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(plain)
}

func (c *TerraformController) AddTerraformState(w http.ResponseWriter, r *http.Request) {
	store, alias, ok := c.backendContext(w, r)
	if !ok {
		return
	}
	body, ok := readTerraformState(w, r)
	if !ok {
		return
	}
	ciphertext, err := util.Config.EncryptAccessSecret(body)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if err = store.PutTerraformState(alias.ProjectID, alias.InventoryID, ciphertext, r.URL.Query().Get("ID")); err != nil {
		c.writeTerraformLockError(w, err, db.TerraformStateLock{})
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (c *TerraformController) DeleteTerraformState(w http.ResponseWriter, r *http.Request) {
	store, alias, ok := c.backendContext(w, r)
	if !ok {
		return
	}
	if err := store.DeleteLatestTerraformState(alias.ProjectID, alias.InventoryID, r.URL.Query().Get("ID")); err != nil {
		c.writeTerraformLockError(w, err, db.TerraformStateLock{})
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (c *TerraformController) LockTerraformState(w http.ResponseWriter, r *http.Request) {
	store, alias, ok := c.backendContext(w, r)
	if !ok {
		return
	}
	body, ok := readTerraformState(w, r)
	if !ok {
		return
	}
	var input struct {
		ID string `json:"ID"`
	}
	if json.Unmarshal(body, &input) != nil || strings.TrimSpace(input.ID) == "" || len(input.ID) > 255 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	projection, marshalErr := json.Marshal(struct {
		ID string `json:"ID"`
	}{ID: input.ID})
	if marshalErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	held, err := store.AcquireTerraformStateLock(alias.ProjectID, alias.InventoryID, db.TerraformStateLock{ID: input.ID, Info: string(projection)})
	if err != nil {
		c.writeTerraformLockError(w, err, held)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (c *TerraformController) UnlockTerraformState(w http.ResponseWriter, r *http.Request) {
	store, alias, ok := c.backendContext(w, r)
	if !ok {
		return
	}
	body, ok := readTerraformState(w, r)
	if !ok {
		return
	}
	var input struct {
		ID string `json:"ID"`
	}
	if json.Unmarshal(body, &input) != nil || strings.TrimSpace(input.ID) == "" || len(input.ID) > 255 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if err := store.ReleaseTerraformStateLock(alias.ProjectID, alias.InventoryID, input.ID); err != nil {
		c.writeTerraformLockError(w, err, db.TerraformStateLock{})
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (c *TerraformController) backendContext(w http.ResponseWriter, r *http.Request) (terraformBackendStore, db.TerraformInventoryAlias, bool) {
	store, ok := c.aliases.(terraformBackendStore)
	alias, aliasOK := helpers.GetFromContext(r, "terraform_alias").(db.TerraformInventoryAlias)
	if !ok || !aliasOK || util.Config == nil || !util.Config.AccessKeyEncryptionEnabled() {
		w.WriteHeader(http.StatusServiceUnavailable)
		return nil, db.TerraformInventoryAlias{}, false
	}
	return store, alias, true
}
func readTerraformState(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, terraformStateBodyLimit+1))
	if err != nil || len(body) == 0 || int64(len(body)) > terraformStateBodyLimit {
		w.WriteHeader(http.StatusBadRequest)
		return nil, false
	}
	return body, true
}
func (c *TerraformController) writeTerraformLockError(w http.ResponseWriter, err error, held db.TerraformStateLock) {
	if errors.Is(err, errTerraformLocked) || strings.Contains(err.Error(), "locked") {
		if held.Info != "" {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(http.StatusConflict)
		if held.Info != "" {
			_, _ = w.Write([]byte(held.Info))
		}
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
}
