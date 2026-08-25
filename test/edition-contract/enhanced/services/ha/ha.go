// Package ha adapts the public Community surface for the workspace fixture.
package ha

import community "github.com/semaphoreui/semaphore/community-pro/services/ha"

type NodeRegistry = community.NodeRegistry
type OrphanCleaner = community.OrphanCleaner
type ClusterInspector = community.ClusterInspector

var (
	NewNodeRegistry         = community.NewNodeRegistry
	NewScheduleDeduplicator = community.NewScheduleDeduplicator
	NewWSBroadcaster        = community.NewWSBroadcaster
	NewOrphanCleaner        = community.NewOrphanCleaner
	NewClusterInspector     = community.NewClusterInspector
	NewWorkflowRunLocker    = community.NewWorkflowRunLocker
)
