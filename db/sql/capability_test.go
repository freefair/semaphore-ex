package sql

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCapabilityDataSurvivesConfigurationTransitions(t *testing.T) {
	store := InitConfigCreateTestStore()
	defer store.Close()

	now := time.Unix(1_700_000_000, 0).UTC()
	require.NoError(t, store.SaveCapabilityConfig(db.CapabilityConfig{
		CapabilityID: "lifecycle_test",
		State:        "active",
		Updated:      now,
	}))
	record, err := store.CreateCapabilityTestRecord(db.CapabilityTestRecord{
		Value:   "preserved",
		Source:  "api",
		Created: now,
	})
	require.NoError(t, err)
	assert.NotZero(t, record.ID)

	require.NoError(t, store.SaveCapabilityConfig(db.CapabilityConfig{
		CapabilityID: "lifecycle_test",
		State:        "disabled",
		Updated:      now.Add(time.Minute),
	}))
	require.NoError(t, store.SaveCapabilityConfig(db.CapabilityConfig{
		CapabilityID: "lifecycle_test",
		State:        "active",
		Updated:      now.Add(2 * time.Minute),
	}))

	records, err := store.GetCapabilityTestRecords()
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "preserved", records[0].Value)
}

func TestMigration_2_20_2RollsBackCapabilityTables(t *testing.T) {
	store := InitConfigCreateTestStore()
	defer store.Close()

	require.NoError(t, store.SaveCapabilityConfig(db.CapabilityConfig{
		CapabilityID: "lifecycle_test",
		State:        "disabled",
		Updated:      time.Now().UTC(),
	}))

	require.NoError(t, db.Rollback(store, "2.20.1"))
	_, err := store.exec("select count(1) from capability_config")
	assert.Error(t, err)
	_, err = store.exec("select count(1) from capability_test_record")
	assert.Error(t, err)
}
