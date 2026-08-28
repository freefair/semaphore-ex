//go:build ldap_integration

package identity

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/pro_interfaces"
)

func TestLDAPTLSIntegration(t *testing.T) {
	configuration := ldapIntegrationConfiguration(t)
	client := NewLDAPClient()
	userPassword := requiredLDAPIntegrationEnv(t, "SEMAPHORE_LDAP_USER_PASSWORD")

	t.Run("search and bind", func(t *testing.T) {
		result, err := client.Authenticate(context.Background(), pro_interfaces.NewLDAPClientRequest(
			configuration, "alice", userPassword,
		))
		if err != nil {
			t.Fatalf("authenticate TLS LDAP user: %v", err)
		}
		if result.Identity.Username != "alice" || result.Identity.Email != "alice@example.test" ||
			result.Identity.ExternalID == "" || !result.Readiness.Connection ||
			!result.Readiness.Search || !result.Readiness.Bind {
			t.Fatalf("unexpected TLS LDAP result: %#v", result)
		}
	})

	t.Run("invalid credentials", func(t *testing.T) {
		_, err := client.Authenticate(context.Background(), pro_interfaces.NewLDAPClientRequest(
			configuration, "alice", "wrong-password",
		))
		if !errors.Is(err, pro_interfaces.ErrLDAPInvalidCredentials) {
			t.Fatalf("invalid user bind = %v, want invalid credentials", err)
		}
	})

	t.Run("escaped filter", func(t *testing.T) {
		_, err := client.Authenticate(context.Background(), pro_interfaces.NewLDAPClientRequest(
			configuration, "*)(uid=*)", userPassword,
		))
		if !errors.Is(err, pro_interfaces.ErrLDAPInvalidCredentials) {
			t.Fatalf("filter injection attempt = %v, want no matching identity", err)
		}
	})

	t.Run("duplicate identity", func(t *testing.T) {
		_, err := client.Authenticate(context.Background(), pro_interfaces.NewLDAPClientRequest(
			configuration, "duplicate", userPassword,
		))
		if !errors.Is(err, pro_interfaces.ErrLDAPDuplicateIdentity) {
			t.Fatalf("duplicate LDAP identity = %v, want duplicate identity", err)
		}
	})

	t.Run("referral", func(t *testing.T) {
		referralConfiguration := configuration
		referralConfiguration.SearchBaseDN = "ou=external,dc=example,dc=test"
		_, err := client.Authenticate(context.Background(), pro_interfaces.NewLDAPClientRequest(
			referralConfiguration, "outside", userPassword,
		))
		if !errors.Is(err, pro_interfaces.ErrLDAPReferral) {
			t.Fatalf("LDAP referral = %v, want rejected referral", err)
		}
	})

	t.Run("outage", func(t *testing.T) {
		outageConfiguration := configuration
		outageConfiguration.ServerURL = closedLDAPIntegrationURL(t)
		_, err := client.Authenticate(context.Background(), pro_interfaces.NewLDAPClientRequest(
			outageConfiguration, "alice", userPassword,
		))
		if !errors.Is(err, pro_interfaces.ErrLDAPProviderUnavailable) {
			t.Fatalf("LDAP outage = %v, want provider unavailable", err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen for LDAP timeout fixture: %v", err)
		}
		defer listener.Close()
		accepted := make(chan net.Conn, 1)
		go func() {
			connection, acceptErr := listener.Accept()
			if acceptErr == nil {
				accepted <- connection
			}
		}()

		timeoutConfiguration := configuration
		timeoutConfiguration.ServerURL = "ldaps://" + listener.Addr().String()
		timeoutClient := &ldapClient{dial: dialLDAP, timeout: 200 * time.Millisecond}
		started := time.Now()
		_, err = timeoutClient.Authenticate(context.Background(), pro_interfaces.NewLDAPClientRequest(
			timeoutConfiguration, "alice", userPassword,
		))
		if !errors.Is(err, pro_interfaces.ErrLDAPProviderUnavailable) {
			t.Fatalf("LDAP timeout = %v, want provider unavailable", err)
		}
		if elapsed := time.Since(started); elapsed > 2*time.Second {
			t.Fatalf("LDAP timeout took %s, want bounded failure", elapsed)
		}
		select {
		case connection := <-accepted:
			_ = connection.Close()
		default:
		}
	})
}

func ldapIntegrationConfiguration(t *testing.T) pro_interfaces.LDAPClientConfiguration {
	t.Helper()
	caPEM, err := os.ReadFile(requiredLDAPIntegrationEnv(t, "SEMAPHORE_LDAP_CA_FILE"))
	if err != nil {
		t.Fatalf("read LDAP integration CA: %v", err)
	}
	return pro_interfaces.LDAPClientConfiguration{
		ServerURL:         requiredLDAPIntegrationEnv(t, "SEMAPHORE_LDAP_URL"),
		TLSMode:           pro_interfaces.LDAPTLSModeLDAPS,
		TrustMode:         pro_interfaces.LDAPTrustModeCustom,
		CAPEM:             string(caPEM),
		BindDN:            "cn=admin,dc=example,dc=test",
		BindPassword:      requiredLDAPIntegrationEnv(t, "SEMAPHORE_LDAP_ADMIN_PASSWORD"),
		SearchBaseDN:      "ou=people,dc=example,dc=test",
		UserFilter:        "(&(objectClass=inetOrgPerson)(uid={{username}}))",
		IdentityAttribute: "entryUUID",
		UsernameAttribute: "uid",
		NameAttribute:     "cn",
		EmailAttribute:    "mail",
	}
}

func requiredLDAPIntegrationEnv(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		t.Fatalf("%s is required for LDAP integration tests", name)
	}
	return value
}

func closedLDAPIntegrationURL(t *testing.T) string {
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
