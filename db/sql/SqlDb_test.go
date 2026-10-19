package sql

import (
	"strings"
	"testing"

	"github.com/go-gorp/gorp/v3"
)

func TestValidatePort(t *testing.T) {
	d := SqlDb{}
	q := d.connection.prepareQueryWithDialect("select * from `test` where id = ?, email = ?", gorp.PostgresDialect{})
	if q != "select * from \"test\" where id = $1, email = $2" {
		t.Error("invalid postgres query")
	}
}

func TestProjectMemberMappedRolesQueryPreparesForPostgres(t *testing.T) {
	d := SqlDb{}
	query := d.connection.prepareQueryWithDialect(projectMemberMappedRolesQuery, gorp.PostgresDialect{})
	if !strings.Contains(query, `join "user" u`) || !strings.Contains(query, "project_id=$1") {
		t.Fatalf("invalid PostgreSQL mapped-role query: %s", query)
	}
}
