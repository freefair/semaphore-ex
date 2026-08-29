package ha

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

const defaultClusterHeartbeatPrefix = "semaphore:cluster:node"

type redisHeartbeatStore struct {
	client pro_interfaces.ClusterHeartbeatRedisClient
	prefix string
}

var _ pro_interfaces.ClusterHeartbeatStore = (*redisHeartbeatStore)(nil)

// NewRedisHeartbeatStore creates the live Redis projection used by the node
// registry. The durable registration remains in SQL.
func NewRedisHeartbeatStore(client pro_interfaces.ClusterHeartbeatRedisClient, prefix string) pro_interfaces.ClusterHeartbeatStore {
	prefix = strings.Trim(strings.TrimSpace(prefix), ":")
	if prefix == "" {
		prefix = defaultClusterHeartbeatPrefix
	}
	return &redisHeartbeatStore{client: client, prefix: prefix}
}

func (s *redisHeartbeatStore) Publish(ctx context.Context, identity pro_interfaces.ClusterNodeIdentity, ttl time.Duration) (time.Time, error) {
	if err := validateHeartbeat(identity, ttl); err != nil {
		return time.Time{}, err
	}
	if s.client == nil {
		return time.Time{}, errors.New("cluster heartbeat redis client is required")
	}
	if err := s.client.SetWithTTL(ctx, s.key(identity), identity.BootID, ttl); err != nil {
		return time.Time{}, err
	}
	return s.client.ServerTime(ctx)
}

func (s *redisHeartbeatStore) IsLive(ctx context.Context, identity pro_interfaces.ClusterNodeIdentity) (bool, time.Time, error) {
	if identity.NodeID == "" || identity.BootID == "" {
		return false, time.Time{}, errors.New("cluster heartbeat identity is required")
	}
	if s.client == nil {
		return false, time.Time{}, errors.New("cluster heartbeat redis client is required")
	}
	now, err := s.client.ServerTime(ctx)
	if err != nil {
		return false, time.Time{}, err
	}
	alive, err := s.client.Exists(ctx, s.key(identity))
	return alive, now, err
}

func (s *redisHeartbeatStore) Remove(ctx context.Context, identity pro_interfaces.ClusterNodeIdentity) error {
	if identity.NodeID == "" || identity.BootID == "" {
		return errors.New("cluster heartbeat identity is required")
	}
	if s.client == nil {
		return errors.New("cluster heartbeat redis client is required")
	}
	return s.client.Delete(ctx, s.key(identity))
}

func (s *redisHeartbeatStore) key(identity pro_interfaces.ClusterNodeIdentity) string {
	return fmt.Sprintf("%s:%s:%s", s.prefix, identity.NodeID, identity.BootID)
}

func validateHeartbeat(identity pro_interfaces.ClusterNodeIdentity, ttl time.Duration) error {
	if identity.NodeID == "" || identity.BootID == "" {
		return errors.New("cluster heartbeat identity is required")
	}
	if ttl <= 0 {
		return errors.New("cluster heartbeat ttl must be positive")
	}
	return nil
}

type goRedisHeartbeatClient struct{ client *redis.Client }

// NewGoRedisHeartbeatClient adapts the official Redis client to the narrow
// heartbeat boundary so deterministic tests do not need a Redis process.
func NewGoRedisHeartbeatClient(client *redis.Client) *goRedisHeartbeatClient {
	return &goRedisHeartbeatClient{client: client}
}

func (c *goRedisHeartbeatClient) ServerTime(ctx context.Context) (time.Time, error) {
	value, err := c.client.Time(ctx).Result()
	return value.UTC(), err
}

func (c *goRedisHeartbeatClient) SetWithTTL(ctx context.Context, key string, value string, ttl time.Duration) error {
	return c.client.Set(ctx, key, value, ttl).Err()
}

func (c *goRedisHeartbeatClient) Exists(ctx context.Context, key string) (bool, error) {
	count, err := c.client.Exists(ctx, key).Result()
	return count > 0, err
}

func (c *goRedisHeartbeatClient) Delete(ctx context.Context, key string) error {
	return c.client.Del(ctx, key).Err()
}

func (c *goRedisHeartbeatClient) RedisDiagnostics(ctx context.Context) (pro_interfaces.RedisInfo, error) {
	if err := c.client.Ping(ctx).Err(); err != nil {
		return pro_interfaces.RedisInfo{Addr: c.client.Options().Addr, Connected: false}, err
	}
	info, err := c.client.Info(ctx, "server", "memory").Result()
	if err != nil {
		return pro_interfaces.RedisInfo{Addr: c.client.Options().Addr, Connected: false}, err
	}
	totalKeys, err := c.client.DBSize(ctx).Result()
	if err != nil {
		return pro_interfaces.RedisInfo{Addr: c.client.Options().Addr, Connected: false}, err
	}
	values := parseRedisInfo(info)
	usedMemoryBytes, _ := strconv.ParseInt(values["used_memory"], 10, 64)
	return pro_interfaces.RedisInfo{
		Addr: c.client.Options().Addr, Connected: true, Version: values["redis_version"],
		UsedMemory: values["used_memory_human"], UsedMemoryBytes: usedMemoryBytes,
		TotalKeys: int(totalKeys), KeyGroups: map[string]int{},
	}, nil
}

func parseRedisInfo(raw string) map[string]string {
	values := make(map[string]string)
	for _, line := range strings.Split(raw, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok {
			values[key] = value
		}
	}
	return values
}
