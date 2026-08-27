package server

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type VaultStorageTokenDeserializer interface {
	DeserializeSecret(key *db.AccessKey) error
}

type VaultAccessKeyDeserializer struct {
	capabilityProvider pro_interfaces.CapabilityProvider
}

func NewVaultAccessKeyDeserializer(
	_ db.AccessKeyManager,
	_ db.SecretStorageRepository,
	_ VaultStorageTokenDeserializer,
	providers ...pro_interfaces.CapabilityProvider,
) *VaultAccessKeyDeserializer {
	var provider pro_interfaces.CapabilityProvider
	if len(providers) > 0 {
		provider = providers[0]
	}
	return &VaultAccessKeyDeserializer{capabilityProvider: provider}
}

func (d *VaultAccessKeyDeserializer) DeleteSecret(key *db.AccessKey) error {
	return nil
}

func (d *VaultAccessKeyDeserializer) SerializeSecret(key *db.AccessKey) (err error) {
	return
}

func (d *VaultAccessKeyDeserializer) DeserializeSecret(key *db.AccessKey) (res string, err error) {
	return "", d.denied(pro_interfaces.CapabilityAccessExecute)
}

var _ pro_interfaces.RuntimeSecretResolver = (*VaultAccessKeyDeserializer)(nil)
var _ pro_interfaces.ManagedSecretProvider = (*VaultAccessKeyDeserializer)(nil)
