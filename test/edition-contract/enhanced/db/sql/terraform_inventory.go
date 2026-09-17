package sql

import (
	"errors"
	"time"

	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/util"
)

var ErrTerraformStateLocked = errors.New("terraform state is locked")

type TerraformStoreImpl struct{ connection *coresql.SqlDbConnection }

var _ db.TerraformStore = (*TerraformStoreImpl)(nil)

func NewTerraformStore(connection *coresql.SqlDbConnection) *TerraformStoreImpl {
	return &TerraformStoreImpl{connection: connection}
}

func (d *TerraformStoreImpl) CreateTerraformInventoryAlias(alias db.TerraformInventoryAlias) (db.TerraformInventoryAlias, error) {
	if d == nil || d.connection == nil || alias.ProjectID <= 0 || alias.InventoryID <= 0 || alias.AuthKeyID <= 0 || alias.Alias == "" {
		return db.TerraformInventoryAlias{}, db.ErrInvalidOperation
	}
	result, err := d.connection.Exec(`insert into project__terraform_inventory_alias(alias, project_id, inventory_id, auth_key_id)
		select ?, ?, ?, ? where exists (select 1 from project__inventory where id=? and project_id=? and type in ('terraform-workspace', 'tofu-workspace', 'terragrunt-workspace'))
		and exists (select 1 from access_key where id=? and project_id=? and type='login_password')`, alias.Alias, alias.ProjectID, alias.InventoryID, alias.AuthKeyID, alias.InventoryID, alias.ProjectID, alias.AuthKeyID, alias.ProjectID)
	if err != nil {
		return db.TerraformInventoryAlias{}, err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return db.TerraformInventoryAlias{}, db.ErrInvalidOperation
	}
	return alias, nil
}

func (d *TerraformStoreImpl) UpdateTerraformInventoryAlias(alias db.TerraformInventoryAlias) error {
	if d == nil || d.connection == nil || alias.ProjectID <= 0 || alias.InventoryID <= 0 || alias.AuthKeyID <= 0 || alias.Alias == "" {
		return db.ErrInvalidOperation
	}
	result, err := d.connection.Exec(`update project__terraform_inventory_alias set auth_key_id=? where alias=? and project_id=? and inventory_id=?
		and exists (select 1 from access_key where id=? and project_id=? and type='login_password')`, alias.AuthKeyID, alias.Alias, alias.ProjectID, alias.InventoryID, alias.AuthKeyID, alias.ProjectID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		if _, lookupErr := d.GetTerraformInventoryAlias(alias.ProjectID, alias.InventoryID, alias.Alias); lookupErr == nil {
			return db.ErrInvalidOperation
		}
		return db.ErrNotFound
	}
	return nil
}

func (d *TerraformStoreImpl) GetTerraformInventoryAliasByAlias(alias string) (result db.TerraformInventoryAlias, err error) {
	if d == nil || d.connection == nil || alias == "" {
		return result, db.ErrInvalidOperation
	}
	err = d.connection.SelectOne(&result, "select alias, project_id, inventory_id, auth_key_id from project__terraform_inventory_alias where alias=?", alias)
	return result, err
}
func (d *TerraformStoreImpl) GetTerraformInventoryAlias(projectID, inventoryID int, aliasID string) (result db.TerraformInventoryAlias, err error) {
	if d == nil || d.connection == nil {
		return result, db.ErrInvalidOperation
	}
	err = d.connection.SelectOne(&result, "select alias, project_id, inventory_id, auth_key_id from project__terraform_inventory_alias where alias=? and project_id=? and inventory_id=?", aliasID, projectID, inventoryID)
	return result, err
}
func (d *TerraformStoreImpl) GetTerraformInventoryAliases(projectID, inventoryID int) (result []db.TerraformInventoryAlias, err error) {
	if d == nil || d.connection == nil {
		return nil, db.ErrInvalidOperation
	}
	_, err = d.connection.SelectAll(&result, "select alias, project_id, inventory_id, auth_key_id from project__terraform_inventory_alias where project_id=? and inventory_id=? order by alias", projectID, inventoryID)
	if result == nil {
		result = []db.TerraformInventoryAlias{}
	}
	return result, err
}
func (d *TerraformStoreImpl) DeleteTerraformInventoryAlias(projectID, inventoryID int, aliasID string) error {
	if d == nil || d.connection == nil {
		return db.ErrInvalidOperation
	}
	result, err := d.connection.Exec("delete from project__terraform_inventory_alias where alias=? and project_id=? and inventory_id=?", aliasID, projectID, inventoryID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return db.ErrNotFound
	}
	return nil
}

func (d *TerraformStoreImpl) CreateTerraformInventoryState(state db.TerraformInventoryState) (db.TerraformInventoryState, error) {
	return db.TerraformInventoryState{}, db.ErrInvalidOperation
}
func (d *TerraformStoreImpl) GetTerraformInventoryState(projectID, inventoryID, stateID int) (state db.TerraformInventoryState, err error) {
	if d == nil || d.connection == nil {
		return state, db.ErrInvalidOperation
	}
	err = d.connection.SelectOne(&state, "select id, project_id, inventory_id, state, created, task_id from project__terraform_inventory_state where id=? and project_id=? and inventory_id=?", stateID, projectID, inventoryID)
	return state, err
}
func (d *TerraformStoreImpl) GetTerraformInventoryStates(projectID, inventoryID int, _ db.RetrieveQueryParams) (states []db.TerraformInventoryState, err error) {
	if d == nil || d.connection == nil {
		return nil, db.ErrInvalidOperation
	}
	_, err = d.connection.SelectAll(&states, "select id, project_id, inventory_id, '' as state, created, task_id from project__terraform_inventory_state where project_id=? and inventory_id=? order by created desc, id desc", projectID, inventoryID)
	if states == nil {
		states = []db.TerraformInventoryState{}
	}
	return states, err
}
func (d *TerraformStoreImpl) DeleteTerraformInventoryState(projectID, inventoryID, stateID int) error {
	return db.ErrInvalidOperation
}
func (d *TerraformStoreImpl) GetTerraformStateCount() (count int, err error) {
	if d == nil || d.connection == nil {
		return 0, db.ErrInvalidOperation
	}
	err = d.connection.SelectOne(&count, "select count(*) from project__terraform_inventory_state")
	return
}

func (d *TerraformStoreImpl) GetLatestTerraformState(projectID, inventoryID int) (db.TerraformInventoryState, error) {
	var state db.TerraformInventoryState
	if d == nil || d.connection == nil {
		return state, db.ErrInvalidOperation
	}
	err := d.connection.SelectOne(&state, `select id, project_id, inventory_id, state, created, task_id from project__terraform_inventory_state s where project_id=? and inventory_id=? and id > coalesce((select max(state_id) from project__terraform_inventory_state_tombstone where project_id=? and inventory_id=?), 0) order by id desc limit 1`, projectID, inventoryID, projectID, inventoryID)
	return state, err
}
func (d *TerraformStoreImpl) PutTerraformState(projectID, inventoryID int, ciphertext, lockID string) error {
	if d == nil || d.connection == nil || ciphertext == "" {
		return db.ErrInvalidOperation
	}
	now := time.Now().UTC()
	result, err := d.connection.Exec(`insert into project__terraform_inventory_state(project_id, inventory_id, state, created) select ?,?,?,? where not exists (select 1 from project__terraform_inventory_lock where project_id=? and inventory_id=?) or exists (select 1 from project__terraform_inventory_lock where project_id=? and inventory_id=? and lock_id=?)`, projectID, inventoryID, ciphertext, now, projectID, inventoryID, projectID, inventoryID, lockID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return ErrTerraformStateLocked
	}
	return nil
}
func (d *TerraformStoreImpl) DeleteLatestTerraformState(projectID, inventoryID int, lockID string) error {
	if d == nil || d.connection == nil {
		return db.ErrInvalidOperation
	}
	result, err := d.connection.Exec(`insert into project__terraform_inventory_state_tombstone(project_id, inventory_id, state_id, created) select ?,?,coalesce(max(id), 0),? from project__terraform_inventory_state where project_id=? and inventory_id=? and (not exists (select 1 from project__terraform_inventory_lock where project_id=? and inventory_id=?) or exists (select 1 from project__terraform_inventory_lock where project_id=? and inventory_id=? and lock_id=?)) having count(*) > 0`, projectID, inventoryID, time.Now().UTC(), projectID, inventoryID, projectID, inventoryID, projectID, inventoryID, lockID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return ErrTerraformStateLocked
	}
	if n == 0 {
		var locks int
		if err = d.connection.SelectOne(&locks, "select count(*) from project__terraform_inventory_lock where project_id=? and inventory_id=?", projectID, inventoryID); err != nil || locks > 0 {
			return ErrTerraformStateLocked
		}
		return nil
	}
	if n != 1 {
		return ErrTerraformStateLocked
	}
	return nil
}

// DeleteTerraformStateIfCurrent appends a tombstone only when expectedStateID
// is still the current visible state, preventing a management delete from
// tombstoning a state written after its read.
func (d *TerraformStoreImpl) DeleteTerraformStateIfCurrent(projectID, inventoryID, expectedStateID int, lockID string) error {
	if d == nil || d.connection == nil || expectedStateID <= 0 {
		return db.ErrInvalidOperation
	}
	result, err := d.connection.Exec(`insert into project__terraform_inventory_state_tombstone(project_id, inventory_id, state_id, created)
		select ?,?,?,? where ? = coalesce((select max(id) from project__terraform_inventory_state where project_id=? and inventory_id=? and id > coalesce((select max(state_id) from project__terraform_inventory_state_tombstone where project_id=? and inventory_id=?), 0)), 0)
		and (not exists (select 1 from project__terraform_inventory_lock where project_id=? and inventory_id=?) or exists (select 1 from project__terraform_inventory_lock where project_id=? and inventory_id=? and lock_id=?))`,
		projectID, inventoryID, expectedStateID, time.Now().UTC(), expectedStateID, projectID, inventoryID, projectID, inventoryID, projectID, inventoryID, projectID, inventoryID, lockID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return ErrTerraformStateLocked
	}
	return nil
}
func (d *TerraformStoreImpl) AcquireTerraformStateLock(projectID, inventoryID int, lock db.TerraformStateLock) (db.TerraformStateLock, error) {
	if d == nil || d.connection == nil || lock.ID == "" || lock.Info == "" {
		return db.TerraformStateLock{}, db.ErrInvalidOperation
	}
	_, err := d.connection.Exec("insert into project__terraform_inventory_lock(project_id, inventory_id, lock_id, lock_info, created) values (?, ?, ?, ?, ?)", projectID, inventoryID, lock.ID, lock.Info, time.Now().UTC())
	if err == nil {
		return db.TerraformStateLock{}, nil
	}
	var held db.TerraformStateLock
	lookupErr := d.connection.SelectOne(&held, "select lock_id as id, lock_info as info from project__terraform_inventory_lock where project_id=? and inventory_id=?", projectID, inventoryID)
	if lookupErr == nil {
		return held, ErrTerraformStateLocked
	}
	return db.TerraformStateLock{}, err
}
func (d *TerraformStoreImpl) ReleaseTerraformStateLock(projectID, inventoryID int, lockID string) error {
	if d == nil || d.connection == nil || lockID == "" {
		return db.ErrInvalidOperation
	}
	result, err := d.connection.Exec("delete from project__terraform_inventory_lock where project_id=? and inventory_id=? and lock_id=?", projectID, inventoryID, lockID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return ErrTerraformStateLocked
	}
	return nil
}

func (d *TerraformStoreImpl) ListTerraformStateCiphertexts() ([]string, error) {
	if d == nil || d.connection == nil {
		return nil, db.ErrInvalidOperation
	}
	var values []string
	_, err := d.connection.SelectAll(&values, "select state from project__terraform_inventory_state")
	return values, err
}

func (d *TerraformStoreImpl) RekeyTerraformStates(oldKey string) error {
	if d == nil || d.connection == nil || util.Config == nil || !util.Config.AccessKeyEncryptionEnabled() {
		return db.ErrInvalidOperation
	}
	var states []db.TerraformInventoryState
	if _, err := d.connection.SelectAll(&states, "select id, state from project__terraform_inventory_state"); err != nil {
		return err
	}
	for _, state := range states {
		var plain []byte
		var err error
		if util.SecretKeyID(state.State) == "" {
			plain = []byte(state.State)
		} else if oldKey == "" {
			plain, err = util.Config.DecryptAccessSecret(state.State)
		} else {
			plain, err = util.Config.DecryptAccessSecretWithKey(state.State, oldKey)
		}
		if err != nil {
			return err
		}
		ciphertext, err := util.Config.EncryptAccessSecret(plain)
		if err != nil {
			return err
		}
		if _, err = d.connection.Exec("update project__terraform_inventory_state set state=? where id=?", ciphertext, state.ID); err != nil {
			return err
		}
	}
	return nil
}
