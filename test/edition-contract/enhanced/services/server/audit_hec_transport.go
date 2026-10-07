package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/url"
)

// newAuditHECTransport keeps the HEC origin TLS name separate from an HTTPS proxy name.
func newAuditHECTransport(endpoint *url.URL, roots *x509.CertPool, serverName string) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment
	originTLSConfig := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	if serverName != "" {
		originTLSConfig.ServerName = serverName
	}
	transport.TLSClientConfig = originTLSConfig
	transport.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		configuredTLS := originTLSConfig.Clone()
		if configuredTLS.ServerName == "" {
			configuredTLS.ServerName = endpoint.Hostname()
		}
		isHTTPSProxy := false
		if transport.Proxy != nil {
			proxyURL, err := transport.Proxy(&http.Request{URL: endpoint})
			if err != nil {
				return nil, err
			}
			if proxyURL != nil && proxyURL.Scheme == "https" {
				configuredTLS.ServerName = proxyURL.Hostname()
				isHTTPSProxy = true
			}
		}
		if isHTTPSProxy {
			configuredTLS.NextProtos = []string{"http/1.1"}
		}

		dialContext := transport.DialContext
		if dialContext == nil {
			dialContext = (&net.Dialer{}).DialContext
		}
		connection, err := dialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		tlsConnection := tls.Client(connection, configuredTLS)
		if err := tlsConnection.HandshakeContext(ctx); err != nil {
			_ = connection.Close()
			return nil, err
		}
		return tlsConnection, nil
	}
	return transport
}
