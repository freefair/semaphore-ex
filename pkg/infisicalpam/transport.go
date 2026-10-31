package infisicalpam

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	alpnProxy        = "infisical-pam-proxy"
	alpnCancellation = "infisical-pam-session-cancellation"

	defaultRelayPort       = 8443
	relayGatewayServerName = "localhost"
	gatewayIdentityPrefix  = "urn:infisical:gateway:"
	directDialTimeout      = 3 * time.Second
	handshakeTimeout       = 10 * time.Second
)

var gatewayIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,128}$`)

// Session is one live PAM session. It carries mTLS key material that grants
// access to the target for the session lifetime, so it stays in memory and is
// never logged or serialized.
type Session struct {
	ID            string
	GatewayID     string
	DirectAddress string
	RelayHost     string
	Expiry        time.Time

	relayCertificate   *tls.Certificate
	relayRoots         *x509.CertPool
	gatewayCertificate tls.Certificate
	gatewayRoots       *x509.CertPool
}

func newSession(response accessResponseBody, expiry time.Time) (*Session, error) {
	if !sessionIDPattern.MatchString(response.SessionID) {
		return nil, errors.New("session id is invalid")
	}
	if response.DirectAddress == "" && response.RelayHost == "" {
		return nil, errors.New("session has no gateway route")
	}
	if !gatewayIDPattern.MatchString(response.GatewayID) {
		return nil, errors.New("session has no valid gateway id")
	}
	if response.DirectAddress != "" {
		if _, _, err := splitHostPort(response.DirectAddress, 0); err != nil {
			return nil, errors.New("direct address is invalid")
		}
	}
	session := &Session{
		ID:            response.SessionID,
		GatewayID:     response.GatewayID,
		DirectAddress: response.DirectAddress,
		RelayHost:     response.RelayHost,
		Expiry:        expiry,
	}
	if response.RelayHost != "" {
		if _, _, err := splitHostPort(response.RelayHost, defaultRelayPort); err != nil {
			return nil, errors.New("relay host is invalid")
		}
		certificate, err := tls.X509KeyPair([]byte(response.RelayClientCertificate), []byte(response.RelayClientPrivateKey))
		if err != nil {
			return nil, errors.New("relay client certificate is invalid")
		}
		session.relayCertificate = &certificate
		session.relayRoots = x509.NewCertPool()
		if !session.relayRoots.AppendCertsFromPEM([]byte(response.RelayServerCertificateChain)) {
			return nil, errors.New("relay server chain is invalid")
		}
	}
	certificate, err := tls.X509KeyPair([]byte(response.GatewayClientCertificate), []byte(response.GatewayClientPrivateKey))
	if err != nil {
		return nil, errors.New("gateway client certificate is invalid")
	}
	session.gatewayCertificate = certificate
	session.gatewayRoots = x509.NewCertPool()
	if !session.gatewayRoots.AppendCertsFromPEM([]byte(response.GatewayServerCertificateChain)) {
		return nil, errors.New("gateway server chain is invalid")
	}
	return session, nil
}

// Expired reports whether the session can no longer be used.
func (s *Session) Expired(now time.Time) bool {
	return !s.Expiry.IsZero() && !now.Before(s.Expiry)
}

// Dial opens one SSH stream to the session's target. The returned connection
// carries a plain SSH protocol; the gateway authenticates to the target.
func (s *Session) Dial(ctx context.Context) (net.Conn, error) {
	return s.dialGateway(ctx, alpnProxy)
}

// Cancel tells the gateway to end the session. Callers fall back to
// Client.EndSession when it fails.
func (s *Session) Cancel(ctx context.Context) error {
	conn, err := s.dialGateway(ctx, alpnCancellation)
	if err != nil {
		return err
	}
	// The completed handshake is the cancellation. The gateway may close the
	// connection first, so a failed close_notify does not undo it.
	_ = conn.Close()
	return nil
}

func (s *Session) dialGateway(ctx context.Context, alpn string) (net.Conn, error) {
	if s.Expired(time.Now()) {
		return nil, newError(ErrorValidation, "dial", errors.New("session expired"))
	}
	var directErr error
	if s.DirectAddress != "" {
		conn, err := s.dialDirect(ctx, alpn)
		if err == nil {
			return conn, nil
		}
		directErr = err
		if s.RelayHost == "" {
			return nil, err
		}
	}
	conn, err := s.dialRelay(ctx, alpn)
	if err != nil && directErr != nil {
		return nil, newError(ErrorTransport, "dial", errors.Join(directErr, err))
	}
	return conn, err
}

func (s *Session) dialDirect(ctx context.Context, alpn string) (net.Conn, error) {
	host, _, err := splitHostPort(s.DirectAddress, 0)
	if err != nil {
		return nil, newError(ErrorValidation, "dial", err)
	}
	dialer := net.Dialer{Timeout: directDialTimeout}
	raw, err := dialer.DialContext(ctx, "tcp", s.DirectAddress)
	if err != nil {
		return nil, newError(ErrorTransport, "dial", err)
	}
	conn, err := s.handshakeGateway(ctx, raw, alpn, host)
	if err != nil {
		_ = raw.Close()
		return nil, err
	}
	return conn, nil
}

func (s *Session) dialRelay(ctx context.Context, alpn string) (net.Conn, error) {
	host, port, err := splitHostPort(s.RelayHost, defaultRelayPort)
	if err != nil {
		return nil, newError(ErrorValidation, "dial", err)
	}
	dialer := net.Dialer{Timeout: handshakeTimeout}
	raw, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, newError(ErrorTransport, "dial", err)
	}
	relay := tls.Client(raw, &tls.Config{
		Certificates: []tls.Certificate{*s.relayCertificate},
		RootCAs:      s.relayRoots,
		ServerName:   host,
		MinVersion:   tls.VersionTLS12,
	})
	if err := handshake(ctx, relay); err != nil {
		_ = raw.Close()
		return nil, newError(ErrorTransport, "dial", fmt.Errorf("relay handshake: %w", err))
	}
	// The relay forwards the inner stream to the gateway, whose certificate
	// carries localhost for this path.
	conn, err := s.handshakeGateway(ctx, relay, alpn, relayGatewayServerName)
	if err != nil {
		_ = relay.Close()
		return nil, err
	}
	return conn, nil
}

func (s *Session) handshakeGateway(ctx context.Context, transport net.Conn, alpn, serverName string) (net.Conn, error) {
	gateway := tls.Client(transport, &tls.Config{
		Certificates:     []tls.Certificate{s.gatewayCertificate},
		RootCAs:          s.gatewayRoots,
		ServerName:       serverName,
		MinVersion:       tls.VersionTLS12,
		MaxVersion:       tls.VersionTLS13,
		NextProtos:       []string{alpn},
		VerifyConnection: verifyGatewayIdentity(s.GatewayID),
	})
	if err := handshake(ctx, gateway); err != nil {
		return nil, newError(ErrorTransport, "dial", fmt.Errorf("gateway handshake: %w", err))
	}
	if gateway.ConnectionState().NegotiatedProtocol != alpn {
		_ = gateway.Close()
		return nil, newError(ErrorTransport, "dial", errors.New("gateway did not accept the PAM protocol"))
	}
	return gateway, nil
}

func handshake(ctx context.Context, conn *tls.Conn) error {
	handshakeCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	return conn.HandshakeContext(handshakeCtx)
}

// verifyGatewayIdentity requires the gateway certificate to name the gateway
// the session was issued for. The Infisical CLI accepts certificates without
// an identity for older gateways; Semaphore does not, so a trust chain that
// covers several gateways cannot route a session to the wrong one.
func verifyGatewayIdentity(gatewayID string) func(tls.ConnectionState) error {
	expected := gatewayIdentityPrefix + gatewayID
	return func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) == 0 {
			return errors.New("gateway presented no certificate")
		}
		for _, uri := range state.PeerCertificates[0].URIs {
			if uri.String() == expected {
				return nil
			}
		}
		return errors.New("gateway certificate does not name the session's gateway")
	}
}

func splitHostPort(address string, defaultPort int) (string, int, error) {
	if address == "" || len(address) > 255 || strings.ContainsAny(address, "/\\@ \t\r\n") {
		return "", 0, errors.New("address is invalid")
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		if defaultPort == 0 || strings.Count(address, ":") > 1 {
			return "", 0, errors.New("address is invalid")
		}
		host, portText = address, strconv.Itoa(defaultPort)
	}
	port, err := strconv.Atoi(portText)
	if host == "" || err != nil || port < 1 || port > 65535 {
		return "", 0, errors.New("address is invalid")
	}
	return host, port, nil
}
