package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pkg/infisicalpam"
	"github.com/semaphoreui/semaphore/pkg/random"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

// GlobalSecretStorageService manages connections configured once for the
// server. Responses never carry the client secret.
type GlobalSecretStorageService interface {
	List() ([]db.GlobalSecretStorage, error)
	Get(storageID int) (db.GlobalSecretStorage, error)
	Create(storage db.GlobalSecretStorage) (db.GlobalSecretStorage, error)
	Update(storage db.GlobalSecretStorage, expectedRevision int) (db.GlobalSecretStorage, error)
	Delete(storageID int) error
}

type globalSecretStorageService struct {
	repo db.GlobalSecretStorageRepository
	keys AccessKeyService
	kv   pro_interfaces.GlobalKVStore
}

// NewGlobalSecretStorageService manages global connections. kv validates
// Vault/OpenBao connections; without it only Infisical can be configured.
func NewGlobalSecretStorageService(repo db.GlobalSecretStorageRepository, keys AccessKeyService, kv pro_interfaces.GlobalKVStore) GlobalSecretStorageService {
	return &globalSecretStorageService{repo: repo, keys: keys, kv: kv}
}

func (s *globalSecretStorageService) List() ([]db.GlobalSecretStorage, error) {
	return s.repo.GetGlobalSecretStorages()
}

func (s *globalSecretStorageService) Get(storageID int) (db.GlobalSecretStorage, error) {
	return s.repo.GetGlobalSecretStorage(storageID)
}

func (s *globalSecretStorageService) Create(storage db.GlobalSecretStorage) (db.GlobalSecretStorage, error) {
	storage.ID = 0
	if err := normalizeGlobalSecretStorage(&storage, true, s.kv); err != nil {
		return db.GlobalSecretStorage{}, err
	}
	key, err := s.createKey(storage)
	if err != nil {
		return db.GlobalSecretStorage{}, err
	}
	storage.CredentialKeyID = &key.ID
	created, err := s.repo.CreateGlobalSecretStorage(storage)
	if err != nil {
		_ = s.repo.DeleteGlobalSecretStorageKey(key.ID)
		return db.GlobalSecretStorage{}, err
	}
	return created, nil
}

// Update replaces the client secret only when a new one is supplied. The new
// key is written before the storage points to it and the old key is removed
// afterwards, so a failed update never leaves the storage without a secret.
func (s *globalSecretStorageService) Update(storage db.GlobalSecretStorage, expectedRevision int) (db.GlobalSecretStorage, error) {
	current, err := s.repo.GetGlobalSecretStorage(storage.ID)
	if err != nil {
		return db.GlobalSecretStorage{}, err
	}
	storage.Type = current.Type
	if err = normalizeGlobalSecretStorage(&storage, false, s.kv); err != nil {
		return db.GlobalSecretStorage{}, err
	}
	storage.CredentialKeyID = current.CredentialKeyID
	var replaced *int
	if storage.Secret != "" {
		key, keyErr := s.createKey(storage)
		if keyErr != nil {
			return db.GlobalSecretStorage{}, keyErr
		}
		replaced = current.CredentialKeyID
		storage.CredentialKeyID = &key.ID
	}
	updated, err := s.repo.UpdateGlobalSecretStorage(storage, expectedRevision)
	if err != nil {
		if replaced != nil || (storage.CredentialKeyID != nil && current.CredentialKeyID == nil) {
			_ = s.repo.DeleteGlobalSecretStorageKey(*storage.CredentialKeyID)
		}
		return db.GlobalSecretStorage{}, err
	}
	if replaced != nil {
		_ = s.repo.DeleteGlobalSecretStorageKey(*replaced)
	}
	return updated, nil
}

func (s *globalSecretStorageService) Delete(storageID int) error {
	current, err := s.repo.GetGlobalSecretStorage(storageID)
	if err != nil {
		return err
	}
	if err = s.repo.DeleteGlobalSecretStorage(storageID); err != nil {
		return err
	}
	if current.CredentialKeyID != nil {
		return s.repo.DeleteGlobalSecretStorageKey(*current.CredentialKeyID)
	}
	return nil
}

func (s *globalSecretStorageService) createKey(storage db.GlobalSecretStorage) (db.AccessKey, error) {
	key := db.AccessKey{
		Name:              random.String(10),
		Type:              db.AccessKeyString,
		Owner:             db.AccessKeyGlobalSecretStorage,
		SourceStorageType: storage.SourceStorageType,
	}
	if storage.SourceStorageType != nil {
		source := storage.Secret
		key.SourceStorageKey = &source
	} else {
		key.String = storage.Secret
	}
	return s.keys.Create(key)
}

// normalizeGlobalSecretStorage validates a connection. Infisical follows the
// rules of a project Infisical storage, including PAM routes; Vault and
// OpenBao follow those of a project Vault/OpenBao storage and keep only known
// parameters, so no credential can end up in clear parameters.
func normalizeGlobalSecretStorage(storage *db.GlobalSecretStorage, requireSecret bool, kv pro_interfaces.GlobalKVStore) error {
	storage.Name = strings.TrimSpace(storage.Name)
	if storage.Name == "" || len(storage.Name) > 128 {
		return common_errors.NewValidationError("name is required and must not exceed 128 characters")
	}
	if storage.SourceStorageType != nil {
		switch *storage.SourceStorageType {
		case db.AccessKeySourceStorageEnv, db.AccessKeySourceStorageFile:
		default:
			return common_errors.NewValidationError("unsupported credential source")
		}
	}
	if requireSecret && storage.Secret == "" {
		return common_errors.NewValidationError("the credential is required")
	}
	for _, projectID := range storage.ProjectIDs {
		if projectID <= 0 {
			return common_errors.NewValidationError("project IDs are invalid")
		}
	}
	scopes, err := globalStorageScopes(storage.Type, storage.Params)
	if err != nil {
		return err
	}
	switch storage.Type {
	case db.SecretStorageTypeInfisical:
		project := db.SecretStorage{Type: db.SecretStorageTypeInfisical, Params: storage.Params, Secret: "validated", ID: 1}
		if err := ValidateInfisicalStorage(&project); err != nil {
			return err
		}
		allowRemoteRunners, err := globalStorageAllowsRemoteRunners(storage.Params)
		if err != nil {
			return err
		}
		storage.Params = project.Params
		storage.Params[globalStorageParamRemoteRunners] = allowRemoteRunners
	case db.SecretStorageTypeVault, db.SecretStorageTypeOpenBao:
		if kv == nil {
			return common_errors.NewValidationError("Vault and OpenBao connections are not available in this build")
		}
		if len(storage.ProjectIDs) > 0 {
			return common_errors.NewValidationError("project assignments apply to Infisical SSH routes only")
		}
		params := db.MapStringAnyField{}
		for key, value := range storage.Params {
			if key == globalStorageParamScopes {
				continue
			}
			if !globalKVParams()[key] {
				return common_errors.NewValidationError("unknown Vault/OpenBao setting " + key)
			}
			params[key] = value
		}
		if err := kv.ValidateGlobalKV(string(storage.Type), params); err != nil {
			return common_errors.NewValidationError("check the address, authentication method and role of the connection")
		}
		storage.Params = params
	default:
		return common_errors.NewValidationError("a global connection is Infisical, Vault or OpenBao")
	}
	storage.Params[globalStorageParamScopes] = scopesParam(scopes)
	return nil
}

// globalKVParams lists the settings a global Vault/OpenBao connection keeps.
func globalKVParams() map[string]bool {
	return map[string]bool{
		"url": true, "namespace": true, "ca_certificate": true, "timeout": true, "max_response_bytes": true,
		"auth_method": true, "auth_mount": true, "role_id": true, "role": true,
	}
}

const (
	globalStorageParamScopes        = "secret_scopes"
	globalStorageParamRemoteRunners = "allow_remote_runners"
)

func globalStorageAllowsRemoteRunners(params db.MapStringAnyField) (bool, error) {
	raw, ok := params[globalStorageParamRemoteRunners]
	if !ok || raw == nil {
		return false, nil
	}
	allowed, ok := raw.(bool)
	if !ok {
		return false, common_errors.NewValidationError("allow_remote_runners must be true or false")
	}
	return allowed, nil
}

// globalStorageScopes reads the administrator's secret scopes. An Infisical
// scope is a project, an environment and an optional folder; a Vault/OpenBao
// scope is a KV v2 mount and an optional folder. An Infisical storage without
// scopes serves SSH routing only.
func globalStorageScopes(storageType db.SecretStorageType, params db.MapStringAnyField) ([]db.GlobalSecretStorageScope, error) {
	raw, ok := params[globalStorageParamScopes]
	if !ok || raw == nil {
		return []db.GlobalSecretStorageScope{}, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, common_errors.NewValidationError("secret scopes are invalid")
	}
	var scopes []db.GlobalSecretStorageScope
	if err = json.Unmarshal(encoded, &scopes); err != nil {
		return nil, common_errors.NewValidationError("secret scopes are invalid")
	}
	if len(scopes) > db.MaxGlobalSecretStorageScopes {
		return nil, common_errors.NewValidationError("too many secret scopes")
	}
	for i := range scopes {
		scopes[i].ProjectID = strings.TrimSpace(scopes[i].ProjectID)
		scopes[i].Environment = strings.TrimSpace(scopes[i].Environment)
		scopes[i].Path = strings.Trim(strings.TrimSpace(scopes[i].Path), "/")
		if storageType == db.SecretStorageTypeVault || storageType == db.SecretStorageTypeOpenBao {
			if scopes[i].Environment != "" || pro_interfaces.ValidateSecretBrowseKVLocation(scopes[i].ProjectID, scopes[i].Path) != nil {
				return nil, common_errors.NewValidationError("a secret scope needs a KV v2 mount and an optional folder")
			}
			continue
		}
		if !scopes[i].Validate() {
			return nil, common_errors.NewValidationError("a secret scope needs an Infisical project, an environment and an optional folder")
		}
	}
	return scopes, nil
}

func scopesParam(scopes []db.GlobalSecretStorageScope) []any {
	result := make([]any, 0, len(scopes))
	for _, scope := range scopes {
		result = append(result, map[string]any{"project_id": scope.ProjectID, "environment": scope.Environment, "path": scope.Path})
	}
	return result
}

// GlobalSecretStorageScopes returns a storage's validated secret scopes, for
// the value-free provider list shown to credential administrators.
func GlobalSecretStorageScopes(storage db.GlobalSecretStorage) []db.GlobalSecretStorageScope {
	scopes, err := globalStorageScopes(storage.Type, storage.Params)
	if err != nil {
		return []db.GlobalSecretStorageScope{}
	}
	return scopes
}

// globalInfisicalSecretSource resolves global credentials stored in Infisical.
type globalInfisicalSecretSource struct {
	repo       db.GlobalSecretStorageRepository
	encryption AccessKeyEncryptionService
	kv         pro_interfaces.GlobalKVStore
}

// globalCredentialProviderRouter sends infisical references and Vault/OpenBao
// references to a global connection in the database to those connections,
// and every other provider to the configured adapter.
type globalCredentialProviderRouter struct {
	fallback  pro_interfaces.GlobalCredentialExternalAdapter
	infisical *globalInfisicalSecretSource
}

// NewGlobalCredentialProviderRouter adds global Infisical storages to the
// configured global credential providers.
func NewGlobalCredentialProviderRouter(
	fallback pro_interfaces.GlobalCredentialExternalAdapter,
	repo db.GlobalSecretStorageRepository,
	encryption AccessKeyEncryptionService,
	kv pro_interfaces.GlobalKVStore,
) pro_interfaces.GlobalCredentialExternalAdapter {
	return &globalCredentialProviderRouter{
		fallback:  fallback,
		infisical: &globalInfisicalSecretSource{repo: repo, encryption: encryption, kv: kv},
	}
}

func (r *globalCredentialProviderRouter) ResolveGlobalCredentialExternal(
	ctx context.Context,
	reference db.GlobalCredentialExternalReference,
) (pro_interfaces.GlobalCredentialExternalResult, error) {
	if reference.Provider == db.GlobalCredentialProviderInfisical {
		return r.infisical.resolve(ctx, reference)
	}
	if _, stored := db.ParseGlobalSecretStorageProviderID(reference.ProviderID); stored {
		return r.infisical.resolveKV(ctx, reference)
	}
	if r.fallback == nil {
		return pro_interfaces.GlobalCredentialExternalResult{}, errors.New("global credential provider unavailable")
	}
	return r.fallback.ResolveGlobalCredentialExternal(ctx, reference)
}

// resolve logs in with the storage's machine identity and reads one secret.
// Errors are value-free: they never carry the reference, a provider message
// or the client secret.
func (s *globalInfisicalSecretSource) resolve(
	ctx context.Context,
	reference db.GlobalCredentialExternalReference,
) (pro_interfaces.GlobalCredentialExternalResult, error) {
	unavailable := errors.New("global credential provider unavailable")
	if s == nil || s.repo == nil || s.encryption == nil || reference.Validate() != nil {
		return pro_interfaces.GlobalCredentialExternalResult{}, unavailable
	}
	storageID, _ := db.ParseGlobalSecretStorageProviderID(reference.ProviderID)
	storage, err := s.repo.GetGlobalSecretStorage(storageID)
	if err != nil || storage.Type != db.SecretStorageTypeInfisical || storage.CredentialKeyID == nil {
		return pro_interfaces.GlobalCredentialExternalResult{}, unavailable
	}
	config, err := infisicalPAMFromParams(storage.Params)
	if err != nil {
		return pro_interfaces.GlobalCredentialExternalResult{}, unavailable
	}
	// Only secrets inside an administrator's scope are read, however broad
	// the machine identity's own access is.
	scopes, err := globalStorageScopes(storage.Type, storage.Params)
	if err != nil {
		return pro_interfaces.GlobalCredentialExternalResult{}, unavailable
	}
	admitted := false
	for _, scope := range scopes {
		if scope.Admits(reference) {
			admitted = true
			break
		}
	}
	if !admitted {
		return pro_interfaces.GlobalCredentialExternalResult{}, unavailable
	}
	key, err := s.repo.GetGlobalSecretStorageKey(*storage.CredentialKeyID)
	if err != nil {
		return pro_interfaces.GlobalCredentialExternalResult{}, unavailable
	}
	if err = s.encryption.DeserializeSecret(&key); err != nil || key.String == "" {
		return pro_interfaces.GlobalCredentialExternalResult{}, unavailable
	}
	clientSecret := []byte(key.String)
	defer zeroBytes(clientSecret)
	client, err := infisicalpam.NewClient(infisicalpam.Config{
		SiteURL: config.SiteURL, ClientID: config.ClientID, OrganizationSlug: config.OrganizationSlug,
		CACertificate: []byte(config.CACertificate), ClientSecret: clientSecret,
	})
	if err != nil {
		return pro_interfaces.GlobalCredentialExternalResult{}, unavailable
	}
	token, err := client.Login(ctx)
	if err != nil {
		return pro_interfaces.GlobalCredentialExternalResult{}, unavailable
	}
	secret, err := client.GetSecret(ctx, token, infisicalpam.SecretRequest{
		ProjectID: reference.Mount, Environment: reference.Environment, Path: reference.InfisicalSecretPath(),
		Name: reference.Field, Version: reference.Version,
	})
	if err != nil {
		return pro_interfaces.GlobalCredentialExternalResult{}, unavailable
	}
	return pro_interfaces.GlobalCredentialExternalResult{Value: secret.Value, ProviderVersion: secret.Version}, nil
}

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

// resolveKV reads a Vault/OpenBao reference through a global connection kept
// in the database, inside the connection's scopes. Errors are value-free.
func (s *globalInfisicalSecretSource) resolveKV(
	ctx context.Context,
	reference db.GlobalCredentialExternalReference,
) (pro_interfaces.GlobalCredentialExternalResult, error) {
	unavailable := errors.New("global credential provider unavailable")
	if s == nil || s.repo == nil || s.encryption == nil || s.kv == nil || reference.Validate() != nil {
		return pro_interfaces.GlobalCredentialExternalResult{}, unavailable
	}
	storageID, _ := db.ParseGlobalSecretStorageProviderID(reference.ProviderID)
	storage, err := s.repo.GetGlobalSecretStorage(storageID)
	if err != nil || string(storage.Type) != reference.Provider || storage.CredentialKeyID == nil {
		return pro_interfaces.GlobalCredentialExternalResult{}, unavailable
	}
	scopes, err := globalStorageScopes(storage.Type, storage.Params)
	if err != nil {
		return pro_interfaces.GlobalCredentialExternalResult{}, unavailable
	}
	admitted := false
	for _, scope := range scopes {
		if scope.Admits(reference) {
			admitted = true
			break
		}
	}
	if !admitted {
		return pro_interfaces.GlobalCredentialExternalResult{}, unavailable
	}
	key, err := s.repo.GetGlobalSecretStorageKey(*storage.CredentialKeyID)
	if err != nil {
		return pro_interfaces.GlobalCredentialExternalResult{}, unavailable
	}
	if err = s.encryption.DeserializeSecret(&key); err != nil || key.String == "" {
		return pro_interfaces.GlobalCredentialExternalResult{}, unavailable
	}
	credential := []byte(key.String)
	defer zeroBytes(credential)
	value, err := s.kv.ReadGlobalKV(ctx, pro_interfaces.GlobalKVConnection{
		StorageID: globalKVCacheID(storage), Type: string(storage.Type), Params: storage.Params, Credential: credential,
	}, pro_interfaces.SecretReference{
		Mount: reference.Mount, Path: reference.Path, Version: reference.Version, Field: reference.Field,
	})
	if err != nil || len(value) == 0 {
		zeroBytes(value)
		return pro_interfaces.GlobalCredentialExternalResult{}, unavailable
	}
	return pro_interfaces.GlobalCredentialExternalResult{Value: value, ProviderVersion: reference.Version}, nil
}
