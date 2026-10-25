package ha

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewHARedis_DisabledReturnsNil(t *testing.T) {
	original := util.Config
	t.Cleanup(func() { util.Config = original })
	util.Config = testHAConfig()
	util.Config.HA.Enabled = false

	assert.Nil(t, NewHARedis())
}

func TestNewHARedis_InvalidHAConfigurationFailsClosedWithoutSecretOrAddress(t *testing.T) {
	original := util.Config
	t.Cleanup(func() { util.Config = original })
	util.Config = testHAConfig()
	util.Config.HA.Enabled = true
	util.Config.HA.NodeID = "node-a"
	util.Config.HA.Engine = util.HAEngineRedisSentinel
	util.Config.HA.RedisSentinel = &util.HARedisSentinelConfig{
		MasterName: "mymaster",
		Pass:       "redis-secret",
	}

	backend := NewHARedis()
	require.NotNil(t, backend)
	err := backend.Ping(context.Background())
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "redis-secret")
	assert.NotContains(t, backend.Describe(), "redis-secret")
}

func TestNewHARedis_SentinelSharesAuthenticatedTLSFailoverClient(t *testing.T) {
	original := util.Config
	t.Cleanup(func() { util.Config = original })
	util.Config = testHAConfig()
	util.Config.HA.Enabled = true
	util.Config.HA.NodeID = "node-a"
	util.Config.HA.Engine = util.HAEngineRedisSentinel
	util.Config.HA.RedisSentinel = &util.HARedisSentinelConfig{
		MasterName:    "mymaster",
		Addrs:         []string{"sentinel-a:26379", "sentinel-b:26379"},
		SentinelUser:  "sentinel-user",
		SentinelPass:  "sentinel-secret",
		User:          "redis-user",
		Pass:          "redis-secret",
		DB:            7,
		TLS:           true,
		TLSSkipVerify: true,
	}

	backend := NewHARedis()
	require.NotNil(t, backend)
	client, err := sharedRedisClient(backend)
	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, "redis-user", client.Options().Username)
	assert.Equal(t, "redis-secret", client.Options().Password)
	assert.Equal(t, 7, client.Options().DB)
	require.NotNil(t, client.Options().TLSConfig)
	assert.Equal(t, uint16(tls.VersionTLS12), client.Options().TLSConfig.MinVersion)
	assert.True(t, client.Options().TLSConfig.InsecureSkipVerify)
	assert.True(t, client.Options().ContextTimeoutEnabled)
	assert.Equal(t, haRedisDialTimeout, client.Options().DialTimeout)
	assert.Equal(t, haRedisReadTimeout, client.Options().ReadTimeout)
	assert.Equal(t, haRedisWriteTimeout, client.Options().WriteTimeout)
	assert.Equal(t, "Redis Sentinel mymaster via sentinel-a:26379,sentinel-b:26379", backend.Describe())
	assert.NotContains(t, backend.Describe(), "secret")
	assert.NoError(t, backend.Close())
}

func TestNewHARedis_DirectConnectionPreservesSpecialCharacterCredentials(t *testing.T) {
	original := util.Config
	t.Cleanup(func() { util.Config = original })
	util.Config = testHAConfig()
	util.Config.HA.Enabled = true
	util.Config.HA.NodeID = "node-a"
	util.Config.HA.Redis = &util.HARedisConfig{
		Addr: "redis.internal:6379",
		User: "user name%:@/",
		Pass: "pass word%:@/",
		DB:   4,
		TLS:  true,
	}

	backend := NewHARedis()
	require.NotNil(t, backend)
	client, err := sharedRedisClient(backend)
	require.NoError(t, err)
	assert.Equal(t, "user name%:@/", client.Options().Username)
	assert.Equal(t, "pass word%:@/", client.Options().Password)
	assert.Equal(t, 4, client.Options().DB)
	assert.Equal(t, "Redis at redis.internal:6379", backend.Describe())
	require.NotNil(t, client.Options().TLSConfig)
	assert.Equal(t, uint16(tls.VersionTLS12), client.Options().TLSConfig.MinVersion)
	assert.False(t, client.Options().TLSConfig.InsecureSkipVerify)
	assert.True(t, client.Options().ContextTimeoutEnabled)
	assert.Equal(t, haRedisDialTimeout, client.Options().DialTimeout)
	assert.Equal(t, haRedisReadTimeout, client.Options().ReadTimeout)
	assert.Equal(t, haRedisWriteTimeout, client.Options().WriteTimeout)
	assert.NoError(t, backend.Close())
}

func TestNewHARedis_DirectHostWithoutPortKeepsLegacyDefault(t *testing.T) {
	original := util.Config
	t.Cleanup(func() { util.Config = original })
	util.Config = testHAConfig()
	util.Config.HA.Enabled = true
	util.Config.HA.NodeID = "node-a"
	util.Config.HA.Redis = &util.HARedisConfig{Addr: "redis.internal"}

	backend := NewHARedis()
	require.NotNil(t, backend)
	client, err := sharedRedisClient(backend)
	require.NoError(t, err)
	assert.Equal(t, "redis.internal:6379", client.Options().Addr)
	assert.NoError(t, backend.Close())
}

func TestDirectRedisAddressPreservesLegacySafeDefaultPortForms(t *testing.T) {
	for input, expected := range map[string]string{
		"redis.internal":  "redis.internal:6379",
		"redis.internal:": "redis.internal:6379",
		"127.0.0.1":       "127.0.0.1:6379",
		"127.0.0.1:":      "127.0.0.1:6379",
		"[::1]":           "[::1]:6379",
		"[::1]:":          "[::1]:6379",
		"[::1]:6380":      "[::1]:6380",
	} {
		t.Run(input, func(t *testing.T) {
			actual, err := directRedisAddress(input)
			require.NoError(t, err)
			assert.Equal(t, expected, actual)
		})
	}
	for _, input := range []string{"::1", "[::1", "[::1]::", "[name]:", "host::", "host:0", "host:65536", "user@host:"} {
		t.Run("reject "+input, func(t *testing.T) {
			_, err := directRedisAddress(input)
			assert.Error(t, err)
		})
	}
}

func TestNewHARedis_DirectTLSVerifiesTrustedServer(t *testing.T) {
	certificate, roots := testTLSCertificate(t)
	server, err := miniredis.RunTLS(&tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
	require.NoError(t, err)
	t.Cleanup(server.Close)

	trusted := newTLSHARedis(t, server.Addr())
	trustedClient, err := sharedRedisClient(trusted)
	require.NoError(t, err)
	trustedClient.Options().TLSConfig.RootCAs = roots
	trustedClient.Options().TLSConfig.ServerName = "127.0.0.1"
	require.NoError(t, trusted.Ping(context.Background()))
	require.NoError(t, trusted.Close())

	untrusted := newTLSHARedis(t, server.Addr())
	untrustedClient, err := sharedRedisClient(untrusted)
	require.NoError(t, err)
	untrustedClient.Options().TLSConfig.RootCAs = x509.NewCertPool()
	untrustedClient.Options().TLSConfig.ServerName = "127.0.0.1"
	require.Error(t, untrusted.Ping(context.Background()))
	require.NoError(t, untrusted.Close())
}

func TestSharedRedisClientRejectsForeignImplementation(t *testing.T) {
	_, err := sharedRedisClient(foreignHARedis{})
	require.Error(t, err)
	assert.False(t, strings.Contains(err.Error(), "foreign"))
}

func TestRedisAddressRejectsAmbiguousOrUnsafeInput(t *testing.T) {
	for _, value := range []string{
		"redis.internal:0",
		"redis.internal:65536",
		"redis.internal:6379/path",
		"redis.internal:6379?query",
		"user@redis.internal:6379",
		" redis.internal:6379",
		"redis.internal:6379\n",
	} {
		t.Run(value, func(t *testing.T) {
			_, err := redisAddress(value)
			assert.Error(t, err)
		})
	}
}

func newTLSHARedis(t *testing.T, addr string) pro_interfaces.HARedis {
	t.Helper()
	original := util.Config
	t.Cleanup(func() { util.Config = original })
	util.Config = testHAConfig()
	util.Config.HA.Enabled = true
	util.Config.HA.NodeID = "tls-node"
	util.Config.HA.Redis = &util.HARedisConfig{Addr: addr, TLS: true}
	backend := NewHARedis()
	require.NotNil(t, backend)
	return backend
}

func testTLSCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:         true,
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	require.NoError(t, err)
	certificate, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: mustMarshalECKey(t, privateKey)}))
	require.NoError(t, err)
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})))
	return certificate, roots
}

func mustMarshalECKey(t *testing.T, privateKey *ecdsa.PrivateKey) []byte {
	t.Helper()
	encoded, err := x509.MarshalECPrivateKey(privateKey)
	require.NoError(t, err)
	return encoded
}

type foreignHARedis struct{}

func (foreignHARedis) Ping(context.Context) error { return nil }
func (foreignHARedis) Close() error               { return nil }
func (foreignHARedis) Describe() string           { return "foreign" }

func testHAConfig() *util.ConfigType {
	config := util.NewConfigType()
	config.HA = &util.HAConfig{}
	return config
}
