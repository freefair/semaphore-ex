package features

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	sqldb "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

func TestCapabilityLifecyclePreservesDataAndGuardsWorkers(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	provider := NewCapabilityProvider(store)
	service := NewCapabilityTestService(store)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	admin := pro_interfaces.CapabilityRequest{UserID: 7, IsAdmin: true, At: now}

	disabled, err := provider.Resolve(ctx, admin)
	if err != nil {
		t.Fatalf("resolve default: %v", err)
	}
	assertCapabilityState(t, disabled, pro_interfaces.CapabilityStateDisabled, pro_interfaces.CapabilityReasonDisabledByAdmin)
	if _, err := service.RunBackgroundAction(ctx, disabled, "blocked"); !isCapabilityDenied(err, pro_interfaces.CapabilityAccessExecute) {
		t.Fatalf("disabled worker action must be denied, got %v", err)
	}

	active, err := provider.Configure(ctx, admin, pro_interfaces.CapabilityConfiguration{
		ID:    pro_interfaces.CapabilityLifecycleTest,
		State: pro_interfaces.CapabilityStateActive,
	})
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	created, err := service.CreateRecord(ctx, active, "preserved")
	if err != nil || created.Source != "api" {
		t.Fatalf("create active record: %#v, %v", created, err)
	}
	if _, err := service.RunBackgroundAction(ctx, active, "worker-value"); err != nil {
		t.Fatalf("active worker action: %v", err)
	}

	disabled, err = provider.Configure(ctx, admin, pro_interfaces.CapabilityConfiguration{
		ID:    pro_interfaces.CapabilityLifecycleTest,
		State: pro_interfaces.CapabilityStateDisabled,
	})
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := service.CreateRecord(ctx, disabled, "blocked"); !isCapabilityDenied(err, pro_interfaces.CapabilityAccessWrite) {
		t.Fatalf("disabled write must be denied, got %v", err)
	}

	active, err = provider.Configure(ctx, admin, pro_interfaces.CapabilityConfiguration{
		ID:    pro_interfaces.CapabilityLifecycleTest,
		State: pro_interfaces.CapabilityStateActive,
	})
	if err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	records, err := service.ListRecords(ctx, active)
	if err != nil {
		t.Fatalf("list re-enabled records: %v", err)
	}
	if len(records) != 2 || records[0].Value != "preserved" || records[1].Source != "worker" {
		t.Fatalf("expected preserved API and worker records, got %#v", records)
	}
}

func TestLDAPCapabilityIsAvailableInEnhancedEdition(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	provider := NewCapabilityProvider(store)

	snapshot, err := provider.Resolve(context.Background(), pro_interfaces.CapabilityRequest{
		UserID: 7, IsAdmin: true, At: time.Unix(1_700_000_000, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("resolve enhanced capabilities: %v", err)
	}
	decision := snapshot.Decision(pro_interfaces.CapabilityLDAP)
	if decision.State() != pro_interfaces.CapabilityStateActive {
		t.Fatalf("LDAP capability state = %q, want active", decision.State())
	}
	for _, access := range []pro_interfaces.CapabilityAccess{
		pro_interfaces.CapabilityAccessRead,
		pro_interfaces.CapabilityAccessWrite,
		pro_interfaces.CapabilityAccessExecute,
	} {
		if !decision.Allows(access) {
			t.Fatalf("LDAP capability must allow %q", access)
		}
	}
}

func TestProviderDistinguishesReadOnlyExpiredAndPermissionStates(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	provider := NewCapabilityProvider(store)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	admin := pro_interfaces.CapabilityRequest{UserID: 7, IsAdmin: true, At: now}

	readOnly, err := provider.Configure(ctx, admin, pro_interfaces.CapabilityConfiguration{
		ID:    pro_interfaces.CapabilityLifecycleTest,
		State: pro_interfaces.CapabilityStateReadOnly,
	})
	if err != nil {
		t.Fatalf("configure read-only: %v", err)
	}
	assertCapabilityState(t, readOnly, pro_interfaces.CapabilityStateReadOnly, pro_interfaces.CapabilityReasonReadOnly)
	if !readOnly.Decision(pro_interfaces.CapabilityLifecycleTest).Allows(pro_interfaces.CapabilityAccessRead) {
		t.Fatal("read-only state must permit reads")
	}

	expiresAt := now.Add(time.Minute)
	_, err = provider.Configure(ctx, admin, pro_interfaces.CapabilityConfiguration{
		ID:        pro_interfaces.CapabilityLifecycleTest,
		State:     pro_interfaces.CapabilityStateActive,
		ExpiresAt: &expiresAt,
	})
	if err != nil {
		t.Fatalf("configure expiry: %v", err)
	}
	expired, err := provider.Resolve(ctx, pro_interfaces.CapabilityRequest{UserID: 7, IsAdmin: true, At: now.Add(2 * time.Minute)})
	if err != nil {
		t.Fatalf("resolve expired: %v", err)
	}
	assertCapabilityState(t, expired, pro_interfaces.CapabilityStateExpired, pro_interfaces.CapabilityReasonEntitlementExpired)

	if _, err = provider.Configure(ctx, admin, pro_interfaces.CapabilityConfiguration{
		ID:    pro_interfaces.CapabilityLifecycleTest,
		State: pro_interfaces.CapabilityStateActive,
	}); err != nil {
		t.Fatalf("configure active: %v", err)
	}
	unauthorized, err := provider.Resolve(ctx, pro_interfaces.CapabilityRequest{UserID: 8, At: now})
	if err != nil {
		t.Fatalf("resolve non-admin: %v", err)
	}
	assertCapabilityState(t, unauthorized, pro_interfaces.CapabilityStateInsufficientPermission, pro_interfaces.CapabilityReasonInsufficientPermission)
}

func TestProviderKeepsExplicitDisableReasonAfterExpiry(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	provider := NewCapabilityProvider(store)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	expiredAt := now.Add(-time.Minute)

	snapshot, err := provider.Configure(ctx, pro_interfaces.CapabilityRequest{
		UserID: 7, IsAdmin: true, At: now,
	}, pro_interfaces.CapabilityConfiguration{
		ID:        pro_interfaces.CapabilityLifecycleTest,
		State:     pro_interfaces.CapabilityStateDisabled,
		ExpiresAt: &expiredAt,
	})

	if err != nil {
		t.Fatalf("configure disabled: %v", err)
	}
	assertCapabilityState(t, snapshot, pro_interfaces.CapabilityStateDisabled, pro_interfaces.CapabilityReasonDisabledByAdmin)
}

func TestRuntimeSecretsCapabilityDefaultsActiveAndDisablesReversibly(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	provider := NewCapabilityProvider(store)
	ctx := context.Background()
	request := pro_interfaces.CapabilityRequest{
		UserID: 7, IsAdmin: true, At: time.Unix(1_700_000_000, 0).UTC(),
	}

	active, err := provider.Resolve(ctx, request)
	if err != nil {
		t.Fatalf("resolve runtime secrets default: %v", err)
	}
	activeDecision := active.Decision(pro_interfaces.CapabilityRuntimeSecrets)
	if activeDecision.State() != pro_interfaces.CapabilityStateActive ||
		!activeDecision.Allows(pro_interfaces.CapabilityAccessExecute) {
		t.Fatalf("runtime secrets must default active, got %s", activeDecision.State())
	}

	disabled, err := provider.Configure(ctx, request, pro_interfaces.CapabilityConfiguration{
		ID: pro_interfaces.CapabilityRuntimeSecrets, State: pro_interfaces.CapabilityStateDisabled,
	})
	if err != nil {
		t.Fatalf("disable runtime secrets: %v", err)
	}
	if disabled.Decision(pro_interfaces.CapabilityRuntimeSecrets).Allows(pro_interfaces.CapabilityAccessExecute) {
		t.Fatal("disabled runtime secrets must block execution")
	}
	if !disabled.Decision(pro_interfaces.CapabilityRuntimeSecrets).Allows(pro_interfaces.CapabilityAccessRead) {
		t.Fatal("disabled runtime secrets must keep configuration readable for rollback")
	}

	reenabled, err := provider.Configure(ctx, request, pro_interfaces.CapabilityConfiguration{
		ID: pro_interfaces.CapabilityRuntimeSecrets, State: pro_interfaces.CapabilityStateActive,
	})
	if err != nil {
		t.Fatalf("re-enable runtime secrets: %v", err)
	}
	if !reenabled.Decision(pro_interfaces.CapabilityRuntimeSecrets).Allows(pro_interfaces.CapabilityAccessExecute) {
		t.Fatal("re-enabled runtime secrets must allow execution")
	}
}

func TestConcurrentResolutionsReturnCompleteSnapshots(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	provider := NewCapabilityProvider(store)
	ctx := context.Background()
	request := pro_interfaces.CapabilityRequest{
		UserID:  7,
		IsAdmin: true,
		At:      time.Unix(1_700_000_000, 0).UTC(),
	}
	if _, err := provider.Configure(ctx, request, pro_interfaces.CapabilityConfiguration{
		ID:    pro_interfaces.CapabilityLifecycleTest,
		State: pro_interfaces.CapabilityStateActive,
	}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	var waitGroup sync.WaitGroup
	errorsChannel := make(chan error, 32)
	for range 32 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			snapshot, err := provider.Resolve(ctx, request)
			if err != nil {
				errorsChannel <- err
				return
			}
			decision := snapshot.Decision(pro_interfaces.CapabilityLifecycleTest)
			if decision.State() != pro_interfaces.CapabilityStateActive || !decision.Allows(pro_interfaces.CapabilityAccessExecute) {
				errorsChannel <- errors.New("incomplete snapshot")
			}
		}()
	}
	waitGroup.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		t.Error(err)
	}
}

func assertCapabilityState(
	t *testing.T,
	snapshot pro_interfaces.CapabilitySnapshot,
	state pro_interfaces.CapabilityState,
	reason pro_interfaces.CapabilityReasonCode,
) {
	t.Helper()
	decision := snapshot.Decision(pro_interfaces.CapabilityLifecycleTest)
	if decision.State() != state || decision.Reason() != reason {
		t.Fatalf("expected %s/%s, got %s/%s", state, reason, decision.State(), decision.Reason())
	}
}

func isCapabilityDenied(err error, access pro_interfaces.CapabilityAccess) bool {
	var denied pro_interfaces.CapabilityDeniedError
	return errors.As(err, &denied) && denied.Required == access
}
