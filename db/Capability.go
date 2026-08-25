package db

import "time"

// CapabilityConfig stores non-secret provider configuration.
type CapabilityConfig struct {
	CapabilityID string     `db:"capability_id" json:"capability_id"`
	State        string     `db:"state" json:"state"`
	ExpiresAt    *time.Time `db:"expires_at" json:"expires_at,omitempty"`
	Updated      time.Time  `db:"updated" json:"updated"`
}

// CapabilityTestRecord is durable lifecycle-test data that survives downgrade.
type CapabilityTestRecord struct {
	ID      int       `db:"id" json:"id"`
	Value   string    `db:"value" json:"value"`
	Source  string    `db:"source" json:"source"`
	Created time.Time `db:"created" json:"created"`
}

// CapabilityRepository persists provider configuration and lifecycle-test data.
type CapabilityRepository interface {
	GetCapabilityConfig(capabilityID string) (CapabilityConfig, error)
	SaveCapabilityConfig(config CapabilityConfig) error
	CreateCapabilityTestRecord(record CapabilityTestRecord) (CapabilityTestRecord, error)
	GetCapabilityTestRecords() ([]CapabilityTestRecord, error)
}
