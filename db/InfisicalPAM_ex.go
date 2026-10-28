package db

// SecretStorageTypeInfisical routes task SSH connections through Infisical
// PAM. It is not a key-value source: access keys and environments cannot
// reference it.
const SecretStorageTypeInfisical SecretStorageType = "infisical"

// InfisicalPAMRoute sends SSH connections for one host through one Infisical
// PAM account (folder/account).
type InfisicalPAMRoute struct {
	Host    string `json:"host"`
	Account string `json:"account"`
}

// TaskInfisicalPAM is the runtime configuration of one Infisical storage for
// one task. It carries no secret in JSON: the machine identity secret travels
// as access key CredentialKeyID through the runner's access key channel and
// is hydrated into Credential on the executing node.
type TaskInfisicalPAM struct {
	StorageID        int                 `json:"storage_id"`
	SiteURL          string              `json:"site_url"`
	ClientID         string              `json:"client_id"`
	OrganizationSlug string              `json:"organization_slug,omitempty"`
	CACertificate    string              `json:"ca_certificate,omitempty"`
	SessionDuration  string              `json:"session_duration"`
	Routes           []InfisicalPAMRoute `json:"routes"`
	CredentialKeyID  int                 `json:"credential_key_id"`
	Credential       AccessKey           `json:"-"`
}
