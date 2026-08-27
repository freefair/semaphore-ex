package db

import (
	"time"
)

const TaskSummarySchemaVersion = 1

type TaskSummaryState string

const (
	TaskSummaryCollecting  TaskSummaryState = "collecting"
	TaskSummaryComplete    TaskSummaryState = "complete"
	TaskSummaryPartial     TaskSummaryState = "partial"
	TaskSummaryEmpty       TaskSummaryState = "empty"
	TaskSummaryUnsupported TaskSummaryState = "unsupported"
)

type TaskSummaryEventKind string

const (
	TaskSummaryEventResult   TaskSummaryEventKind = "task_result"
	TaskSummaryEventHost     TaskSummaryEventKind = "host_summary"
	TaskSummaryEventComplete TaskSummaryEventKind = "run_complete"
	TaskSummaryEventFailure  TaskSummaryEventKind = "collection_error"
)

type TaskSummaryEvent struct {
	Version       int                  `json:"version"`
	Kind          TaskSummaryEventKind `json:"event"`
	EventID       string               `json:"event_id"`
	PlayID        string               `json:"play_id,omitempty"`
	StageID       string               `json:"stage_id,omitempty"`
	Stage         string               `json:"stage,omitempty"`
	Host          string               `json:"host,omitempty"`
	Status        string               `json:"status,omitempty"`
	Changed       int                  `json:"changed,omitempty"`
	Failed        int                  `json:"failed,omitempty"`
	Ignored       int                  `json:"ignored,omitempty"`
	Ok            int                  `json:"ok,omitempty"`
	Rescued       int                  `json:"rescued,omitempty"`
	Skipped       int                  `json:"skipped,omitempty"`
	Unreachable   int                  `json:"unreachable,omitempty"`
	ExpectedHosts int                  `json:"expected_hosts,omitempty"`
	Started       *time.Time           `json:"started_at,omitempty"`
	Ended         *time.Time           `json:"ended_at,omitempty"`
	DurationMS    int64                `json:"duration_ms,omitempty"`
	Error         string               `json:"error,omitempty"`
}

type TaskSummary struct {
	Version             int              `json:"version" db:"schema_version"`
	TaskID              int              `json:"task_id" db:"task_id"`
	ProjectID           int              `json:"project_id" db:"project_id"`
	RunnerResultVersion int              `json:"runner_result_version" db:"runner_result_version"`
	State               TaskSummaryState `json:"state" db:"state"`
	TaskStatus          string           `json:"task_status" db:"task_status"`
	ExpectedHosts       int              `json:"expected_hosts" db:"expected_hosts"`
	TotalHosts          int              `json:"total_hosts" db:"total_hosts"`
	OkHosts             int              `json:"ok_hosts" db:"ok_hosts"`
	FailedHosts         int              `json:"failed_hosts" db:"failed_hosts"`
	EventCount          int              `json:"event_count" db:"event_count"`
	Diagnostic          string           `json:"diagnostic,omitempty" db:"diagnostic"`
	Started             *time.Time       `json:"started_at,omitempty" db:"started_at"`
	Ended               *time.Time       `json:"ended_at,omitempty" db:"ended_at"`
	Updated             time.Time        `json:"updated" db:"updated"`
}

type TaskSummaryHost struct {
	ID          int        `json:"id" db:"id"`
	Host        string     `json:"host" db:"host"`
	Status      string     `json:"status" db:"status"`
	Changed     int        `json:"changed" db:"changed"`
	Failed      int        `json:"failed" db:"failed"`
	Ignored     int        `json:"ignored" db:"ignored"`
	Ok          int        `json:"ok" db:"ok"`
	Rescued     int        `json:"rescued" db:"rescued"`
	Skipped     int        `json:"skipped" db:"skipped"`
	Unreachable int        `json:"unreachable" db:"unreachable"`
	Started     *time.Time `json:"started_at,omitempty" db:"started_at"`
	Ended       *time.Time `json:"ended_at,omitempty" db:"ended_at"`
	DurationMS  int64      `json:"duration_ms" db:"duration_ms"`
}

type TaskSummaryStage struct {
	ID          int        `json:"id" db:"id"`
	StageID     string     `json:"stage_id" db:"stage_id"`
	Stage       string     `json:"stage" db:"stage"`
	Ok          int        `json:"ok" db:"ok"`
	Changed     int        `json:"changed" db:"changed"`
	Failed      int        `json:"failed" db:"failed"`
	Ignored     int        `json:"ignored" db:"ignored"`
	Rescued     int        `json:"rescued" db:"rescued"`
	Skipped     int        `json:"skipped" db:"skipped"`
	Unreachable int        `json:"unreachable" db:"unreachable"`
	Started     *time.Time `json:"started_at,omitempty" db:"started_at"`
	Ended       *time.Time `json:"ended_at,omitempty" db:"ended_at"`
	DurationMS  int64      `json:"duration_ms" db:"duration_ms"`
}

type TaskSummaryError struct {
	ID         int        `json:"id" db:"id"`
	EventID    string     `json:"event_id" db:"event_id"`
	Host       string     `json:"host" db:"host"`
	Stage      string     `json:"stage" db:"stage"`
	Status     string     `json:"status" db:"status"`
	Error      string     `json:"error" db:"error"`
	OutputTime *time.Time `json:"output_time,omitempty" db:"output_time"`
}

type TaskSummaryPage[T any] struct {
	Items      []T  `json:"items"`
	NextCursor *int `json:"next_cursor,omitempty"`
}
