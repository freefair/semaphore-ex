package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSystemInfoEditionMetadataJSON(t *testing.T) {
	resolvedAt := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	info := SystemInfo{
		Edition:          pro_interfaces.EditionEnhanced,
		ContractVersion:  pro_interfaces.CoreContractVersion,
		Implementation:   "enhanced-revision",
		CoreRevision:     "core-revision",
		EnhancedRevision: "enhanced-revision",
		Capabilities: pro_interfaces.NewCapabilitySnapshot(
			pro_interfaces.CapabilityRequest{UserID: 42, IsAdmin: true, At: resolvedAt},
			[]pro_interfaces.CapabilityDecision{pro_interfaces.NewCapabilityDecision(
				pro_interfaces.CapabilityLifecycleTest,
				pro_interfaces.CapabilityStateActive,
				pro_interfaces.CapabilityReasonActive,
				[]pro_interfaces.CapabilityAccess{pro_interfaces.CapabilityAccessRead},
				nil,
			)},
		),
	}

	encoded, err := json.Marshal(info)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"version":"",
		"ansible":"",
		"web_host":"",
		"use_remote_runner":false,
		"auth_methods":{},
		"login_with_password":false,
		"features":{"project_runners":false,"terraform_backend":false,"task_summary":false,"secret_storages":false,"secret_storage_management":false,"secret_storage_management_ex":false,"custom_roles_management":false,"high_availability":false,"workflows":false,"docker_executor":false,"k8s_executor":false},
		"subscription_state":"",
		"git_client":"",
		"schedule_timezone":"",
		"teams":null,
		"roles":null,
		"boltdb_used":false,
		"jwt":{"enabled":false},
		"edition":"enhanced",
		"contract_version":"1.8.0",
		"implementation_version":"enhanced-revision",
		"core_revision":"core-revision",
		"enhanced_revision":"enhanced-revision",
		"capabilities":{
			"resolved_at":"2026-08-25T10:00:00Z",
			"capabilities":[{
				"id":"lifecycle_test",
				"state":"active",
				"reason":"active",
				"access":["read"],
				"limits":{}
			}]
		}
	}`, string(encoded))
	assert.NotContains(t, string(encoded), "42")
}

func TestSystemInfoOmitsCommunityEnhancedRevision(t *testing.T) {
	encoded, err := json.Marshal(SystemInfo{Edition: pro_interfaces.EditionCommunity})
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "enhanced_revision")
}
