package features

import (
	"context"
	"errors"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type communityCapabilityProvider struct{}

// NewCapabilityProvider returns the Community provider, which cannot activate
// enhanced capabilities even when called directly.
func NewCapabilityProvider(_ db.CapabilityRepository) pro_interfaces.CapabilityProvider {
	return &communityCapabilityProvider{}
}

func (p *communityCapabilityProvider) Resolve(
	_ context.Context,
	request pro_interfaces.CapabilityRequest,
) (pro_interfaces.CapabilitySnapshot, error) {
	if request.At.IsZero() {
		return pro_interfaces.CapabilitySnapshot{}, errors.New("capability resolution time is required")
	}
	return pro_interfaces.NewCapabilitySnapshot(
		request,
		[]pro_interfaces.CapabilityDecision{
			communityUnavailableDecision(pro_interfaces.CapabilityLifecycleTest),
			communityUnavailableDecision(pro_interfaces.CapabilityRuntimeSecrets),
		},
	), nil
}

func (p *communityCapabilityProvider) Configure(
	_ context.Context,
	_ pro_interfaces.CapabilityRequest,
	configuration pro_interfaces.CapabilityConfiguration,
) (pro_interfaces.CapabilitySnapshot, error) {
	return pro_interfaces.CapabilitySnapshot{}, pro_interfaces.CapabilityDeniedError{
		Decision: communityUnavailableDecision(configuration.ID),
		Required: pro_interfaces.CapabilityAccessWrite,
	}
}

type communityCapabilityTestService struct{}

// NewCapabilityTestService returns a Community-safe service whose entry points
// remain unavailable independently of caller-supplied snapshots.
func NewCapabilityTestService(_ db.CapabilityRepository) pro_interfaces.CapabilityTestService {
	return &communityCapabilityTestService{}
}

func (s *communityCapabilityTestService) ListRecords(
	_ context.Context,
	_ pro_interfaces.CapabilitySnapshot,
) ([]db.CapabilityTestRecord, error) {
	return nil, communityDenied(pro_interfaces.CapabilityLifecycleTest, pro_interfaces.CapabilityAccessRead)
}

func (s *communityCapabilityTestService) CreateRecord(
	_ context.Context,
	_ pro_interfaces.CapabilitySnapshot,
	_ string,
) (db.CapabilityTestRecord, error) {
	return db.CapabilityTestRecord{}, communityDenied(pro_interfaces.CapabilityLifecycleTest, pro_interfaces.CapabilityAccessWrite)
}

func (s *communityCapabilityTestService) RunBackgroundAction(
	_ context.Context,
	_ pro_interfaces.CapabilitySnapshot,
	_ string,
) (db.CapabilityTestRecord, error) {
	return db.CapabilityTestRecord{}, communityDenied(pro_interfaces.CapabilityLifecycleTest, pro_interfaces.CapabilityAccessExecute)
}

func communityUnavailableDecision(id pro_interfaces.CapabilityID) pro_interfaces.CapabilityDecision {
	return pro_interfaces.NewCapabilityDecision(
		id,
		pro_interfaces.CapabilityStateUnavailable,
		pro_interfaces.CapabilityReasonProviderUnavailable,
		nil,
		nil,
	)
}

func communityDenied(id pro_interfaces.CapabilityID, access pro_interfaces.CapabilityAccess) error {
	return pro_interfaces.CapabilityDeniedError{
		Decision: communityUnavailableDecision(id),
		Required: access,
	}
}

var _ pro_interfaces.CapabilityProvider = (*communityCapabilityProvider)(nil)
var _ pro_interfaces.CapabilityTestService = (*communityCapabilityTestService)(nil)
