package db

// UpstreamProjectPermissions translates an upstream backup role mask without
// interpreting overlapping EX bit positions as the same permissions.
func UpstreamProjectPermissions(mask ProjectUserPermission, version int) ProjectUserPermission {
	const upstreamRun, upstreamEdit, upstreamApprove, upstreamRead = 16, 32, 64, 128
	result := mask & (CanRunProjectTasks | CanUpdateProject | CanManageProjectResources | CanManageProjectUsers)
	if version < 3 {
		if mask&1 != 0 {
			mask |= upstreamRun | upstreamApprove
		}
		if mask&4 != 0 {
			mask |= upstreamEdit
		}
	}
	if version < 4 {
		mask |= upstreamRead
	}
	if mask&upstreamRun != 0 {
		result |= CanStartWorkflows
	}
	if mask&upstreamEdit != 0 {
		result |= CanEditWorkflows
	}
	if mask&upstreamApprove != 0 {
		result |= CanApproveWorkflows
	}
	if mask&upstreamRead != 0 {
		result |= CanReadProject | CanViewProjectResources | CanViewWorkflows
	}
	return result
}

// UpstreamTemplatePermissions translates the independent template action mask.
func UpstreamTemplatePermissions(mask ProjectUserPermission, version int) TemplatePermission {
	var result TemplatePermission
	if mask&1 != 0 {
		result |= CanRunTemplate
	}
	if mask&4 != 0 {
		result |= CanEditTemplate | CanDeleteTemplate
	}
	if mask&128 != 0 || (version < 4 && mask != 0) {
		result |= CanReadTemplate
	}
	return result
}

// UpstreamWorkflowPermissions translates an upstream workflow grant mask.
func UpstreamWorkflowPermissions(mask ProjectUserPermission, version int) ProjectUserPermission {
	var result ProjectUserPermission
	if mask&16 != 0 {
		result |= CanStartWorkflows
	}
	if mask&32 != 0 {
		result |= CanEditWorkflows
	}
	if mask&64 != 0 {
		result |= CanApproveWorkflows
	}
	if mask&128 != 0 || (version < 4 && mask != 0) {
		result |= CanReadProject
	}
	return result
}
