package server

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

const maximumRuntimeSecretProviderTimeout = 30 * time.Second

func normalizeRuntimeSecretReference(key *db.AccessKey, storageRepo db.SecretStorageRepository) error {
	if key.SourceStorageType == nil || *key.SourceStorageType != db.AccessKeySourceStorageVault {
		return nil
	}
	if key.SourceStorageID == nil || key.SourceStorageKey == nil {
		return common_errors.NewValidationError("runtime secret storage and path are required")
	}
	if key.ProjectID == nil || storageRepo == nil {
		return common_errors.NewValidationError("runtime secret project and storage are required")
	}
	storage, err := storageRepo.GetSecretStorage(*key.ProjectID, *key.SourceStorageID)
	if err != nil || storage.ProjectID != *key.ProjectID ||
		(storage.Type != db.SecretStorageTypeVault && storage.Type != db.SecretStorageTypeOpenBao) {
		return common_errors.NewValidationError("runtime secret storage is invalid")
	}
	if decoded, err := pro_interfaces.DecodeSecretReference(*key.SourceStorageKey); err == nil {
		if decoded.StorageID != *key.SourceStorageID {
			return common_errors.NewValidationError("runtime secret storage does not match the reference")
		}
		return nil
	}
	mount := key.SourceStorageMount
	if mount == "" {
		mount = runtimeSecretStringParam(storage.Params, "mount")
	}
	if mount == "" {
		mount = "secret"
	}
	reference := pro_interfaces.SecretReference{
		StorageID: *key.SourceStorageID,
		Mount:     mount,
		Path:      *key.SourceStorageKey,
		Version:   key.SourceStorageVersion,
		Field:     key.SourceStorageField,
	}
	encoded, err := reference.Encode()
	if err != nil {
		return common_errors.NewValidationError(err.Error())
	}
	key.SourceStorageKey = &encoded
	key.String = ""
	key.LoginPassword = db.LoginPassword{}
	key.SshKey = db.SshKey{}
	key.Secret = nil
	return nil
}

// ExposeRuntimeSecretReference expands canonical metadata for API/UI use. It
// never resolves or adds a plaintext value.
func ExposeRuntimeSecretReference(key *db.AccessKey) {
	if key == nil || key.SourceStorageType == nil || *key.SourceStorageType != db.AccessKeySourceStorageVault ||
		key.SourceStorageKey == nil {
		return
	}
	reference, err := pro_interfaces.DecodeSecretReference(*key.SourceStorageKey)
	if err != nil {
		key.SourceStorageKey = nil
		return
	}
	key.SourceStorageID = &reference.StorageID
	key.SourceStorageMount = reference.Mount
	key.SourceStorageKey = &reference.Path
	key.SourceStorageVersion = reference.Version
	key.SourceStorageField = reference.Field
}

func ValidateRuntimeSecretStorage(storage *db.SecretStorage) error {
	if storage == nil {
		return common_errors.NewValidationError("secret storage is required")
	}
	if storage.Type != db.SecretStorageTypeVault && storage.Type != db.SecretStorageTypeOpenBao {
		return common_errors.NewValidationError("only Vault and OpenBao runtime secret providers are supported")
	}
	if storage.Params == nil {
		storage.Params = db.MapStringAnyField{}
	}
	address := runtimeSecretStringParam(storage.Params, "url")
	parsed, err := url.Parse(address)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return common_errors.NewValidationError("provider URL is invalid")
	}
	if parsed.Scheme != "https" {
		host := parsed.Hostname()
		ip := net.ParseIP(host)
		if parsed.Scheme != "http" || (host != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return common_errors.NewValidationError("provider URL must use HTTPS except on loopback")
		}
	}
	if value, ok := storage.Params["tls_skip_verify"].(bool); ok && value {
		return common_errors.NewValidationError("TLS verification cannot be disabled")
	}
	if configured := runtimeSecretStringParam(storage.Params, "timeout"); configured != "" {
		timeout, parseErr := time.ParseDuration(configured)
		if parseErr != nil || timeout <= 0 || timeout > maximumRuntimeSecretProviderTimeout {
			return common_errors.NewValidationError("provider timeout must be between 1ns and 30s")
		}
	}
	authMethod := runtimeSecretStringParam(storage.Params, "auth_method")
	if authMethod == "" {
		authMethod = string(pro_interfaces.SecretProviderAuthToken)
		storage.Params["auth_method"] = authMethod
	}
	switch pro_interfaces.SecretProviderAuthMethod(authMethod) {
	case pro_interfaces.SecretProviderAuthToken:
	case pro_interfaces.SecretProviderAuthAppRole:
		if runtimeSecretStringParam(storage.Params, "role_id") == "" {
			return common_errors.NewValidationError("AppRole role ID is required")
		}
	case pro_interfaces.SecretProviderAuthKubernetes:
		if runtimeSecretStringParam(storage.Params, "role") == "" {
			return common_errors.NewValidationError("Kubernetes role is required")
		}
	default:
		return common_errors.NewValidationError("provider authentication method is invalid")
	}
	if storage.Secret == "" && storage.ID == 0 {
		return common_errors.NewValidationError("provider credential is required")
	}
	storage.ReadOnly = true
	storage.SyncEnabled = false
	storage.SyncPaths = []db.SecretSyncPath{}
	return nil
}

func runtimeSecretStringParam(params db.MapStringAnyField, key string) string {
	value, _ := params[key].(string)
	return strings.TrimSpace(value)
}

func EncodeEnvironmentRuntimeSecretReference(secret db.EnvironmentSecret) (string, error) {
	if secret.StorageID == nil {
		return "", fmt.Errorf("runtime secret storage is required")
	}
	mount := secret.Mount
	if mount == "" {
		mount = "secret"
	}
	return pro_interfaces.SecretReference{
		StorageID: *secret.StorageID,
		Mount:     mount,
		Path:      secret.Path,
		Version:   secret.Version,
		Field:     secret.Field,
	}.Encode()
}
