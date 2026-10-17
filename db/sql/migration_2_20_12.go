package sql

import (
	"fmt"

	"github.com/go-gorp/gorp/v3"
)

type migration_2_20_12 struct {
	db *SqlDb
}

// PreApply rejects legacy memberships whose NULL role_id cannot be represented
// by either a built-in role or an existing global role. Continuing would create
// a dangling relation and fail closed only after the schema change committed.
func (m migration_2_20_12) PreApply(tx *gorp.Transaction) error {
	unknown, err := tx.SelectInt(m.db.PrepareQuery(`
		select count(*) from project__user pu
		where pu.role_id is null
		  and lower(pu.role) not in ('owner', 'manager', 'task_runner', 'guest')
		  and not exists (
			select 1 from role r where (r.project_id=pu.project_id or r.project_id is null) and lower(r.slug)=lower(pu.role)
		  )`))
	if err != nil {
		return fmt.Errorf("inspect project role migration references: %w", err)
	}
	if unknown > 0 {
		return fmt.Errorf("cannot apply 2.20.12: %d memberships reference an unknown role", unknown)
	}
	danglingIDs, err := tx.SelectInt(m.db.PrepareQuery(`
		select count(*) from project__user pu
		where pu.role_id is not null
		  and not exists (
			select 1 from role r
			where r.role_id=pu.role_id and (r.project_id=pu.project_id or r.project_id is null)
		  )`))
	if err != nil {
		return fmt.Errorf("inspect project role migration identities: %w", err)
	}
	if danglingIDs > 0 {
		return fmt.Errorf("cannot apply 2.20.12: %d memberships reference a missing or out-of-scope role ID", danglingIDs)
	}
	return nil
}

// PreRollback refuses a rollback that would erase EX-only membership state.
// The legacy schema can represent one role and no directory provenance only.
func (m migration_2_20_12) PreRollback(tx *gorp.Transaction) error {
	multiRoleMembers, err := tx.SelectInt(m.db.PrepareQuery(`
		select count(*) from (
			select project_id, user_id from project__user_role
			group by project_id, user_id having count(*) > 1
		) nonrepresentable`))
	if err != nil {
		return fmt.Errorf("inspect project role rollback safety: %w", err)
	}
	if multiRoleMembers > 0 {
		return fmt.Errorf("cannot roll back 2.20.12: %d memberships have multiple roles", multiRoleMembers)
	}
	provenanceAssignments, err := tx.SelectInt(m.db.PrepareQuery(`
		select count(*) from project__user_role
		where ldap_group_managed_assignment_id is not null
		   or oidc_group_managed_assignment_id is not null`))
	if err != nil {
		return fmt.Errorf("inspect project role provenance rollback safety: %w", err)
	}
	if provenanceAssignments > 0 {
		return fmt.Errorf("cannot roll back 2.20.12: %d role assignments have directory provenance", provenanceAssignments)
	}
	invalidBuiltinTemplateRoles, err := tx.SelectInt(m.db.PrepareQuery(`
		select count(*) from project__template_role
		where role_id like 'builtin:%'
		  and (lower(role_slug) <> lower(substr(role_id, 9))
		       or lower(substr(role_id, 9)) not in ('owner', 'manager', 'task_runner', 'guest'))`))
	if err != nil {
		return fmt.Errorf("inspect built-in template role rollback safety: %w", err)
	}
	if invalidBuiltinTemplateRoles > 0 {
		return fmt.Errorf("cannot roll back 2.20.12: %d built-in template grants have mismatched role slugs", invalidBuiltinTemplateRoles)
	}
	return nil
}
