package ha

import (
	"github.com/semaphoreui/semaphore/api/sockets"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/schedules"
)

// These aliases keep the historical package API while making the shared
// pro_interfaces definitions authoritative for both editions.
type NodeRegistry = pro_interfaces.NodeRegistry
type OrphanCleaner = pro_interfaces.OrphanCleaner
type ClusterInspector = pro_interfaces.ClusterInspector

// Stubs – these are replaced by pro_impl via Go workspace.

func NewNodeRegistry(_ db.Store) NodeRegistry                 { return nil }
func NewScheduleDeduplicator() schedules.ScheduleDeduplicator { return nil }
func NewWSBroadcaster() sockets.Broadcaster                   { return nil }
func NewOrphanCleaner(_ db.Store) OrphanCleaner               { return nil }
func NewClusterInspector(_ db.Store) ClusterInspector         { return nil }
func NewWorkflowRunLocker() pro_interfaces.WorkflowRunLocker  { return nil }
