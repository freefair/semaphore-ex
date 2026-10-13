package projects

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWorkflowPublicOutputsProjectsOnlyValidatedPublicEnvelope(t *testing.T) {
	valid := `{"values":{"id":9007199254740993,"semaphore_workflow_outputs":{"token":"private"}},"skipped":{"name":"sensitive","semaphore_workflow_outputs":"private"},"unknown":"hidden"}`
	projected := workflowPublicOutputs(&valid)
	if assert.NotNil(t, projected) {
		assert.JSONEq(t, `{"values":{"id":9007199254740993},"skipped":{"name":"sensitive"}}`, *projected)
		assert.Contains(t, *projected, `"id":9007199254740993`, "large integer tokens must survive without float64 rounding")
		assert.NotContains(t, *projected, "private")
		assert.NotContains(t, *projected, "unknown")
	}
	valuesOnly := `{"values":{"image_tag":"v1.2.3","replicas":3}}`
	projected = workflowPublicOutputs(&valuesOnly)
	if assert.NotNil(t, projected) {
		assert.JSONEq(t, valuesOnly, *projected)
	}
	for _, input := range []*string{nil, stringPointer(`not json`), stringPointer(`{"values":{"bad name":1}}`), stringPointer(`{"values":{"large":"` + strings.Repeat("x", 32*1024+1) + `"}}`), stringPointer(`{"skipped":{"x":"unknown"}}`), stringPointer(`{"values":{"x":1},"padding":"` + strings.Repeat("x", 256*1024) + `"}`)} {
		assert.Nil(t, workflowPublicOutputs(input))
	}
}

func stringPointer(value string) *string { return &value }
