package server

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	log "github.com/sirupsen/logrus"
)

const ldapGroupReconciliationInterval = 5 * time.Minute

type LDAPGroupReconciliationService interface {
	Providers(context.Context) ([]pro_interfaces.LDAPProviderConfiguration, error)
	GroupMappings(context.Context, string) ([]pro_interfaces.LDAPGroupMapping, error)
	ReconcileGroupMappings(context.Context, pro_interfaces.LDAPGroupPreviewRequest) (pro_interfaces.LDAPGroupPreview, error)
}

// LDAPGroupReconciliationScheduler periodically refreshes managed role grants.
// Directory failures are retained in reconciliation history by the service and
// never trigger grant removal.
type LDAPGroupReconciliationScheduler struct {
	service LDAPGroupReconciliationService
	stop    chan struct{}
	wg      sync.WaitGroup
}

func NewLDAPGroupReconciliationScheduler(
	service LDAPGroupReconciliationService,
) *LDAPGroupReconciliationScheduler {
	return &LDAPGroupReconciliationScheduler{service: service, stop: make(chan struct{})}
}

func (s *LDAPGroupReconciliationScheduler) Start() {
	s.wg.Add(1)
	go s.run()
}

func (s *LDAPGroupReconciliationScheduler) Stop() {
	close(s.stop)
	s.wg.Wait()
}

func (s *LDAPGroupReconciliationScheduler) run() {
	defer s.wg.Done()
	ticker := time.NewTicker(ldapGroupReconciliationInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.tick(context.Background(), tz.Now())
		}
	}
}

func (s *LDAPGroupReconciliationScheduler) tick(ctx context.Context, now time.Time) {
	providers, err := s.service.Providers(ctx)
	if errors.Is(err, pro_interfaces.ErrLDAPUnavailable) {
		return
	}
	if err != nil {
		log.WithError(err).Warn("LDAP group reconciliation: failed to list providers")
		return
	}
	for _, provider := range providers {
		if provider.State != pro_interfaces.LDAPStateActive &&
			provider.State != pro_interfaces.LDAPStateSelectedUsers {
			continue
		}
		mappings, mappingErr := s.service.GroupMappings(ctx, provider.ID)
		if mappingErr != nil {
			log.WithError(mappingErr).WithField("provider_id", provider.ID).
				Warn("LDAP group reconciliation: failed to list mappings")
			continue
		}
		if len(mappings) == 0 {
			continue
		}
		if _, reconcileErr := s.service.ReconcileGroupMappings(ctx, pro_interfaces.LDAPGroupPreviewRequest{
			ProviderID: provider.ID, Source: "scheduled", Now: now,
		}); reconcileErr != nil {
			log.WithError(reconcileErr).WithField("provider_id", provider.ID).
				Warn("LDAP group reconciliation failed")
		}
	}
}
