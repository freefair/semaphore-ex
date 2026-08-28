//go:build ldap_integration

package features

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/identity"
	"github.com/semaphoreui/semaphore/util"
)

func TestLDAPLifecycleTLSOutageAndRecoveryIntegration(t *testing.T) {
	caPEM, err := os.ReadFile(requiredFeatureLDAPIntegrationEnv(t, "SEMAPHORE_LDAP_CA_FILE"))
	if err != nil {
		t.Fatalf("read LDAP integration CA: %v", err)
	}
	store, service, _, now := newLDAPServiceTest(t)
	service = NewLDAPService(store, NewCapabilityProvider(store), identity.NewLDAPClient())
	previousKey := util.Config.AccessKeyEncryption
	util.Config.AccessKeyEncryption = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("i", 32)))
	t.Cleanup(func() { util.Config.AccessKeyEncryption = previousKey })
	admin, err := store.CreateUser(db.UserWithPwd{User: db.User{
		Username: "recovery-admin", Name: "Recovery Admin",
		Email: "recovery@example.test", Admin: true,
	}, Pwd: "local-recovery-password"})
	if err != nil {
		t.Fatalf("create local recovery admin: %v", err)
	}
	_, err = service.Configure(context.Background(), pro_interfaces.LDAPConfigureRequest{
		ActorID: admin.ID, ActorIsAdmin: true, Now: now,
		Provider: pro_interfaces.LDAPProviderInput{
			ID: "integration", DisplayName: "Integration LDAP",
			ServerURL: requiredFeatureLDAPIntegrationEnv(t, "SEMAPHORE_LDAP_URL"),
			TLSMode:   pro_interfaces.LDAPTLSModeLDAPS, TrustMode: pro_interfaces.LDAPTrustModeCustom,
			CAPEM: string(caPEM), BindDN: "cn=admin,dc=example,dc=test",
			BindPassword:      requiredFeatureLDAPIntegrationEnv(t, "SEMAPHORE_LDAP_ADMIN_PASSWORD"),
			SearchBaseDN:      "ou=people,dc=example,dc=test",
			UserFilter:        "(&(objectClass=inetOrgPerson)(uid={{username}}))",
			IdentityAttribute: "entryUUID", UsernameAttribute: "uid",
			NameAttribute: "cn", EmailAttribute: "mail",
		},
	})
	if err != nil {
		t.Fatalf("configure TLS LDAP provider: %v", err)
	}
	_, err = service.Test(context.Background(), pro_interfaces.LDAPTestRequest{
		ActorID: admin.ID, ActorIsAdmin: true, ProviderID: "integration",
		Username: "alice", Password: requiredFeatureLDAPIntegrationEnv(t, "SEMAPHORE_LDAP_USER_PASSWORD"),
		RecoveryAdminUserID: admin.ID, RecoveryAdminPassword: "local-recovery-password", Now: now,
	})
	if err != nil {
		t.Fatalf("prove directory and local recovery readiness: %v", err)
	}
	_, err = service.SetState(context.Background(), pro_interfaces.LDAPStateRequest{
		ActorID: admin.ID, ActorIsAdmin: true, ProviderID: "integration",
		State: pro_interfaces.LDAPStateActive, Now: now,
	})
	if err != nil {
		t.Fatalf("activate ready LDAP provider: %v", err)
	}
	_, err = service.Authenticate(context.Background(), pro_interfaces.LDAPAuthenticationRequest{
		ProviderID: "integration", Username: "alice",
		Password: requiredFeatureLDAPIntegrationEnv(t, "SEMAPHORE_LDAP_USER_PASSWORD"),
		Now:      now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("authenticate through active LDAP provider: %v", err)
	}

	provider, err := store.GetLDAPProvider("integration")
	if err != nil {
		t.Fatalf("load active LDAP provider: %v", err)
	}
	provider.ServerURL = closedFeatureLDAPIntegrationURL(t)
	if err = store.SaveLDAPProvider(provider); err != nil {
		t.Fatalf("simulate LDAP outage: %v", err)
	}
	_, err = service.Authenticate(context.Background(), pro_interfaces.LDAPAuthenticationRequest{
		ProviderID: "integration", Username: "alice", Password: "not-logged",
		Now: now.Add(2 * time.Minute),
	})
	if !errors.Is(err, pro_interfaces.ErrLDAPProviderUnavailable) {
		t.Fatalf("LDAP outage = %v, want provider unavailable", err)
	}
	allowed, err := service.AllowLocalRecovery(context.Background(), "recovery@example.test")
	if err != nil || !allowed {
		t.Fatalf("local recovery during LDAP outage: allowed=%t err=%v", allowed, err)
	}
}

func requiredFeatureLDAPIntegrationEnv(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		t.Fatalf("%s is required for LDAP integration tests", name)
	}
	return value
}

func closedFeatureLDAPIntegrationURL(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve LDAP outage port: %v", err)
	}
	address := listener.Addr().String()
	if err = listener.Close(); err != nil {
		t.Fatalf("close LDAP outage port: %v", err)
	}
	return "ldaps://" + address
}
