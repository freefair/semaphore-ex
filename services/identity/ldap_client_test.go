package identity

import (
	"context"
	"crypto/tls"
	"errors"
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
