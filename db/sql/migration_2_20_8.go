package sql

import (
	"github.com/go-gorp/gorp/v3"
)

type migration_2_20_8 struct {
	db *SqlDb
}

// PostApply gives every existing workflow template its first revision and
// attaches the template's current nodes and edges to it. EX runs keep their
// immutable WorkflowVersion snapshots, so their nullable upstream scaffold
// reference deliberately remains unset.
//
// Idempotent: templates that already have a revision are skipped and only
// rows without a revision are attached, so a partially applied upgrade can be
// re-run safely.
func (m migration_2_20_8) PostApply(tx *gorp.Transaction) error {
	queries := []string{
		"insert into project__workflow_revision (project_id, workflow_template_id, `number`, created) " +
			"select t.project_id, t.id, 1, CURRENT_TIMESTAMP from project__workflow_template t " +
			"where not exists (select 1 from project__workflow_revision r where r.workflow_template_id = t.id)",

		"update project__workflow_node set revision_id = " +
			"(select r.id from project__workflow_revision r where r.workflow_template_id = project__workflow_node.workflow_template_id and r.`number` = 1) " +
			"where revision_id is null",

		"update project__workflow_edge set revision_id = " +
			"(select r.id from project__workflow_revision r where r.workflow_template_id = project__workflow_edge.workflow_template_id and r.`number` = 1) " +
			"where revision_id is null",
	}

	for _, query := range queries {
		if _, err := tx.Exec(m.db.PrepareQuery(query)); err != nil {
			return err
		}
	}

	return nil
}
