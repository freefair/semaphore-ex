package pro_interfaces

import (
	"context"
	"errors"
	"strings"
)

// SecretBrowseNodeKind names one level of a secret store tree.
type SecretBrowseNodeKind string

const (
	SecretBrowseProject     SecretBrowseNodeKind = "project"
	SecretBrowseEnvironment SecretBrowseNodeKind = "environment"
	SecretBrowseMount       SecretBrowseNodeKind = "mount"
	SecretBrowseFolder      SecretBrowseNodeKind = "folder"
	SecretBrowseSecret      SecretBrowseNodeKind = "secret"
	SecretBrowseField       SecretBrowseNodeKind = "field"
)

// SecretBrowseNode is one entry of a secret store listing: a display name and
// the identifier a reference stores. It never carries a secret value.
type SecretBrowseNode struct {
	Kind SecretBrowseNodeKind `json:"kind"`
	// Name is shown to people; Value is what a reference stores.
	Name    string `json:"name"`
	Value   string `json:"value"`
	Version int    `json:"version,omitempty"`
}

// SecretBrowseLocation selects the level to list. Empty fields stop the walk:
// no Mount lists mounts, a Mount and Path list that folder, and Secret lists
// the fields of one secret. Infisical uses Project and Environment instead of
// Mount.
type SecretBrowseLocation struct {
	Project     string `json:"project,omitempty"`
	Environment string `json:"environment,omitempty"`
	Mount       string `json:"mount,omitempty"`
	Path        string `json:"path,omitempty"`
	Secret      string `json:"secret,omitempty"`
}

// SecretBrowseIdentity is what a successful connection test reports: who the
// connection acts as, without any credential material.
type SecretBrowseIdentity struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// VaultOpenBaoBrowser lists KV v2 mounts, folders, secrets and field names.
// Field names require reading the secret; implementations return the names
// only and discard the values.
type VaultOpenBaoBrowser interface {
	Identity(context.Context, SecretProviderConfiguration) (SecretBrowseIdentity, error)
	ListKVMounts(context.Context, SecretProviderConfiguration) ([]SecretBrowseNode, error)
	ListKV(context.Context, SecretProviderConfiguration, string, string) ([]SecretBrowseNode, error)
	ListKVFields(context.Context, SecretProviderConfiguration, string, string) ([]SecretBrowseNode, error)
}

// RuntimeSecretBrowser browses a project's Vault/OpenBao storage with its own
// credentials. It is implemented next to the runtime secret resolver.
type RuntimeSecretBrowser interface {
	BrowseRuntimeSecretProvider(context.Context, int, int, SecretBrowseLocation) ([]SecretBrowseNode, error)
	RuntimeSecretProviderIdentity(context.Context, int, int) (SecretBrowseIdentity, error)
}

// ValidateSecretBrowseKVLocation accepts a KV mount and a folder or secret
// path under the rules of SecretReference; an empty path is the mount root.
func ValidateSecretBrowseKVLocation(mount string, path string) error {
	if !secretReferenceMountPattern.MatchString(mount) {
		return errors.New("secret mount is invalid")
	}
	if path == "" {
		return nil
	}
	if len(path) > 512 || strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") {
		return errors.New("secret path is invalid")
	}
	for segment := range strings.SplitSeq(path, "/") {
		if segment == "." || segment == ".." || !secretReferencePathSegment.MatchString(segment) {
			return errors.New("secret path is invalid")
		}
	}
	return nil
}

// BrowseKV lists the level of a Vault/OpenBao store a location selects:
// mounts, then folders and secrets of a path, then the fields of a secret.
func BrowseKV(
	ctx context.Context,
	browser VaultOpenBaoBrowser,
	configuration SecretProviderConfiguration,
	location SecretBrowseLocation,
) ([]SecretBrowseNode, error) {
	if browser == nil {
		return nil, errors.New("Vault/OpenBao browsing is unavailable")
	}
	switch {
	case location.Mount == "":
		return browser.ListKVMounts(ctx, configuration)
	case location.Secret != "":
		return browser.ListKVFields(ctx, configuration, location.Mount, location.Secret)
	default:
		return browser.ListKV(ctx, configuration, location.Mount, location.Path)
	}
}

// GlobalKVConnection is a global Vault/OpenBao connection with its decrypted
// bootstrap credential (token, AppRole secret ID or Kubernetes JWT). Callers
// zero Credential after use.
type GlobalKVConnection struct {
	StorageID  int
	Type       string
	Params     map[string]any
	Credential []byte
	// Draft marks a connection as entered in its dialog, not saved yet. A
	// draft never reads or fills a token cache, so a changed address or a
	// second draft cannot receive a token issued for another connection.
	Draft bool
}

// GlobalKVStore serves global Vault/OpenBao connections kept in the database:
// parameter validation, value reads for global credentials, the connection
// test and browsing. Implementations keep their own token cache, apart from
// project storages.
type GlobalKVStore interface {
	ValidateGlobalKV(storageType string, params map[string]any) error
	ReadGlobalKV(context.Context, GlobalKVConnection, SecretReference) ([]byte, error)
	GlobalKVIdentity(context.Context, GlobalKVConnection) (SecretBrowseIdentity, error)
	BrowseGlobalKV(context.Context, GlobalKVConnection, SecretBrowseLocation) ([]SecretBrowseNode, error)
}
