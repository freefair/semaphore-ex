package features

import (
	"context"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type communityLDAPService struct{}

// NewLDAPService returns the Community marker that keeps the existing legacy
// configuration path active while every managed lifecycle operation remains unavailable.
func NewLDAPService(
	_ db.Store,
	_ pro_interfaces.CapabilityProvider,
	_ pro_interfaces.LDAPClient,
) pro_interfaces.LDAPService {
	return &communityLDAPService{}
}

func (*communityLDAPService) Initialize(context.Context) error { return nil }

func (*communityLDAPService) LoginProviders(context.Context) ([]pro_interfaces.LDAPLoginProvider, error) {
	return nil, pro_interfaces.ErrLDAPUnavailable
}

func (*communityLDAPService) AllowLocalRecovery(context.Context, string) (bool, error) {
	return false, pro_interfaces.ErrLDAPUnavailable
}

func (*communityLDAPService) Authenticate(context.Context, pro_interfaces.LDAPAuthenticationRequest) (db.User, error) {
	return db.User{}, pro_interfaces.ErrLDAPUnavailable
}

func (*communityLDAPService) Link(context.Context, pro_interfaces.LDAPLinkRequest) error {
	return pro_interfaces.ErrLDAPUnavailable
}

func (*communityLDAPService) Providers(context.Context) ([]pro_interfaces.LDAPProviderConfiguration, error) {
	return nil, pro_interfaces.ErrLDAPUnavailable
}

func (*communityLDAPService) Configure(context.Context, pro_interfaces.LDAPConfigureRequest) (pro_interfaces.LDAPProviderConfiguration, error) {
	return pro_interfaces.LDAPProviderConfiguration{}, pro_interfaces.ErrLDAPUnavailable
}

func (*communityLDAPService) Test(context.Context, pro_interfaces.LDAPTestRequest) (pro_interfaces.LDAPReadiness, error) {
	return pro_interfaces.LDAPReadiness{}, pro_interfaces.ErrLDAPUnavailable
}

func (*communityLDAPService) SetState(context.Context, pro_interfaces.LDAPStateRequest) (pro_interfaces.LDAPProviderConfiguration, error) {
	return pro_interfaces.LDAPProviderConfiguration{}, pro_interfaces.ErrLDAPUnavailable
}

func (*communityLDAPService) Transitions(context.Context, string) ([]db.LDAPCapabilityTransition, error) {
	return nil, pro_interfaces.ErrLDAPUnavailable
}

var _ pro_interfaces.LDAPService = (*communityLDAPService)(nil)
