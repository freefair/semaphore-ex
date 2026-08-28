package identity

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type legacyLDAPClient struct{}

func NewLegacyLDAPClient() pro_interfaces.LegacyLDAPClient {
	return &legacyLDAPClient{}
}

func (c *legacyLDAPClient) Authenticate(
	ctx context.Context,
	request pro_interfaces.LegacyLDAPClientRequest,
) (*pro_interfaces.LegacyLDAPClientResult, error) {
	configuration := request.Configuration
	scheme := "ldap"
	if configuration.TLS {
		scheme = "ldaps"
	}
	serverURL, err := url.Parse(scheme + "://" + configuration.Server)
	if err != nil || serverURL.Hostname() == "" {
		return nil, fmt.Errorf("invalid legacy LDAP server")
	}
	tlsConfiguration := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         serverURL.Hostname(),
		InsecureSkipVerify: configuration.TLSSkipVerify, //nolint:gosec // explicit legacy compatibility setting
	}
	connection, err := ldap.DialURL(serverURL.String(),
		ldap.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}),
		ldap.DialWithTLSConfig(tlsConfiguration),
	)
	if err != nil {
		return nil, err
	}
	defer connection.Close() //nolint:errcheck
	if deadline, ok := ctx.Deadline(); ok {
		connection.SetTimeout(time.Until(deadline))
	} else {
		connection.SetTimeout(10 * time.Second)
	}
	if err = connection.Bind(configuration.BindDN, configuration.BindPassword); err != nil {
		return nil, err
	}
	search := ldap.NewSearchRequest(
		configuration.SearchBaseDN,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		2,
		5,
		false,
		fmt.Sprintf(configuration.SearchFilter, ldap.EscapeFilter(request.Username)),
		configuration.Attributes,
		nil,
	)
	search.EnforceSizeLimit = true
	result, err := connection.Search(search)
	if err != nil {
		return nil, err
	}
	if len(result.Referrals) != 0 {
		return nil, pro_interfaces.ErrLDAPReferral
	}
	if len(result.Entries) == 0 {
		return nil, nil
	}
	if len(result.Entries) != 1 {
		return nil, pro_interfaces.ErrLDAPDuplicateIdentity
	}
	entry := result.Entries[0]
	if err = connection.Bind(entry.DN, request.Credential); err != nil {
		return nil, err
	}
	attributes := make(map[string]any, len(entry.Attributes))
	for _, attribute := range entry.Attributes {
		if len(attribute.Values) != 0 {
			attributes[attribute.Name] = attribute.Values[0]
		}
	}
	return &pro_interfaces.LegacyLDAPClientResult{ExternalID: entry.DN, Attributes: attributes}, nil
}

var _ pro_interfaces.LegacyLDAPClient = (*legacyLDAPClient)(nil)
