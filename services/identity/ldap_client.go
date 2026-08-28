package identity

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

const (
	ldapOperationTimeout = 5 * time.Second
	ldapSearchSizeLimit  = 2
	ldapUsernameMarker   = "{{username}}"
)

var ldapAttributePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*$`)

var immutableLDAPAttributes = map[string]string{
	"entryuuid":   "entryUUID",
	"objectguid":  "objectGUID",
	"nsuniqueid":  "nsUniqueId",
	"ipauniqueid": "ipaUniqueID",
}

type ldapConnection interface {
	Bind(username string, password string) error
	Search(*ldap.SearchRequest) (*ldap.SearchResult, error)
	StartTLS(*tls.Config) error
	SetTimeout(time.Duration)
	Close() error
}

type ldapDialFunc func(
	serverURL string,
	mode pro_interfaces.LDAPTLSMode,
	tlsConfig *tls.Config,
	timeout time.Duration,
) (ldapConnection, error)

type ldapClient struct {
	dial    ldapDialFunc
	timeout time.Duration
}

// NewLDAPClient returns the production outbound LDAP protocol adapter.
func NewLDAPClient() pro_interfaces.LDAPClient {
	return &ldapClient{dial: dialLDAP, timeout: ldapOperationTimeout}
}

func (*ldapClient) Validate(configuration pro_interfaces.LDAPClientConfiguration) error {
	_, _, err := validateLDAPConfiguration(configuration)
	return err
}

func (c *ldapClient) Authenticate(
	ctx context.Context,
	request pro_interfaces.LDAPClientRequest,
) (pro_interfaces.LDAPClientResult, error) {
	result := pro_interfaces.LDAPClientResult{Readiness: pro_interfaces.LDAPReadiness{
		Status: pro_interfaces.LDAPReadinessFailed,
	}}
	if err := ctx.Err(); err != nil {
		return result, fmt.Errorf("LDAP request cancelled: %w", err)
	}
	serverURL, tlsConfig, err := validateLDAPConfiguration(request.Configuration)
	if err != nil {
		result.Readiness.Code = "invalid_configuration"
		return result, err
	}
	if strings.TrimSpace(request.Username) == "" || request.Credential == "" {
		result.Readiness.Code = "invalid_credentials"
		return result, pro_interfaces.ErrLDAPInvalidCredentials
	}

	connection, err := c.dial(serverURL, request.Configuration.TLSMode, tlsConfig, c.timeout)
	if err != nil {
		result.Readiness.Code = "connection_failed"
		return result, fmt.Errorf("%w: connect", pro_interfaces.ErrLDAPProviderUnavailable)
	}
	defer func() { _ = connection.Close() }()
	connection.SetTimeout(c.timeout)
	result.Readiness.Connection = true

	if err = connection.Bind(request.Configuration.BindDN, request.Configuration.BindPassword); err != nil {
		result.Readiness.Code = "service_bind_failed"
		return result, fmt.Errorf("%w: service bind", pro_interfaces.ErrLDAPProviderUnavailable)
	}

	filter, err := buildLDAPFilter(request.Configuration.UserFilter, request.Username)
	if err != nil {
		result.Readiness.Code = "invalid_filter"
		return result, err
	}
	attributes := []string{
		request.Configuration.IdentityAttribute,
		request.Configuration.UsernameAttribute,
		request.Configuration.NameAttribute,
		request.Configuration.EmailAttribute,
	}
	searchRequest := ldap.NewSearchRequest(
		request.Configuration.SearchBaseDN,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		ldapSearchSizeLimit,
		int(c.timeout/time.Second),
		false,
		filter,
		attributes,
		nil,
	)
	searchRequest.EnforceSizeLimit = true
	searchResult, err := connection.Search(searchRequest)
	if errors.Is(err, ldap.ErrSizeLimitExceeded) {
		result.Readiness.Code = "duplicate_identity"
		return result, pro_interfaces.ErrLDAPDuplicateIdentity
	}
	if ldap.IsErrorWithCode(err, ldap.LDAPResultReferral) {
		result.Readiness.Code = "referral_rejected"
		return result, pro_interfaces.ErrLDAPReferral
	}
	if err != nil {
		result.Readiness.Code = "search_failed"
		return result, fmt.Errorf("%w: search", pro_interfaces.ErrLDAPProviderUnavailable)
	}
	if len(searchResult.Referrals) != 0 {
		result.Readiness.Code = "referral_rejected"
		return result, pro_interfaces.ErrLDAPReferral
	}
	if len(searchResult.Entries) == 0 {
		result.Readiness.Code = "invalid_credentials"
		return result, pro_interfaces.ErrLDAPInvalidCredentials
	}
	if len(searchResult.Entries) != 1 {
		result.Readiness.Code = "duplicate_identity"
		return result, pro_interfaces.ErrLDAPDuplicateIdentity
	}
	result.Readiness.Search = true

	entry := searchResult.Entries[0]
	identity, err := ldapIdentityFromEntry(entry, request.Configuration)
	if err != nil {
		result.Readiness.Code = "invalid_identity"
		return result, err
	}
	if err = connection.Bind(entry.DN, request.Credential); err != nil {
		result.Readiness.Code = "invalid_credentials"
		if ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
			return result, pro_interfaces.ErrLDAPInvalidCredentials
		}
		return result, fmt.Errorf("%w: user bind", pro_interfaces.ErrLDAPProviderUnavailable)
	}

	result.Identity = identity
	result.Readiness.Bind = true
	result.Readiness.Status = pro_interfaces.LDAPReadinessReady
	result.Readiness.Code = "ready"
	return result, nil
}

func validateLDAPConfiguration(
	configuration pro_interfaces.LDAPClientConfiguration,
) (string, *tls.Config, error) {
	parsed, err := url.Parse(configuration.ServerURL)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Path != "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", nil, fmt.Errorf("invalid LDAP server URL")
	}
	expectedScheme := "ldaps"
	if configuration.TLSMode == pro_interfaces.LDAPTLSModeStartTLS {
		expectedScheme = "ldap"
	} else if configuration.TLSMode != pro_interfaces.LDAPTLSModeLDAPS {
		return "", nil, fmt.Errorf("unsupported LDAP TLS mode")
	}
	if parsed.Scheme != expectedScheme {
		return "", nil, fmt.Errorf("LDAP URL and TLS mode do not match")
	}
	if net.ParseIP(parsed.Hostname()) == nil && !validTLSHostname(parsed.Hostname()) {
		return "", nil, fmt.Errorf("invalid LDAP TLS hostname")
	}
	if _, err = ldap.ParseDN(configuration.BindDN); err != nil {
		return "", nil, fmt.Errorf("invalid LDAP bind DN")
	}
	if configuration.BindPassword == "" {
		return "", nil, fmt.Errorf("LDAP bind password is required")
	}
	if _, err = ldap.ParseDN(configuration.SearchBaseDN); err != nil {
		return "", nil, fmt.Errorf("invalid LDAP search base DN")
	}
	if _, err = buildLDAPFilter(configuration.UserFilter, "validation"); err != nil {
		return "", nil, err
	}
	if _, err = canonicalIdentityAttribute(configuration.IdentityAttribute); err != nil {
		return "", nil, err
	}
	for _, attribute := range []string{
		configuration.UsernameAttribute,
		configuration.NameAttribute,
		configuration.EmailAttribute,
	} {
		if !ldapAttributePattern.MatchString(attribute) {
			return "", nil, fmt.Errorf("invalid LDAP attribute mapping")
		}
	}

	var roots *x509.CertPool
	switch configuration.TrustMode {
	case pro_interfaces.LDAPTrustModeSystem:
		roots, err = x509.SystemCertPool()
		if err != nil {
			return "", nil, fmt.Errorf("load system LDAP trust: %w", err)
		}
	case pro_interfaces.LDAPTrustModeCustom:
		roots = x509.NewCertPool()
		if strings.TrimSpace(configuration.CAPEM) == "" ||
			!roots.AppendCertsFromPEM([]byte(configuration.CAPEM)) {
			return "", nil, fmt.Errorf("invalid LDAP CA certificate")
		}
	default:
		return "", nil, fmt.Errorf("unsupported LDAP trust mode")
	}
	return parsed.String(), &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    roots,
		ServerName: parsed.Hostname(),
	}, nil
}

func dialLDAP(
	serverURL string,
	mode pro_interfaces.LDAPTLSMode,
	tlsConfig *tls.Config,
	timeout time.Duration,
) (ldapConnection, error) {
	dialer := &net.Dialer{Timeout: timeout}
	connection, err := ldap.DialURL(
		serverURL,
		ldap.DialWithDialer(dialer),
		ldap.DialWithTLSConfig(tlsConfig),
	)
	if err != nil {
		return nil, err
	}
	connection.SetTimeout(timeout)
	if mode == pro_interfaces.LDAPTLSModeStartTLS {
		if err = connection.StartTLS(tlsConfig); err != nil {
			_ = connection.Close()
			return nil, err
		}
	}
	return connection, nil
}

func buildLDAPFilter(template string, username string) (string, error) {
	if strings.Count(template, ldapUsernameMarker) != 1 {
		return "", fmt.Errorf("LDAP user filter must contain exactly one %s marker", ldapUsernameMarker)
	}
	filter := strings.Replace(template, ldapUsernameMarker, ldap.EscapeFilter(username), 1)
	if _, err := ldap.CompileFilter(filter); err != nil {
		return "", fmt.Errorf("invalid LDAP user filter: %w", err)
	}
	return filter, nil
}

func ldapIdentityFromEntry(
	entry *ldap.Entry,
	configuration pro_interfaces.LDAPClientConfiguration,
) (pro_interfaces.LDAPIdentity, error) {
	identityAttribute, err := canonicalIdentityAttribute(configuration.IdentityAttribute)
	if err != nil {
		return pro_interfaces.LDAPIdentity{}, err
	}
	identityValues := entry.GetEqualFoldRawAttributeValues(identityAttribute)
	if len(identityValues) != 1 {
		return pro_interfaces.LDAPIdentity{}, fmt.Errorf("LDAP immutable identity must have exactly one value")
	}
	normalizedIdentity, err := normalizeLDAPIdentity(identityAttribute, identityValues[0])
	if err != nil {
		return pro_interfaces.LDAPIdentity{}, err
	}
	identity := pro_interfaces.LDAPIdentity{
		ExternalID: strings.ToLower(identityAttribute) + ":" + normalizedIdentity,
		Username: strings.ToLower(strings.TrimSpace(
			entry.GetEqualFoldAttributeValue(configuration.UsernameAttribute))),
		Name:  strings.TrimSpace(entry.GetEqualFoldAttributeValue(configuration.NameAttribute)),
		Email: strings.ToLower(strings.TrimSpace(entry.GetEqualFoldAttributeValue(configuration.EmailAttribute))),
	}
	if identity.Username == "" || identity.Name == "" || identity.Email == "" {
		return pro_interfaces.LDAPIdentity{}, fmt.Errorf("LDAP identity is missing required attributes")
	}
	return identity, nil
}

func canonicalIdentityAttribute(attribute string) (string, error) {
	canonical, ok := immutableLDAPAttributes[strings.ToLower(strings.TrimSpace(attribute))]
	if !ok {
		return "", fmt.Errorf("LDAP identity attribute is not allow-listed")
	}
	return canonical, nil
}

func normalizeLDAPIdentity(attribute string, value []byte) (string, error) {
	if strings.EqualFold(attribute, "objectGUID") && len(value) == 16 {
		value = []byte{
			value[3], value[2], value[1], value[0],
			value[5], value[4], value[7], value[6],
			value[8], value[9], value[10], value[11], value[12], value[13], value[14], value[15],
		}
		return formatUUID(value), nil
	}
	compact := strings.ToLower(strings.TrimSpace(string(value)))
	compact = strings.TrimPrefix(compact, "{")
	compact = strings.TrimSuffix(compact, "}")
	compact = strings.ReplaceAll(compact, "-", "")
	decoded, err := hex.DecodeString(compact)
	if err != nil || len(decoded) != 16 {
		return "", fmt.Errorf("LDAP immutable identity is not a UUID")
	}
	return formatUUID(decoded), nil
}

func formatUUID(value []byte) string {
	encoded := hex.EncodeToString(value)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" +
		encoded[16:20] + "-" + encoded[20:32]
}

func validTLSHostname(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
				(character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

var _ pro_interfaces.LDAPClient = (*ldapClient)(nil)
