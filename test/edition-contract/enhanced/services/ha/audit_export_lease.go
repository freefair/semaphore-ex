package ha

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
)

const (
	auditExportLeaseTTL    = 15 * time.Second
	auditExportLeaseRenew  = 5 * time.Second
	auditExportLeasePrefix = "semaphore:cluster:audit-export"
)

type localAuditExportLeaser struct {
	mu     sync.Mutex
	active map[string]*localAuditExportLease
}

type localAuditExportLease struct {
	owner *localAuditExportLeaser
	id    string
	lost  chan struct{}
	valid atomic.Bool
	once  sync.Once
}

var _ pro_interfaces.AuditExportLeaser = (*localAuditExportLeaser)(nil)
var _ pro_interfaces.AuditExportLease = (*localAuditExportLease)(nil)

func newLocalAuditExportLeaser() *localAuditExportLeaser {
	return &localAuditExportLeaser{active: make(map[string]*localAuditExportLease)}
}

func (l *localAuditExportLeaser) TryAcquire(ctx context.Context, destinationID string) (pro_interfaces.AuditExportLease, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active[destinationID] != nil {
		return nil, false, nil
	}
	lease := &localAuditExportLease{owner: l, id: destinationID, lost: make(chan struct{})}
	lease.valid.Store(true)
	l.active[destinationID] = lease
	return lease, true, nil
}

func (l *localAuditExportLease) Lost() <-chan struct{} { return l.lost }
func (l *localAuditExportLease) Valid() bool           { return l.valid.Load() }
func (l *localAuditExportLease) Release() {
	l.once.Do(func() {
		l.owner.mu.Lock()
		if l.owner.active[l.id] == l {
			delete(l.owner.active, l.id)
		}
		l.owner.mu.Unlock()
		l.valid.Store(false)
		close(l.lost)
	})
}

type unavailableAuditExportLeaser struct{ err error }

func (l unavailableAuditExportLeaser) TryAcquire(ctx context.Context, _ string) (pro_interfaces.AuditExportLease, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if l.err != nil {
		return nil, false, l.err
	}
	return nil, false, errors.New("audit export lease coordination is unavailable")
}

type redisAuditExportLeaser struct {
	client *redis.Client
	owner  string
}

type redisAuditExportLease struct {
	client  *redis.Client
	key     string
	token   string
	lost    chan struct{}
	valid   atomic.Bool
	stop    chan struct{}
	once    sync.Once
	mu      sync.Mutex
	expires time.Time
}

var _ pro_interfaces.AuditExportLeaser = (*redisAuditExportLeaser)(nil)
var _ pro_interfaces.AuditExportLease = (*redisAuditExportLease)(nil)

// NewAuditExportLeaser uses a process-local lease for a single node. HA uses
// Redis compare-and-renew ownership; missing or invalid HA coordination fails
// closed so no node exports until Redis is usable again.
func NewAuditExportLeaser() pro_interfaces.AuditExportLeaser {
	if util.Config == nil || !util.HAEnabled() {
		return newLocalAuditExportLeaser()
	}
	if util.Config.HA == nil || util.Config.HA.NodeID == "" || util.Config.HA.Redis == nil || util.Config.HA.Redis.Addr == "" {
		return unavailableAuditExportLeaser{err: errors.New("audit export HA requires node ID and Redis address")}
	}
	options, err := redisOptionsForHA(util.Config.HA.Redis)
	if err != nil {
		return unavailableAuditExportLeaser{err: errors.New("audit export Redis configuration is invalid")}
	}
	identity, err := clusterIdentity(util.Config.HA.NodeID)
	if err != nil {
		return unavailableAuditExportLeaser{err: errors.New("audit export cluster identity is unavailable")}
	}
	return &redisAuditExportLeaser{client: newAuditExportRedisClient(options), owner: identity.BootID}
}

func newAuditExportRedisClient(options *redis.Options) *redis.Client {
	auditOptions := *options
	// Lease loss must bound an in-flight Redis request even when the peer accepts
	// TCP and never replies. Do not change the shared HA client defaults.
	auditOptions.ContextTimeoutEnabled = true
	auditOptions.DialTimeout = time.Second
	auditOptions.ReadTimeout = time.Second
	auditOptions.WriteTimeout = time.Second
	return redis.NewClient(&auditOptions)
}

func (l *redisAuditExportLeaser) TryAcquire(ctx context.Context, destinationID string) (pro_interfaces.AuditExportLease, bool, error) {
	token, err := auditExportToken()
	if err != nil {
		return nil, false, err
	}
	key := auditExportLeasePrefix + ":" + destinationID
	leaseStarted := time.Now()
	acquired, err := l.client.SetNX(ctx, key, l.owner+":"+token, auditExportLeaseTTL).Result()
	if err != nil || !acquired {
		return nil, acquired, err
	}
	lease := &redisAuditExportLease{client: l.client, key: key, token: l.owner + ":" + token, lost: make(chan struct{}), stop: make(chan struct{})}
	lease.valid.Store(true)
	lease.setExpiry(leaseStarted.Add(auditExportLeaseTTL))
	go lease.renew()
	go lease.expire()
	return lease, true, nil
}

func (l *redisAuditExportLease) Lost() <-chan struct{} { return l.lost }
func (l *redisAuditExportLease) Valid() bool {
	l.mu.Lock()
	expires := l.expires
	l.mu.Unlock()
	if !l.valid.Load() || !time.Now().Before(expires) {
		l.invalidate()
		return false
	}
	return true
}
func (l *redisAuditExportLease) Release() {
	l.once.Do(func() {
		close(l.stop)
		l.invalidate()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = l.client.Eval(ctx, "if redis.call('get', KEYS[1]) == ARGV[1] then return redis.call('del', KEYS[1]) end return 0", []string{l.key}, l.token).Result()
	})
}

func (l *redisAuditExportLease) renew() {
	ticker := time.NewTicker(auditExportLeaseRenew)
	defer ticker.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-ticker.C:
			if !l.renewOnce() {
				return
			}
		}
	}
}

func (l *redisAuditExportLease) renewOnce() bool {
	renewStarted := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := l.client.Eval(ctx, "if redis.call('get', KEYS[1]) == ARGV[1] then return redis.call('pexpire', KEYS[1], ARGV[2]) end return 0", []string{l.key}, l.token, auditExportLeaseTTL.Milliseconds()).Int64()
	if err != nil || result != 1 {
		l.invalidate()
		return false
	}
	l.setExpiry(renewStarted.Add(auditExportLeaseTTL))
	return true
}

func (l *redisAuditExportLease) expire() {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-ticker.C:
			l.mu.Lock()
			expires := l.expires
			l.mu.Unlock()
			if !time.Now().Before(expires) {
				l.invalidate()
				return
			}
		}
	}
}

func (l *redisAuditExportLease) setExpiry(expires time.Time) {
	l.mu.Lock()
	l.expires = expires
	l.mu.Unlock()
}

func (l *redisAuditExportLease) invalidate() {
	if l.valid.Swap(false) {
		close(l.lost)
	}
}

func auditExportToken() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
