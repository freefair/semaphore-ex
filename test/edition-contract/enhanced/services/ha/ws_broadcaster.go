package ha

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
)

const (
	maxClusterEventMessageBytes  = 64 * 1024
	maxClusterEventDedupe        = 1024
	clusterEventReconnectMin     = 100 * time.Millisecond
	clusterEventReconnectMax     = 5 * time.Second
	clusterEventChannel          = "semaphore:cluster:events:v1"
	clusterEventSubscriberBuffer = 128
)

// clusterEventTransport is intentionally lossy: it distributes only live
// invalidation/wake-up events. SQL/API refresh remains the recovery path.
type clusterEventTransport interface {
	Publish(ctx context.Context, payload []byte) error
	Subscribe(ctx context.Context) (<-chan []byte, func(), error)
}

type clusterEventEnvelope struct {
	ID      string `json:"id"`
	Origin  string `json:"origin"`
	UserID  int    `json:"user_id"`
	Message []byte `json:"message"`
}

type goRedisEventTransport struct{ client *redis.Client }

func NewGoRedisEventTransport(client *redis.Client) *goRedisEventTransport {
	return &goRedisEventTransport{client: client}
}

func (t *goRedisEventTransport) Publish(ctx context.Context, payload []byte) error {
	return t.client.Publish(ctx, clusterEventChannel, payload).Err()
}

func (t *goRedisEventTransport) Subscribe(ctx context.Context) (<-chan []byte, func(), error) {
	pubsub := t.client.Subscribe(ctx, clusterEventChannel)
	if _, err := pubsub.ReceiveTimeout(ctx, clusterEventReconnectMax); err != nil {
		_ = pubsub.Close()
		return nil, func() {}, err
	}
	output := make(chan []byte, clusterEventSubscriberBuffer)
	messages := pubsub.Channel(redis.WithChannelSize(clusterEventSubscriberBuffer))
	var closeOnce sync.Once
	closeSubscription := func() {
		closeOnce.Do(func() { _ = pubsub.Close() })
	}
	go func() {
		defer close(output)
		defer closeSubscription()
		for {
			select {
			case <-ctx.Done():
				return
			case message, ok := <-messages:
				if !ok {
					return
				}
				payload := []byte(message.Payload)
				select {
				case output <- payload:
				default:
					log.Warn("cluster live-event subscriber queue overflow; clients will recover via API refresh")
				}
			}
		}
	}()
	return output, closeSubscription, nil
}

type managedWSBroadcaster struct {
	transport clusterEventTransport
	origin    string
	deliver   func(int, []byte)
	health    *liveEventHealth

	mutex     sync.Mutex
	seen      map[string]struct{}
	seenIDs   []string
	cancel    context.CancelFunc
	done      chan struct{}
	ready     chan struct{}
	readyOnce *sync.Once
}

func NewManagedWSBroadcaster(
	transport clusterEventTransport,
	origin string,
	deliver func(int, []byte),
	health ...*liveEventHealth,
) *managedWSBroadcaster {
	currentHealth := newLiveEventHealth()
	if len(health) > 0 && health[0] != nil {
		currentHealth = health[0]
	}
	return &managedWSBroadcaster{
		transport: transport,
		origin:    origin,
		deliver:   deliver,
		health:    currentHealth,
		seen:      make(map[string]struct{}),
	}
}

func (b *managedWSBroadcaster) Start() {
	b.mutex.Lock()
	if b.cancel != nil || b.transport == nil || b.deliver == nil {
		b.mutex.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	b.done = make(chan struct{})
	b.ready = make(chan struct{})
	b.readyOnce = &sync.Once{}
	done := b.done
	ready := b.ready
	readyOnce := b.readyOnce
	b.mutex.Unlock()
	go b.subscribe(ctx, done, ready, readyOnce)
}

func (b *managedWSBroadcaster) Stop() {
	b.mutex.Lock()
	cancel := b.cancel
	done := b.done
	b.cancel = nil
	b.done = nil
	b.mutex.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

func (b *managedWSBroadcaster) Publish(userID int, message []byte) {
	if b.deliver != nil {
		b.deliver(userID, append([]byte(nil), message...))
	}
	if b.transport == nil || len(message) > maxClusterEventMessageBytes {
		return
	}
	envelope := clusterEventEnvelope{
		ID:      clusterEventID(),
		Origin:  b.origin,
		UserID:  userID,
		Message: append([]byte(nil), message...),
	}
	if envelope.ID == "" {
		return
	}
	b.markSeen(envelope.ID)
	payload, err := json.Marshal(envelope)
	if err != nil {
		return
	}
	if err = b.transport.Publish(context.Background(), payload); err != nil {
		b.health.degraded(err.Error())
		log.WithError(err).Warn("cluster live-event publish failed; clients will recover via API refresh")
	} else {
		b.health.healthy()
	}
}

func (b *managedWSBroadcaster) subscribe(
	ctx context.Context,
	done chan struct{},
	ready chan struct{},
	readyOnce *sync.Once,
) {
	defer close(done)
	backoff := clusterEventReconnectMin
	for {
		messages, unsubscribe, err := b.transport.Subscribe(ctx)
		if err != nil {
			b.health.degraded(err.Error())
			if !waitClusterEventBackoff(ctx, backoff) {
				return
			}
			backoff = minDuration(backoff*2, clusterEventReconnectMax)
			continue
		}
		backoff = clusterEventReconnectMin
		b.health.healthy()
		readyOnce.Do(func() { close(ready) })
		for {
			select {
			case <-ctx.Done():
				unsubscribe()
				return
			case payload, ok := <-messages:
				if !ok {
					unsubscribe()
					goto reconnect
				}
				b.handleIncoming(payload)
			}
		}
	reconnect:
	}
}

func (b *managedWSBroadcaster) handleIncoming(payload []byte) {
	var envelope clusterEventEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil || envelope.ID == "" || len(envelope.Message) > maxClusterEventMessageBytes {
		return
	}
	if envelope.Origin == b.origin || !b.markSeen(envelope.ID) {
		return
	}
	if b.deliver != nil {
		b.deliver(envelope.UserID, append([]byte(nil), envelope.Message...))
	}
}

func (b *managedWSBroadcaster) markSeen(id string) bool {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if _, exists := b.seen[id]; exists {
		return false
	}
	b.seen[id] = struct{}{}
	b.seenIDs = append(b.seenIDs, id)
	if len(b.seenIDs) > maxClusterEventDedupe {
		oldest := b.seenIDs[0]
		delete(b.seen, oldest)
		b.seenIDs = b.seenIDs[1:]
	}
	return true
}

func clusterEventID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return ""
	}
	return hex.EncodeToString(bytes)
}

func waitClusterEventBackoff(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func minDuration(first time.Duration, second time.Duration) time.Duration {
	if first < second {
		return first
	}
	return second
}
