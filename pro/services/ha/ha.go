package ha

import (
	"github.com/semaphoreui/semaphore/api/sockets"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/schedules"
	"github.com/semaphoreui/semaphore/services/tasks"
)

// These aliases keep the historical package API while making the shared
// pro_interfaces definitions authoritative for both editions.
type NodeRegistry = pro_interfaces.NodeRegistry
type OrphanCleaner = pro_interfaces.OrphanCleaner
type ClusterInspector = pro_interfaces.ClusterInspector

// Unselected compatibility stubs; the workspace selects the Enhanced module.

func NewHARedis() pro_interfaces.HARedis { return nil }

func NewNodeRegistry(_ pro_interfaces.HARedis, _ db.Store) NodeRegistry { return nil }
func NewScheduleDeduplicator(_ pro_interfaces.HARedis, _ db.Store) schedules.ScheduleDeduplicator {
	return nil
}
func NewWSBroadcaster(_ pro_interfaces.HARedis, _ db.Store) sockets.Broadcaster { return nil }
func NewOrphanCleaner(_ pro_interfaces.HARedis, _ db.Store, _ *tasks.TaskPool) OrphanCleaner {
	return nil
}

func NewClusterInspector(_ pro_interfaces.HARedis, _ db.Store, _ ...pro_interfaces.ClusterDrainer) ClusterInspector {
	return nil
}
func NewWorkflowRunLocker(_ pro_interfaces.HARedis, _ db.Store) pro_interfaces.WorkflowRunLocker {
	return nil
}
func NewAuditExportLeaser(_ pro_interfaces.HARedis) pro_interfaces.AuditExportLeaser { return nil }
