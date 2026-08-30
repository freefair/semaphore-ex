package identity

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeLDAPConnection struct {
	binds         [][2]string
	searchRequest *ldap.SearchRequest
	searchResult  *ldap.SearchResult
	searchError   error
	bindErrorAt   int
	bindError     error
	pagedRequests []*ldap.SearchRequest
	pagedResults  []*ldap.SearchResult
	pagedError    error
}

func (f *fakeLDAPConnection) Bind(username string, password string) error {
	f.binds = append(f.binds, [2]string{username, password})
	if f.bindErrorAt == len(f.binds) {
		return f.bindError
	}
	return nil
}

func (f *fakeLDAPConnection) Search(request *ldap.SearchRequest) (*ldap.SearchResult, error) {
	f.searchRequest = request
	f.pagedRequests = append(f.pagedRequests, request)
	if len(f.pagedResults) != 0 {
		index := len(f.pagedRequests) - 1
		if index >= len(f.pagedResults) {
			return &ldap.SearchResult{}, nil
		}
		if f.pagedError != nil {
			return nil, f.pagedError
		}
		return f.pagedResults[index], nil
	}
	return f.searchResult, f.searchError
}

func (*fakeLDAPConnection) StartTLS(*tls.Config) error { return nil }
func (*fakeLDAPConnection) SetTimeout(time.Duration)   {}
func (*fakeLDAPConnection) Close() error               { return nil }

func TestBuildLDAPFilterEscapesUserControlledAssertionValue(t *testing.T) {
	filter, err := buildLDAPFilter("(&(objectClass=person)(uid={{username}}))", "*)(uid=*)")

	require.NoError(t, err)
	assert.Equal(t, `(&(objectClass=person)(uid=\2a\29\28uid=\2a\29))`, filter)
}

func TestBuildLDAPFilterRequiresExactlyOneMarker(t *testing.T) {
	_, missingErr := buildLDAPFilter("(uid=static)", "alice")
	_, duplicateErr := buildLDAPFilter("(|(uid={{username}})(mail={{username}}))", "alice")

	assert.Error(t, missingErr)
	assert.Error(t, duplicateErr)
}

func TestNormalizeLDAPIdentityHandlesRFCUUIDAndActiveDirectoryObjectGUID(t *testing.T) {
	entryUUID, err := normalizeLDAPIdentity("entryUUID", []byte("{00112233-4455-6677-8899-AABBCCDDEEFF}"))
	require.NoError(t, err)
	assert.Equal(t, "00112233-4455-6677-8899-aabbccddeeff", entryUUID)

	objectGUID, err := normalizeLDAPIdentity("objectGUID", []byte{
		0x33, 0x22, 0x11, 0x00, 0x55, 0x44, 0x77, 0x66,
		0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
	})
	require.NoError(t, err)
	assert.Equal(t, "00112233-4455-6677-8899-aabbccddeeff", objectGUID)
}

func TestLDAPClientUsesBoundedSearchAndImmutableIdentity(t *testing.T) {
	connection := &fakeLDAPConnection{searchResult: &ldap.SearchResult{
		Entries: []*ldap.Entry{ldap.NewEntry(
			"uid=alice,ou=people,dc=example,dc=test",
			map[string][]string{
				"entryUUID": {"00112233-4455-6677-8899-aabbccddeeff"},
				"uid":       {"Alice"},
				"cn":        {"Alice Example"},
				"mail":      {"Alice@Example.Test"},
			},
		)},
	}}
	client := &ldapClient{
		timeout: ldapOperationTimeout,
		dial: func(serverURL string, mode pro_interfaces.LDAPTLSMode, config *tls.Config, timeout time.Duration) (ldapConnection, error) {
			assert.Equal(t, "ldaps://ldap.example.test:636", serverURL)
			assert.Equal(t, pro_interfaces.LDAPTLSModeLDAPS, mode)
			assert.Equal(t, "ldap.example.test", config.ServerName)
			assert.Equal(t, ldapOperationTimeout, timeout)
			return connection, nil
		},
	}

	result, err := client.Authenticate(context.Background(), pro_interfaces.LDAPClientRequest{
		Configuration: validLDAPClientConfiguration(),
		Username:      "*)(uid=*)",
		Credential:    "user-password",
	})

	require.NoError(t, err)
	assert.Equal(t, "entryuuid:00112233-4455-6677-8899-aabbccddeeff", result.Identity.ExternalID)
	assert.Equal(t, "alice", result.Identity.Username)
	assert.Equal(t, "alice@example.test", result.Identity.Email)
	require.NotNil(t, connection.searchRequest)
	assert.Equal(t, ldapSearchSizeLimit, connection.searchRequest.SizeLimit)
	assert.Equal(t, int(ldapOperationTimeout/time.Second), connection.searchRequest.TimeLimit)
	assert.True(t, connection.searchRequest.EnforceSizeLimit)
	assert.Equal(t, ldap.NeverDerefAliases, connection.searchRequest.DerefAliases)
	assert.Equal(t, `(&(objectClass=person)(uid=\2a\29\28uid=\2a\29))`, connection.searchRequest.Filter)
	assert.Equal(t, [][2]string{
		{"cn=reader,dc=example,dc=test", "bind-password"},
		{"uid=alice,ou=people,dc=example,dc=test", "user-password"},
	}, connection.binds)
}

func TestLDAPClientRejectsReferrals(t *testing.T) {
	connection := &fakeLDAPConnection{searchResult: &ldap.SearchResult{
		Referrals: []string{"ldaps://outside.example.test/dc=escape"},
	}}
	client := &ldapClient{
		timeout: ldapOperationTimeout,
		dial: func(string, pro_interfaces.LDAPTLSMode, *tls.Config, time.Duration) (ldapConnection, error) {
			return connection, nil
		},
	}

	_, err := client.Authenticate(context.Background(), pro_interfaces.LDAPClientRequest{
		Configuration: validLDAPClientConfiguration(), Username: "alice", Credential: "password",
	})

	assert.ErrorIs(t, err, pro_interfaces.ErrLDAPReferral)
}

func TestLDAPClientRejectsReferralResultCodes(t *testing.T) {
	connection := &fakeLDAPConnection{
		searchError: ldap.NewError(ldap.LDAPResultReferral, errors.New("directory referral detail")),
	}
	client := &ldapClient{
		timeout: ldapOperationTimeout,
		dial: func(string, pro_interfaces.LDAPTLSMode, *tls.Config, time.Duration) (ldapConnection, error) {
			return connection, nil
		},
	}

	_, err := client.Authenticate(context.Background(), pro_interfaces.LDAPClientRequest{
		Configuration: validLDAPClientConfiguration(), Username: "alice", Credential: "password",
	})

	assert.ErrorIs(t, err, pro_interfaces.ErrLDAPReferral)
	assert.NotContains(t, err.Error(), "directory referral detail")
}

func TestLDAPClientMapsUserBindFailureWithoutLeakingDiagnostics(t *testing.T) {
	connection := &fakeLDAPConnection{
		searchResult: &ldap.SearchResult{Entries: []*ldap.Entry{ldap.NewEntry(
			"uid=alice,ou=people,dc=example,dc=test",
			map[string][]string{
				"entryUUID": {"00112233-4455-6677-8899-aabbccddeeff"},
				"uid":       {"alice"}, "cn": {"Alice"}, "mail": {"alice@example.test"},
			},
		)}},
		bindErrorAt: 2,
		bindError:   ldap.NewError(ldap.LDAPResultInvalidCredentials, errors.New("directory detail")),
	}
	client := &ldapClient{
		timeout: ldapOperationTimeout,
		dial: func(string, pro_interfaces.LDAPTLSMode, *tls.Config, time.Duration) (ldapConnection, error) {
			return connection, nil
		},
	}

	_, err := client.Authenticate(context.Background(), pro_interfaces.LDAPClientRequest{
		Configuration: validLDAPClientConfiguration(), Username: "alice", Credential: "wrong",
	})

	assert.ErrorIs(t, err, pro_interfaces.ErrLDAPInvalidCredentials)
	assert.NotContains(t, err.Error(), "directory detail")
}

func TestValidateLDAPConfigurationRejectsDowngradeAndMutableIdentity(t *testing.T) {
	configuration := validLDAPClientConfiguration()
	configuration.ServerURL = "ldap://ldap.example.test:389"
	_, _, downgradeErr := validateLDAPConfiguration(configuration)

	configuration = validLDAPClientConfiguration()
	configuration.IdentityAttribute = "uid"
	_, _, identityErr := validateLDAPConfiguration(configuration)

	configuration = validLDAPClientConfiguration()
	configuration.TrustMode = pro_interfaces.LDAPTrustModeCustom
	configuration.CAPEM = ""
	_, _, trustErr := validateLDAPConfiguration(configuration)

	assert.Error(t, downgradeErr)
	assert.Error(t, identityErr)
	assert.Error(t, trustErr)
}

func TestLDAPGroupSnapshotUsesPagingAndBoundsNestedCycles(t *testing.T) {
	connection := &fakeLDAPConnection{pagedResults: []*ldap.SearchResult{
		{Entries: []*ldap.Entry{
			ldap.NewEntry("uid=alice,ou=people,dc=example,dc=test", map[string][]string{
				"entryUUID": {"00112233-4455-6677-8899-aabbccddeeff"},
			}),
		}},
		{Entries: []*ldap.Entry{
			ldap.NewEntry("cn=direct,ou=groups,dc=example,dc=test", map[string][]string{
				"entryUUID": {"10112233-4455-6677-8899-aabbccddeeff"},
				"member":    {"uid=alice,ou=people,dc=example,dc=test", "cn=parent,ou=groups,dc=example,dc=test"},
			}),
			ldap.NewEntry("cn=parent,ou=groups,dc=example,dc=test", map[string][]string{
				"entryUUID": {"20112233-4455-6677-8899-aabbccddeeff"},
				"member":    {"cn=direct,ou=groups,dc=example,dc=test"},
			}),
		}},
	}}
	client := &ldapClient{
		timeout: ldapOperationTimeout,
		dial: func(string, pro_interfaces.LDAPTLSMode, *tls.Config, time.Duration) (ldapConnection, error) {
			return connection, nil
		},
	}
	configuration := validLDAPClientConfiguration()
	configuration.GroupSearchBaseDN = "ou=groups,dc=example,dc=test"
	configuration.GroupUserFilter = "(objectClass=person)"
	configuration.GroupFilter = "(objectClass=groupOfNames)"
	configuration.GroupIdentityAttribute = "entryUUID"
	configuration.GroupMemberAttribute = "member"
	configuration.GroupMaxDepth = 4

	snapshot, err := client.ReadGroupSnapshot(context.Background(), configuration)

	require.NoError(t, err)
	require.Len(t, snapshot.Users, 1)
	assert.Equal(t, []string{
		"entryuuid:10112233-4455-6677-8899-aabbccddeeff",
		"entryuuid:20112233-4455-6677-8899-aabbccddeeff",
	}, snapshot.Users[0].GroupExternalIDs)
	assert.NotEmpty(t, snapshot.Revision)
	assert.Len(t, connection.pagedRequests, 2)
	assert.Equal(t, ldap.NeverDerefAliases, connection.pagedRequests[0].DerefAliases)
}

func TestLDAPGroupSearchRejectsEntryLimitBeforeAppendingNextPage(t *testing.T) {
	entries := make([]*ldap.Entry, ldapGroupEntryLimit)
	for index := range entries {
		entries[index] = ldap.NewEntry(fmt.Sprintf("uid=user-%d,dc=example,dc=test", index), nil)
	}
	page := ldap.NewControlPaging(ldapGroupPageSize)
	page.SetCookie([]byte("next-page"))
	connection := &fakeLDAPConnection{pagedResults: []*ldap.SearchResult{
		{Entries: entries, Controls: []ldap.Control{page}},
		{Entries: []*ldap.Entry{ldap.NewEntry("uid=overflow,dc=example,dc=test", nil)}},
	}}
	client := &ldapClient{}

	_, err := client.searchLDAPPageSet(context.Background(), connection, ldap.NewSearchRequest(
		"dc=example,dc=test", ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		"(objectClass=person)", nil, nil,
	))

	assert.ErrorIs(t, err, pro_interfaces.ErrLDAPProviderUnavailable)
	assert.Contains(t, err.Error(), "entries exceed")
	require.Len(t, connection.pagedRequests, 3)
	abortControl, ok := ldap.FindControl(connection.pagedRequests[2].Controls, ldap.ControlTypePaging).(*ldap.ControlPaging)
	require.True(t, ok)
	assert.Zero(t, abortControl.PagingSize)
}

func TestLDAPGroupSearchFollowsPagingCookie(t *testing.T) {
	page := ldap.NewControlPaging(ldapGroupPageSize)
	page.SetCookie([]byte("next-page"))
	connection := &fakeLDAPConnection{pagedResults: []*ldap.SearchResult{
		{Entries: []*ldap.Entry{ldap.NewEntry("uid=one,dc=example,dc=test", nil)}, Controls: []ldap.Control{page}},
		{Entries: []*ldap.Entry{ldap.NewEntry("uid=two,dc=example,dc=test", nil)}},
	}}
	client := &ldapClient{}

	entries, err := client.searchLDAPPageSet(context.Background(), connection, ldap.NewSearchRequest(
		"dc=example,dc=test", ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		"(objectClass=person)", nil, nil,
	))

	require.NoError(t, err)
	assert.Len(t, entries, 2)
	require.Len(t, connection.pagedRequests, 2)
	control, ok := ldap.FindControl(connection.pagedRequests[1].Controls, ldap.ControlTypePaging).(*ldap.ControlPaging)
	require.True(t, ok)
	assert.Equal(t, []byte("next-page"), control.Cookie)
}

func TestLDAPGroupSearchRejectsReferralsOnEveryPage(t *testing.T) {
	connection := &fakeLDAPConnection{pagedResults: []*ldap.SearchResult{{
		Referrals: []string{"ldaps://outside.example.test/dc=escape"},
	}}}
	client := &ldapClient{}

	_, err := client.searchLDAPPageSet(context.Background(), connection, ldap.NewSearchRequest(
		"dc=example,dc=test", ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		"(objectClass=person)", nil, nil,
	))

	assert.ErrorIs(t, err, pro_interfaces.ErrLDAPReferral)
}

func TestLDAPGroupSearchRejectsNonProgressingPages(t *testing.T) {
	t.Run("empty page with cookie", func(t *testing.T) {
		page := ldap.NewControlPaging(ldapGroupPageSize)
		page.SetCookie([]byte("next-page"))
		connection := &fakeLDAPConnection{pagedResults: []*ldap.SearchResult{{Controls: []ldap.Control{page}}}}
		client := &ldapClient{}

		_, err := client.searchLDAPPageSet(context.Background(), connection, ldap.NewSearchRequest(
			"dc=example,dc=test", ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
			"(objectClass=person)", nil, nil,
		))

		assert.ErrorIs(t, err, pro_interfaces.ErrLDAPProviderUnavailable)
		assert.Contains(t, err.Error(), "paging progress")
	})

	t.Run("repeated cookie", func(t *testing.T) {
		firstPage := ldap.NewControlPaging(ldapGroupPageSize)
		firstPage.SetCookie([]byte("same-cookie"))
		secondPage := ldap.NewControlPaging(ldapGroupPageSize)
		secondPage.SetCookie([]byte("same-cookie"))
		connection := &fakeLDAPConnection{pagedResults: []*ldap.SearchResult{
			{Entries: []*ldap.Entry{ldap.NewEntry("uid=one,dc=example,dc=test", nil)}, Controls: []ldap.Control{firstPage}},
			{Entries: []*ldap.Entry{ldap.NewEntry("uid=two,dc=example,dc=test", nil)}, Controls: []ldap.Control{secondPage}},
		}}
		client := &ldapClient{}

		_, err := client.searchLDAPPageSet(context.Background(), connection, ldap.NewSearchRequest(
			"dc=example,dc=test", ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
			"(objectClass=person)", nil, nil,
		))

		assert.ErrorIs(t, err, pro_interfaces.ErrLDAPProviderUnavailable)
		assert.Contains(t, err.Error(), "paging progress")
	})
}

func TestLDAPGroupSnapshotRejectsMemberAndGraphLimits(t *testing.T) {
	configuration := validLDAPClientConfiguration()
	configuration.GroupSearchBaseDN = "ou=groups,dc=example,dc=test"
	configuration.GroupUserFilter = "(objectClass=person)"
	configuration.GroupFilter = "(objectClass=groupOfNames)"
	configuration.GroupIdentityAttribute = "entryUUID"
	configuration.GroupMemberAttribute = "member"
	configuration.GroupMaxDepth = 4

	t.Run("member values", func(t *testing.T) {
		members := make([]string, ldapGroupMemberLimit+1)
		for index := range members {
			members[index] = "uid=member,ou=people,dc=example,dc=test"
		}
		connection := &fakeLDAPConnection{pagedResults: []*ldap.SearchResult{
			{Entries: []*ldap.Entry{ldap.NewEntry("uid=member,ou=people,dc=example,dc=test", map[string][]string{
				"entryUUID": {"00112233-4455-6677-8899-aabbccddeeff"},
			})}},
			{Entries: []*ldap.Entry{ldap.NewEntry("cn=oversized,ou=groups,dc=example,dc=test", map[string][]string{
				"entryUUID": {"10112233-4455-6677-8899-aabbccddeeff"}, "member": members,
			})}},
		}}
		client := &ldapClient{timeout: ldapOperationTimeout, dial: func(string, pro_interfaces.LDAPTLSMode, *tls.Config, time.Duration) (ldapConnection, error) {
			return connection, nil
		}}

		_, err := client.ReadGroupSnapshot(context.Background(), configuration)

		assert.ErrorIs(t, err, pro_interfaces.ErrLDAPProviderUnavailable)
		assert.Contains(t, err.Error(), "member values exceed")
	})

	t.Run("graph edges", func(t *testing.T) {
		users := make([]*ldap.Entry, 0, ldapGroupGraphEdgeLimit/2+1)
		groups := make([]*ldap.Entry, 0, ldapGroupGraphEdgeLimit/2+1)
		for index := 0; index <= ldapGroupGraphEdgeLimit/2; index++ {
			dn := fmt.Sprintf("cn=entry-%d,ou=groups,dc=example,dc=test", index)
			users = append(users, ldap.NewEntry(dn, map[string][]string{
				"entryUUID": {fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", index+1)},
			}))
			groups = append(groups, ldap.NewEntry(dn, map[string][]string{
				"entryUUID": {fmt.Sprintf("%08x-4455-6677-8899-aabbccd00000", index+1)}, "member": {dn},
			}))
		}
		connection := &fakeLDAPConnection{pagedResults: []*ldap.SearchResult{{Entries: users}, {Entries: groups}}}
		client := &ldapClient{timeout: ldapOperationTimeout, dial: func(string, pro_interfaces.LDAPTLSMode, *tls.Config, time.Duration) (ldapConnection, error) {
			return connection, nil
		}}

		_, err := client.ReadGroupSnapshot(context.Background(), configuration)

		assert.ErrorIs(t, err, pro_interfaces.ErrLDAPProviderUnavailable)
		assert.Contains(t, err.Error(), "membership graph edges exceed")
	})
}

func TestValidateLDAPGroupConfigurationRequiresStaticFiltersAndBoundedDepth(t *testing.T) {
	configuration := validLDAPClientConfiguration()
	configuration.GroupSearchBaseDN = "ou=groups,dc=example,dc=test"
	configuration.GroupUserFilter = "(uid={{username}})"
	configuration.GroupFilter = "(objectClass=groupOfNames)"
	configuration.GroupIdentityAttribute = "entryUUID"
	configuration.GroupMemberAttribute = "member"
	configuration.GroupMaxDepth = 17

	assert.Error(t, validateLDAPGroupConfiguration(configuration))
}

func validLDAPClientConfiguration() pro_interfaces.LDAPClientConfiguration {
	return pro_interfaces.LDAPClientConfiguration{
		ServerURL:         "ldaps://ldap.example.test:636",
		TLSMode:           pro_interfaces.LDAPTLSModeLDAPS,
		TrustMode:         pro_interfaces.LDAPTrustModeSystem,
		BindDN:            "cn=reader,dc=example,dc=test",
		BindPassword:      "bind-password",
		SearchBaseDN:      "ou=people,dc=example,dc=test",
		UserFilter:        "(&(objectClass=person)(uid={{username}}))",
		IdentityAttribute: "entryUUID",
		UsernameAttribute: "uid",
		NameAttribute:     "cn",
		EmailAttribute:    "mail",
	}
}
