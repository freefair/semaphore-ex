package tasks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func decodeProviderTaskStartWire(t *testing.T, filename string) db.Task {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("testdata", filename))
	require.NoError(t, err)

	var task db.Task
	require.NoError(t, json.Unmarshal(payload, &task))
	return task
}

func TestProviderAnsibleTaskStartWireBuildsPermittedAndSuppressedCommands(t *testing.T) {
	task := decodeProviderTaskStartWire(t, "task-start-ansible-wire.json")
	require.NotNil(t, task.BuildTaskID)
	assert.Equal(t, 8, *task.BuildTaskID)
	require.NotNil(t, task.CommitHash)
	assert.Equal(t, "deadbeef", *task.CommitHash)
	require.NotNil(t, task.Version)
	assert.Equal(t, "requested-version", *task.Version)

	for _, tt := range []struct {
		name           string
		templateParams db.MapStringAnyField
		expected       []string
		notExpected    []string
	}{
		{
			name: "permitted task controls",
			templateParams: db.MapStringAnyField{
				"allow_debug": true, "allow_override_limit": true, "allow_override_tags": true,
			},
			expected:    []string{"-vvv", "--diff", "--check", "--limit=web", "--tags=deploy"},
			notExpected: []string{"--limit=template-host", "--tags=template-tag"},
		},
		{
			name: "template suppresses task controls",
			templateParams: db.MapStringAnyField{
				"hide_dry_run": true, "hide_diff": true,
				"limit": []string{"template-host"}, "tags": []string{"template-tag"},
			},
			expected:    []string{"--limit=template-host", "--tags=template-tag"},
			notExpected: []string{"-vvv", "--diff", "--check", "--limit=web", "--tags=deploy"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			setupExecutorConfig(t)
			executor := LocalExecutor{
				Task: task,
				Template: db.Template{
					App: db.AppAnsible, Playbook: "site.yml", TaskParams: tt.templateParams,
				},
				Inventory: db.Inventory{Type: db.InventoryStatic},
			}

			args, _, err := executor.getPlaybookArgs("operator", nil)
			require.NoError(t, err)
			for _, expected := range tt.expected {
				assert.Contains(t, args, expected)
			}
			for _, unexpected := range tt.notExpected {
				assert.NotContains(t, args, unexpected)
			}
		})
	}
}

func TestProviderTerraformTaskStartWireBuildsPlanDestroyAndApprovalMetadata(t *testing.T) {
	setupExecutorConfig(t)
	task := decodeProviderTaskStartWire(t, "task-start-terraform-wire.json")
	require.NotNil(t, task.BuildTaskID)
	assert.Equal(t, 8, *task.BuildTaskID)
	require.NotNil(t, task.CommitHash)
	assert.Equal(t, "deadbeef", *task.CommitHash)
	require.NotNil(t, task.Version)
	assert.Equal(t, "requested-version", *task.Version)

	executor := LocalExecutor{
		Task:     task,
		Template: db.Template{App: db.AppTerraform, Playbook: "infra"},
	}
	args, err := executor.getTerraformArgs("operator", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"-destroy"}, args["default"])

	var params db.TerraformTaskParams
	require.NoError(t, task.ExtractParams(&params))
	assert.Equal(t, db.TerraformTaskParams{
		Plan: true, Destroy: true, AutoApprove: true, Upgrade: true, Reconfigure: true,
	}, params)

	executor.preparedParams = &params
	executor.preparedTplParams = &db.TerraformTemplateParams{AllowAutoApprove: true}
	bootstrap, plan, err := executor.containerTerraformBootstrap(args)
	require.NoError(t, err)
	assert.True(t, plan.PlanOnly)
	assert.True(t, plan.AutoApprove)
	assert.Contains(t, bootstrap, "'terraform' 'init' '-lock=false' '-input=false' '-upgrade' '-reconfigure'")
	assert.NotContains(t, bootstrap, "-migrate-state")

	planCommand, applyCommand := executor.containerTerraformCommands(args)
	assert.Equal(t, []string{"terraform", "plan", "-lock=false", "-detailed-exitcode", "-input=false", "-destroy"}, planCommand)
	assert.Equal(t, []string{"terraform", "apply", "-auto-approve", "-lock=false", "-input=false", "-destroy"}, applyCommand)

	executor.preparedTplParams = &db.TerraformTemplateParams{}
	_, deniedApproval, err := executor.containerTerraformBootstrap(args)
	require.NoError(t, err)
	assert.True(t, deniedApproval.PlanOnly)
	assert.False(t, deniedApproval.AutoApprove)
}
