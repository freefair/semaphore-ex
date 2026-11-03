package db

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUpstreamPermissionTranslationDoesNotCollideWithEXBits(t *testing.T) {
	tests := []struct {
		name    string
		mask    ProjectUserPermission
		version int
		want    ProjectUserPermission
	}{
		{"legacy run retains upstream defaults", 1, 2, CanRunProjectTasks | CanStartWorkflows | CanApproveWorkflows | CanReadProject | CanViewProjectResources | CanViewWorkflows},
		{"upstream run is not EX resource visibility", 16, 4, CanStartWorkflows},
		{"upstream edit is not EX workflow view", 32, 4, CanEditWorkflows},
		{"upstream approve is not EX workflow edit", 64, 4, CanApproveWorkflows},
		{"upstream read is not EX workflow start", 128, 4, CanReadProject | CanViewProjectResources | CanViewWorkflows},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) { assert.Equal(t, test.want, UpstreamProjectPermissions(test.mask, test.version)) })
	}
	assert.Equal(t, CanReadTemplate|CanRunTemplate|CanEditTemplate|CanDeleteTemplate, UpstreamTemplatePermissions(1|4, 3))
	assert.Equal(t, CanStartWorkflows|CanEditWorkflows|CanApproveWorkflows|CanReadProject, UpstreamWorkflowPermissions(16|32|64|128, 4))
	assert.Zero(t, (CanViewProjectResources|CanViewWorkflows|CanEditWorkflows|CanStartWorkflows)&(CanReadProject|CanApproveWorkflows))
}
