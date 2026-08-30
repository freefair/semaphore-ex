package identity

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

const (
	ldapOperationTimeout    = 5 * time.Second
	ldapSearchSizeLimit     = 2
	ldapUsernameMarker      = "{{username}}"
	ldapGroupPageSize       = 100
	ldapGroupEntryLimit     = 10000
	ldapGroupMemberLimit    = 10000
	ldapGroupGraphEdgeLimit = 10000
	ldapGroupPageLimit      = 1024
	ldapResponseByteLimit   = 32 << 20
	ldapGroupMaxDepthLimit  = 16
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

func (c *ldapClient) ReadGroupSnapshot(
	ctx context.Context,
	configuration pro_interfaces.LDAPClientConfiguration,
) (pro_interfaces.LDAPGroupDirectorySnapshot, error) {
	result := pro_interfaces.LDAPGroupDirectorySnapshot{}
	if err := ctx.Err(); err != nil {
		return result, fmt.Errorf("LDAP group request cancelled: %w", err)
	}
	serverURL, tlsConfig, err := validateLDAPConfiguration(configuration)
	if err != nil {
		return result, err
	}
	if err = validateLDAPGroupConfiguration(configuration); err != nil {
		return result, err
	}
	connection, err := c.dial(serverURL, configuration.TLSMode, tlsConfig, c.timeout)
	if err != nil {
		return result, fmt.Errorf("%w: connect", pro_interfaces.ErrLDAPProviderUnavailable)
	}
	defer func() { _ = connection.Close() }()
	connection.SetTimeout(c.timeout)
	if err = connection.Bind(configuration.BindDN, configuration.BindPassword); err != nil {
		return result, fmt.Errorf("%w: service bind", pro_interfaces.ErrLDAPProviderUnavailable)
	}

	userEntries, err := c.searchLDAPPageSet(ctx, connection, ldap.NewSearchRequest(
		configuration.SearchBaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases,
		0, int(c.timeout/time.Second), false, configuration.GroupUserFilter,
		[]string{configuration.IdentityAttribute}, nil,
	))
	if err != nil {
		return result, err
	}
	groupEntries, err := c.searchLDAPPageSet(ctx, connection, ldap.NewSearchRequest(
		configuration.GroupSearchBaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases,
		0, int(c.timeout/time.Second), false, configuration.GroupFilter,
		[]string{configuration.GroupIdentityAttribute, configuration.GroupMemberAttribute}, nil,
	))
	if err != nil {
		return result, err
	}

	userByDN := make(map[string]string, len(userEntries))
	for _, entry := range userEntries {
		externalID, identityErr := immutableLDAPExternalID(entry, configuration.IdentityAttribute)
		if identityErr != nil {
			return result, identityErr
		}
		dn, dnErr := canonicalLDAPDN(entry.DN)
		if dnErr != nil {
			return result, dnErr
		}
		if existing := userByDN[dn]; existing != "" && existing != externalID {
			return result, pro_interfaces.ErrLDAPDuplicateIdentity
		}
		userByDN[dn] = externalID
	}

	type directoryGroup struct {
		externalID string
		memberDNs  []string
	}
	groupsByDN := make(map[string]directoryGroup, len(groupEntries))
	memberCount := 0
	for _, entry := range groupEntries {
		externalID, identityErr := immutableLDAPExternalID(entry, configuration.GroupIdentityAttribute)
		if identityErr != nil {
			return result, identityErr
		}
		dn, dnErr := canonicalLDAPDN(entry.DN)
		if dnErr != nil {
			return result, dnErr
		}
		members := entry.GetEqualFoldAttributeValues(configuration.GroupMemberAttribute)
		memberCount += len(members)
		if memberCount > ldapGroupMemberLimit {
			return result, ldapGroupLimitError("member values", ldapGroupMemberLimit)
		}
		memberDNs := make([]string, 0, len(members))
		for _, member := range members {
			memberDN, memberErr := canonicalLDAPDN(member)
			if memberErr != nil {
				return result, memberErr
			}
			memberDNs = append(memberDNs, memberDN)
		}
		sort.Strings(memberDNs)
		if _, exists := groupsByDN[dn]; exists {
			return result, pro_interfaces.ErrLDAPDuplicateIdentity
		}
		groupsByDN[dn] = directoryGroup{externalID: externalID, memberDNs: memberDNs}
		result.GroupExternalIDs = append(result.GroupExternalIDs, externalID)
	}
	sort.Strings(result.GroupExternalIDs)

	parents := make(map[string][]string)
	directGroups := make(map[string][]string)
	graphEdges := 0
	for groupDN, group := range groupsByDN {
		for _, memberDN := range group.memberDNs {
			if userExternalID := userByDN[memberDN]; userExternalID != "" {
				if err = addLDAPGroupGraphEdge(&graphEdges); err != nil {
					return result, err
				}
				directGroups[userExternalID] = append(directGroups[userExternalID], groupDN)
			}
			if _, isGroup := groupsByDN[memberDN]; isGroup {
				if err = addLDAPGroupGraphEdge(&graphEdges); err != nil {
					return result, err
				}
				parents[memberDN] = append(parents[memberDN], groupDN)
			}
		}
	}
	userIDs := make([]string, 0, len(userByDN))
	for _, externalID := range userByDN {
		userIDs = append(userIDs, externalID)
	}
	sort.Strings(userIDs)
	for _, externalID := range userIDs {
		membership := collectNestedLDAPGroups(directGroups[externalID], parents, configuration.GroupMaxDepth)
		groupIDs := make([]string, 0, len(membership))
		for groupDN := range membership {
			groupIDs = append(groupIDs, groupsByDN[groupDN].externalID)
		}
		sort.Strings(groupIDs)
		result.Users = append(result.Users, pro_interfaces.LDAPDirectoryUser{
			ExternalID: externalID, GroupExternalIDs: groupIDs,
		})
	}
	result.CapturedAt = time.Now().UTC()
	revisionInput, err := json.Marshal(struct {
		Groups []string
		Users  []pro_interfaces.LDAPDirectoryUser
	}{result.GroupExternalIDs, result.Users})
	if err != nil {
		return pro_interfaces.LDAPGroupDirectorySnapshot{}, fmt.Errorf("encode LDAP group snapshot: %w", err)
	}
	digest := sha256.Sum256(revisionInput)
	result.Revision = hex.EncodeToString(digest[:])
	return result, nil
}

func (c *ldapClient) searchLDAPPageSet(
	ctx context.Context,
	connection ldapConnection,
	request *ldap.SearchRequest,
) ([]*ldap.Entry, error) {
	entries := make([]*ldap.Entry, 0, ldapGroupPageSize)
	pagingControl := ldap.NewControlPaging(ldapGroupPageSize)
	request.Controls = append(request.Controls, pagingControl)
	request.SizeLimit = ldapGroupEntryLimit
	request.EnforceSizeLimit = true
	seenCookies := make(map[string]bool)
	abandonPaging := func() {
		if len(pagingControl.Cookie) == 0 {
			return
		}
		pagingControl.PagingSize = 0
		_, _ = connection.Search(request)
	}
	for pageCount := 0; pageCount < ldapGroupPageLimit; pageCount++ {
		if err := ctx.Err(); err != nil {
			abandonPaging()
			return nil, fmt.Errorf("LDAP group request cancelled: %w", err)
		}
		result, err := connection.Search(request)
		if ldap.IsErrorWithCode(err, ldap.LDAPResultReferral) {
			abandonPaging()
			return nil, pro_interfaces.ErrLDAPReferral
		}
		if err != nil {
			abandonPaging()
			return nil, fmt.Errorf("%w: paged search", pro_interfaces.ErrLDAPProviderUnavailable)
		}
		if len(result.Referrals) != 0 {
			abandonPaging()
			return nil, pro_interfaces.ErrLDAPReferral
		}
		if len(entries)+len(result.Entries) > ldapGroupEntryLimit {
			abandonPaging()
			return nil, ldapGroupLimitError("entries", ldapGroupEntryLimit)
		}
		entries = append(entries, result.Entries...)

		control := ldap.FindControl(result.Controls, ldap.ControlTypePaging)
		page, ok := control.(*ldap.ControlPaging)
		if !ok || len(page.Cookie) == 0 {
			return entries, nil
		}
		if len(result.Entries) == 0 || seenCookies[string(page.Cookie)] {
			abandonPaging()
			return nil, fmt.Errorf("%w: invalid LDAP paging progress", pro_interfaces.ErrLDAPProviderUnavailable)
		}
		seenCookies[string(page.Cookie)] = true
		pagingControl.SetCookie(page.Cookie)
	}
	abandonPaging()
	return nil, fmt.Errorf("%w: LDAP group search exceeds %d pages", pro_interfaces.ErrLDAPProviderUnavailable, ldapGroupPageLimit)
}

func addLDAPGroupGraphEdge(edgeCount *int) error {
	*edgeCount++
	if *edgeCount > ldapGroupGraphEdgeLimit {
		return ldapGroupLimitError("membership graph edges", ldapGroupGraphEdgeLimit)
	}
	return nil
}

func ldapGroupLimitError(resource string, limit int) error {
	return fmt.Errorf("%w: LDAP group %s exceed the configured limit of %d", pro_interfaces.ErrLDAPProviderUnavailable, resource, limit)
}

func collectNestedLDAPGroups(
	direct []string,
	parents map[string][]string,
	maxDepth int,
) map[string]bool {
	visited := make(map[string]bool)
	type queuedGroup struct {
		dn    string
		depth int
	}
	queue := make([]queuedGroup, 0, len(direct))
	for _, groupDN := range direct {
		queue = append(queue, queuedGroup{dn: groupDN})
	}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if visited[current.dn] {
			continue
		}
		visited[current.dn] = true
		if current.depth >= maxDepth {
			continue
		}
		for _, parentDN := range parents[current.dn] {
			queue = append(queue, queuedGroup{dn: parentDN, depth: current.depth + 1})
		}
	}
	return visited
}

func canonicalLDAPDN(value string) (string, error) {
	dn, err := ldap.ParseDN(strings.TrimSpace(value))
	if err != nil {
		return "", fmt.Errorf("invalid LDAP distinguished name")
	}
	return strings.ToLower(dn.String()), nil
}

func immutableLDAPExternalID(entry *ldap.Entry, attribute string) (string, error) {
	canonical, err := canonicalIdentityAttribute(attribute)
	if err != nil {
		return "", err
	}
	values := entry.GetEqualFoldRawAttributeValues(canonical)
	if len(values) != 1 {
		return "", fmt.Errorf("LDAP immutable identity must have exactly one value")
	}
	normalized, err := normalizeLDAPIdentity(canonical, values[0])
	if err != nil {
		return "", err
	}
	return strings.ToLower(canonical) + ":" + normalized, nil
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
	groupFields := []string{
		configuration.GroupSearchBaseDN, configuration.GroupUserFilter, configuration.GroupFilter,
		configuration.GroupIdentityAttribute, configuration.GroupMemberAttribute,
	}
	groupConfigured := false
	for _, value := range groupFields {
		groupConfigured = groupConfigured || strings.TrimSpace(value) != ""
	}
	if groupConfigured {
		if err = validateLDAPGroupConfiguration(configuration); err != nil {
			return "", nil, err
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

func validateLDAPGroupConfiguration(configuration pro_interfaces.LDAPClientConfiguration) error {
	if _, err := ldap.ParseDN(configuration.GroupSearchBaseDN); err != nil {
		return fmt.Errorf("invalid LDAP group search base DN")
	}
	for name, filter := range map[string]string{
		"user":  configuration.GroupUserFilter,
		"group": configuration.GroupFilter,
	} {
		if strings.Contains(filter, "{{") || strings.Contains(filter, "}}") {
			return fmt.Errorf("LDAP group %s filter cannot contain markers", name)
		}
		if _, err := ldap.CompileFilter(filter); err != nil {
			return fmt.Errorf("invalid LDAP group %s filter", name)
		}
	}
	if _, err := canonicalIdentityAttribute(configuration.GroupIdentityAttribute); err != nil {
		return err
	}
	if !ldapAttributePattern.MatchString(configuration.GroupMemberAttribute) {
		return fmt.Errorf("invalid LDAP group member attribute")
	}
	if configuration.GroupMaxDepth < 1 || configuration.GroupMaxDepth > ldapGroupMaxDepthLimit {
		return fmt.Errorf("LDAP group max depth must be between 1 and %d", ldapGroupMaxDepthLimit)
	}
	return nil
}

func dialLDAP(
	serverURL string,
	mode pro_interfaces.LDAPTLSMode,
	tlsConfig *tls.Config,
	timeout time.Duration,
) (ldapConnection, error) {
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return nil, err
	}
	port := parsed.Port()
	if port == "" {
		if mode == pro_interfaces.LDAPTLSModeLDAPS {
			port = "636"
		} else {
			port = "389"
		}
	}
	rawConnection, err := (&net.Dialer{Timeout: timeout}).Dial("tcp", net.JoinHostPort(parsed.Hostname(), port))
	if err != nil {
		return nil, err
	}
	limitedConnection := &limitedLDAPNetConn{Conn: rawConnection, remaining: ldapResponseByteLimit}
	if err = limitedConnection.SetDeadline(time.Now().Add(timeout)); err != nil {
		_ = limitedConnection.Close()
		return nil, err
	}
	var connection *ldap.Conn
	if mode == pro_interfaces.LDAPTLSModeLDAPS {
		tlsConnection := tls.Client(limitedConnection, tlsConfig)
		if err = tlsConnection.Handshake(); err != nil {
			_ = limitedConnection.Close()
			return nil, err
		}
		connection = ldap.NewConn(tlsConnection, true)
	} else {
		connection = ldap.NewConn(limitedConnection, false)
	}
	if err = limitedConnection.SetDeadline(time.Time{}); err != nil {
		_ = connection.Close()
		return nil, err
	}
	connection.Start()
	connection.SetTimeout(timeout)
	if mode == pro_interfaces.LDAPTLSModeStartTLS {
		if err = connection.StartTLS(tlsConfig); err != nil {
			_ = connection.Close()
			return nil, err
		}
	}
	return connection, nil
}

type limitedLDAPNetConn struct {
	net.Conn
	remaining int64
}

func (c *limitedLDAPNetConn) Read(buffer []byte) (int, error) {
	if c.remaining <= 0 {
		return 0, fmt.Errorf("LDAP response exceeds %d bytes", ldapResponseByteLimit)
	}
	if int64(len(buffer)) > c.remaining {
		buffer = buffer[:int(c.remaining)]
	}
	read, err := c.Conn.Read(buffer)
	c.remaining -= int64(read)
	return read, err
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
