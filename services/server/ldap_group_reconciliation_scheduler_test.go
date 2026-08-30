package server

import (
	"context"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
)

type ldapGroupSchedulerServiceStub struct {
	providers []pro_interfaces.LDAPProviderConfiguration
	mappings  map[string][]pro_interfaces.LDAPGroupMapping
	requests  []pro_interfaces.LDAPGroupPreviewRequest
}

func (s *ldapGroupSchedulerServiceStub) Providers(context.Context) ([]pro_interfaces.LDAPProviderConfiguration, error) {
	return s.providers, nil
}

func (s *ldapGroupSchedulerServiceStub) GroupMappings(
	_ context.Context,
	providerID string,
) ([]pro_interfaces.LDAPGroupMapping, error) {
	return s.mappings[providerID], nil
}

func (s *ldapGroupSchedulerServiceStub) ReconcileGroupMappings(
	_ context.Context,
	request pro_interfaces.LDAPGroupPreviewRequest,
) (pro_interfaces.LDAPGroupPreview, error) {
	s.requests = append(s.requests, request)
	return pro_interfaces.LDAPGroupPreview{}, nil
}

func TestLDAPGroupSchedulerRunsOnlyActiveProvidersWithMappings(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	service := &ldapGroupSchedulerServiceStub{
		providers: []pro_interfaces.LDAPProviderConfiguration{
			{ID: "active", State: pro_interfaces.LDAPStateActive},
			{ID: "empty", State: pro_interfaces.LDAPStateActive},
			{ID: "disabled", State: pro_interfaces.LDAPStateDisabled},
		},
		mappings: map[string][]pro_interfaces.LDAPGroupMapping{
			"active": {{ID: "mapped"}},
		},
	}
	scheduler := NewLDAPGroupReconciliationScheduler(service)

	scheduler.tick(context.Background(), now)

	if assert.Len(t, service.requests, 1) {
		assert.Equal(t, "active", service.requests[0].ProviderID)
		assert.Equal(t, "scheduled", service.requests[0].Source)
		assert.Equal(t, now, service.requests[0].Now)
	}
}
