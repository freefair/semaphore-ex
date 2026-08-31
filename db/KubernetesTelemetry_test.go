package db

import "testing"

func TestKubernetesTelemetryTypeMatrixRejectsUnboundedFields(t *testing.T) {
	valid := KubernetesTelemetryBatch{Events: []KubernetesTelemetryEvent{
		{Sequence: 1, Kind: KubernetesTelemetryAPILatency, Operation: KubernetesTelemetryOperationCreateJob, DurationMilliseconds: 1},
		{Sequence: 2, Kind: KubernetesTelemetryWatchReconnect, Operation: KubernetesTelemetryOperationWatchJobs, Count: 1},
		{Sequence: 3, Kind: KubernetesTelemetryDenial, PolicyRule: KubernetesPolicyRuleAdmissionDenied},
		{Sequence: 4, Kind: KubernetesTelemetryCleanupFailure, CleanupResource: KubernetesTelemetryResourceSecret},
		{Sequence: 5, Kind: KubernetesTelemetryReconciliation, ReconciliationState: KubernetesTelemetryReconciliationObserved, Count: 1},
		{Sequence: 6, Kind: KubernetesTelemetryOrphan, Count: 1},
		{Sequence: 7, Kind: KubernetesTelemetryQuarantine, Count: 1},
		{Sequence: 8, Kind: KubernetesTelemetryDrop, DropReason: KubernetesTelemetryDropQueueFull, Count: 1},
	}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid Kubernetes telemetry rejected: %v", err)
	}
	invalid := valid
	invalid.Events = append([]KubernetesTelemetryEvent(nil), valid.Events...)
	invalid.Events[0].PolicyRule = "admission contained a token"
	if invalid.Validate() == nil {
		t.Fatal("Kubernetes telemetry accepted raw error text")
	}
	invalid = valid
	invalid.Events = make([]KubernetesTelemetryEvent, 101)
	if invalid.Validate() == nil {
		t.Fatal("Kubernetes telemetry accepted an unbounded batch")
	}
}
