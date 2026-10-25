package ha

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
)

const (
	haRedisDialTimeout  = time.Second
	haRedisReadTimeout  = time.Second
	haRedisWriteTimeout = time.Second
)

// managedHARedis owns the one Redis connection pool used by all HA adapters.
// Adapters may close their own Pub/Sub subscriptions, but never this client.
type managedHARedis struct {
	client      *redis.Client
	description string
}

var _ pro_interfaces.HARedis = (*managedHARedis)(nil)

func (r *managedHARedis) Ping(ctx context.Context) error {
	if r == nil || r.client == nil {
		return errors.New("HA Redis backend is unavailable")
	}
	if err := r.client.Ping(ctx).Err(); err != nil {
		return errors.New("HA Redis backend is unavailable")
	}
	return nil
}

func (r *managedHARedis) Close() error {
	if r == nil || r.client == nil {
		return nil
	}
	if err := r.client.Close(); err != nil {
		return errors.New("HA Redis backend close failed")
	}
	return nil
}

func (r *managedHARedis) Describe() string {
	if r == nil {
		return "HA Redis unavailable"
	}
	return r.description
}

func (r *managedHARedis) redisClient() *redis.Client { return r.client }

// failedHARedis makes configuration failures observable at startup without
// returning a nil backend from enabled HA mode.
type failedHARedis struct{}

var _ pro_interfaces.HARedis = failedHARedis{}

func (failedHARedis) Ping(context.Context) error {
	return errors.New("HA Redis configuration is invalid")
}
func (failedHARedis) Close() error     { return nil }
func (failedHARedis) Describe() string { return "HA Redis unavailable" }

// NewHARedis creates the sole Redis client for an HA node. HA consumers borrow
// this client through sharedRedisClient; server startup alone closes it.
func NewHARedis() pro_interfaces.HARedis {
	if util.Config == nil || !util.HAEnabled() {
		return nil
	}

	client, description, err := newHARedisClient(util.Config.HA)
	if err != nil {
		return failedHARedis{}
	}
	return &managedHARedis{client: client, description: description}
}

// sharedRedisClient returns the server-owned client to HA adapters. It keeps
// the ownership boundary explicit so an adapter cannot close the pool.
func sharedRedisClient(backend pro_interfaces.HARedis) (*redis.Client, error) {
	owner, ok := backend.(interface{ redisClient() *redis.Client })
	if !ok || owner.redisClient() == nil {
		return nil, errors.New("HA Redis backend is unavailable")
	}
	return owner.redisClient(), nil
}

func newHARedisClient(config *util.HAConfig) (*redis.Client, string, error) {
	if config == nil || strings.TrimSpace(config.NodeID) == "" {
		return nil, "", errors.New("invalid HA Redis configuration")
	}

	switch config.EngineType() {
	case util.HAEngineRedis:
		if config.Redis == nil {
			return nil, "", errors.New("invalid HA Redis configuration")
		}
		addr, err := directRedisAddress(config.Redis.Addr)
		if err != nil {
			return nil, "", errors.New("invalid HA Redis configuration")
		}
		client, err := newDirectHARedisClient(config.Redis)
		if err != nil {
			return nil, "", err
		}
		return client, "Redis at " + addr, nil
	case util.HAEngineRedisSentinel:
		if config.RedisSentinel == nil {
			return nil, "", errors.New("invalid HA Redis configuration")
		}
		client, err := newSentinelHARedisClient(config.RedisSentinel)
		if err != nil {
			return nil, "", err
		}
		return client, sentinelDescription(config.RedisSentinel), nil
	default:
		return nil, "", errors.New("invalid HA Redis configuration")
	}
}

func newDirectHARedisClient(config *util.HARedisConfig) (*redis.Client, error) {
	addr, err := directRedisAddress(config.Addr)
	if err != nil {
		return nil, errors.New("invalid HA Redis configuration")
	}
	options := &redis.Options{
		Addr:      addr,
		DB:        config.DB,
		TLSConfig: redisTLSConfig(config.TLS, config.TLSSkipVerify),
	}
	options.Username = config.User
	options.Password = config.Pass
	setHARedisTimeouts(options)
	return redis.NewClient(options), nil
}

func newSentinelHARedisClient(config *util.HARedisSentinelConfig) (*redis.Client, error) {
	masterName := strings.TrimSpace(config.MasterName)
	if masterName == "" || !safeRedisName(masterName) || len(config.Addrs) == 0 {
		return nil, errors.New("invalid HA Redis configuration")
	}
	addrs := make([]string, 0, len(config.Addrs))
	for _, configuredAddr := range config.Addrs {
		addr, err := redisAddress(configuredAddr)
		if err != nil {
			return nil, errors.New("invalid HA Redis configuration")
		}
		addrs = append(addrs, addr)
	}
	options := &redis.FailoverOptions{
		MasterName:    masterName,
		SentinelAddrs: addrs,
		DB:            config.DB,
		TLSConfig:     redisTLSConfig(config.TLS, config.TLSSkipVerify),
	}
	options.SentinelUsername = config.SentinelUser
	options.SentinelPassword = config.SentinelPass
	options.Username = config.User
	options.Password = config.Pass
	setFailoverHARedisTimeouts(options)
	return redis.NewFailoverClient(options), nil
}

func setHARedisTimeouts(options *redis.Options) {
	options.ContextTimeoutEnabled = true
	options.DialTimeout = haRedisDialTimeout
	options.ReadTimeout = haRedisReadTimeout
	options.WriteTimeout = haRedisWriteTimeout
}

func setFailoverHARedisTimeouts(options *redis.FailoverOptions) {
	options.ContextTimeoutEnabled = true
	options.DialTimeout = haRedisDialTimeout
	options.ReadTimeout = haRedisReadTimeout
	options.WriteTimeout = haRedisWriteTimeout
}

func redisAddress(value string) (string, error) {
	if value != strings.TrimSpace(value) {
		return "", errors.New("invalid Redis address")
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil || host == "" || strings.ContainsAny(host, "@/\\?#\r\n \t") {
		return "", errors.New("invalid Redis address")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", errors.New("invalid Redis address")
	}
	return net.JoinHostPort(host, port), nil
}

// directRedisAddress retains the historical go-redis default port for a
// standalone Redis hostname. Sentinel seeds remain explicit host:port values.
func directRedisAddress(value string) (string, error) {
	if addr, err := redisAddress(value); err == nil {
		return addr, nil
	}
	if value == "" || value != strings.TrimSpace(value) || strings.ContainsAny(value, "@/\\?#\r\n \t") {
		return "", errors.New("invalid Redis address")
	}
	if host, port, splitErr := net.SplitHostPort(value); splitErr == nil && port == "" {
		if host == "" || strings.ContainsAny(host, "@/\\?#\r\n \t") || (strings.HasPrefix(value, "[") && net.ParseIP(host) == nil) {
			return "", errors.New("invalid Redis address")
		}
		return redisAddress(net.JoinHostPort(host, "6379"))
	}
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		if address := net.ParseIP(strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")); address != nil {
			return net.JoinHostPort(address.String(), "6379"), nil
		}
		return "", errors.New("invalid Redis address")
	}
	if strings.HasSuffix(value, ":") {
		value = strings.TrimSuffix(value, ":")
	}
	if value == "" || strings.Contains(value, ":") {
		return "", errors.New("invalid Redis address")
	}
	return net.JoinHostPort(value, "6379"), nil
}

func sentinelDescription(config *util.HARedisSentinelConfig) string {
	addrs := make([]string, 0, len(config.Addrs))
	for _, configuredAddr := range config.Addrs {
		addr, err := redisAddress(configuredAddr)
		if err != nil {
			return "Redis Sentinel unavailable"
		}
		addrs = append(addrs, addr)
	}
	return "Redis Sentinel " + strings.TrimSpace(config.MasterName) + " via " + strings.Join(addrs, ",")
}

func safeRedisName(value string) bool {
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '.' || character == '_' || character == '-') {
			return false
		}
	}
	return true
}

func redisTLSConfig(enabled bool, skipVerify bool) *tls.Config {
	if !enabled {
		return nil
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: skipVerify}
}
