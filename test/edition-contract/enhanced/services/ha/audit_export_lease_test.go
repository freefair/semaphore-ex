package ha

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnavailableAuditExportLeaserFailsClosedWithConcreteError(t *testing.T) {
	_, acquired, err := (unavailableAuditExportLeaser{err: errors.New("Redis unavailable")}).TryAcquire(context.Background(), "siem-primary")
	assert.False(t, acquired)
	assert.ErrorContains(t, err, "Redis unavailable")
}

func TestLocalAuditExportLeaseAllowsOnlyOneOwnerPerDestination(t *testing.T) {
	leaser := newLocalAuditExportLeaser()
	first, acquired, err := leaser.TryAcquire(context.Background(), "siem-primary")
	require.NoError(t, err)
	require.True(t, acquired)
	second, acquired, err := leaser.TryAcquire(context.Background(), "siem-primary")
	require.NoError(t, err)
	assert.False(t, acquired)
	assert.Nil(t, second)
	first.Release()
	third, acquired, err := leaser.TryAcquire(context.Background(), "siem-primary")
	require.NoError(t, err)
	assert.True(t, acquired)
	require.NotNil(t, third)
	third.Release()
}

func TestRedisAuditExportLeaseSignalsLossAtLocalExpiryWithoutValidCall(t *testing.T) {
	lease := &redisAuditExportLease{lost: make(chan struct{}), stop: make(chan struct{})}
	lease.valid.Store(true)
	lease.setExpiry(time.Now().Add(25 * time.Millisecond))
	go lease.expire()
	lost := false
	select {
	case <-lease.Lost():
		lost = true
	case <-time.After(time.Second):
	}
	require.True(t, lost, "lease expiry did not signal loss")
	assert.False(t, lease.Valid())
}

func TestRedisAuditExportLeaseReleaseAndRenewAreBoundedWhenRedisStalls(t *testing.T) {
	address := blackholeRedisAddress(t)
	client := newAuditExportRedisClient(&redis.Options{Addr: address})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	lease := &redisAuditExportLease{
		client: client,
		key:    "audit-export", token: "owner", lost: make(chan struct{}), stop: make(chan struct{}),
	}
	lease.valid.Store(true)
	lease.setExpiry(time.Now().Add(time.Minute))
	started := time.Now()
	lease.Release()
	require.Less(t, time.Since(started), 1500*time.Millisecond)

	lease = &redisAuditExportLease{
		client: client,
		key:    "audit-export", token: "owner", lost: make(chan struct{}), stop: make(chan struct{}),
	}
	lease.valid.Store(true)
	lease.setExpiry(time.Now().Add(time.Minute))
	started = time.Now()
	assert.False(t, lease.renewOnce())
	require.Less(t, time.Since(started), 1500*time.Millisecond)
	select {
	case <-lease.Lost():
	case <-time.After(time.Second):
		require.FailNow(t, "stalled renewal did not mark the lease lost")
	}
}

func blackholeRedisAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	done := make(chan struct{})
	var connections sync.WaitGroup
	t.Cleanup(func() {
		close(done)
		require.NoError(t, listener.Close())
		connections.Wait()
	})
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			connections.Add(1)
			go func() {
				defer connections.Done()
				defer connection.Close()
				<-done
			}()
		}
	}()
	return listener.Addr().String()
}
