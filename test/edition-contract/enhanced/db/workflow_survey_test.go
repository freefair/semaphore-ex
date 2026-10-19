package db

import (
	"testing"

	coreDB "github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveWorkflowSurveyEnvironment(t *testing.T) {
	vars := []coreDB.SurveyVar{
		{Name: "name", Type: coreDB.SurveyVarStr, Required: true},
		{Name: "count", Type: coreDB.SurveyVarInt},
		{Name: "tier", Type: coreDB.SurveyVarEnum, Values: []coreDB.SurveyVarEnumValue{{Value: "prod"}}},
		{Name: "zones", Type: coreDB.SurveyVarSelect, Values: []coreDB.SurveyVarEnumValue{{Value: "eu"}}},
	}
	env, err := resolveWorkflowSurveyEnvironment(vars, coreDB.MapStringAnyField{
		"name": "release", "count": float64(2), "tier": "prod", "zones": []any{"eu"},
	})
	require.NoError(t, err)
	assert.Equal(t, "release", env["name"])
	assert.Equal(t, float64(2), env["count"])
	assert.Equal(t, []any{"eu"}, env["zones"])

	for _, input := range []coreDB.MapStringAnyField{
		{"unknown": "x", "name": "release"}, {"name": 3}, {"name": "release", "count": 1.5},
		{"name": "release", "tier": "dev"}, {"name": "release", "zones": "eu"},
	} {
		_, err = resolveWorkflowSurveyEnvironment(vars, input)
		assert.Error(t, err)
	}
	_, err = resolveWorkflowSurveyEnvironment(vars, coreDB.MapStringAnyField{})
	assert.Error(t, err)
}

func TestResolveWorkflowSurveyEnvironmentCopiesInput(t *testing.T) {
	input := coreDB.MapStringAnyField{"zones": []any{"eu"}}
	env, err := resolveWorkflowSurveyEnvironment([]coreDB.SurveyVar{{Name: "zones", Type: coreDB.SurveyVarSelect, Values: []coreDB.SurveyVarEnumValue{{Value: "eu"}}}}, input)
	require.NoError(t, err)
	input["zones"].([]any)[0] = "changed"
	assert.Equal(t, []any{"eu"}, env["zones"])
}

func TestResolveWorkflowSurveyEnvironmentSelectDefaultsAndRequired(t *testing.T) {
	defaultValue := &coreDB.SurveyVarDefaultValue{Values: []string{"eu"}}
	vars := []coreDB.SurveyVar{{Name: "zones", Type: coreDB.SurveyVarSelect, Required: true, Values: []coreDB.SurveyVarEnumValue{{Value: "eu"}}, DefaultValue: defaultValue}}
	env, err := resolveWorkflowSurveyEnvironment(vars, coreDB.MapStringAnyField{})
	require.NoError(t, err)
	assert.Equal(t, []any{"eu"}, env["zones"])
	_, err = resolveWorkflowSurveyEnvironment([]coreDB.SurveyVar{{Name: "zones", Type: coreDB.SurveyVarSelect, Required: true, Values: []coreDB.SurveyVarEnumValue{{Value: "eu"}}}}, coreDB.MapStringAnyField{"zones": []any{}})
	assert.Error(t, err)
}

func TestValidateWorkflowTemplateRejectsInvalidSurveyBindings(t *testing.T) {
	store := validWorkflowStore()
	store.templates[11] = coreDB.Template{ID: 11, ProjectID: 7, SurveyVars: []coreDB.SurveyVar{{Name: "target"}, {Name: "secret", Type: coreDB.SurveyVarSecret}}}
	base := validWorkflow()
	base.SurveyVars = []coreDB.SurveyVar{{Name: "source"}}
	copyWorkflow := func() coreDB.WorkflowTemplate {
		clone := base
		clone.Nodes = append([]coreDB.WorkflowNode(nil), base.Nodes...)
		return clone
	}
	cases := []coreDB.WorkflowTemplate{
		func() coreDB.WorkflowTemplate {
			w := copyWorkflow()
			w.Nodes[0].SurveyInputs = []coreDB.WorkflowNodeSurveyInput{{Var: "missing", WorkflowVar: "source"}}
			return w
		}(),
		func() coreDB.WorkflowTemplate {
			w := copyWorkflow()
			w.Nodes[0].SurveyInputs = []coreDB.WorkflowNodeSurveyInput{{Var: "secret", WorkflowVar: "source"}}
			return w
		}(),
		func() coreDB.WorkflowTemplate {
			w := copyWorkflow()
			w.Nodes[0].SurveyInputs = []coreDB.WorkflowNodeSurveyInput{{Var: "target", WorkflowVar: "missing"}}
			return w
		}(),
		func() coreDB.WorkflowTemplate {
			w := copyWorkflow()
			w.Nodes[0].SurveyInputs = []coreDB.WorkflowNodeSurveyInput{{Var: "target", WorkflowVar: "source"}, {Var: "target", WorkflowVar: "source"}}
			return w
		}(),
		func() coreDB.WorkflowTemplate {
			w := copyWorkflow()
			w.Nodes[1].SurveyInputs = []coreDB.WorkflowNodeSurveyInput{{Var: "target", WorkflowVar: "source"}}
			return w
		}(),
		func() coreDB.WorkflowTemplate {
			w := copyWorkflow()
			w.Nodes[0].CrossProjectTemplateReference = &coreDB.CrossProjectTemplateReference{GrantID: 1, TemplateID: 11, TemplateVersionNumber: 1}
			w.Nodes[0].SurveyInputs = []coreDB.WorkflowNodeSurveyInput{{Var: "target", WorkflowVar: "source"}}
			return w
		}(),
	}
	for _, workflow := range cases {
		result, err := ValidateWorkflowTemplate(store, workflow)
		require.NoError(t, err)
		assert.False(t, result.Valid)
	}
	valid := copyWorkflow()
	valid.Nodes[0].SurveyInputs = []coreDB.WorkflowNodeSurveyInput{{Var: "target", WorkflowVar: "source"}}
	result, err := ValidateWorkflowTemplate(store, valid)
	require.NoError(t, err)
	assert.True(t, result.Valid)
}
