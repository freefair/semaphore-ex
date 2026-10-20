package features

import "testing"

func TestWorkflowEditorFeatureIsAvailableInEnhancedEdition(t *testing.T) {
	if !GetFeatures().Workflows {
		t.Fatal("enhanced edition must advertise the workflow editor")
	}
}

func TestFeaturesAdvertiseImplementedGlobalRolesAndWorkflowIntegrations(t *testing.T) {
	features := GetFeatures()
	if !features.GlobalRolesManagement {
		t.Fatal("enhanced edition must advertise implemented global role management")
	}
	if !features.WorkflowIntegrationsManagement {
		t.Fatal("enhanced edition must advertise implemented workflow integrations")
	}
}
