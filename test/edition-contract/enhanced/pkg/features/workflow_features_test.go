package features

import "testing"

func TestWorkflowEditorFeatureIsAvailableInEnhancedEdition(t *testing.T) {
	if !GetFeatures().Workflows {
		t.Fatal("enhanced edition must advertise the workflow editor")
	}
}
