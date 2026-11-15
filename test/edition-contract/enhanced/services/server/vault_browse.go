package server

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/semaphoreui/semaphore/pro_interfaces"
)

// Browsing lists what a Vault/OpenBao token can see so a reference is picked,
// not typed. Only KV v2 mounts are offered, as references read KV v2.

const maximumBrowseEntries = 2000

// Identity reports the token's display name and policies; it doubles as the
// connection test.
func (c *vaultOpenBaoClient) Identity(
	ctx context.Context,
	configuration pro_interfaces.SecretProviderConfiguration,
) (pro_interfaces.SecretBrowseIdentity, error) {
	token, _, err := c.token(ctx, configuration)
	if err != nil {
		return pro_interfaces.SecretBrowseIdentity{}, err
	}
	defer zero(token)
	var response struct {
		Data struct {
			DisplayName string   `json:"display_name"`
			Policies    []string `json:"policies"`
		} `json:"data"`
	}
	if err = c.doJSON(ctx, configuration, http.MethodGet, "/v1/auth/token/lookup-self", nil, token, nil, &response); err != nil {
		return pro_interfaces.SecretBrowseIdentity{}, err
	}
	name := response.Data.DisplayName
	if name == "" {
		name = "token"
	}
	identity := pro_interfaces.SecretBrowseIdentity{Name: name}
	if len(response.Data.Policies) > 0 {
		identity.Description = "Policies: " + strings.Join(response.Data.Policies, ", ")
	}
	return identity, nil
}

// ListKVMounts lists the KV v2 mounts visible to the token.
func (c *vaultOpenBaoClient) ListKVMounts(
	ctx context.Context,
	configuration pro_interfaces.SecretProviderConfiguration,
) ([]pro_interfaces.SecretBrowseNode, error) {
	token, _, err := c.token(ctx, configuration)
	if err != nil {
		return nil, err
	}
	defer zero(token)
	var response struct {
		Data struct {
			Secret map[string]struct {
				Type        string            `json:"type"`
				Description string            `json:"description"`
				Options     map[string]string `json:"options"`
			} `json:"secret"`
		} `json:"data"`
	}
	if err = c.doJSON(ctx, configuration, http.MethodGet, "/v1/sys/internal/ui/mounts", nil, token, nil, &response); err != nil {
		return nil, err
	}
	nodes := make([]pro_interfaces.SecretBrowseNode, 0)
	for path, mount := range response.Data.Secret {
		name := strings.TrimSuffix(path, "/")
		if mount.Type != "kv" || mount.Options["version"] != "2" ||
			pro_interfaces.ValidateSecretBrowseKVLocation(name, "") != nil {
			continue
		}
		nodes = append(nodes, pro_interfaces.SecretBrowseNode{Kind: pro_interfaces.SecretBrowseMount, Name: name, Value: name})
	}
	return sortedBrowseNodes(nodes), nil
}

// ListKV lists the folders and secrets directly below a path of a mount.
func (c *vaultOpenBaoClient) ListKV(
	ctx context.Context,
	configuration pro_interfaces.SecretProviderConfiguration,
	mount string,
	path string,
) ([]pro_interfaces.SecretBrowseNode, error) {
	if err := pro_interfaces.ValidateSecretBrowseKVLocation(mount, path); err != nil {
		return nil, providerError(pro_interfaces.SecretProviderErrorValidation, "list")
	}
	token, _, err := c.token(ctx, configuration)
	if err != nil {
		return nil, err
	}
	defer zero(token)
	query := url.Values{}
	query.Set("list", "true")
	endpoint := "/v1/" + url.PathEscape(mount) + "/metadata/"
	if path != "" {
		endpoint += escapeSecretPath(path) + "/"
	}
	var response struct {
		Data struct {
			Keys []string `json:"keys"`
		} `json:"data"`
	}
	if err = c.doJSON(ctx, configuration, http.MethodGet, endpoint, query, token, nil, &response); err != nil {
		if errorCategory(err) == pro_interfaces.SecretProviderErrorNotFound {
			return []pro_interfaces.SecretBrowseNode{}, nil
		}
		return nil, err
	}
	nodes := make([]pro_interfaces.SecretBrowseNode, 0, len(response.Data.Keys))
	for _, key := range response.Data.Keys {
		kind := pro_interfaces.SecretBrowseSecret
		name := key
		if strings.HasSuffix(key, "/") {
			kind, name = pro_interfaces.SecretBrowseFolder, strings.TrimSuffix(key, "/")
		}
		value := name
		if path != "" {
			value = path + "/" + name
		}
		if pro_interfaces.ValidateSecretBrowseKVLocation(mount, value) != nil {
			continue
		}
		nodes = append(nodes, pro_interfaces.SecretBrowseNode{Kind: kind, Name: name, Value: value})
		if len(nodes) >= maximumBrowseEntries {
			break
		}
	}
	return sortedBrowseNodes(nodes), nil
}

// ListKVFields returns the field names of a secret. Vault has no value-free
// way to list them, so the secret is read and its values are discarded here.
func (c *vaultOpenBaoClient) ListKVFields(
	ctx context.Context,
	configuration pro_interfaces.SecretProviderConfiguration,
	mount string,
	path string,
) ([]pro_interfaces.SecretBrowseNode, error) {
	if path == "" || pro_interfaces.ValidateSecretBrowseKVLocation(mount, path) != nil {
		return nil, providerError(pro_interfaces.SecretProviderErrorValidation, "fields")
	}
	token, _, err := c.token(ctx, configuration)
	if err != nil {
		return nil, err
	}
	defer zero(token)
	var response struct {
		Data struct {
			Data     map[string]any `json:"data"`
			Metadata struct {
				Version int `json:"version"`
			} `json:"metadata"`
		} `json:"data"`
	}
	endpoint := "/v1/" + url.PathEscape(mount) + "/data/" + escapeSecretPath(path)
	if err = c.doJSON(ctx, configuration, http.MethodGet, endpoint, nil, token, nil, &response); err != nil {
		return nil, err
	}
	nodes := make([]pro_interfaces.SecretBrowseNode, 0, len(response.Data.Data))
	for field, value := range response.Data.Data {
		if _, text := value.(string); !text {
			continue
		}
		nodes = append(nodes, pro_interfaces.SecretBrowseNode{
			Kind: pro_interfaces.SecretBrowseField, Name: field, Value: field, Version: response.Data.Metadata.Version,
		})
	}
	clear(response.Data.Data)
	return sortedBrowseNodes(nodes), nil
}

func sortedBrowseNodes(nodes []pro_interfaces.SecretBrowseNode) []pro_interfaces.SecretBrowseNode {
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].Kind != nodes[j].Kind {
			return nodes[i].Kind == pro_interfaces.SecretBrowseFolder
		}
		return strings.ToLower(nodes[i].Name) < strings.ToLower(nodes[j].Name)
	})
	return nodes
}

var _ pro_interfaces.VaultOpenBaoBrowser = (*vaultOpenBaoClient)(nil)

// RuntimeSecretProviderIdentity tests a project Vault/OpenBao storage with its
// own credentials and reports who it acts as.
func (d *VaultAccessKeyDeserializer) RuntimeSecretProviderIdentity(
	ctx context.Context,
	projectID int,
	storageID int,
) (pro_interfaces.SecretBrowseIdentity, error) {
	if err := d.requireCapability(ctx, pro_interfaces.CapabilityAccessExecute); err != nil {
		return pro_interfaces.SecretBrowseIdentity{}, err
	}
	configuration, credential, err := d.configuration(projectID, storageID)
	if err != nil {
		return pro_interfaces.SecretBrowseIdentity{}, err
	}
	defer zero(credential)
	configuration.Auth.BootstrapCredential = credential
	return d.client.Identity(ctx, configuration)
}

// BrowseRuntimeSecretProvider lists one level of a project Vault/OpenBao
// storage: mounts, then folders and secrets, then the fields of a secret.
func (d *VaultAccessKeyDeserializer) BrowseRuntimeSecretProvider(
	ctx context.Context,
	projectID int,
	storageID int,
	location pro_interfaces.SecretBrowseLocation,
) ([]pro_interfaces.SecretBrowseNode, error) {
	if err := d.requireCapability(ctx, pro_interfaces.CapabilityAccessExecute); err != nil {
		return nil, err
	}
	configuration, credential, err := d.configuration(projectID, storageID)
	if err != nil {
		return nil, err
	}
	defer zero(credential)
	configuration.Auth.BootstrapCredential = credential
	return pro_interfaces.BrowseKV(ctx, d.client, configuration, location)
}

var _ pro_interfaces.RuntimeSecretBrowser = (*VaultAccessKeyDeserializer)(nil)
