package ha

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
)

// NewRunnerCache retains the store's memory cache on a single node. HA reads
// SQL on every lookup because separate nodes cannot atomically invalidate a
// process-local cache alongside a SQL write.
func NewRunnerCache(_ pro_interfaces.HARedis) db.RunnerCache {
	if util.Config == nil || !util.HAEnabled() {
		return nil
	}
	return uncachedRunners{}
}

type uncachedRunners struct{}

func (uncachedRunners) Get(string) (db.Runner, uint64, bool) { return db.Runner{}, 0, false }
func (uncachedRunners) Put(string, db.Runner, uint64)        {}
func (uncachedRunners) Forget(string)                        {}
func (uncachedRunners) Invalidate()                          {}
