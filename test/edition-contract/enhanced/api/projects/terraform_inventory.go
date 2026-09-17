package projects

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/random"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
)

const terraformAliasBodyLimit int64 = 8 * 1024

type terraformInventoryController struct {
	store db.TerraformStore
}

type terraformInventoryAliasResponse struct {
	ID          string `json:"id"`
	ProjectID   int    `json:"project_id"`
	InventoryID int    `json:"inventory_id"`
	AuthKeyID   int    `json:"auth_key_id"`
	URL         string `json:"url"`
}

type terraformInventoryAliasInput struct {
	ID          string `json:"id"`
	ProjectID   int    `json:"project_id"`
	InventoryID int    `json:"inventory_id"`
	AuthKeyID   int    `json:"auth_key_id"`
	URL         string `json:"url"`
}

var _ pro_interfaces.TerraformInventoryController = (*terraformInventoryController)(nil)

func NewTerraformInventoryController(store db.TerraformStore) pro_interfaces.TerraformInventoryController {
	return &terraformInventoryController{store: store}
}

func (c *terraformInventoryController) GetTerraformInventoryAliases(w http.ResponseWriter, r *http.Request) {
	project, inventory, ok := terraformInventoryContext(w, r)
	if !ok || c.store == nil {
		if ok {
			helpers.WriteErrorStatus(w, "Terraform backend is unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	aliases, err := c.store.GetTerraformInventoryAliases(project.ID, inventory.ID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	response := make([]terraformInventoryAliasResponse, 0, len(aliases))
	for _, alias := range aliases {
		response = append(response, terraformInventoryAliasPublic(alias))
	}
	helpers.WriteJSON(w, http.StatusOK, response)
}

func (c *terraformInventoryController) AddTerraformInventoryAlias(w http.ResponseWriter, r *http.Request) {
	project, inventory, ok := terraformInventoryContext(w, r)
	if !ok || c.store == nil {
		if ok {
			helpers.WriteErrorStatus(w, "Terraform backend is unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	var input terraformInventoryAliasInput
	if !decodeTerraformAliasInput(w, r, &input) || !input.matchesScope(project, inventory, "") || input.AuthKeyID <= 0 {
		helpers.WriteErrorStatus(w, "invalid Terraform alias request", http.StatusBadRequest)
		return
	}
	created, err := c.store.CreateTerraformInventoryAlias(db.TerraformInventoryAlias{
		ProjectID: project.ID, InventoryID: inventory.ID, AuthKeyID: input.AuthKeyID, Alias: random.String(32),
	})
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusCreated, terraformInventoryAliasPublic(created))
}

func (c *terraformInventoryController) GetTerraformInventoryAlias(w http.ResponseWriter, r *http.Request) {
	project, inventory, aliasID, ok := c.terraformInventoryAliasContext(w, r)
	if !ok || c.store == nil {
		if ok {
			helpers.WriteErrorStatus(w, "Terraform backend is unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	alias, err := c.store.GetTerraformInventoryAlias(project.ID, inventory.ID, aliasID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, terraformInventoryAliasPublic(alias))
}

func (c *terraformInventoryController) DeleteTerraformInventoryAlias(w http.ResponseWriter, r *http.Request) {
	project, inventory, aliasID, ok := c.terraformInventoryAliasContext(w, r)
	if !ok || c.store == nil {
		if ok {
			helpers.WriteErrorStatus(w, "Terraform backend is unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	if err := c.store.DeleteTerraformInventoryAlias(project.ID, inventory.ID, aliasID); err != nil {
		helpers.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *terraformInventoryController) SetTerraformInventoryAliasAccessKey(w http.ResponseWriter, r *http.Request) {
	project, inventory, aliasID, ok := c.terraformInventoryAliasContext(w, r)
	if !ok || c.store == nil {
		if ok {
			helpers.WriteErrorStatus(w, "Terraform backend is unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	var input terraformInventoryAliasInput
	if !decodeTerraformAliasInput(w, r, &input) || !input.matchesScope(project, inventory, aliasID) || input.AuthKeyID <= 0 {
		helpers.WriteErrorStatus(w, "invalid Terraform alias request", http.StatusBadRequest)
		return
	}
	alias, err := c.store.GetTerraformInventoryAlias(project.ID, inventory.ID, aliasID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	alias.AuthKeyID = input.AuthKeyID
	if err = c.store.UpdateTerraformInventoryAlias(alias); err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, terraformInventoryAliasPublic(alias))
}

func (c *terraformInventoryController) GetTerraformInventoryStates(w http.ResponseWriter, r *http.Request) {
	project, inventory, ok := terraformInventoryContext(w, r)
	if !ok || c.store == nil {
		if ok {
			helpers.WriteErrorStatus(w, "Terraform backend is unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	states, err := c.store.GetTerraformInventoryStates(project.ID, inventory.ID, helpers.QueryParams(r.URL))
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, states)
}

func (c *terraformInventoryController) GetTerraformInventoryLatestState(w http.ResponseWriter, r *http.Request) {
	project, inventory, ok := terraformInventoryContext(w, r)
	if !ok || c.store == nil {
		if ok {
			helpers.WriteErrorStatus(w, "Terraform backend is unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	backend, ok := c.store.(interface {
		GetLatestTerraformState(int, int) (db.TerraformInventoryState, error)
	})
	if !ok {
		helpers.WriteErrorStatus(w, "Terraform backend is unavailable", http.StatusServiceUnavailable)
		return
	}
	state, err := backend.GetLatestTerraformState(project.ID, inventory.ID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	c.writeTerraformState(w, state)
}

func (c *terraformInventoryController) GetTerraformInventoryState(w http.ResponseWriter, r *http.Request) {
	project, inventory, ok := terraformInventoryContext(w, r)
	if !ok || c.store == nil {
		if ok {
			helpers.WriteErrorStatus(w, "Terraform backend is unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	stateID, ok := helpers.GetIntParamOrAbort("state_id", w, r)
	if !ok {
		return
	}
	state, err := c.store.GetTerraformInventoryState(project.ID, inventory.ID, stateID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	c.writeTerraformState(w, state)
}

func (c *terraformInventoryController) DeleteTerraformInventoryState(w http.ResponseWriter, r *http.Request) {
	project, inventory, ok := terraformInventoryContext(w, r)
	if !ok || c.store == nil {
		if ok {
			helpers.WriteErrorStatus(w, "Terraform backend is unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	stateID, ok := helpers.GetIntParamOrAbort("state_id", w, r)
	if !ok {
		return
	}
	backend, ok := c.store.(interface {
		GetLatestTerraformState(int, int) (db.TerraformInventoryState, error)
		DeleteTerraformStateIfCurrent(int, int, int, string) error
	})
	if !ok {
		helpers.WriteErrorStatus(w, "Terraform backend is unavailable", http.StatusServiceUnavailable)
		return
	}
	latest, err := backend.GetLatestTerraformState(project.ID, inventory.ID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	if latest.ID != stateID {
		helpers.WriteErrorStatus(w, "only the current Terraform state can be deleted", http.StatusConflict)
		return
	}
	if err = backend.DeleteTerraformStateIfCurrent(project.ID, inventory.ID, stateID, ""); err != nil {
		helpers.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *terraformInventoryController) writeTerraformState(w http.ResponseWriter, state db.TerraformInventoryState) {
	if util.Config == nil || !util.Config.AccessKeyEncryptionEnabled() || util.SecretKeyID(state.State) == "" {
		helpers.WriteErrorStatus(w, "Terraform state requires encryption migration", http.StatusServiceUnavailable)
		return
	}
	plain, err := util.Config.DecryptAccessSecret(state.State)
	if err != nil {
		helpers.WriteErrorStatus(w, "Terraform state is unavailable", http.StatusServiceUnavailable)
		return
	}
	state.State = string(plain)
	helpers.WriteJSON(w, http.StatusOK, state)
}

func (c *terraformInventoryController) terraformInventoryAliasContext(w http.ResponseWriter, r *http.Request) (db.Project, db.Inventory, string, bool) {
	project, inventory, ok := terraformInventoryContext(w, r)
	if !ok {
		return db.Project{}, db.Inventory{}, "", false
	}
	aliasID, ok := helpers.GetStrParamOrAbort("alias_id", w, r)
	if !ok || strings.TrimSpace(aliasID) == "" || len(aliasID) > 100 {
		if ok {
			helpers.WriteErrorStatus(w, "invalid alias identifier", http.StatusBadRequest)
		}
		return db.Project{}, db.Inventory{}, "", false
	}
	return project, inventory, aliasID, true
}

func terraformInventoryContext(w http.ResponseWriter, r *http.Request) (db.Project, db.Inventory, bool) {
	project, projectOK := helpers.GetFromContext(r, "project").(db.Project)
	inventory, inventoryOK := helpers.GetFromContext(r, "inventory").(db.Inventory)
	if !projectOK || !inventoryOK || project.ID <= 0 || inventory.ID <= 0 || inventory.ProjectID != project.ID || !terraformWorkspaceInventory(inventory.Type) {
		helpers.WriteErrorStatus(w, "Terraform inventory not found", http.StatusNotFound)
		return db.Project{}, db.Inventory{}, false
	}
	return project, inventory, true
}

func terraformWorkspaceInventory(kind db.InventoryType) bool {
	return kind == db.InventoryTerraformWorkspace || kind == db.InventoryTofuWorkspace || kind == db.InventoryTerragruntWorkspace
}

func decodeTerraformAliasInput(w http.ResponseWriter, r *http.Request, out *terraformInventoryAliasInput) bool {
	decoder := json.NewDecoder(io.LimitReader(r.Body, terraformAliasBodyLimit+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		helpers.WriteErrorStatus(w, "invalid Terraform alias request", http.StatusBadRequest)
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		helpers.WriteErrorStatus(w, "invalid Terraform alias request", http.StatusBadRequest)
		return false
	}
	return true
}

func (input terraformInventoryAliasInput) matchesScope(project db.Project, inventory db.Inventory, aliasID string) bool {
	if input.ProjectID != 0 && input.ProjectID != project.ID || input.InventoryID != 0 && input.InventoryID != inventory.ID {
		return false
	}
	if aliasID == "" {
		return input.ID == "" && input.URL == ""
	}
	return (input.ID == "" || input.ID == aliasID) && (input.URL == "" || input.URL == util.GetPublicAliasURL("terraform", aliasID))
}

func terraformInventoryAliasPublic(alias db.TerraformInventoryAlias) terraformInventoryAliasResponse {
	return terraformInventoryAliasResponse{ID: alias.Alias, ProjectID: alias.ProjectID, InventoryID: alias.InventoryID, AuthKeyID: alias.AuthKeyID, URL: util.GetPublicAliasURL("terraform", alias.Alias)}
}
