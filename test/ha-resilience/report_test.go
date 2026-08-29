package haresilience

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestRandomBase64ProducesAES256Key(t *testing.T) {
	encoded, err := randomBase64(32)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("generated key is not base64: %v", err)
	}
	if len(decoded) != 32 {
		t.Fatalf("decoded key length = %d, want 32", len(decoded))
	}
}

func TestReportValidationRequiresEveryNamedFaultAndPassingAssertion(t *testing.T) {
	report := Report{Result: "passed", Invariants: InvariantSummary{AuditContinuous: true, AcceptedWrites: 1}}
	for _, name := range RequiredScenarioNames() {
		report.Scenarios = append(report.Scenarios, Scenario{
			Name: name, Assertions: []Assertion{{Name: "invariant", Passed: true}},
		})
	}
	if err := report.Validate(); err != nil {
		t.Fatalf("complete report rejected: %v", err)
	}

	report.Scenarios[0].Assertions[0].Passed = false
	if err := report.Validate(); err == nil {
		t.Fatal("failed assertion did not fail the report")
	}
	report.Scenarios[0].Assertions[0].Passed = true
	report.Scenarios = report.Scenarios[:len(report.Scenarios)-1]
	if err := report.Validate(); err == nil {
		t.Fatal("missing named fault did not fail the report")
	}
}

func TestReportJSONRetainsMachineReadableSkewAndInvariantEvidence(t *testing.T) {
	report := Report{
		SchemaVersion: 1,
		UpgradePolicy: UpgradePolicy{
			Edition: "same", Protocol: "exact", Schema: "exact", Capabilities: "required subset",
			ApplicationVersion: "may differ", Build: "may differ",
		},
		Invariants: InvariantSummary{DuplicateSchedules: 0, DuplicateTasks: 0, DuplicateWorkflowNodes: 0, DuplicateApprovalDecisions: 0, AuditContinuous: true},
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["schema_version"] != float64(1) {
		t.Fatalf("schema version missing from report: %s", encoded)
	}
	if decoded["upgrade_policy"].(map[string]any)["application_version"] != "may differ" {
		t.Fatalf("upgrade policy missing from report: %s", encoded)
	}
}
