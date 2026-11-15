package infisicalpam

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Browsing lets an administrator pick projects, environments, folders and
// secrets by name instead of copying identifiers. Every call returns names and
// identifiers only: secret listings ask Infisical not to send values, and the
// response types below have no field a value could be decoded into.

const (
	browseProjectPageSize = 100
	browseProjectMaxPages = 20
	browseMaxEntries      = 2000
)

// Organization is the organization a machine identity belongs to.
type Organization struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// Environment of an Infisical project.
type Environment struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// Project the machine identity can access.
type Project struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Slug         string        `json:"slug"`
	Environments []Environment `json:"environments"`
}

// Folder below a path of a project environment.
type Folder struct {
	Name string `json:"name"`
}

// SecretKey names a secret without its value.
type SecretKey struct {
	Key     string `json:"key"`
	Version int    `json:"version"`
}

// FolderRequest names a path of a project environment.
type FolderRequest struct {
	ProjectID   string
	Environment string
	// Path is absolute, "/" for the root.
	Path string
}

func (r FolderRequest) validate() error {
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`).MatchString(r.ProjectID) {
		return errors.New("project ID is invalid")
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`).MatchString(r.Environment) {
		return errors.New("environment slug is invalid")
	}
	return validateSecretPath(r.Path)
}

// validateSecretPath accepts "/" or an absolute path of safe segments.
func validateSecretPath(path string) error {
	if path == "/" {
		return nil
	}
	if !strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") || len(path) > 512 {
		return errors.New("secret path is invalid")
	}
	segment := regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	for part := range strings.SplitSeq(path[1:], "/") {
		if !segment.MatchString(part) {
			return errors.New("secret path is invalid")
		}
	}
	return nil
}

// Organization returns the organization of the token's machine identity. It
// doubles as the connection test: it needs a valid token and nothing else.
func (c *Client) Organization(ctx context.Context, token AccessToken) (Organization, error) {
	if token.Value == "" {
		return Organization{}, newError(ErrorAuthentication, "identity", nil)
	}
	var response struct {
		IdentityDetails struct {
			Organization Organization `json:"organization"`
		} `json:"identityDetails"`
	}
	if err := c.do(ctx, http.MethodGet, "identity", "/api/v1/identities/details", nil, token.Value, nil, &response); err != nil {
		return Organization{}, err
	}
	organization := response.IdentityDetails.Organization
	if organization.ID == "" {
		return Organization{}, newError(ErrorResponseInvalid, "identity", nil)
	}
	return organization, nil
}

// Projects lists the secret-manager projects the identity is a member of,
// with their environments, sorted by name.
func (c *Client) Projects(ctx context.Context, token AccessToken) ([]Project, error) {
	if token.Value == "" {
		return nil, newError(ErrorAuthentication, "projects", nil)
	}
	projects := make([]Project, 0)
	for page := range browseProjectMaxPages {
		query := url.Values{}
		query.Set("type", "secret-manager")
		query.Set("limit", strconv.Itoa(browseProjectPageSize))
		query.Set("offset", strconv.Itoa(page*browseProjectPageSize))
		var response struct {
			Projects   []Project `json:"projects"`
			TotalCount int       `json:"totalCount"`
		}
		if err := c.do(ctx, http.MethodGet, "projects", "/api/v1/projects", query, token.Value, nil, &response); err != nil {
			return nil, err
		}
		for _, project := range response.Projects {
			if project.ID == "" {
				return nil, newError(ErrorResponseInvalid, "projects", nil)
			}
			if project.Name == "" {
				project.Name = project.Slug
			}
			projects = append(projects, project)
		}
		if len(response.Projects) < browseProjectPageSize || len(projects) >= response.TotalCount {
			break
		}
	}
	sort.SliceStable(projects, func(i, j int) bool {
		return strings.ToLower(projects[i].Name) < strings.ToLower(projects[j].Name)
	})
	return projects, nil
}

// Folders lists the folders directly below a path, sorted by name.
func (c *Client) Folders(ctx context.Context, token AccessToken, request FolderRequest) ([]Folder, error) {
	if err := request.validate(); err != nil {
		return nil, newError(ErrorValidation, "folders", err)
	}
	if token.Value == "" {
		return nil, newError(ErrorAuthentication, "folders", nil)
	}
	query := url.Values{}
	query.Set("projectId", request.ProjectID)
	query.Set("environment", request.Environment)
	query.Set("path", request.Path)
	var response struct {
		Folders []Folder `json:"folders"`
	}
	if err := c.do(ctx, http.MethodGet, "folders", "/api/v2/folders", query, token.Value, nil, &response); err != nil {
		return nil, err
	}
	if len(response.Folders) > browseMaxEntries {
		response.Folders = response.Folders[:browseMaxEntries]
	}
	sort.SliceStable(response.Folders, func(i, j int) bool {
		return strings.ToLower(response.Folders[i].Name) < strings.ToLower(response.Folders[j].Name)
	})
	return response.Folders, nil
}

// SecretKeys lists the secrets directly in a path by name and version. The
// request asks Infisical not to send values and the decoded type has no
// value field.
func (c *Client) SecretKeys(ctx context.Context, token AccessToken, request FolderRequest) ([]SecretKey, error) {
	if err := request.validate(); err != nil {
		return nil, newError(ErrorValidation, "secret keys", err)
	}
	if token.Value == "" {
		return nil, newError(ErrorAuthentication, "secret keys", nil)
	}
	query := url.Values{}
	query.Set("projectId", request.ProjectID)
	query.Set("environment", request.Environment)
	query.Set("secretPath", request.Path)
	query.Set("viewSecretValue", "false")
	query.Set("expandSecretReferences", "false")
	query.Set("includeImports", "false")
	query.Set("recursive", "false")
	var response struct {
		Secrets []struct {
			SecretKey string `json:"secretKey"`
			Version   int    `json:"version"`
			Type      string `json:"type"`
		} `json:"secrets"`
	}
	if err := c.do(ctx, http.MethodGet, "secret keys", "/api/v4/secrets", query, token.Value, nil, &response); err != nil {
		return nil, err
	}
	keys := make([]SecretKey, 0, len(response.Secrets))
	for _, secret := range response.Secrets {
		if secret.SecretKey == "" || (secret.Type != "" && secret.Type != "shared") {
			continue
		}
		keys = append(keys, SecretKey{Key: secret.SecretKey, Version: secret.Version})
		if len(keys) >= browseMaxEntries {
			break
		}
	}
	sort.SliceStable(keys, func(i, j int) bool { return strings.ToLower(keys[i].Key) < strings.ToLower(keys[j].Key) })
	return keys, nil
}
