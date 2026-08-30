package db

import "testing"

func TestDockerTelemetryBatchRejectsUnboundedAndUntrustedFields(t *testing.T) {
	valid := DockerTelemetryBatch{Events: []DockerTelemetryEvent{{Sequence: 1, Kind: DockerTelemetryResourceUsage, Role: DockerTelemetryRoleTask, CPUUsageNanoseconds: 1, MemoryBytes: 2, PIDs: 3}}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid bounded telemetry rejected: %v", err)
	}
	invalid := valid
	invalid.Events[0].PolicyRule = "daemon-error-with-sensitive-input"
	if err := invalid.Validate(); err == nil {
		t.Fatal("resource event accepted a raw policy field")
	}
	overgrown := DockerTelemetryBatch{Events: make([]DockerTelemetryEvent, 101)}
	if err := overgrown.Validate(); err == nil {
		t.Fatal("batch over 100 events was accepted")
	}
}
