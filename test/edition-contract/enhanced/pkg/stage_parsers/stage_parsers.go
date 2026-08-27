// Package stage_parsers adapts the public Community surface for the workspace fixture.
package stage_parsers

import community "github.com/semaphoreui/semaphore/community-pro/pkg/stage_parsers"

var (
	MoveToNextStage                    = community.MoveToNextStage
	IngestTaskSummaryOutput            = community.IngestTaskSummaryOutput
	ParseTaskSummaryEvent              = community.ParseTaskSummaryEvent
	RedactTaskSummaryError             = community.RedactTaskSummaryError
	TaskSummaryCollectionFailureOutput = community.TaskSummaryCollectionFailureOutput
	TaskSummaryCallbackEnvironment     = community.TaskSummaryCallbackEnvironment
)
