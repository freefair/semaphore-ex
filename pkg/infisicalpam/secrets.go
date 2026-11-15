package infisicalpam

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
)

// SecretRequest names one secret of an Infisical project environment.
type SecretRequest struct {
	ProjectID   string
	Environment string
	// Path is the folder, "/" for the root. It is always absolute.
	Path string
	Name string
	// Version pins a version; 0 reads the current one.
	Version int
}

func (r SecretRequest) validate() error {
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`).MatchString(r.ProjectID) {
		return errors.New("project ID is invalid")
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`).MatchString(r.Environment) {
		return errors.New("environment slug is invalid")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,254}$`).MatchString(r.Name) {
		return errors.New("secret name is invalid")
	}
	if r.Version < 0 {
		return errors.New("version is invalid")
	}
	return validateSecretPath(r.Path)
}

// Secret is one resolved value. Callers must zero Value after use.
type Secret struct {
	Value   []byte
	Version int
}

type secretResponse struct {
	Secret struct {
		SecretKey   string `json:"secretKey"`
		SecretValue string `json:"secretValue"`
		Version     int    `json:"version"`
		// SecretValueHidden is set when the identity may not read the value.
		SecretValueHidden bool `json:"secretValueHidden"`
	} `json:"secret"`
}

// GetSecret reads one secret value with GET /api/v4/secrets/{name}. Secret
// references are expanded and imports included, as in the Infisical UI.
func (c *Client) GetSecret(ctx context.Context, token AccessToken, request SecretRequest) (Secret, error) {
	if err := request.validate(); err != nil {
		return Secret{}, newError(ErrorValidation, "secret", err)
	}
	if token.Value == "" {
		return Secret{}, newError(ErrorAuthentication, "secret", nil)
	}
	query := url.Values{}
	query.Set("projectId", request.ProjectID)
	query.Set("environment", request.Environment)
	query.Set("secretPath", request.Path)
	query.Set("type", "shared")
	query.Set("viewSecretValue", "true")
	query.Set("expandSecretReferences", "true")
	query.Set("includeImports", "true")
	if request.Version > 0 {
		query.Set("version", strconv.Itoa(request.Version))
	}
	var response secretResponse
	if err := c.do(ctx, http.MethodGet, "secret", "/api/v4/secrets/"+url.PathEscape(request.Name), query, token.Value, nil, &response); err != nil {
		return Secret{}, err
	}
	if response.Secret.SecretKey != request.Name || response.Secret.Version <= 0 ||
		(request.Version > 0 && response.Secret.Version != request.Version) {
		return Secret{}, newError(ErrorResponseInvalid, "secret", nil)
	}
	if response.Secret.SecretValueHidden {
		return Secret{}, newError(ErrorPermission, "secret", nil)
	}
	if response.Secret.SecretValue == "" {
		return Secret{}, newError(ErrorNotFound, "secret", errors.New("secret value is empty"))
	}
	return Secret{Value: []byte(response.Secret.SecretValue), Version: response.Secret.Version}, nil
}
