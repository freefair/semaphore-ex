package server

import (
	"context"
	"errors"
	"hash/fnv"
	"os"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
)

type globalCredentialExternalAdapter struct {
	providers map[string]globalCredentialExternalProvider
	lookup    func(string) (string, bool)
	client    pro_interfaces.VaultOpenBaoClient
	invalid   bool
}

type globalCredentialExternalProvider struct {
	configuration pro_interfaces.SecretProviderConfiguration
	envName       string
}

// NewGlobalCredentialExternalAdapter builds an independent global provider
// registry. It intentionally does not accept a SecretStorage repository or a
// project ID: provider authority is global and bootstrap material is obtained
// only from each provider's derived process environment variable.
func NewGlobalCredentialExternalAdapter(
	configuration *util.ConfigType,
	lookups ...func(string) (string, bool),
) pro_interfaces.GlobalCredentialExternalAdapter {
	lookup := os.LookupEnv
	if len(lookups) > 0 && lookups[0] != nil {
		lookup = lookups[0]
	}
	adapter := &globalCredentialExternalAdapter{
		providers: make(map[string]globalCredentialExternalProvider), lookup: lookup, client: newVaultOpenBaoClient(),
	}
	if configuration == nil {
		adapter.invalid = true
		return adapter
	}
	seenEnv := make(map[string]struct{}, len(configuration.GlobalCredentialProviders))
	seenStorageID := make(map[int]struct{}, len(configuration.GlobalCredentialProviders))
	for providerID, provider := range configuration.GlobalCredentialProviders {
		envName, err := util.GlobalCredentialProviderCredentialEnv(providerID)
		if err != nil {
			adapter.invalid = true
			continue
		}
		if _, duplicate := seenEnv[envName]; duplicate {
			adapter.invalid = true
			continue
		}
		seenEnv[envName] = struct{}{}
		storageID := globalCredentialProviderStorageID(providerID)
		if _, duplicate := seenStorageID[storageID]; duplicate {
			adapter.invalid = true
			continue
		}
		seenStorageID[storageID] = struct{}{}
		parsed, parseErr := globalCredentialProviderConfiguration(providerID, storageID, provider)
		if parseErr != nil {
			adapter.invalid = true
			continue
		}
		adapter.providers[providerID] = globalCredentialExternalProvider{configuration: parsed, envName: envName}
	}
	return adapter
}

func (a *globalCredentialExternalAdapter) ResolveGlobalCredentialExternal(
	ctx context.Context,
	reference db.GlobalCredentialExternalReference,
) (pro_interfaces.GlobalCredentialExternalResult, error) {
	if a == nil || a.invalid || a.client == nil || reference.Validate() != nil {
		return pro_interfaces.GlobalCredentialExternalResult{}, errors.New("global credential provider unavailable")
	}
	provider, exists := a.providers[reference.ProviderID]
	if !exists || string(provider.configuration.Type) != reference.Provider {
		return pro_interfaces.GlobalCredentialExternalResult{}, errors.New("global credential provider unavailable")
	}
	bootstrap, exists := a.lookup(provider.envName)
	if !exists || bootstrap == "" {
		return pro_interfaces.GlobalCredentialExternalResult{}, errors.New("global credential provider unavailable")
	}
	credential := []byte(bootstrap)
	defer zero(credential)
	configuration := provider.configuration
	configuration.Auth.BootstrapCredential = credential
	value, err := a.client.ReadKV(ctx, configuration, pro_interfaces.SecretReference{
		StorageID: configuration.StorageID, Mount: reference.Mount, Path: reference.Path,
		Version: reference.Version, Field: reference.Field,
	})
	if err != nil || len(value) == 0 {
		zero(value)
		return pro_interfaces.GlobalCredentialExternalResult{}, errors.New("global credential provider unavailable")
	}
	return pro_interfaces.GlobalCredentialExternalResult{Value: value, ProviderVersion: reference.Version}, nil
}

func globalCredentialProviderStorageID(providerID string) int {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte("semaphore-global-credential-provider:v1\x00" + providerID))
	return int(hash.Sum32()&0x3fffffff) + 1
}

func globalCredentialProviderConfiguration(
	providerID string,
	storageID int,
	provider util.GlobalCredentialProviderConfig,
) (pro_interfaces.SecretProviderConfiguration, error) {
	providerType := pro_interfaces.SecretProviderType(provider.Type)
	if storageID <= 0 || (providerType != pro_interfaces.SecretProviderVault && providerType != pro_interfaces.SecretProviderOpenBao) ||
		validateProviderAddress(provider.URL) != nil || strings.ContainsAny(provider.Namespace, "\r\n") {
		return pro_interfaces.SecretProviderConfiguration{}, errors.New("global credential provider configuration is invalid")
	}
	timeout := defaultProviderTimeout
	if provider.Timeout != "" {
		parsed, err := time.ParseDuration(provider.Timeout)
		if err != nil || parsed <= 0 || parsed > maximumProviderTimeout {
			return pro_interfaces.SecretProviderConfiguration{}, errors.New("global credential provider configuration is invalid")
		}
		timeout = parsed
	}
	maxResponse := provider.MaxResponseBytes
	if maxResponse == 0 {
		maxResponse = defaultProviderResponseSize
	}
	if maxResponse < 1 || maxResponse > maximumProviderResponseSize {
		return pro_interfaces.SecretProviderConfiguration{}, errors.New("global credential provider configuration is invalid")
	}
	authMethod := pro_interfaces.SecretProviderAuthMethod(provider.AuthMethod)
	if authMethod == "" {
		authMethod = pro_interfaces.SecretProviderAuthToken
	}
	authMount := provider.AuthMount
	switch authMethod {
	case pro_interfaces.SecretProviderAuthToken:
	case pro_interfaces.SecretProviderAuthAppRole:
		if authMount == "" {
			authMount = "approle"
		}
		if provider.RoleID == "" {
			return pro_interfaces.SecretProviderConfiguration{}, errors.New("global credential provider configuration is invalid")
		}
	case pro_interfaces.SecretProviderAuthKubernetes:
		if authMount == "" {
			authMount = "kubernetes"
		}
		if provider.Role == "" {
			return pro_interfaces.SecretProviderConfiguration{}, errors.New("global credential provider configuration is invalid")
		}
	default:
		return pro_interfaces.SecretProviderConfiguration{}, errors.New("global credential provider configuration is invalid")
	}
	if authMount != "" && (!safeURLSegment(authMount) || strings.ContainsAny(authMount, "\x00\r\n")) ||
		!safeGlobalCredentialAuthValue(provider.RoleID) || !safeGlobalCredentialAuthValue(provider.Role) {
		return pro_interfaces.SecretProviderConfiguration{}, errors.New("global credential provider configuration is invalid")
	}
	return pro_interfaces.SecretProviderConfiguration{
		StorageID: storageID, ProjectID: 0, Type: providerType, Address: provider.URL, Namespace: provider.Namespace,
		CACertificate: []byte(provider.CACertificate), Timeout: timeout, MaxResponseSize: maxResponse,
		Auth: pro_interfaces.SecretProviderAuth{Method: authMethod, Mount: authMount, RoleID: provider.RoleID, Role: provider.Role},
	}, nil
}

// Role metadata is carried only in a JSON login body, not a URL or header, so
// path punctuation remains valid. Bound and reject controls nonetheless to
// avoid oversized/config-injection request bodies.
func safeGlobalCredentialAuthValue(value string) bool {
	return len(value) <= 1024 && !strings.ContainsAny(value, "\x00\r\n")
}

var _ pro_interfaces.GlobalCredentialExternalAdapter = (*globalCredentialExternalAdapter)(nil)
