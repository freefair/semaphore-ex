package sql

import (
	"errors"

	"github.com/semaphoreui/semaphore/db"
)

func (d *SqlDb) GetCapabilityConfig(capabilityID string) (config db.CapabilityConfig, err error) {
	err = d.selectOne(
		&config,
		"select capability_id, state, expires_at, updated from capability_config where capability_id=?",
		capabilityID,
	)
	return
}

func (d *SqlDb) SaveCapabilityConfig(config db.CapabilityConfig) error {
	_, err := d.GetCapabilityConfig(config.CapabilityID)
	switch {
	case err == nil:
		_, err = d.exec(
			"update capability_config set state=?, expires_at=?, updated=? where capability_id=?",
			config.State,
			config.ExpiresAt,
			config.Updated,
			config.CapabilityID,
		)
		return err
	case errors.Is(err, db.ErrNotFound):
		_, err = d.exec(
			"insert into capability_config (capability_id, state, expires_at, updated) values (?, ?, ?, ?)",
			config.CapabilityID,
			config.State,
			config.ExpiresAt,
			config.Updated,
		)
		return err
	default:
		return err
	}
}

func (d *SqlDb) CreateCapabilityTestRecord(record db.CapabilityTestRecord) (db.CapabilityTestRecord, error) {
	id, err := d.insert(
		"id",
		"insert into capability_test_record (value, source, created) values (?, ?, ?)",
		record.Value,
		record.Source,
		record.Created,
	)
	if err != nil {
		return db.CapabilityTestRecord{}, err
	}
	record.ID = id
	return record, nil
}

func (d *SqlDb) GetCapabilityTestRecords() (records []db.CapabilityTestRecord, err error) {
	records = make([]db.CapabilityTestRecord, 0)
	_, err = d.selectAll(
		&records,
		"select id, value, source, created from capability_test_record order by id",
	)
	return
}
