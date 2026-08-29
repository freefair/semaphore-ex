package ha

import (
	"sync"
	"time"

	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type liveEventHealth struct {
	mutex      sync.RWMutex
	liveEvents string
	reason     string
	observedAt time.Time
}

func newLiveEventHealth() *liveEventHealth {
	return &liveEventHealth{liveEvents: "unavailable", reason: "live-event subscriber not started", observedAt: time.Now().UTC()}
}

func (h *liveEventHealth) CoordinatorHealth() pro_interfaces.ClusterCoordinatorHealth {
	h.mutex.RLock()
	defer h.mutex.RUnlock()
	return pro_interfaces.ClusterCoordinatorHealth{
		SQLAuthoritative: true,
		LiveEvents:       h.liveEvents,
		Reason:           h.reason,
		ObservedAt:       h.observedAt,
	}
}

func (h *liveEventHealth) healthy() {
	h.set("healthy", "")
}

func (h *liveEventHealth) degraded(reason string) {
	h.set("degraded", reason)
}

func (h *liveEventHealth) unavailable(reason string) {
	h.set("unavailable", reason)
}

func (h *liveEventHealth) set(liveEvents string, reason string) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.liveEvents = liveEvents
	h.reason = reason
	h.observedAt = time.Now().UTC()
}

var sharedCoordinatorHealth struct {
	sync.Mutex
	nodeID string
	health *liveEventHealth
}

func coordinatorHealthFor(nodeID string) *liveEventHealth {
	sharedCoordinatorHealth.Lock()
	defer sharedCoordinatorHealth.Unlock()
	if sharedCoordinatorHealth.health != nil && sharedCoordinatorHealth.nodeID == nodeID {
		return sharedCoordinatorHealth.health
	}
	sharedCoordinatorHealth.nodeID = nodeID
	sharedCoordinatorHealth.health = newLiveEventHealth()
	return sharedCoordinatorHealth.health
}
