package ha

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagedWSBroadcasterBoundsPublishesAndDeduplicatesRedisDelivery(t *testing.T) {
	transport := &eventTransportFake{}
	var deliveries []eventDelivery
	broadcaster := NewManagedWSBroadcaster(transport, "boot-a", func(userID int, message []byte) {
		deliveries = append(deliveries, eventDelivery{userID: userID, message: string(message)})
	})

	broadcaster.Publish(7, []byte("task-updated"))
	require.Equal(t, []eventDelivery{{userID: 7, message: "task-updated"}}, deliveries)
	require.Len(t, transport.published, 1)
	broadcaster.handleIncoming(transport.published[0])
	require.Len(t, deliveries, 1)

	broadcaster.Publish(7, make([]byte, maxClusterEventMessageBytes+1))
	assert.Len(t, deliveries, 2)
	assert.Len(t, transport.published, 1)
}

func TestGoRedisEventTransportDeliversOneEventToAnotherNode(t *testing.T) {
	server := miniredis.RunT(t)
	firstTransport := NewGoRedisEventTransport(redis.NewClient(&redis.Options{Addr: server.Addr()}))
	secondTransport := NewGoRedisEventTransport(redis.NewClient(&redis.Options{Addr: server.Addr()}))
	firstDelivery := make(chan eventDelivery, 2)
	secondDelivery := make(chan eventDelivery, 2)
	first := NewManagedWSBroadcaster(firstTransport, "boot-a", func(userID int, message []byte) {
		firstDelivery <- eventDelivery{userID: userID, message: string(message)}
	})
	second := NewManagedWSBroadcaster(secondTransport, "boot-b", func(userID int, message []byte) {
		secondDelivery <- eventDelivery{userID: userID, message: string(message)}
	})
	first.Start()
	second.Start()
	t.Cleanup(first.Stop)
	t.Cleanup(second.Stop)
	select {
	case <-first.ready:
	case <-time.After(2 * time.Second):
		t.Fatal("expected first node subscription readiness")
	}
	select {
	case <-second.ready:
	case <-time.After(2 * time.Second):
		t.Fatal("expected second node subscription readiness")
	}

	first.Publish(7, []byte("task-updated"))
	require.Equal(t, eventDelivery{userID: 7, message: "task-updated"}, <-firstDelivery)
	select {
	case received := <-secondDelivery:
		assert.Equal(t, eventDelivery{userID: 7, message: "task-updated"}, received)
	case <-time.After(2 * time.Second):
		t.Fatal("expected event delivery on the second node")
	}
	select {
	case duplicate := <-firstDelivery:
		t.Fatalf("sender must not receive duplicate Redis delivery: %#v", duplicate)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestManagedWSBroadcasterReportsDegradedAndHealthyLiveEventTransport(t *testing.T) {
	transport := &subscribingEventTransportFake{messages: make(chan []byte)}
	health := newLiveEventHealth()
	broadcaster := NewManagedWSBroadcaster(transport, "boot-a", func(int, []byte) {}, health)
	broadcaster.Start()
	select {
	case <-broadcaster.ready:
	case <-time.After(2 * time.Second):
		t.Fatal("expected broadcaster readiness")
	}
	assert.Equal(t, "healthy", health.CoordinatorHealth().LiveEvents)
	broadcaster.Stop()

	failedHealth := newLiveEventHealth()
	failed := NewManagedWSBroadcaster(&subscribingEventTransportFake{err: errors.New("redis unavailable")}, "boot-b", func(int, []byte) {}, failedHealth)
	failed.Start()
	require.Eventually(t, func() bool {
		return failedHealth.CoordinatorHealth().LiveEvents == "degraded"
	}, time.Second, 10*time.Millisecond)
	failed.Stop()
}

func TestManagedWSBroadcasterCanRestartWithoutClosingTheNewLifecycleChannels(t *testing.T) {
	transport := &subscribingEventTransportFake{messages: make(chan []byte)}
	broadcaster := NewManagedWSBroadcaster(transport, "boot-a", func(int, []byte) {})
	broadcaster.Start()
	select {
	case <-broadcaster.ready:
	case <-time.After(2 * time.Second):
		t.Fatal("expected first readiness")
	}
	broadcaster.Stop()
	broadcaster.Start()
	select {
	case <-broadcaster.ready:
	case <-time.After(2 * time.Second):
		t.Fatal("expected fresh readiness after restart")
	}
	broadcaster.Stop()
}

type eventDelivery struct {
	userID  int
	message string
}

type eventTransportFake struct {
	published  [][]byte
	publishErr error
}

func (f *eventTransportFake) Publish(_ context.Context, payload []byte) error {
	if f.publishErr != nil {
		return f.publishErr
	}
	f.published = append(f.published, append([]byte(nil), payload...))
	return nil
}

func (*eventTransportFake) Subscribe(context.Context) (<-chan []byte, func(), error) {
	return nil, func() {}, errors.New("not used by this contract")
}

type subscribingEventTransportFake struct {
	messages chan []byte
	err      error
}

func (*subscribingEventTransportFake) Publish(context.Context, []byte) error { return nil }
func (f *subscribingEventTransportFake) Subscribe(context.Context) (<-chan []byte, func(), error) {
	if f.err != nil {
		return nil, func() {}, f.err
	}
	return f.messages, func() {}, nil
}
