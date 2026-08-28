package features

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	sqldb "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
)

type ldapClientStub struct {
	result   pro_interfaces.LDAPClientResult
	err      error
	requests []pro_interfaces.LDAPClientRequest
}

func (c *ldapClientStub) Validate(pro_interfaces.LDAPClientConfiguration) error { return nil }

func (c *ldapClientStub) Authenticate(
	_ context.Context,
	request pro_interfaces.LDAPClientRequest,
) (pro_interfaces.LDAPClientResult, error) {
	c.requests = append(c.requests, request)
	return c.result, c.err
}

func TestLDAPConfigureStoresWriteOnlyEncryptedCredential(t *testing.T) {
	store, service, client, now := newLDAPServiceTest(t)

	configuration, err := service.Configure(context.Background(), pro_interfaces.LDAPConfigureRequest{
		ActorID: 1, ActorIsAdmin: true, Provider: testLDAPProviderInput(), Now: now,
	})
	if err != nil {
		t.Fatalf("configure LDAP: %v", err)
	}
	if !configuration.BindPasswordConfigured {
		t.Fatal("configured credential must be represented by a write-only presence flag")
	}
	stored, err := store.GetLDAPProvider("corp")
	if err != nil {
		t.Fatalf("load stored provider: %v", err)
	}
	if stored.EncryptedBindPassword == "bind-secret" || stored.EncryptedBindPassword == "" {
		t.Fatalf("bind credential was not encrypted at rest: %q", stored.EncryptedBindPassword)
	}
	plaintext, err := util.Config.DecryptOption(stored.EncryptedBindPassword)
	if err != nil || string(plaintext) != "bind-secret" {
		t.Fatalf("decrypt stored bind credential: %v", err)
	}
	zeroLDAPBytes(plaintext)
	if len(client.requests) != 0 {
		t.Fatal("configuration validation must not contact the directory")
	}
}

func TestLDAPLifecycleRequiresDirectoryAndLocalRecoveryReadiness(t *testing.T) {
	store, service, client, now := newLDAPServiceTest(t)
	admin, err := store.CreateUser(db.UserWithPwd{User: db.User{
		Username: "local-admin", Name: "Local Admin", Email: "admin@example.test", Admin: true,
	}, Pwd: "local-password"})
	if err != nil {
		t.Fatalf("create local admin: %v", err)
	}
	_, err = service.Configure(context.Background(), pro_interfaces.LDAPConfigureRequest{
		ActorID: admin.ID, ActorIsAdmin: true, Provider: testLDAPProviderInput(), Now: now,
	})
	if err != nil {
		t.Fatalf("configure LDAP: %v", err)
	}
	_, err = service.SetState(context.Background(), pro_interfaces.LDAPStateRequest{
		ActorID: admin.ID, ActorIsAdmin: true, ProviderID: "corp", State: pro_interfaces.LDAPStateActive, Now: now,
	})
	if !errors.Is(err, pro_interfaces.ErrLDAPReadiness) {
		t.Fatalf("enable without readiness = %v, want readiness error", err)
	}
	client.result = pro_interfaces.LDAPClientResult{
		Identity: pro_interfaces.LDAPIdentity{
			ExternalID: "40f1c82a-b773-4d41-a587-7c4cf7f3cd67", Username: "ldap-admin",
			Name: "LDAP Admin", Email: "ldap-admin@example.test",
		},
		Readiness: pro_interfaces.LDAPReadiness{
			Status: pro_interfaces.LDAPReadinessReady, Connection: true, Search: true, Bind: true,
		},
	}
	readiness, err := service.Test(context.Background(), pro_interfaces.LDAPTestRequest{
		ActorID: admin.ID, ActorIsAdmin: true, ProviderID: "corp",
		Username: "ldap-admin", Password: "ldap-password",
		RecoveryAdminUserID: admin.ID, RecoveryAdminPassword: "local-password", Now: now,
	})
	if err != nil || !readiness.Recovery {
		t.Fatalf("test LDAP readiness: %#v, %v", readiness, err)
	}
	configuration, err := service.SetState(context.Background(), pro_interfaces.LDAPStateRequest{
		ActorID: admin.ID, ActorIsAdmin: true, ProviderID: "corp", State: pro_interfaces.LDAPStateActive, Now: now,
	})
	if err != nil || configuration.State != pro_interfaces.LDAPStateActive {
		t.Fatalf("enable ready LDAP: %#v, %v", configuration, err)
	}
	allowed, err := service.AllowLocalRecovery(context.Background(), "ADMIN@example.test")
	if err != nil || !allowed {
		t.Fatalf("local recovery login must remain available: allowed=%t err=%v", allowed, err)
	}
}

func TestLDAPActiveProviderConfigurationFailsClosed(t *testing.T) {
	_, service, _, now := readyActiveLDAPServiceTest(t)
	input := testLDAPProviderInput()
	input.ServerURL = "ldaps://replacement.example.test:636"

	_, err := service.Configure(context.Background(), pro_interfaces.LDAPConfigureRequest{
		ActorID: 1, ActorIsAdmin: true, Provider: input, Now: now,
	})
	if !errors.Is(err, pro_interfaces.ErrLDAPReconfigurationRequiresInactive) {
		t.Fatalf("active LDAP reconfiguration = %v, want inactive-provider error", err)
	}
}

func TestLDAPCollisionDoesNotMergeLocalUser(t *testing.T) {
	store, service, client, now := readyActiveLDAPServiceTest(t)
	_, err := store.CreateUser(db.UserWithPwd{User: db.User{
		Username: "jdoe", Name: "Unrelated Local User", Email: "local@example.test",
	}, Pwd: "local-password"})
	if err != nil {
		t.Fatalf("create local user: %v", err)
	}
	client.result.Identity = pro_interfaces.LDAPIdentity{
		ExternalID: "40f1c82a-b773-4d41-a587-7c4cf7f3cd67", Username: "jdoe",
		Name: "Directory User", Email: "directory@example.test",
	}

	_, err = service.Authenticate(context.Background(), pro_interfaces.LDAPAuthenticationRequest{
		ProviderID: "corp", Username: "jdoe", Password: "directory-password", Now: now.Add(time.Minute),
	})
	if !errors.Is(err, pro_interfaces.ErrLDAPIdentityCollision) {
		t.Fatalf("colliding LDAP login = %v, want identity collision", err)
	}
}

func TestDisablingLDAPRejectsLoginAndPreservesLinkedIdentity(t *testing.T) {
	store, service, client, now := readyActiveLDAPServiceTest(t)
	client.result.Identity = pro_interfaces.LDAPIdentity{
		ExternalID: "40f1c82a-b773-4d41-a587-7c4cf7f3cd67", Username: "jdoe",
		Name: "Directory User", Email: "jdoe@example.test",
	}
	user, err := service.Authenticate(context.Background(), pro_interfaces.LDAPAuthenticationRequest{
		ProviderID: "corp", Username: "jdoe", Password: "directory-password", Now: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("initial LDAP login: %v", err)
	}
	_, err = service.SetState(context.Background(), pro_interfaces.LDAPStateRequest{
		ActorID: 1, ActorIsAdmin: true, ProviderID: "corp", State: pro_interfaces.LDAPStateDisabled,
		Now: now.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("disable LDAP: %v", err)
	}
	_, err = service.Authenticate(context.Background(), pro_interfaces.LDAPAuthenticationRequest{
		ProviderID: "corp", Username: "jdoe", Password: "directory-password", Now: now.Add(3 * time.Minute),
	})
	if !errors.Is(err, pro_interfaces.ErrLDAPDisabled) {
		t.Fatalf("disabled LDAP login = %v, want disabled", err)
	}
	identities, err := store.GetUserExternalIdentities(user.ID)
	if err != nil || len(identities) != 1 || identities[0].ExternalUID != client.result.Identity.ExternalID {
		t.Fatalf("linked identity history after disable: %#v, %v", identities, err)
	}
	providers, err := service.Providers(context.Background())
	if err != nil || len(providers) != 1 || len(providers[0].EligibleUserIDs) != 1 ||
		providers[0].EligibleUserIDs[0] != user.ID {
		t.Fatalf("eligible selected-user identities: %#v, %v", providers, err)
	}
}

func TestLDAPAuthenticationThrottlesRepeatedFailures(t *testing.T) {
	_, service, client, now := readyActiveLDAPServiceTest(t)
	client.err = pro_interfaces.ErrLDAPInvalidCredentials

	var err error
	for attempt := 0; attempt < ldapMaxFailures; attempt++ {
		_, err = service.Authenticate(context.Background(), pro_interfaces.LDAPAuthenticationRequest{
			ProviderID: "corp", Username: "attacker", Password: "wrong", Now: now.Add(time.Duration(attempt) * time.Second),
		})
	}
	if !errors.Is(err, pro_interfaces.ErrLDAPThrottled) {
		t.Fatalf("fifth failed LDAP login = %v, want throttled", err)
	}
	requestCount := len(client.requests)
	_, err = service.Authenticate(context.Background(), pro_interfaces.LDAPAuthenticationRequest{
		ProviderID: "corp", Username: "attacker", Password: "wrong", Now: now.Add(10 * time.Second),
	})
	if !errors.Is(err, pro_interfaces.ErrLDAPThrottled) || len(client.requests) != requestCount {
		t.Fatalf("blocked login contacted directory: err=%v requests=%d want=%d", err, len(client.requests), requestCount)
	}
}

func newLDAPServiceTest(t *testing.T) (*sqldb.SqlDb, pro_interfaces.LDAPService, *ldapClientStub, time.Time) {
	t.Helper()
	store := sqldb.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	previousKey := util.Config.AccessKeyEncryption
	util.Config.AccessKeyEncryption = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	t.Cleanup(func() { util.Config.AccessKeyEncryption = previousKey })
	client := &ldapClientStub{}
	service := NewLDAPService(store, NewCapabilityProvider(store), client)
	return store, service, client, time.Unix(1_700_000_000, 0).UTC()
}

func readyActiveLDAPServiceTest(
	t *testing.T,
) (*sqldb.SqlDb, pro_interfaces.LDAPService, *ldapClientStub, time.Time) {
	t.Helper()
	store, service, client, now := newLDAPServiceTest(t)
	admin, err := store.CreateUser(db.UserWithPwd{User: db.User{
		Username: "local-admin", Name: "Local Admin", Email: "admin@example.test", Admin: true,
	}, Pwd: "local-password"})
	if err != nil {
		t.Fatalf("create local admin: %v", err)
	}
	client.result = pro_interfaces.LDAPClientResult{
		Identity: pro_interfaces.LDAPIdentity{
			ExternalID: "e6aa082d-976f-4b32-af13-5ca878368957", Username: "readiness-user",
			Name: "Readiness User", Email: "readiness@example.test",
		},
		Readiness: pro_interfaces.LDAPReadiness{
			Status: pro_interfaces.LDAPReadinessReady, Connection: true, Search: true, Bind: true,
		},
	}
	_, err = service.Configure(context.Background(), pro_interfaces.LDAPConfigureRequest{
		ActorID: admin.ID, ActorIsAdmin: true, Provider: testLDAPProviderInput(), Now: now,
	})
	if err != nil {
		t.Fatalf("configure LDAP: %v", err)
	}
	_, err = service.Test(context.Background(), pro_interfaces.LDAPTestRequest{
		ActorID: admin.ID, ActorIsAdmin: true, ProviderID: "corp", Username: "readiness-user",
		Password: "directory-password", RecoveryAdminUserID: admin.ID,
		RecoveryAdminPassword: "local-password", Now: now,
	})
	if err != nil {
		t.Fatalf("test LDAP: %v", err)
	}
	_, err = service.SetState(context.Background(), pro_interfaces.LDAPStateRequest{
		ActorID: admin.ID, ActorIsAdmin: true, ProviderID: "corp", State: pro_interfaces.LDAPStateActive, Now: now,
	})
	if err != nil {
		t.Fatalf("activate LDAP: %v", err)
	}
	client.requests = nil
	return store, service, client, now
}

func testLDAPProviderInput() pro_interfaces.LDAPProviderInput {
	return pro_interfaces.LDAPProviderInput{
		ID: "corp", DisplayName: "Corporate LDAP", ServerURL: "ldaps://ldap.example.test:636",
		TLSMode: pro_interfaces.LDAPTLSModeLDAPS, TrustMode: pro_interfaces.LDAPTrustModeSystem,
		BindDN: "cn=bind,dc=example,dc=test", BindPassword: "bind-secret",
		SearchBaseDN: "ou=users,dc=example,dc=test", UserFilter: "(uid={{username}})",
		IdentityAttribute: "entryUUID", UsernameAttribute: "uid", NameAttribute: "cn", EmailAttribute: "mail",
	}
}

var _ pro_interfaces.LDAPClient = (*ldapClientStub)(nil)
