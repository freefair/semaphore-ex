package server

import (
	"context"
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
}

func NewGlobalSecretStorageService(repo db.GlobalSecretStorageRepository, keys AccessKeyService) GlobalSecretStorageService {
	return &globalSecretStorageService{repo: repo, keys: keys}
}

func (s *globalSecretStorageService) List() ([]db.GlobalSecretStorage, error) {
	return s.repo.GetGlobalSecretStorages()
}

func (s *globalSecretStorageService) Get(storageID int) (db.GlobalSecretStorage, error) {
	return s.repo.GetGlobalSecretStorage(storageID)
}

func (s *globalSecretStorageService) Create(storage db.GlobalSecretStorage) (db.GlobalSecretStorage, error) {
	storage.ID = 0
	if err := normalizeGlobalSecretStorage(&storage, true); err != nil {
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
	if err = normalizeGlobalSecretStorage(&storage, false); err != nil {
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

// normalizeGlobalSecretStorage validates the connection with the same rules as
// a project Infisical storage, including PAM routes.
func normalizeGlobalSecretStorage(storage *db.GlobalSecretStorage, requireSecret bool) error {
	storage.Name = strings.TrimSpace(storage.Name)
	if storage.Name == "" || len(storage.Name) > 128 {
		return common_errors.NewValidationError("name is required and must not exceed 128 characters")
	}
	if storage.Type != db.SecretStorageTypeInfisical {
		return common_errors.NewValidationError("only Infisical can be configured as a global secret storage")
	}
	if storage.SourceStorageType != nil {
		switch *storage.SourceStorageType {
		case db.AccessKeySourceStorageEnv, db.AccessKeySourceStorageFile:
		default:
			return common_errors.NewValidationError("unsupported client secret source")
		}
	}
	if requireSecret && storage.Secret == "" {
		return common_errors.NewValidationError("client secret is required")
	}
	for _, projectID := range storage.ProjectIDs {
		if projectID <= 0 {
			return common_errors.NewValidationError("project IDs are invalid")
		}
	}
	project := db.SecretStorage{Type: db.SecretStorageTypeInfisical, Params: storage.Params, Secret: "validated", ID: 1}
	if err := ValidateInfisicalStorage(&project); err != nil {
		return err
	}
	storage.Params = project.Params
	return nil
}

// globalInfisicalSecretSource resolves global credentials stored in Infisical.
type globalInfisicalSecretSource struct {
	repo       db.GlobalSecretStorageRepository
	encryption AccessKeyEncryptionService
}

// globalCredentialProviderRouter sends infisical references to the global
// Infisical storages and every other provider to the configured adapter.
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
) pro_interfaces.GlobalCredentialExternalAdapter {
	return &globalCredentialProviderRouter{
		fallback:  fallback,
		infisical: &globalInfisicalSecretSource{repo: repo, encryption: encryption},
	}
}

func (r *globalCredentialProviderRouter) ResolveGlobalCredentialExternal(
	ctx context.Context,
	reference db.GlobalCredentialExternalReference,
) (pro_interfaces.GlobalCredentialExternalResult, error) {
	if reference.Provider == db.GlobalCredentialProviderInfisical {
		return r.infisical.resolve(ctx, reference)
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
