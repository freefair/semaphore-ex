package pro_interfaces

import (
	"testing"
	"time"
)

func TestNewClusterNodeIdentitySeparatesStableAndBootIdentities(t *testing.T) {
	first, err := NewClusterNodeIdentity("node-a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewClusterNodeIdentity("node-a")
	if err != nil {
		t.Fatal(err)
	}
	if first.NodeID != "node-a" || first.BootID == "" {
		t.Fatalf("unexpected identity: %#v", first)
	}
	if second.NodeID != first.NodeID || second.BootID == first.BootID {
		t.Fatalf("expected stable node id and distinct boot ids: %#v %#v", first, second)
	}
}

func TestNewClusterNodeIdentityRejectsBlankStableIdentity(t *testing.T) {
	if _, err := NewClusterNodeIdentity(" \t "); err == nil {
		t.Fatal("expected blank node id rejection")
	}
}

func TestEvaluateClusterNodeCompatibilityRequiresProtocolSchemaAndCapabilities(t *testing.T) {
	compatible := EvaluateClusterNodeCompatibility(ClusterNodeRegistration{
		ProtocolVersion: 1,
		SchemaVersion:   "2.20.23",
		Capabilities:    []string{"workflows", "runners"},
	}, ClusterCompatibilityRequirements{
		ProtocolVersion:      1,
		SchemaVersion:        "2.20.23",
		RequiredCapabilities: []string{"workflows"},
	})
	if compatible.State != ClusterNodeCompatible || !compatible.Ready {
		t.Fatalf("expected compatible ready node, got %#v", compatible)
	}

	incompatible := EvaluateClusterNodeCompatibility(ClusterNodeRegistration{
		ProtocolVersion: 1,
		SchemaVersion:   "2.20.22",
		Capabilities:    []string{"workflows"},
	}, ClusterCompatibilityRequirements{ProtocolVersion: 1, SchemaVersion: "2.20.23"})
	if incompatible.State != ClusterNodeIncompatibleSchema || incompatible.Ready {
		t.Fatalf("expected incompatible non-ready node, got %#v", incompatible)
	}
}

func TestScheduleOccurrenceKeyBindsRevisionAndUTCInstant(t *testing.T) {
	at := time.Date(2026, 8, 29, 12, 34, 56, 0, time.UTC)
	first, err := ScheduleOccurrenceKey(42, "revision-a", at)
	if err != nil {
		t.Fatal(err)
	}
	sameInstant, err := ScheduleOccurrenceKey(42, "revision-a", at.In(time.FixedZone("offset", 2*60*60)))
	if err != nil {
		t.Fatal(err)
	}
	if first != sameInstant {
		t.Fatalf("expected UTC-normalized occurrence key, got %q and %q", first, sameInstant)
	}
	changedRevision, err := ScheduleOccurrenceKey(42, "revision-b", at)
	if err != nil {
		t.Fatal(err)
	}
	if changedRevision == first {
		t.Fatal("expected revision change to create a distinct occurrence key")
	}
}
