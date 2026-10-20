package pro_interfaces

type Features struct {
	ProjectRunners            bool `json:"project_runners"`
	TerraformBackend          bool `json:"terraform_backend"`
	TaskSummary               bool `json:"task_summary"`
	SecretStorages            bool `json:"secret_storages"`
	SecretStorageManagement   bool `json:"secret_storage_management"`
	SecretStorageManagementEx bool `json:"secret_storage_management_ex"`
	CustomRolesManagement     bool `json:"custom_roles_management"`
	// GlobalRolesManagement enables instance-level roles with global permissions and their assignment to users.
	GlobalRolesManagement bool `json:"global_roles_management"`
	HighAvailability      bool `json:"high_availability"`
	Workflows             bool `json:"workflows"`
	// WorkflowIntegrationsManagement describes support for integrations that start workflows instead of templates.
	WorkflowIntegrationsManagement bool `json:"workflow_integrations_management"`
	DockerExecutor                 bool `json:"docker_executor"`
	K8sExecutor                    bool `json:"k8s_executor"`
	AuditLogFilters                bool `json:"audit_log_filters"`
}
