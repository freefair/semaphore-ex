package server

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/infisicalpam"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

// SecretBrowseService lets people pick what a secret store holds instead of
// typing identifiers. It tests connections and lists one level at a time:
// names and identifiers only, never values.
type SecretBrowseService interface {
	TestProjectStorage(ctx context.Context, projectID int, storageID int) (pro_interfaces.SecretBrowseIdentity, error)
	BrowseProjectStorage(ctx context.Context, projectID int, storageID int, location pro_interfaces.SecretBrowseLocation) ([]pro_interfaces.SecretBrowseNode, error)
	TestGlobalStorage(ctx context.Context, storageID int) (pro_interfaces.SecretBrowseIdentity, error)
	// BrowseGlobalStorage lists a global connection. With scoped set, only
	// what lies inside the connection's secret scopes is listed, so people
	// who may create credentials cannot look past what they may reference.
	BrowseGlobalStorage(ctx context.Context, storageID int, location pro_interfaces.SecretBrowseLocation, scoped bool) ([]pro_interfaces.SecretBrowseNode, error)
	TestGlobalDraft(ctx context.Context, draft db.GlobalSecretStorage) (pro_interfaces.SecretBrowseIdentity, error)
	BrowseGlobalDraft(ctx context.Context, draft db.GlobalSecretStorage, location pro_interfaces.SecretBrowseLocation) ([]pro_interfaces.SecretBrowseNode, error)
}

// errSecretBrowseUnsupported reports a storage type that cannot be browsed.
func errSecretBrowseUnsupported() error {
	return errors.New("this storage type cannot be browsed")
}

type secretBrowseService struct {
	storages   db.SecretStorageRepository
	keys       db.AccessKeyManager
	global     db.GlobalSecretStorageRepository
	encryption AccessKeyEncryptionService
	kv         pro_interfaces.GlobalKVStore
}

// NewSecretBrowseService builds the browse service.
func NewSecretBrowseService(
	storages db.SecretStorageRepository,
	keys db.AccessKeyManager,
	global db.GlobalSecretStorageRepository,
	encryption AccessKeyEncryptionService,
	kv pro_interfaces.GlobalKVStore,
) SecretBrowseService {
	return &secretBrowseService{storages: storages, keys: keys, global: global, encryption: encryption, kv: kv}
}

func (s *secretBrowseService) TestProjectStorage(ctx context.Context, projectID int, storageID int) (pro_interfaces.SecretBrowseIdentity, error) {
	storage, err := s.storages.GetSecretStorage(projectID, storageID)
	if err != nil {
		return pro_interfaces.SecretBrowseIdentity{}, err
	}
	switch storage.Type {
	case db.SecretStorageTypeVault, db.SecretStorageTypeOpenBao:
		browser, ok := s.encryption.(pro_interfaces.RuntimeSecretBrowser)
		if !ok {
			return pro_interfaces.SecretBrowseIdentity{}, errSecretBrowseUnsupported()
		}
		return browser.RuntimeSecretProviderIdentity(ctx, projectID, storageID)
	case db.SecretStorageTypeInfisical:
		session, err := s.projectInfisical(ctx, projectID, storage)
		if err != nil {
			return pro_interfaces.SecretBrowseIdentity{}, err
		}
		return session.identity(ctx)
	default:
		return pro_interfaces.SecretBrowseIdentity{}, errSecretBrowseUnsupported()
	}
}

func (s *secretBrowseService) BrowseProjectStorage(
	ctx context.Context,
	projectID int,
	storageID int,
	location pro_interfaces.SecretBrowseLocation,
) ([]pro_interfaces.SecretBrowseNode, error) {
	storage, err := s.storages.GetSecretStorage(projectID, storageID)
	if err != nil {
		return nil, err
	}
	switch storage.Type {
	case db.SecretStorageTypeVault, db.SecretStorageTypeOpenBao:
		browser, ok := s.encryption.(pro_interfaces.RuntimeSecretBrowser)
		if !ok {
			return nil, errSecretBrowseUnsupported()
		}
		return browser.BrowseRuntimeSecretProvider(ctx, projectID, storageID, location)
	case db.SecretStorageTypeInfisical:
		session, err := s.projectInfisical(ctx, projectID, storage)
		if err != nil {
			return nil, err
		}
		return session.browse(ctx, location, nil)
	default:
		return nil, errSecretBrowseUnsupported()
	}
}

func (s *secretBrowseService) TestGlobalStorage(ctx context.Context, storageID int) (pro_interfaces.SecretBrowseIdentity, error) {
	storage, err := s.global.GetGlobalSecretStorage(storageID)
	if err != nil {
		return pro_interfaces.SecretBrowseIdentity{}, err
	}
	switch storage.Type {
	case db.SecretStorageTypeInfisical:
		session, err := s.globalInfisical(ctx, storage)
		if err != nil {
			return pro_interfaces.SecretBrowseIdentity{}, err
		}
		return session.identity(ctx)
	case db.SecretStorageTypeVault, db.SecretStorageTypeOpenBao:
		connection, err := s.globalKVConnection(storage)
		if err != nil {
			return pro_interfaces.SecretBrowseIdentity{}, err
		}
		defer zeroBytes(connection.Credential)
		return s.kv.GlobalKVIdentity(ctx, connection)
	default:
		return pro_interfaces.SecretBrowseIdentity{}, errSecretBrowseUnsupported()
	}
}

func (s *secretBrowseService) BrowseGlobalStorage(
	ctx context.Context,
	storageID int,
	location pro_interfaces.SecretBrowseLocation,
	scoped bool,
) ([]pro_interfaces.SecretBrowseNode, error) {
	storage, err := s.global.GetGlobalSecretStorage(storageID)
	if err != nil {
		return nil, err
	}
	var scopes []db.GlobalSecretStorageScope
	if scoped {
		scopes = GlobalSecretStorageScopes(storage)
		if scopes == nil {
			scopes = []db.GlobalSecretStorageScope{}
		}
	}
	switch storage.Type {
	case db.SecretStorageTypeInfisical:
		session, err := s.globalInfisical(ctx, storage)
		if err != nil {
			return nil, err
		}
		return session.browse(ctx, location, scopes)
	case db.SecretStorageTypeVault, db.SecretStorageTypeOpenBao:
		connection, err := s.globalKVConnection(storage)
		if err != nil {
			return nil, err
		}
		defer zeroBytes(connection.Credential)
		nodes, err := s.kv.BrowseGlobalKV(ctx, connection, location)
		if err != nil {
			return nil, err
		}
		return filterKVScopes(nodes, location, scopes), nil
	default:
		return nil, errSecretBrowseUnsupported()
	}
}

// infisicalBrowseSession is one logged-in machine identity.
type infisicalBrowseSession struct {
	client *infisicalpam.Client
	token  infisicalpam.AccessToken
}

func newInfisicalBrowseSession(ctx context.Context, params db.MapStringAnyField, secret string) (*infisicalBrowseSession, error) {
	config, err := infisicalPAMFromParams(params)
	if err != nil {
		return nil, err
	}
	clientSecret := []byte(secret)
	defer zeroBytes(clientSecret)
	client, err := infisicalpam.NewClient(infisicalpam.Config{
		SiteURL: config.SiteURL, ClientID: config.ClientID, OrganizationSlug: config.OrganizationSlug,
		CACertificate: []byte(config.CACertificate), ClientSecret: clientSecret,
	})
	if err != nil {
		return nil, err
	}
	token, err := client.Login(ctx)
	if err != nil {
		return nil, err
	}
	return &infisicalBrowseSession{client: client, token: token}, nil
}

func (s *infisicalBrowseSession) identity(ctx context.Context) (pro_interfaces.SecretBrowseIdentity, error) {
	organization, err := s.client.Organization(ctx, s.token)
	if err != nil {
		return pro_interfaces.SecretBrowseIdentity{}, err
	}
	name := organization.Name
	if name == "" {
		name = organization.Slug
	}
	return pro_interfaces.SecretBrowseIdentity{Name: name, Description: "Organization " + organization.Slug}, nil
}

// browse lists projects, then the environments of a project, then the
// folders and secrets of a path. Infisical paths are kept relative, as global
// credential references store them: "" is the root folder.
func (s *infisicalBrowseSession) browse(
	ctx context.Context,
	location pro_interfaces.SecretBrowseLocation,
	scopes []db.GlobalSecretStorageScope,
) ([]pro_interfaces.SecretBrowseNode, error) {
	if location.Project == "" || location.Environment == "" {
		projects, err := s.client.Projects(ctx, s.token)
		if err != nil {
			return nil, err
		}
		nodes := make([]pro_interfaces.SecretBrowseNode, 0)
		for _, project := range projects {
			if location.Project == "" {
				if scopes == nil || scopesAdmitProject(scopes, project.ID) {
					nodes = append(nodes, pro_interfaces.SecretBrowseNode{Kind: pro_interfaces.SecretBrowseProject, Name: project.Name, Value: project.ID})
				}
				continue
			}
			if project.ID != location.Project {
				continue
			}
			for _, environment := range project.Environments {
				if scopes == nil || scopesAdmitEnvironment(scopes, project.ID, environment.Slug) {
					nodes = append(nodes, pro_interfaces.SecretBrowseNode{Kind: pro_interfaces.SecretBrowseEnvironment, Name: environment.Name, Value: environment.Slug})
				}
			}
		}
		return nodes, nil
	}
	path := strings.Trim(location.Path, "/")
	request := infisicalpam.FolderRequest{ProjectID: location.Project, Environment: location.Environment, Path: "/" + path}
	inside, onTheWay := true, map[string]bool(nil)
	if scopes != nil {
		inside, onTheWay = scopeFolderAccess(scopes, location.Project, location.Environment, path)
		if !inside && len(onTheWay) == 0 {
			return []pro_interfaces.SecretBrowseNode{}, nil
		}
	}
	folders, err := s.client.Folders(ctx, s.token, request)
	if err != nil {
		return nil, err
	}
	nodes := make([]pro_interfaces.SecretBrowseNode, 0, len(folders))
	for _, folder := range folders {
		if !inside && !onTheWay[folder.Name] {
			continue
		}
		nodes = append(nodes, pro_interfaces.SecretBrowseNode{Kind: pro_interfaces.SecretBrowseFolder, Name: folder.Name, Value: joinBrowsePath(path, folder.Name)})
	}
	if !inside {
		return nodes, nil
	}
	keys, err := s.client.SecretKeys(ctx, s.token, request)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		nodes = append(nodes, pro_interfaces.SecretBrowseNode{Kind: pro_interfaces.SecretBrowseSecret, Name: key.Key, Value: key.Key, Version: key.Version})
	}
	return nodes, nil
}

func (s *secretBrowseService) projectInfisical(ctx context.Context, projectID int, storage db.SecretStorage) (*infisicalBrowseSession, error) {
	keys, err := s.keys.GetAccessKeys(projectID, db.GetAccessKeyOptions{
		Owner: db.AccessKeySecretStorage, StorageID: &storage.ID,
	}, db.RetrieveQueryParams{})
	if err != nil {
		return nil, err
	}
	if len(keys) != 1 {
		return nil, errors.New("the storage has no client secret")
	}
	key := keys[0]
	if err = s.encryption.DeserializeSecret(&key); err != nil || key.String == "" {
		return nil, errors.New("the client secret cannot be read")
	}
	return newInfisicalBrowseSession(ctx, storage.Params, key.String)
}

func (s *secretBrowseService) globalInfisical(ctx context.Context, storage db.GlobalSecretStorage) (*infisicalBrowseSession, error) {
	secret, err := s.globalSecret(storage)
	if err != nil {
		return nil, err
	}
	return newInfisicalBrowseSession(ctx, storage.Params, secret)
}

func (s *secretBrowseService) globalSecret(storage db.GlobalSecretStorage) (string, error) {
	if storage.CredentialKeyID == nil {
		return "", errors.New("the connection has no credential")
	}
	key, err := s.global.GetGlobalSecretStorageKey(*storage.CredentialKeyID)
	if err != nil {
		return "", err
	}
	if err = s.encryption.DeserializeSecret(&key); err != nil || key.String == "" {
		return "", errors.New("the connection credential cannot be read")
	}
	return key.String, nil
}

// globalKVConnection loads a global Vault/OpenBao connection with its
// decrypted credential. The caller zeroes the credential.
func (s *secretBrowseService) globalKVConnection(storage db.GlobalSecretStorage) (pro_interfaces.GlobalKVConnection, error) {
	if s.kv == nil {
		return pro_interfaces.GlobalKVConnection{}, errSecretBrowseUnsupported()
	}
	secret, err := s.globalSecret(storage)
	if err != nil {
		return pro_interfaces.GlobalKVConnection{}, err
	}
	return pro_interfaces.GlobalKVConnection{
		StorageID: globalKVCacheID(storage), Type: string(storage.Type), Params: storage.Params, Credential: []byte(secret),
	}, nil
}

func scopesAdmitProject(scopes []db.GlobalSecretStorageScope, projectID string) bool {
	for _, scope := range scopes {
		if scope.ProjectID == projectID {
			return true
		}
	}
	return false
}

func scopesAdmitEnvironment(scopes []db.GlobalSecretStorageScope, projectID string, environment string) bool {
	for _, scope := range scopes {
		if scope.ProjectID == projectID && scope.Environment == environment {
			return true
		}
	}
	return false
}

// scopeFolderAccess reports whether path lies inside a scope, where all of it
// may be listed, and otherwise which child folders lead towards a scope.
func scopeFolderAccess(scopes []db.GlobalSecretStorageScope, projectID string, environment string, path string) (bool, map[string]bool) {
	onTheWay := map[string]bool{}
	for _, scope := range scopes {
		if scope.ProjectID != projectID || scope.Environment != environment {
			continue
		}
		if scope.Path == "" || path == scope.Path || strings.HasPrefix(path, scope.Path+"/") {
			return true, nil
		}
		prefix := ""
		if path != "" {
			prefix = path + "/"
		}
		if strings.HasPrefix(scope.Path, prefix) {
			onTheWay[strings.SplitN(strings.TrimPrefix(scope.Path, prefix), "/", 2)[0]] = true
		}
	}
	return false, onTheWay
}

// filterKVScopes applies connection scopes to a Vault/OpenBao listing. A
// scope's project is the mount and its path a folder; environments do not
// exist there.
func filterKVScopes(nodes []pro_interfaces.SecretBrowseNode, location pro_interfaces.SecretBrowseLocation, scopes []db.GlobalSecretStorageScope) []pro_interfaces.SecretBrowseNode {
	if scopes == nil {
		return nodes
	}
	filtered := make([]pro_interfaces.SecretBrowseNode, 0, len(nodes))
	if location.Mount == "" {
		for _, node := range nodes {
			if scopesAdmitProject(scopes, node.Value) {
				filtered = append(filtered, node)
			}
		}
		return filtered
	}
	folder := location.Path
	if location.Secret != "" {
		folder = location.Secret
	}
	inside, onTheWay := scopeFolderAccess(scopes, location.Mount, "", folder)
	for _, node := range nodes {
		if inside || (node.Kind == pro_interfaces.SecretBrowseFolder && onTheWay[node.Name]) {
			filtered = append(filtered, node)
		}
	}
	return filtered
}

func joinBrowsePath(parent string, name string) string {
	if parent == "" {
		return name
	}
	return parent + "/" + name
}

// TestGlobalDraft tests a connection as entered in its dialog, before it is
// saved. Without a new credential it uses the stored one of the same
// connection.
func (s *secretBrowseService) TestGlobalDraft(ctx context.Context, draft db.GlobalSecretStorage) (pro_interfaces.SecretBrowseIdentity, error) {
	storage, err := s.prepareDraft(draft)
	if err != nil {
		return pro_interfaces.SecretBrowseIdentity{}, err
	}
	switch storage.Type {
	case db.SecretStorageTypeInfisical:
		session, err := newInfisicalBrowseSession(ctx, storage.Params, storage.Secret)
		if err != nil {
			return pro_interfaces.SecretBrowseIdentity{}, err
		}
		return session.identity(ctx)
	default:
		connection := pro_interfaces.GlobalKVConnection{StorageID: draftStorageID(storage), Type: string(storage.Type), Params: storage.Params, Credential: []byte(storage.Secret), Draft: true}
		defer zeroBytes(connection.Credential)
		return s.kv.GlobalKVIdentity(ctx, connection)
	}
}

// BrowseGlobalDraft lists a connection as entered in its dialog, so its
// scopes can be picked before it is saved. It is not limited by scopes: only
// administrators edit connections.
func (s *secretBrowseService) BrowseGlobalDraft(ctx context.Context, draft db.GlobalSecretStorage, location pro_interfaces.SecretBrowseLocation) ([]pro_interfaces.SecretBrowseNode, error) {
	storage, err := s.prepareDraft(draft)
	if err != nil {
		return nil, err
	}
	switch storage.Type {
	case db.SecretStorageTypeInfisical:
		session, err := newInfisicalBrowseSession(ctx, storage.Params, storage.Secret)
		if err != nil {
			return nil, err
		}
		return session.browse(ctx, location, nil)
	default:
		connection := pro_interfaces.GlobalKVConnection{StorageID: draftStorageID(storage), Type: string(storage.Type), Params: storage.Params, Credential: []byte(storage.Secret), Draft: true}
		defer zeroBytes(connection.Credential)
		return s.kv.BrowseGlobalKV(ctx, connection, location)
	}
}

// prepareDraft validates a draft connection and fills in its credential:
// the entered one, one read from its environment or file source, or the
// stored one of the saved connection.
func (s *secretBrowseService) prepareDraft(draft db.GlobalSecretStorage) (db.GlobalSecretStorage, error) {
	storage := draft
	if storage.ID > 0 {
		current, err := s.global.GetGlobalSecretStorage(storage.ID)
		if err != nil {
			return db.GlobalSecretStorage{}, err
		}
		storage.Type = current.Type
		if storage.Secret == "" {
			// The stored credential only goes where it went before.
			probe := storage
			if err := normalizeGlobalSecretStorage(&probe, false, s.kv); err != nil {
				return db.GlobalSecretStorage{}, err
			}
			if connectionTargetChanged(current, probe) {
				return db.GlobalSecretStorage{}, errNewCredentialRequired()
			}
			secret, err := s.globalSecret(current)
			if err != nil {
				return db.GlobalSecretStorage{}, err
			}
			storage.Secret = secret
			storage.SourceStorageType = nil
		}
	}
	if storage.Params == nil {
		storage.Params = db.MapStringAnyField{}
	}
	if err := normalizeGlobalSecretStorage(&storage, true, s.kv); err != nil {
		return db.GlobalSecretStorage{}, err
	}
	if storage.SourceStorageType != nil {
		source := storage.Secret
		key := db.AccessKey{Type: db.AccessKeyString, SourceStorageType: storage.SourceStorageType, SourceStorageKey: &source}
		if err := s.encryption.DeserializeSecret(&key); err != nil || key.String == "" {
			return db.GlobalSecretStorage{}, errors.New("the credential cannot be read from its source")
		}
		storage.Secret = key.String
	}
	return storage, nil
}

// draftStorageID gives a draft a positive storage ID for the provider
// configuration; drafts bypass the token cache, so it never selects a token.
func draftStorageID(storage db.GlobalSecretStorage) int {
	if storage.ID > 0 {
		return storage.ID
	}
	return 1 << 30
}

// globalKVCacheID keys a saved connection's token cache by its ID and
// revision, so a changed address or credential never reuses a token issued
// before the change.
func globalKVCacheID(storage db.GlobalSecretStorage) int {
	hash := fnv.New32a()
	_, _ = fmt.Fprintf(hash, "global-secret-storage:%d:%d", storage.ID, storage.Revision)
	return int(hash.Sum32()&0x3fffffff) + 1
}
