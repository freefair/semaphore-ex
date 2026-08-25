package capabilities

import (
	"context"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type serviceFacade struct {
	provider pro_interfaces.CapabilityProvider
	service  pro_interfaces.CapabilityTestService
}

// NewServiceFacade creates the API-facing capability use-case boundary.
func NewServiceFacade(
	provider pro_interfaces.CapabilityProvider,
	service pro_interfaces.CapabilityTestService,
) pro_interfaces.CapabilityServiceFacade {
	return &serviceFacade{provider: provider, service: service}
}

func (f *serviceFacade) Resolve(
	ctx context.Context,
	request pro_interfaces.CapabilityRequest,
) (pro_interfaces.CapabilitySnapshot, error) {
	return f.provider.Resolve(ctx, request)
}

func (f *serviceFacade) Configure(
	ctx context.Context,
	request pro_interfaces.CapabilityRequest,
	configuration pro_interfaces.CapabilityConfiguration,
) (pro_interfaces.CapabilitySnapshot, error) {
	return f.provider.Configure(ctx, request, configuration)
}

func (f *serviceFacade) ListRecords(
	ctx context.Context,
	snapshot pro_interfaces.CapabilitySnapshot,
) ([]pro_interfaces.CapabilityTestRecordDTO, error) {
	records, err := f.service.ListRecords(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	result := make([]pro_interfaces.CapabilityTestRecordDTO, len(records))
	for i, record := range records {
		result[i] = toDTO(record)
	}
	return result, nil
}

func (f *serviceFacade) CreateRecord(
	ctx context.Context,
	snapshot pro_interfaces.CapabilitySnapshot,
	value string,
) (pro_interfaces.CapabilityTestRecordDTO, error) {
	record, err := f.service.CreateRecord(ctx, snapshot, value)
	if err != nil {
		return pro_interfaces.CapabilityTestRecordDTO{}, err
	}
	return toDTO(record), nil
}

func (f *serviceFacade) RunBackgroundAction(
	ctx context.Context,
	snapshot pro_interfaces.CapabilitySnapshot,
	value string,
) (pro_interfaces.CapabilityTestRecordDTO, error) {
	record, err := f.service.RunBackgroundAction(ctx, snapshot, value)
	if err != nil {
		return pro_interfaces.CapabilityTestRecordDTO{}, err
	}
	return toDTO(record), nil
}

func toDTO(record db.CapabilityTestRecord) pro_interfaces.CapabilityTestRecordDTO {
	return pro_interfaces.CapabilityTestRecordDTO{
		ID:      record.ID,
		Value:   record.Value,
		Source:  record.Source,
		Created: record.Created,
	}
}

var _ pro_interfaces.CapabilityServiceFacade = (*serviceFacade)(nil)
