package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuditHECTransportUsesSeparateNamesForHTTPSProxyAndOrigin(t *testing.T) {
	originCA, originKey := newAuditTransportCA(t, "origin CA")
	proxyCA, proxyKey := newAuditTransportCA(t, "proxy CA")
	originProtocol := make(chan int, 1)
	origin := newAuditTransportTLSServer(t, newAuditTransportCertificate(t, originCA, originKey, "origin-override.test"), func(w http.ResponseWriter, request *http.Request) {
		originProtocol <- request.ProtoMajor
		w.WriteHeader(http.StatusNoContent)
	})
	defer origin.Close()
	proxy := newAuditTransportHTTPSProxy(t, newAuditTransportCertificate(t, proxyCA, proxyKey, "origin.test"), origin.Listener.Addr().String())
	defer proxy.Close()

	roots := x509.NewCertPool()
	roots.AddCert(originCA)
	roots.AddCert(proxyCA)
	endpoint := auditTransportURL(t, "origin.test", origin.Listener.Addr().String())
	transport := newAuditHECTransport(endpoint, roots, "origin-override.test")
	transport.Proxy = http.ProxyURL(auditTransportURL(t, "origin.test", proxy.Listener.Addr().String()))
	transport.DialContext = auditTransportDialer("origin.test", origin.Listener.Addr().String(), proxy.Listener.Addr().String())
	client := &http.Client{Transport: transport}
	client.Timeout = time.Second
	defer client.CloseIdleConnections()

	response, err := client.Get(endpoint.String())
	require.NoError(t, err)
	defer response.Body.Close()
	assert.Equal(t, http.StatusNoContent, response.StatusCode)
	assert.Equal(t, 2, <-originProtocol)
}

func TestAuditHECTransportHonorsConfiguredOriginNameWithoutProxy(t *testing.T) {
	originCA, originKey := newAuditTransportCA(t, "origin CA")
	origin := newAuditTransportTLSServer(t, newAuditTransportCertificate(t, originCA, originKey, "origin-override.test"), func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	defer origin.Close()
	roots := x509.NewCertPool()
	roots.AddCert(originCA)
	endpoint := auditTransportURL(t, "origin.test", origin.Listener.Addr().String())
	transport := newAuditHECTransport(endpoint, roots, "origin-override.test")
	transport.Proxy = nil
	transport.DialContext = auditTransportDialer("", origin.Listener.Addr().String(), "")
	client := &http.Client{Transport: transport, Timeout: time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Get(endpoint.String())
	require.NoError(t, err)
	defer response.Body.Close()
	assert.Equal(t, http.StatusNoContent, response.StatusCode)
}

func TestAuditHECTransportUsesEndpointAndProxyNamesByDefault(t *testing.T) {
	originCA, originKey := newAuditTransportCA(t, "origin CA")
	proxyCA, proxyKey := newAuditTransportCA(t, "proxy CA")
	origin := newAuditTransportTLSServer(t, newAuditTransportCertificate(t, originCA, originKey, "origin.test"), func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	defer origin.Close()
	proxy := newAuditTransportHTTPSProxy(t, newAuditTransportCertificate(t, proxyCA, proxyKey, "proxy.test"), origin.Listener.Addr().String())
	defer proxy.Close()
	roots := x509.NewCertPool()
	roots.AddCert(originCA)
	roots.AddCert(proxyCA)
	endpoint := auditTransportURL(t, "origin.test", origin.Listener.Addr().String())
	transport := newAuditHECTransport(endpoint, roots, "")
	transport.Proxy = http.ProxyURL(auditTransportURL(t, "proxy.test", proxy.Listener.Addr().String()))
	transport.DialContext = auditTransportDialer("proxy.test", origin.Listener.Addr().String(), proxy.Listener.Addr().String())
	client := &http.Client{Transport: transport, Timeout: time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Get(endpoint.String())
	require.NoError(t, err)
	defer response.Body.Close()
	assert.Equal(t, http.StatusNoContent, response.StatusCode)
}

func TestAuditHECTransportHonorsConfiguredOriginNameThroughHTTPProxy(t *testing.T) {
	originCA, originKey := newAuditTransportCA(t, "origin CA")
	origin := newAuditTransportTLSServer(t, newAuditTransportCertificate(t, originCA, originKey, "origin-override.test"), func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	defer origin.Close()
	proxy := newAuditTransportHTTPProxy(t, origin.Listener.Addr().String())
	defer proxy.Close()
	roots := x509.NewCertPool()
	roots.AddCert(originCA)
	endpoint := auditTransportURL(t, "origin.test", origin.Listener.Addr().String())
	transport := newAuditHECTransport(endpoint, roots, "origin-override.test")
	transport.Proxy = http.ProxyURL(&url.URL{Scheme: "http", Host: proxy.Listener.Addr().String()})
	client := &http.Client{Transport: transport, Timeout: time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Get(endpoint.String())
	require.NoError(t, err)
	defer response.Body.Close()
	assert.Equal(t, http.StatusNoContent, response.StatusCode)
}

func TestAuditHECTransportRejectsUntrustedOriginBehindHTTPSProxy(t *testing.T) {
	originCA, originKey := newAuditTransportCA(t, "origin CA")
	proxyCA, proxyKey := newAuditTransportCA(t, "proxy CA")
	origin := newAuditTransportTLSServer(t, newAuditTransportCertificate(t, originCA, originKey, "origin-override.test"), func(http.ResponseWriter, *http.Request) {})
	defer origin.Close()
	proxy := newAuditTransportHTTPSProxy(t, newAuditTransportCertificate(t, proxyCA, proxyKey, "proxy.test"), origin.Listener.Addr().String())
	defer proxy.Close()

	roots := x509.NewCertPool()
	roots.AddCert(proxyCA)
	endpoint := auditTransportURL(t, "origin.test", origin.Listener.Addr().String())
	transport := newAuditHECTransport(endpoint, roots, "origin-override.test")
	transport.Proxy = http.ProxyURL(auditTransportURL(t, "proxy.test", proxy.Listener.Addr().String()))
	transport.DialContext = auditTransportDialer("proxy.test", origin.Listener.Addr().String(), proxy.Listener.Addr().String())
	client := &http.Client{Transport: transport}
	client.Timeout = time.Second
	defer client.CloseIdleConnections()

	_, err := client.Get(endpoint.String())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "certificate signed by unknown authority")
}

func TestAuditHECTransportRejectsWrongHTTPSProxyName(t *testing.T) {
	originCA, originKey := newAuditTransportCA(t, "origin CA")
	proxyCA, proxyKey := newAuditTransportCA(t, "proxy CA")
	origin := newAuditTransportTLSServer(t, newAuditTransportCertificate(t, originCA, originKey, "origin-override.test"), func(http.ResponseWriter, *http.Request) {})
	defer origin.Close()
	proxy := newAuditTransportHTTPSProxy(t, newAuditTransportCertificate(t, proxyCA, proxyKey, "wrong-proxy.test"), origin.Listener.Addr().String())
	defer proxy.Close()

	roots := x509.NewCertPool()
	roots.AddCert(originCA)
	roots.AddCert(proxyCA)
	endpoint := auditTransportURL(t, "origin.test", origin.Listener.Addr().String())
	transport := newAuditHECTransport(endpoint, roots, "origin-override.test")
	transport.Proxy = http.ProxyURL(auditTransportURL(t, "proxy.test", proxy.Listener.Addr().String()))
	transport.DialContext = auditTransportDialer("proxy.test", origin.Listener.Addr().String(), proxy.Listener.Addr().String())
	client := &http.Client{Transport: transport}
	client.Timeout = time.Second
	defer client.CloseIdleConnections()

	_, err := client.Get(endpoint.String())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "certificate is valid for wrong-proxy.test")
}

func auditTransportURL(t *testing.T, host, address string) *url.URL {
	t.Helper()
	_, port, err := net.SplitHostPort(address)
	require.NoError(t, err)
	endpoint, err := url.Parse("https://" + net.JoinHostPort(host, port) + "/event")
	require.NoError(t, err)
	return endpoint
}

func auditTransportDialer(proxyHost, originAddress, proxyAddress string) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if host, _, err := net.SplitHostPort(address); err == nil && host == proxyHost {
			return (&net.Dialer{}).DialContext(ctx, network, proxyAddress)
		}
		return (&net.Dialer{}).DialContext(ctx, network, originAddress)
	}
}

func newAuditTransportTLSServer(t *testing.T, certificate tls.Certificate, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	require.NoError(t, server.Listener.Close())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server.Listener = listener
	server.EnableHTTP2 = true
	server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}}
	server.StartTLS()
	return server
}

func newAuditTransportHTTPSProxy(t *testing.T, certificate tls.Certificate, originAddress string) *httptest.Server {
	t.Helper()
	return newAuditTransportProxy(t, originAddress, func(handler http.Handler) *httptest.Server {
		server := httptest.NewUnstartedServer(handler)
		require.NoError(t, server.Listener.Close())
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		server.Listener = listener
		server.EnableHTTP2 = true
		server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}}
		server.StartTLS()
		return server
	})
}

func newAuditTransportHTTPProxy(t *testing.T, originAddress string) *httptest.Server {
	t.Helper()
	return newAuditTransportProxy(t, originAddress, httptest.NewServer)
}

func newAuditTransportProxy(t *testing.T, originAddress string, newServer func(http.Handler) *httptest.Server) *httptest.Server {
	t.Helper()
	return newServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodConnect {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		require.True(t, ok)
		connection, buffered, err := hijacker.Hijack()
		require.NoError(t, err)
		defer connection.Close()
		origin, err := net.Dial("tcp", originAddress)
		require.NoError(t, err)
		defer origin.Close()
		_, err = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		require.NoError(t, err)
		require.NoError(t, buffered.Flush())
		go func() { _, _ = io.Copy(origin, connection) }()
		_, _ = io.Copy(connection, origin)
	}))
}

func newAuditTransportCA(t *testing.T, commonName string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: commonName}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	certificate, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return certificate, key
}

func newAuditTransportCertificate(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, dnsName string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: dnsName}, DNSNames: []string{dnsName}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	require.NoError(t, err)
	certificate, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	tlsCertificate, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	require.NoError(t, err)
	tlsCertificate.Leaf = certificate
	return tlsCertificate
}
