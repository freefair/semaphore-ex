//go:build ldap_integration

package features

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"net"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"
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

func TestLDAPGroupMappingNestedRenameOutageAndRetryIntegration(t *testing.T) {
	ctx := context.Background()
	caPEM, err := os.ReadFile(requiredFeatureLDAPIntegrationEnv(t, "SEMAPHORE_LDAP_CA_FILE"))
	if err != nil {
		t.Fatalf("read LDAP integration CA: %v", err)
	}
	store, _, _, now := newLDAPServiceTest(t)
	service := NewLDAPService(store, NewCapabilityProvider(store), identity.NewLDAPClient())
	admin, err := store.CreateUser(db.UserWithPwd{User: db.User{
		Username: "group-admin", Name: "Group Admin", Email: "group-admin@example.test", Admin: true,
	}, Pwd: "local-group-password"})
	if err != nil {
		t.Fatalf("create group mapping admin: %v", err)
	}
	serverURL := requiredFeatureLDAPIntegrationEnv(t, "SEMAPHORE_LDAP_URL")
	provider := pro_interfaces.LDAPProviderInput{
		ID: "group-integration", DisplayName: "Group Integration LDAP",
		ServerURL: serverURL, TLSMode: pro_interfaces.LDAPTLSModeLDAPS,
		TrustMode: pro_interfaces.LDAPTrustModeCustom, CAPEM: string(caPEM),
		BindDN:            "cn=admin,dc=example,dc=test",
		BindPassword:      requiredFeatureLDAPIntegrationEnv(t, "SEMAPHORE_LDAP_ADMIN_PASSWORD"),
		SearchBaseDN:      "ou=people,dc=example,dc=test",
		UserFilter:        "(&(objectClass=inetOrgPerson)(uid={{username}}))",
		IdentityAttribute: "entryUUID", UsernameAttribute: "uid",
		NameAttribute: "cn", EmailAttribute: "mail",
		GroupSearchBaseDN:      "ou=groups,dc=example,dc=test",
		GroupUserFilter:        "(&(objectClass=inetOrgPerson)(uid=alice))",
		GroupFilter:            "(objectClass=groupOfNames)",
		GroupIdentityAttribute: "entryUUID", GroupMemberAttribute: "member", GroupMaxDepth: 4,
	}
	if _, err = service.Configure(ctx, pro_interfaces.LDAPConfigureRequest{
		ActorID: admin.ID, ActorIsAdmin: true, Provider: provider, Now: now,
	}); err != nil {
		t.Fatalf("configure LDAP group provider: %v", err)
	}
	if _, err = service.Test(ctx, pro_interfaces.LDAPTestRequest{
		ActorID: admin.ID, ActorIsAdmin: true, ProviderID: provider.ID,
		Username: "alice", Password: requiredFeatureLDAPIntegrationEnv(t, "SEMAPHORE_LDAP_USER_PASSWORD"),
		RecoveryAdminUserID: admin.ID, RecoveryAdminPassword: "local-group-password", Now: now,
	}); err != nil {
		t.Fatalf("test LDAP group provider: %v", err)
	}
	if _, err = service.SetState(ctx, pro_interfaces.LDAPStateRequest{
		ActorID: admin.ID, ActorIsAdmin: true, ProviderID: provider.ID,
		State: pro_interfaces.LDAPStateActive, Now: now,
	}); err != nil {
		t.Fatalf("activate LDAP group provider: %v", err)
	}
	user, err := service.Authenticate(ctx, pro_interfaces.LDAPAuthenticationRequest{
		ProviderID: provider.ID, Username: "alice",
		Password: requiredFeatureLDAPIntegrationEnv(t, "SEMAPHORE_LDAP_USER_PASSWORD"),
		Now:      now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("authenticate linked LDAP group user: %v", err)
	}

	directory := openFeatureLDAPIntegrationAdmin(t, caPEM)
	defer directory.Close()
	stableGroupDN := "cn=stable-direct,ou=groups,dc=example,dc=test"
	nestedGroupDN := "cn=nested-b,ou=groups,dc=example,dc=test"
	stableGroupID := featureLDAPIntegrationEntryUUID(t, directory, stableGroupDN)
	nestedGroupID := featureLDAPIntegrationEntryUUID(t, directory, nestedGroupDN)

	client := identity.NewLDAPClient()
	snapshot, err := client.ReadGroupSnapshot(ctx, pro_interfaces.LDAPClientConfiguration{
		ServerURL: provider.ServerURL, TLSMode: provider.TLSMode, TrustMode: provider.TrustMode,
		CAPEM: provider.CAPEM, BindDN: provider.BindDN, BindPassword: provider.BindPassword,
		SearchBaseDN: provider.SearchBaseDN, UserFilter: provider.UserFilter,
		IdentityAttribute: provider.IdentityAttribute, UsernameAttribute: provider.UsernameAttribute,
		NameAttribute: provider.NameAttribute, EmailAttribute: provider.EmailAttribute,
		GroupSearchBaseDN: provider.GroupSearchBaseDN, GroupUserFilter: provider.GroupUserFilter,
		GroupFilter: provider.GroupFilter, GroupIdentityAttribute: provider.GroupIdentityAttribute,
		GroupMemberAttribute: provider.GroupMemberAttribute, GroupMaxDepth: provider.GroupMaxDepth,
	})
	if err != nil {
		t.Fatalf("read paged LDAP group snapshot: %v", err)
	}
	if len(snapshot.GroupExternalIDs) < 109 {
		t.Fatalf("paged LDAP group count = %d, want at least 109", len(snapshot.GroupExternalIDs))
	}
	if !featureLDAPUserHasGroups(snapshot, "entryuuid:", stableGroupID, nestedGroupID) {
		t.Fatalf("nested/cyclic LDAP memberships not present for Alice: %#v", snapshot.Users)
	}

	stableRole, err := store.CreateGlobalRole(db.Role{
		ID: "ldap_stable_role", Name: "LDAP stable role",
		GlobalPermissions: db.CanReadGlobalAudit, Revision: 1,
	})
	if err != nil {
		t.Fatalf("create stable mapped role: %v", err)
	}
	nestedRole, err := store.CreateGlobalRole(db.Role{
		ID: "ldap_nested_role", Name: "LDAP nested role",
		GlobalPermissions: db.CanManageGlobalSystem, Revision: 1,
	})
	if err != nil {
		t.Fatalf("create nested mapped role: %v", err)
	}
	manualRole, err := store.CreateGlobalRole(db.Role{
		ID: "ldap_manual_role", Name: "LDAP manual role",
		GlobalPermissions: db.CanManageGlobalUsers, Revision: 1,
	})
	if err != nil {
		t.Fatalf("create manual role: %v", err)
	}
	if _, err = store.CreateGlobalRoleAssignment(db.GlobalRoleAssignment{
		UserID: user.ID, RoleID: manualRole.ID, Revision: 1,
	}); err != nil {
		t.Fatalf("create manual role assignment: %v", err)
	}
	for _, mapping := range []pro_interfaces.LDAPGroupMapping{
		{ID: "stable-mapping", ProviderID: provider.ID, GroupExternalID: stableGroupID, Enabled: true,
			Target: pro_interfaces.LDAPRoleTarget{Scope: pro_interfaces.LDAPRoleScopeGlobal, RoleID: string(stableRole.ID)}},
		{ID: "nested-mapping", ProviderID: provider.ID, GroupExternalID: nestedGroupID, Enabled: true,
			Target: pro_interfaces.LDAPRoleTarget{Scope: pro_interfaces.LDAPRoleScopeGlobal, RoleID: string(nestedRole.ID)}},
	} {
		if _, err = service.SaveGroupMapping(ctx, pro_interfaces.LDAPGroupMappingRequest{
			ActorID: admin.ID, ActorIsAdmin: true, Mapping: mapping, Now: now.Add(2 * time.Second),
		}); err != nil {
			t.Fatalf("save LDAP group mapping %s: %v", mapping.ID, err)
		}
	}
	actorID := admin.ID
	preview, err := service.PreviewGroupMappings(ctx, pro_interfaces.LDAPGroupPreviewRequest{
		ActorID: &actorID, ActorIsAdmin: true, ProviderID: provider.ID,
		Source: "manual", Now: now.Add(3 * time.Second),
	})
	if err != nil || len(preview.Additions) != 2 {
		t.Fatalf("preview nested group additions: additions=%d err=%v", len(preview.Additions), err)
	}
	if _, err = service.ApplyGroupPreview(ctx, pro_interfaces.LDAPGroupApplyRequest{
		ActorID: admin.ID, ActorIsAdmin: true, ProviderID: provider.ID,
		Token: preview.Token, Now: now.Add(4 * time.Second),
	}); err != nil {
		t.Fatalf("apply nested group additions: %v", err)
	}
	featureLDAPRequireRoleIDs(t, store, user.ID, manualRole.ID, nestedRole.ID, stableRole.ID)

	if err = directory.ModifyDN(ldap.NewModifyDNRequest(
		stableGroupDN, "cn=stable-renamed", true, "")); err != nil {
		t.Fatalf("rename LDAP group: %v", err)
	}
	stableGroupDN = "cn=stable-renamed,ou=groups,dc=example,dc=test"
	preview, err = service.PreviewGroupMappings(ctx, pro_interfaces.LDAPGroupPreviewRequest{
		ActorID: &actorID, ActorIsAdmin: true, ProviderID: provider.ID,
		Source: "manual", Now: now.Add(5 * time.Second),
	})
	if err != nil || len(preview.Additions) != 0 || len(preview.Removals) != 0 {
		t.Fatalf("stable-ID preview after group rename: additions=%d removals=%d err=%v",
			len(preview.Additions), len(preview.Removals), err)
	}

	featureLDAPReplaceGroupMember(t, directory, stableGroupDN,
		"uid=duplicate-one,ou=duplicate-one,ou=people,dc=example,dc=test")
	featureLDAPReplaceGroupMember(t, directory,
		"cn=nested-leaf,ou=groups,dc=example,dc=test",
		"uid=duplicate-one,ou=duplicate-one,ou=people,dc=example,dc=test")
	preview, err = service.PreviewGroupMappings(ctx, pro_interfaces.LDAPGroupPreviewRequest{
		ActorID: &actorID, ActorIsAdmin: true, ProviderID: provider.ID,
		Source: "manual", Now: now.Add(6 * time.Second),
	})
	if err != nil || len(preview.Removals) != 2 {
		t.Fatalf("preview LDAP group removals: removals=%d err=%v", len(preview.Removals), err)
	}
	if _, err = service.ApplyGroupPreview(ctx, pro_interfaces.LDAPGroupApplyRequest{
		ActorID: admin.ID, ActorIsAdmin: true, ProviderID: provider.ID,
		Token: preview.Token, Now: now.Add(7 * time.Second),
	}); err != nil {
		t.Fatalf("apply LDAP group removals: %v", err)
	}
	featureLDAPRequireRoleIDs(t, store, user.ID, manualRole.ID)

	featureLDAPReplaceGroupMember(t, directory, stableGroupDN,
		"uid=alice,ou=people,dc=example,dc=test")
	featureLDAPReplaceGroupMember(t, directory,
		"cn=nested-leaf,ou=groups,dc=example,dc=test",
		"uid=alice,ou=people,dc=example,dc=test")
	if _, err = service.ReconcileGroupMappings(ctx, pro_interfaces.LDAPGroupPreviewRequest{
		ProviderID: provider.ID, Source: "scheduled", Now: now.Add(8 * time.Second),
	}); err != nil {
		t.Fatalf("scheduled LDAP group retry addition: %v", err)
	}
	featureLDAPRequireRoleIDs(t, store, user.ID, manualRole.ID, nestedRole.ID, stableRole.ID)

	storedProvider, err := store.GetLDAPProvider(provider.ID)
	if err != nil {
		t.Fatalf("load LDAP group provider: %v", err)
	}
	storedProvider.ServerURL = closedFeatureLDAPIntegrationURL(t)
	if err = store.SaveLDAPProvider(storedProvider); err != nil {
		t.Fatalf("simulate LDAP group outage: %v", err)
	}
	if _, err = service.ReconcileGroupMappings(ctx, pro_interfaces.LDAPGroupPreviewRequest{
		ProviderID: provider.ID, Source: "scheduled", Now: now.Add(9 * time.Second),
	}); !errors.Is(err, pro_interfaces.ErrLDAPProviderUnavailable) {
		t.Fatalf("LDAP group outage = %v, want provider unavailable", err)
	}
	featureLDAPRequireRoleIDs(t, store, user.ID, manualRole.ID, nestedRole.ID, stableRole.ID)

	storedProvider.ServerURL = serverURL
	if err = store.SaveLDAPProvider(storedProvider); err != nil {
		t.Fatalf("restore LDAP group provider: %v", err)
	}
	featureLDAPReplaceGroupMember(t, directory, stableGroupDN,
		"uid=duplicate-one,ou=duplicate-one,ou=people,dc=example,dc=test")
	featureLDAPReplaceGroupMember(t, directory,
		"cn=nested-leaf,ou=groups,dc=example,dc=test",
		"uid=duplicate-one,ou=duplicate-one,ou=people,dc=example,dc=test")
	if _, err = service.ReconcileGroupMappings(ctx, pro_interfaces.LDAPGroupPreviewRequest{
		ProviderID: provider.ID, Source: "scheduled", Now: now.Add(10 * time.Second),
	}); err != nil {
		t.Fatalf("LDAP group reconciliation after recovery: %v", err)
	}
	featureLDAPRequireRoleIDs(t, store, user.ID, manualRole.ID)
	history, err := service.GroupReconciliationHistory(ctx, provider.ID, 20)
	if err != nil || !containsFeatureLDAPReconciliationStatus(history, "stale") {
		t.Fatalf("LDAP group outage history: history=%#v err=%v", history, err)
	}
}

func openFeatureLDAPIntegrationAdmin(t *testing.T, caPEM []byte) *ldap.Conn {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("parse LDAP integration CA")
	}
	connection, err := ldap.DialURL(requiredFeatureLDAPIntegrationEnv(t, "SEMAPHORE_LDAP_URL"),
		ldap.DialWithTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}))
	if err != nil {
		t.Fatalf("connect LDAP integration admin: %v", err)
	}
	if err = connection.Bind("cn=admin,dc=example,dc=test",
		requiredFeatureLDAPIntegrationEnv(t, "SEMAPHORE_LDAP_ADMIN_PASSWORD")); err != nil {
		connection.Close()
		t.Fatalf("bind LDAP integration admin: %v", err)
	}
	return connection
}

func featureLDAPIntegrationEntryUUID(t *testing.T, connection *ldap.Conn, dn string) string {
	t.Helper()
	result, err := connection.Search(ldap.NewSearchRequest(
		dn, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, 5, false,
		"(objectClass=*)", []string{"entryUUID"}, nil))
	if err != nil || len(result.Entries) != 1 {
		t.Fatalf("read LDAP group entryUUID for %s: entries=%d err=%v", dn, len(result.Entries), err)
	}
	return "entryuuid:" + strings.ToLower(result.Entries[0].GetAttributeValue("entryUUID"))
}

func featureLDAPUserHasGroups(
	snapshot pro_interfaces.LDAPGroupDirectorySnapshot,
	userPrefix string,
	groupIDs ...string,
) bool {
	for _, user := range snapshot.Users {
		if !strings.HasPrefix(user.ExternalID, userPrefix) {
			continue
		}
		groups := make(map[string]bool, len(user.GroupExternalIDs))
		for _, groupID := range user.GroupExternalIDs {
			groups[groupID] = true
		}
		matches := true
		for _, groupID := range groupIDs {
			matches = matches && groups[groupID]
		}
		if matches {
			return true
		}
	}
	return false
}

func featureLDAPReplaceGroupMember(t *testing.T, connection *ldap.Conn, groupDN string, memberDN string) {
	t.Helper()
	request := ldap.NewModifyRequest(groupDN, nil)
	request.Replace("member", []string{memberDN})
	if err := connection.Modify(request); err != nil {
		t.Fatalf("replace LDAP group member for %s: %v", groupDN, err)
	}
}

func featureLDAPRequireRoleIDs(
	t *testing.T,
	store interface {
		GetGlobalRoleAssignments(int) ([]db.GlobalRoleAssignment, error)
	},
	userID int,
	want ...db.ProjectRoleID,
) {
	t.Helper()
	assignments, err := store.GetGlobalRoleAssignments(userID)
	if err != nil {
		t.Fatalf("load LDAP group role assignments: %v", err)
	}
	got := make([]string, 0, len(assignments))
	for _, assignment := range assignments {
		got = append(got, string(assignment.RoleID))
	}
	expected := make([]string, 0, len(want))
	for _, roleID := range want {
		expected = append(expected, string(roleID))
	}
	sort.Strings(got)
	sort.Strings(expected)
	if strings.Join(got, ",") != strings.Join(expected, ",") {
		t.Fatalf("LDAP group role IDs = %v, want %v", got, expected)
	}
}

func containsFeatureLDAPReconciliationStatus(history []db.LDAPGroupReconciliation, want string) bool {
	for _, item := range history {
		if item.Status == want {
			return true
		}
	}
	return false
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
