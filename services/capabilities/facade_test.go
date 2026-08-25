package capabilities

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testProvider struct {
	snapshot pro_interfaces.CapabilitySnapshot
	err      error
}

func (p testProvider) Resolve(context.Context, pro_interfaces.CapabilityRequest) (pro_interfaces.CapabilitySnapshot, error) {
	return p.snapshot, p.err
}

func (p testProvider) Configure(context.Context, pro_interfaces.CapabilityRequest, pro_interfaces.CapabilityConfiguration) (pro_interfaces.CapabilitySnapshot, error) {
	return p.snapshot, p.err
}

type testService struct {
	record db.CapabilityTestRecord
	err    error
}

func (s testService) ListRecords(context.Context, pro_interfaces.CapabilitySnapshot) ([]db.CapabilityTestRecord, error) {
	return []db.CapabilityTestRecord{s.record}, s.err
}

func (s testService) CreateRecord(context.Context, pro_interfaces.CapabilitySnapshot, string) (db.CapabilityTestRecord, error) {
	return s.record, s.err
}

func (s testService) RunBackgroundAction(context.Context, pro_interfaces.CapabilitySnapshot, string) (db.CapabilityTestRecord, error) {
	return s.record, s.err
}

func TestServiceFacadeDelegatesProviderOperations(t *testing.T) {
	snapshot := pro_interfaces.NewCapabilitySnapshot(
		pro_interfaces.CapabilityRequest{At: time.Now()},
		nil,
	)
	facade := NewServiceFacade(testProvider{snapshot: snapshot}, testService{})

	resolved, err := facade.Resolve(context.Background(), pro_interfaces.CapabilityRequest{})
	require.NoError(t, err)
	assert.Equal(t, snapshot.Request(), resolved.Request())

	configured, err := facade.Configure(
		context.Background(),
		pro_interfaces.CapabilityRequest{},
		pro_interfaces.CapabilityConfiguration{},
	)
	require.NoError(t, err)
	assert.Equal(t, snapshot.Request(), configured.Request())
}

func TestServiceFacadeMapsCreatedRecordsAndPropagatesErrors(t *testing.T) {
	created := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	record := db.CapabilityTestRecord{ID: 3, Value: "kept", Source: "worker", Created: created}
	facade := NewServiceFacade(testProvider{}, testService{record: record})

	createdDTO, err := facade.CreateRecord(context.Background(), pro_interfaces.CapabilitySnapshot{}, record.Value)
	require.NoError(t, err)
	assert.Equal(t, pro_interfaces.CapabilityTestRecordDTO{
		ID: 3, Value: "kept", Source: "worker", Created: created,
	}, createdDTO)

	workerDTO, err := facade.RunBackgroundAction(context.Background(), pro_interfaces.CapabilitySnapshot{}, record.Value)
	require.NoError(t, err)
	assert.Equal(t, createdDTO, workerDTO)

	expected := errors.New("service failed")
	failing := NewServiceFacade(testProvider{err: expected}, testService{err: expected})
	_, err = failing.Resolve(context.Background(), pro_interfaces.CapabilityRequest{})
	assert.ErrorIs(t, err, expected)
	_, err = failing.ListRecords(context.Background(), pro_interfaces.CapabilitySnapshot{})
	assert.ErrorIs(t, err, expected)
	_, err = failing.CreateRecord(context.Background(), pro_interfaces.CapabilitySnapshot{}, "value")
	assert.ErrorIs(t, err, expected)
	_, err = failing.RunBackgroundAction(context.Background(), pro_interfaces.CapabilitySnapshot{}, "value")
	assert.ErrorIs(t, err, expected)
}

func TestServiceFacadeMapsRecordsToDTOs(t *testing.T) {
	created := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	facade := NewServiceFacade(
		testProvider{},
		testService{record: db.CapabilityTestRecord{ID: 3, Value: "kept", Source: "api", Created: created}},
	)

	records, err := facade.ListRecords(context.Background(), pro_interfaces.CapabilitySnapshot{})

	require.NoError(t, err)
	assert.Equal(t, []pro_interfaces.CapabilityTestRecordDTO{{
		ID: 3, Value: "kept", Source: "api", Created: created,
	}}, records)
}
