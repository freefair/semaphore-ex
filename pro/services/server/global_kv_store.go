package server

import (
	"context"
	"errors"

	"github.com/semaphoreui/semaphore/pro_interfaces"
)

// unavailableGlobalKVStore keeps the global Vault/OpenBao seam in Community
// builds and fails closed.
type unavailableGlobalKVStore struct{}

func errGlobalKVUnavailable() error {
	return errors.New("global Vault/OpenBao connections are unavailable")
}

func NewGlobalKVStore() pro_interfaces.GlobalKVStore {
	return &unavailableGlobalKVStore{}
}

func (*unavailableGlobalKVStore) ValidateGlobalKV(string, map[string]any) error {
	return errGlobalKVUnavailable()
}

func (*unavailableGlobalKVStore) ReadGlobalKV(context.Context, pro_interfaces.GlobalKVConnection, pro_interfaces.SecretReference) ([]byte, error) {
	return nil, errGlobalKVUnavailable()
}

func (*unavailableGlobalKVStore) GlobalKVIdentity(context.Context, pro_interfaces.GlobalKVConnection) (pro_interfaces.SecretBrowseIdentity, error) {
	return pro_interfaces.SecretBrowseIdentity{}, errGlobalKVUnavailable()
}

func (*unavailableGlobalKVStore) BrowseGlobalKV(context.Context, pro_interfaces.GlobalKVConnection, pro_interfaces.SecretBrowseLocation) ([]pro_interfaces.SecretBrowseNode, error) {
	return nil, errGlobalKVUnavailable()
}
