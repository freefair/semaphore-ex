package pro_interfaces

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestRequiredQueryParametersHaveDreddExamples(t *testing.T) {
	source, err := os.ReadFile("../api-docs.yml")
	require.NoError(t, err)

	var document any
	require.NoError(t, yaml.Unmarshal(source, &document))

	var missing []string
	visitYAML(document, func(value map[string]any) {
		if value["in"] != "query" || value["required"] != true {
			return
		}
		if _, ok := value["x-example"]; ok {
			return
		}
		if _, ok := value["default"]; ok {
			return
		}
		if values, ok := value["enum"].([]any); ok && len(values) > 0 {
			return
		}
		name, _ := value["name"].(string)
		missing = append(missing, name)
	})

	assert.Empty(t, missing, "required query parameters need x-example, default, or enum for Dredd")
}

func visitYAML(value any, visit func(map[string]any)) {
	switch typed := value.(type) {
	case map[string]any:
		visit(typed)
		for _, child := range typed {
			visitYAML(child, visit)
		}
	case []any:
		for _, child := range typed {
			visitYAML(child, visit)
		}
	}
}
