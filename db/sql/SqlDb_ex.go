package sql

import (
	"github.com/go-gorp/gorp/v3"
)

func (d *SqlDbConnection) Begin() (*gorp.Transaction, error) {
	return d.sql.Begin()
}
