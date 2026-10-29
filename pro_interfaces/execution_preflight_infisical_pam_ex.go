package pro_interfaces

// ExecutionReferenceSecretStorage names a project secret storage the task
// uses at run time, such as an Infisical storage that routes SSH hosts.
const ExecutionReferenceSecretStorage ExecutionPreflightReferenceKind = "secret_storage"

// ExecutionReasonInfisicalPAMInvalid reports Infisical PAM routing that the
// task could not apply, so it would fail before running.
const ExecutionReasonInfisicalPAMInvalid ExecutionPreflightReasonCode = "infisical_pam_invalid"

// ExecutionReasonInfisicalPAMSkipped reports Infisical PAM routes that a
// container executor does not apply.
const ExecutionReasonInfisicalPAMSkipped ExecutionPreflightReasonCode = "infisical_pam_skipped"
