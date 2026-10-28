// Package pamtest provides an in-process Infisical PAM stand-in for tests: an
// API, a relay, a gateway, and an SSH target behind the gateway. It mirrors
// the protocol of the Infisical CLI and gateway closely enough to exercise the
// client end to end without a licensed Infisical instance.
package pamtest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

const (
	ClientID     = "test-client-id"
	ClientSecret = "test-client-secret"
	AccessToken  = "test-access-token"
	GatewayID    = "gateway-1"
)

// Options select the routing a fake session announces.
type Options struct {
	// Direct announces the gateway's own listener as direct address.
	Direct bool
	// NoRelay omits the relay from sessions; only valid with Direct.
	NoRelay bool
	// GatewayIdentity overrides the identity URI in the gateway certificate.
	GatewayIdentity string
	// OmitGatewayIdentity issues a gateway certificate without identity URI.
	OmitGatewayIdentity bool
	// OmitGatewayID leaves the gateway id out of session responses.
	OmitGatewayID bool
	// AccountType overrides the account type of session responses.
	AccountType string
	// AccessError, when set, is returned by the access endpoint as an
	// Infisical error name with HTTP 403.
	AccessError string
}

// Environment is a running fake PAM installation.
type Environment struct {
	API        *httptest.Server
	APICACert  []byte
	Options    Options
	t          testing.TB
	ca         *x509.Certificate
	caKey      *ecdsa.PrivateKey
	relay      net.Listener
	gateway    net.Listener
	gatewayTLS *tls.Config
	relayTLS   *tls.Config
	hostKey    ssh.Signer

	mu        sync.Mutex
	hang      bool
	held      []net.Conn
	accesses  []AccessRecord
	ended     []string
	cancelled []string
	commands  []string
	sessions  int
}

// AccessRecord is one observed access request.
type AccessRecord struct {
	Path     string
	Duration string
	Reason   string
	Token    string
}

// Start launches the environment and registers cleanup.
func Start(t testing.TB, options Options) *Environment {
	t.Helper()
	env := &Environment{t: t, Options: options}
	env.ca, env.caKey = newCA(t)
	env.relayTLS = env.serverTLS(t, []string{"127.0.0.1"}, "")
	identity := options.GatewayIdentity
	if identity == "" && !options.OmitGatewayIdentity {
		identity = "urn:infisical:gateway:" + GatewayID
	}
	env.gatewayTLS = env.serverTLS(t, []string{"localhost", "127.0.0.1"}, identity)
	env.gatewayTLS.NextProtos = []string{"infisical-pam-proxy", "infisical-pam-session-cancellation"}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	env.hostKey, err = ssh.NewSignerFromKey(key)
	require.NoError(t, err)
	env.relay = listen(t)
	env.gateway = listen(t)
	go env.serveRelay()
	go env.serveGateway()
	env.API = httptest.NewTLSServer(http.HandlerFunc(env.serveAPI))
	env.APICACert = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: env.API.Certificate().Raw})
	t.Cleanup(func() {
		env.mu.Lock()
		for _, conn := range env.held {
			_ = conn.Close()
		}
		env.mu.Unlock()
		env.API.Close()
		_ = env.relay.Close()
		_ = env.gateway.Close()
	})
	return env
}

// SetHang makes the relay and gateway accept connections without ever
// completing a handshake, as an unresponsive gateway would.
func (e *Environment) SetHang(hang bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.hang = hang
}

// hold keeps a connection open without answering while hanging; the
// reference stops a finalizer from closing it.
func (e *Environment) hold(conn net.Conn) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.hang {
		e.held = append(e.held, conn)
	}
	return e.hang
}

func (e *Environment) accountType() string {
	if e.Options.AccountType != "" {
		return e.Options.AccountType
	}
	return "ssh"
}

// URL is the site URL to configure the client with.
func (e *Environment) URL() string { return e.API.URL }

func (e *Environment) Accesses() []AccessRecord {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]AccessRecord(nil), e.accesses...)
}

func (e *Environment) Ended() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.ended...)
}

func (e *Environment) Cancelled() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.cancelled...)
}

// Commands lists exec requests the SSH target received.
func (e *Environment) Commands() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.commands...)
}

func (e *Environment) serveAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	switch {
	case r.URL.Path == "/api/v1/auth/universal-auth/login":
		var body struct {
			ClientID     string `json:"clientId"`
			ClientSecret string `json:"clientSecret"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.ClientID != ClientID || body.ClientSecret != ClientSecret {
			writeError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		writeJSON(w, map[string]any{"accessToken": AccessToken, "expiresIn": 7200, "tokenType": "Bearer"})
	case r.URL.Path == "/api/v1/pam/accounts/access":
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token != AccessToken {
			writeError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		var body struct {
			Path     string `json:"path"`
			Duration string `json:"duration"`
			Reason   string `json:"reason"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		e.mu.Lock()
		e.accesses = append(e.accesses, AccessRecord{Path: body.Path, Duration: body.Duration, Reason: body.Reason, Token: token})
		e.sessions++
		sessionID := fmt.Sprintf("session-%d", e.sessions)
		e.mu.Unlock()
		if e.Options.AccessError != "" {
			writeError(w, http.StatusForbidden, e.Options.AccessError)
			return
		}
		if strings.HasPrefix(body.Path, "missing/") {
			writeError(w, http.StatusNotFound, "NotFound")
			return
		}
		writeJSON(w, e.sessionResponse(sessionID))
	case strings.HasPrefix(r.URL.Path, "/api/v1/pam/sessions/") && strings.HasSuffix(r.URL.Path, "/end"):
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/pam/sessions/"), "/end")
		e.mu.Lock()
		e.ended = append(e.ended, id)
		e.mu.Unlock()
		writeJSON(w, map[string]any{})
	default:
		http.NotFound(w, r)
	}
}

func (e *Environment) sessionResponse(sessionID string) map[string]any {
	clientCert, clientKey := e.clientCertificate(sessionID)
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: e.ca.Raw}))
	response := map[string]any{
		"sessionId":                     sessionID,
		"accountType":                   e.accountType(),
		"resourceType":                  "ssh",
		"gatewayId":                     GatewayID,
		"gatewayClientCertificate":      clientCert,
		"gatewayClientPrivateKey":       clientKey,
		"gatewayServerCertificateChain": caPEM,
	}
	if e.Options.OmitGatewayID {
		delete(response, "gatewayId")
	}
	if !e.Options.NoRelay {
		response["relayHost"] = e.relay.Addr().String()
		response["relayClientCertificate"] = clientCert
		response["relayClientPrivateKey"] = clientKey
		response["relayServerCertificateChain"] = caPEM
	}
	if e.Options.Direct {
		response["directAddress"] = e.gateway.Addr().String()
	}
	return response
}

// serveRelay terminates the relay TLS layer and treats the inner stream as a
// gateway connection, as the real relay forwards it.
func (e *Environment) serveRelay() {
	for {
		conn, err := e.relay.Accept()
		if err != nil {
			return
		}
		if e.hold(conn) {
			continue
		}
		go func() {
			relay := tls.Server(conn, e.relayTLS)
			if err := relay.Handshake(); err != nil {
				_ = conn.Close()
				return
			}
			e.handleGateway(relay)
		}()
	}
}

func (e *Environment) serveGateway() {
	for {
		conn, err := e.gateway.Accept()
		if err != nil {
			return
		}
		if e.hold(conn) {
			continue
		}
		go e.handleGateway(conn)
	}
}

func (e *Environment) handleGateway(transport net.Conn) {
	defer transport.Close()
	gateway := tls.Server(transport, e.gatewayTLS)
	if err := gateway.Handshake(); err != nil {
		return
	}
	state := gateway.ConnectionState()
	sessionID := ""
	if len(state.PeerCertificates) > 0 {
		sessionID = state.PeerCertificates[0].Subject.CommonName
	}
	switch state.NegotiatedProtocol {
	case "infisical-pam-session-cancellation":
		e.mu.Lock()
		e.cancelled = append(e.cancelled, sessionID)
		e.mu.Unlock()
	case "infisical-pam-proxy":
		e.serveSSH(gateway)
	}
}

// serveSSH plays the gateway's SSH side: it accepts any client and answers
// exec requests with "ok <command>".
func (e *Environment) serveSSH(conn net.Conn) {
	config := &ssh.ServerConfig{NoClientAuth: true}
	config.AddHostKey(e.hostKey)
	server, channels, requests, err := ssh.NewServerConn(conn, config)
	if err != nil {
		return
	}
	defer server.Close()
	go ssh.DiscardRequests(requests)
	for newChannel := range channels {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "unsupported")
			continue
		}
		channel, channelRequests, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go e.serveSession(channel, channelRequests)
	}
}

func (e *Environment) serveSession(channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()
	for request := range requests {
		if request.Type != "exec" {
			_ = request.Reply(false, nil)
			continue
		}
		var payload struct{ Command string }
		if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
			_ = request.Reply(false, nil)
			return
		}
		_ = request.Reply(true, nil)
		e.mu.Lock()
		e.commands = append(e.commands, payload.Command)
		e.mu.Unlock()
		_, _ = io.WriteString(channel, "ok "+payload.Command+"\n")
		_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
		return
	}
}

func (e *Environment) serverTLS(t testing.TB, names []string, identity string) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: serial(t),
		Subject:      pkix.Name{CommonName: names[0]},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, name := range names {
		if ip := net.ParseIP(name); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, name)
		}
	}
	if identity != "" {
		parsed, parseErr := url.Parse(identity)
		require.NoError(t, parseErr)
		template.URIs = []*url.URL{parsed}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, e.ca, &key.PublicKey, e.caKey)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	pool.AddCert(e.ca)
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
		MinVersion:   tls.VersionTLS12,
	}
}

func (e *Environment) clientCertificate(sessionID string) (string, string) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(e.t, err)
	template := &x509.Certificate{
		SerialNumber: serial(e.t),
		Subject:      pkix.Name{CommonName: sessionID},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, e.ca, &key.PublicKey, e.caKey)
	require.NoError(e.t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(e.t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
}

func newCA(t testing.TB) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber:          serial(t),
		Subject:               pkix.Name{CommonName: "pamtest CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	ca, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return ca, key
}

func serial(t testing.TB) *big.Int {
	value, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	require.NoError(t, err)
	return value
}

func listen(t testing.TB) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	return listener
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, name string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": name, "message": "test", "statusCode": status})
}
