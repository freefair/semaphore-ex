package haresilience

import "testing"

func TestParseRecoveryAssignment(t *testing.T) {
	assignment, err := parseRecoveryAssignment("42|server-b|0123456789abcdef0123456789abcdef\n")
	if err != nil {
		t.Fatal(err)
	}
	if assignment.TaskID != 42 || assignment.OwnerNodeID != "server-b" || assignment.OwnerBootID != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("unexpected assignment: %+v", assignment)
	}

	for _, invalid := range []string{"", "42", "zero|server-a|0123456789abcdef0123456789abcdef", "42|server-c|0123456789abcdef0123456789abcdef", "42|server-a|not-a-boot-id"} {
		if _, err := parseRecoveryAssignment(invalid); err == nil {
			t.Fatalf("invalid assignment %q was accepted", invalid)
		}
	}
}
