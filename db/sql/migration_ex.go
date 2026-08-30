package sql

import (
	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
)

func (d *SqlDb) preflightMigration22030Rollback(tx *gorp.Transaction) error {
	builtInAdministrators, err := tx.SelectInt(d.PrepareQuery(
		"select count(1) from `user` where admin=true"))
	if err != nil {
		return err
	}
	delegatedAdministrators, err := tx.SelectInt(d.PrepareQuery(
		"select count(distinct a.user_id) from user__global_role a "+
			"join `role` r on r.role_id=a.role_id and r.project_id is null "+
			"where (r.global_permissions & ?) = ?"),
		db.CanManageGlobalRoles,
		db.CanManageGlobalRoles,
	)
	if err != nil {
		return err
	}
	if builtInAdministrators == 0 && delegatedAdministrators > 0 {
		return db.ErrLastGlobalAdministrator
	}

	deniedTemplatePermissions, err := tx.SelectInt(d.PrepareQuery(
		"select count(1) from project__template_role where denied_permissions<>0"))
	if err != nil {
		return err
	}
	if deniedTemplatePermissions > 0 {
		return db.ErrInvalidOperation
	}
	return nil
}
