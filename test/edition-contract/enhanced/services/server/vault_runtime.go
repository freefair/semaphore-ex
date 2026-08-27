package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

const (
	defaultProviderTimeout      = 5 * time.Second
	maximumProviderTimeout      = 30 * time.Second
	defaultProviderResponseSize = int64(1 << 20)
	maximumProviderResponseSize = int64(4 << 20)
)

type VaultStorageTokenDeserializer interface {
	DeserializeSecret(key *db.AccessKey) error
}

type VaultAccessKeyDeserializer struct {
	accessKeys         db.AccessKeyManager
	storages           db.SecretStorageRepository
	credentialReader   VaultStorageTokenDeserializer
	capabilityProvider pro_interfaces.CapabilityProvider
	client             *vaultOpenBaoClient
}

func NewVaultAccessKeyDeserializer(
	accessKeys db.AccessKeyManager,
	storages db.SecretStorageRepository,
	credentialReader VaultStorageTokenDeserializer,
	providers ...pro_interfaces.CapabilityProvider,
) *VaultAccessKeyDeserializer {
	var provider pro_interfaces.CapabilityProvider
	if len(providers) > 0 {
		provider = providers[0]
	}
	return &VaultAccessKeyDeserializer{
		accessKeys: accessKeys, storages: storages, credentialReader: credentialReader,
		capabilityProvider: provider, client: newVaultOpenBaoClient(),
	}
}

func (d *VaultAccessKeyDeserializer) DeleteSecret(_ *db.AccessKey) error {
	return errors.New("runtime secret providers are read-only")
}

func (d *VaultAccessKeyDeserializer) SerializeSecret(_ *db.AccessKey) error {
	return errors.New("runtime secret providers are read-only")
}

func (d *VaultAccessKeyDeserializer) DeserializeSecret(key *db.AccessKey) (string, error) {
	if key == nil || key.ProjectID == nil || key.SourceStorageID == nil || key.SourceStorageKey == nil {
		return "", providerError(pro_interfaces.SecretProviderErrorValidation, "resolve")
	}
	reference, err := pro_interfaces.DecodeSecretReference(*key.SourceStorageKey)
	if err != nil || reference.StorageID != *key.SourceStorageID {
		return "", providerError(pro_interfaces.SecretProviderErrorValidation, "resolve")
	}
	value, err := d.ResolveRuntimeSecret(context.Background(), *key.ProjectID, reference)
	if err != nil {
		return "", err
	}
	defer zero(value)
	return string(value), nil
}

func (d *VaultAccessKeyDeserializer) ResolveRuntimeSecret(
	ctx context.Context,
	projectID int,
	reference pro_interfaces.SecretReference,
) ([]byte, error) {
	if err := d.requireCapability(ctx, pro_interfaces.CapabilityAccessExecute); err != nil {
		return nil, err
	}
	if err := reference.Validate(); err != nil {
		return nil, providerError(pro_interfaces.SecretProviderErrorValidation, "resolve")
	}
	configuration, credential, err := d.configuration(projectID, reference.StorageID)
	if err != nil {
		return nil, err
	}
	defer zero(credential)
	configuration.Auth.BootstrapCredential = credential
	return d.client.ReadKV(ctx, configuration, reference)
}

func (d *VaultAccessKeyDeserializer) TestRuntimeSecretProvider(
	ctx context.Context,
	projectID int,
	storageID int,
) (pro_interfaces.SecretProviderHealth, error) {
	checkedAt := d.client.now().UTC()
	if err := d.requireCapability(ctx, pro_interfaces.CapabilityAccessExecute); err != nil {
		return pro_interfaces.SecretProviderHealth{
			StorageID: storageID, State: pro_interfaces.SecretProviderHealthFailed,
			ErrorCategory: pro_interfaces.SecretProviderErrorCapabilityDisabled, CheckedAt: checkedAt,
		}, err
	}
	configuration, credential, err := d.configuration(projectID, storageID)
	if err != nil {
		category := errorCategory(err)
		return pro_interfaces.SecretProviderHealth{
			StorageID: storageID, State: pro_interfaces.SecretProviderHealthFailed,
			ErrorCategory: category, CheckedAt: checkedAt,
		}, err
	}
	defer zero(credential)
	configuration.Auth.BootstrapCredential = credential
	return d.client.TestConnection(ctx, configuration)
}

func (d *VaultAccessKeyDeserializer) requireCapability(
	ctx context.Context,
	access pro_interfaces.CapabilityAccess,
) error {
	if d.capabilityProvider == nil {
		return providerError(pro_interfaces.SecretProviderErrorCapabilityDisabled, "authorize")
	}
	snapshot, err := d.capabilityProvider.Resolve(ctx, pro_interfaces.CapabilityRequest{
		IsAdmin: true, At: d.client.now().UTC(),
	})
	if err != nil {
		return providerError(pro_interfaces.SecretProviderErrorUnavailable, "authorize")
	}
	if err = snapshot.Require(pro_interfaces.CapabilityRuntimeSecrets, access); err != nil {
		return providerError(pro_interfaces.SecretProviderErrorCapabilityDisabled, "authorize")
	}
	return nil
}

func (d *VaultAccessKeyDeserializer) configuration(
	projectID int,
	storageID int,
) (pro_interfaces.SecretProviderConfiguration, []byte, error) {
	if projectID <= 0 || storageID <= 0 || d.storages == nil || d.accessKeys == nil || d.credentialReader == nil {
		return pro_interfaces.SecretProviderConfiguration{}, nil,
			providerError(pro_interfaces.SecretProviderErrorValidation, "configure")
	}
	storage, err := d.storages.GetSecretStorage(projectID, storageID)
	if err != nil || storage.ProjectID != projectID {
		return pro_interfaces.SecretProviderConfiguration{}, nil,
			providerError(pro_interfaces.SecretProviderErrorValidation, "configure")
	}
	configuration, err := parseProviderConfiguration(storage)
	if err != nil {
		return pro_interfaces.SecretProviderConfiguration{}, nil, err
	}
	keys, err := d.accessKeys.GetAccessKeys(projectID, db.GetAccessKeyOptions{
		Owner: db.AccessKeySecretStorage, StorageID: &storageID,
	}, db.RetrieveQueryParams{})
	if err != nil || len(keys) != 1 {
		return pro_interfaces.SecretProviderConfiguration{}, nil,
			providerError(pro_interfaces.SecretProviderErrorAuthentication, "authenticate")
	}
	credentialKey := keys[0]
	if err = d.credentialReader.DeserializeSecret(&credentialKey); err != nil || credentialKey.String == "" {
		return pro_interfaces.SecretProviderConfiguration{}, nil,
			providerError(pro_interfaces.SecretProviderErrorAuthentication, "authenticate")
	}
	credential := []byte(credentialKey.String)
	credentialKey.String = ""
	return configuration, credential, nil
}

func parseProviderConfiguration(storage db.SecretStorage) (pro_interfaces.SecretProviderConfiguration, error) {
	providerType := pro_interfaces.SecretProviderType(storage.Type)
	if providerType != pro_interfaces.SecretProviderVault && providerType != pro_interfaces.SecretProviderOpenBao {
		return pro_interfaces.SecretProviderConfiguration{}, providerError(
			pro_interfaces.SecretProviderErrorValidation, "configure",
		)
	}
	params := storage.Params
	address := stringParam(params, "url")
	if err := validateProviderAddress(address); err != nil {
		return pro_interfaces.SecretProviderConfiguration{}, err
	}
	if boolParam(params, "tls_skip_verify") {
		return pro_interfaces.SecretProviderConfiguration{}, providerError(
			pro_interfaces.SecretProviderErrorValidation, "configure",
		)
	}
	mount := stringParam(params, "mount")
	if mount == "" {
		mount = "secret"
	}
	timeout := defaultProviderTimeout
	if configured := stringParam(params, "timeout"); configured != "" {
		parsed, err := time.ParseDuration(configured)
		if err != nil || parsed <= 0 || parsed > maximumProviderTimeout {
			return pro_interfaces.SecretProviderConfiguration{}, providerError(
				pro_interfaces.SecretProviderErrorValidation, "configure",
			)
		}
		timeout = parsed
	}
	responseSize := int64Param(params, "max_response_bytes")
	if responseSize == 0 {
		responseSize = defaultProviderResponseSize
	}
	if responseSize < 1 || responseSize > maximumProviderResponseSize {
		return pro_interfaces.SecretProviderConfiguration{}, providerError(
			pro_interfaces.SecretProviderErrorValidation, "configure",
		)
	}
	authMethod := pro_interfaces.SecretProviderAuthMethod(stringParam(params, "auth_method"))
	if authMethod == "" {
		authMethod = pro_interfaces.SecretProviderAuthToken
	}
	authMount := stringParam(params, "auth_mount")
	switch authMethod {
	case pro_interfaces.SecretProviderAuthToken:
	case pro_interfaces.SecretProviderAuthAppRole:
		if authMount == "" {
			authMount = "approle"
		}
		if stringParam(params, "role_id") == "" {
			return pro_interfaces.SecretProviderConfiguration{}, providerError(
				pro_interfaces.SecretProviderErrorValidation, "configure",
			)
		}
	case pro_interfaces.SecretProviderAuthKubernetes:
		if authMount == "" {
			authMount = "kubernetes"
		}
		if stringParam(params, "role") == "" {
			return pro_interfaces.SecretProviderConfiguration{}, providerError(
				pro_interfaces.SecretProviderErrorValidation, "configure",
			)
		}
	default:
		return pro_interfaces.SecretProviderConfiguration{}, providerError(
			pro_interfaces.SecretProviderErrorValidation, "configure",
		)
	}
	if authMount != "" && !safeURLSegment(authMount) {
		return pro_interfaces.SecretProviderConfiguration{}, providerError(
			pro_interfaces.SecretProviderErrorValidation, "configure",
		)
	}
	return pro_interfaces.SecretProviderConfiguration{
		StorageID: storage.ID, ProjectID: storage.ProjectID, Type: providerType,
		Address: address, Namespace: stringParam(params, "namespace"), DefaultMount: mount,
		CACertificate: []byte(stringParam(params, "ca_certificate")), Timeout: timeout,
		MaxResponseSize: responseSize,
		Auth: pro_interfaces.SecretProviderAuth{
			Method: authMethod, Mount: authMount, RoleID: stringParam(params, "role_id"),
			Role: stringParam(params, "role"),
		},
	}, nil
}

func validateProviderAddress(address string) error {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return providerError(pro_interfaces.SecretProviderErrorValidation, "configure")
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		host := parsed.Hostname()
		ip := net.ParseIP(host)
		if host == "localhost" || (ip != nil && ip.IsLoopback()) {
			return nil
		}
	}
	return providerError(pro_interfaces.SecretProviderErrorValidation, "configure")
}

type cachedProviderToken struct {
	value     []byte
	expiresAt time.Time
	renewable bool
	lease     time.Duration
}

type vaultOpenBaoClient struct {
	cacheMu sync.Mutex
	cache   map[int]cachedProviderToken
	now     func() time.Time
}

func newVaultOpenBaoClient() *vaultOpenBaoClient {
	return &vaultOpenBaoClient{cache: map[int]cachedProviderToken{}, now: time.Now}
}

func (c *vaultOpenBaoClient) ReadKV(
	ctx context.Context,
	configuration pro_interfaces.SecretProviderConfiguration,
	reference pro_interfaces.SecretReference,
) ([]byte, error) {
	if err := reference.Validate(); err != nil || reference.StorageID != configuration.StorageID {
		return nil, providerError(pro_interfaces.SecretProviderErrorValidation, "read")
	}
	token, _, err := c.token(ctx, configuration)
	if err != nil {
		return nil, err
	}
	defer zero(token)
	query := url.Values{}
	if reference.Version > 0 {
		query.Set("version", strconv.Itoa(reference.Version))
	}
	var response struct {
		Data struct {
			Data map[string]any `json:"data"`
		} `json:"data"`
	}
	endpoint := "/v1/" + url.PathEscape(reference.Mount) + "/data/" + escapeSecretPath(reference.Path)
	if err = c.doJSON(ctx, configuration, http.MethodGet, endpoint, query, token, nil, &response); err != nil {
		return nil, err
	}
	value, ok := response.Data.Data[reference.Field]
	if !ok {
		return nil, providerError(pro_interfaces.SecretProviderErrorFieldMissing, "read")
	}
	text, ok := value.(string)
	if !ok {
		return nil, providerError(pro_interfaces.SecretProviderErrorResponseInvalid, "read")
	}
	return []byte(text), nil
}

func (c *vaultOpenBaoClient) TestConnection(
	ctx context.Context,
	configuration pro_interfaces.SecretProviderConfiguration,
) (pro_interfaces.SecretProviderHealth, error) {
	started := c.now()
	health := pro_interfaces.SecretProviderHealth{StorageID: configuration.StorageID, CheckedAt: started.UTC()}
	token, cached, err := c.token(ctx, configuration)
	if err == nil {
		defer zero(token)
		var response map[string]any
		err = c.doJSON(ctx, configuration, http.MethodGet, "/v1/auth/token/lookup-self", nil, token, nil, &response)
	}
	health.LatencyMillis = c.now().Sub(started).Milliseconds()
	if err != nil {
		health.State = pro_interfaces.SecretProviderHealthFailed
		health.ErrorCategory = errorCategory(err)
		return health, err
	}
	health.State = pro_interfaces.SecretProviderHealthHealthy
	if cached != nil {
		expiresAt := cached.expiresAt.UTC()
		health.TokenExpiresAt = &expiresAt
		health.TokenRenewable = cached.renewable
	}
	return health, nil
}

func (c *vaultOpenBaoClient) token(
	ctx context.Context,
	configuration pro_interfaces.SecretProviderConfiguration,
) ([]byte, *cachedProviderToken, error) {
	if configuration.Auth.Method == pro_interfaces.SecretProviderAuthToken {
		if len(configuration.Auth.BootstrapCredential) == 0 {
			return nil, nil, providerError(pro_interfaces.SecretProviderErrorAuthentication, "authenticate")
		}
		return append([]byte(nil), configuration.Auth.BootstrapCredential...), nil, nil
	}
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	now := c.now()
	if cached, ok := c.cache[configuration.StorageID]; ok {
		renewAt := cached.expiresAt.Add(-cached.lease / 5)
		if now.Before(renewAt) {
			copy := cached
			return append([]byte(nil), cached.value...), &copy, nil
		}
		if cached.renewable && now.Before(cached.expiresAt) {
			renewed, err := c.renew(ctx, configuration, cached)
			if err == nil {
				c.replaceCachedToken(configuration.StorageID, renewed)
				copy := renewed
				return append([]byte(nil), renewed.value...), &copy, nil
			}
		}
		zero(cached.value)
		delete(c.cache, configuration.StorageID)
	}
	loggedIn, err := c.login(ctx, configuration)
	if err != nil {
		return nil, nil, err
	}
	c.replaceCachedToken(configuration.StorageID, loggedIn)
	copy := loggedIn
	return append([]byte(nil), loggedIn.value...), &copy, nil
}

func (c *vaultOpenBaoClient) login(
	ctx context.Context,
	configuration pro_interfaces.SecretProviderConfiguration,
) (cachedProviderToken, error) {
	var payload map[string]string
	switch configuration.Auth.Method {
	case pro_interfaces.SecretProviderAuthAppRole:
		payload = map[string]string{
			"role_id":   configuration.Auth.RoleID,
			"secret_id": string(configuration.Auth.BootstrapCredential),
		}
	case pro_interfaces.SecretProviderAuthKubernetes:
		payload = map[string]string{
			"role": configuration.Auth.Role,
			"jwt":  string(configuration.Auth.BootstrapCredential),
		}
	default:
		return cachedProviderToken{}, providerError(pro_interfaces.SecretProviderErrorAuthentication, "authenticate")
	}
	var response authResponse
	endpoint := "/v1/auth/" + url.PathEscape(configuration.Auth.Mount) + "/login"
	if err := c.doJSON(ctx, configuration, http.MethodPost, endpoint, nil, nil, payload, &response); err != nil {
		return cachedProviderToken{}, err
	}
	return c.tokenFromAuth(response)
}

func (c *vaultOpenBaoClient) renew(
	ctx context.Context,
	configuration pro_interfaces.SecretProviderConfiguration,
	cached cachedProviderToken,
) (cachedProviderToken, error) {
	var response authResponse
	if err := c.doJSON(ctx, configuration, http.MethodPost, "/v1/auth/token/renew-self", nil,
		cached.value, map[string]string{}, &response); err != nil {
		return cachedProviderToken{}, err
	}
	if response.Auth.ClientToken == "" {
		response.Auth.ClientToken = string(cached.value)
	}
	return c.tokenFromAuth(response)
}

type authResponse struct {
	Auth struct {
		ClientToken   string `json:"client_token"`
		LeaseDuration int64  `json:"lease_duration"`
		Renewable     bool   `json:"renewable"`
	} `json:"auth"`
}

func (c *vaultOpenBaoClient) tokenFromAuth(response authResponse) (cachedProviderToken, error) {
	if response.Auth.ClientToken == "" || response.Auth.LeaseDuration <= 0 {
		return cachedProviderToken{}, providerError(pro_interfaces.SecretProviderErrorResponseInvalid, "authenticate")
	}
	lease := time.Duration(response.Auth.LeaseDuration) * time.Second
	return cachedProviderToken{
		value: []byte(response.Auth.ClientToken), expiresAt: c.now().Add(lease),
		renewable: response.Auth.Renewable, lease: lease,
	}, nil
}

func (c *vaultOpenBaoClient) replaceCachedToken(storageID int, next cachedProviderToken) {
	if previous, ok := c.cache[storageID]; ok {
		zero(previous.value)
	}
	c.cache[storageID] = next
}

func (c *vaultOpenBaoClient) doJSON(
	ctx context.Context,
	configuration pro_interfaces.SecretProviderConfiguration,
	method string,
	endpoint string,
	query url.Values,
	token []byte,
	payload any,
	result any,
) error {
	client, err := providerHTTPClient(configuration)
	if err != nil {
		return err
	}
	base, _ := url.Parse(configuration.Address)
	base.Path = endpoint
	if query != nil {
		base.RawQuery = query.Encode()
	}
	var body io.Reader
	if payload != nil {
		encoded, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return providerError(pro_interfaces.SecretProviderErrorValidation, "request")
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, base.String(), body)
	if err != nil {
		return providerError(pro_interfaces.SecretProviderErrorValidation, "request")
	}
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if len(token) > 0 {
		request.Header.Set("X-Vault-Token", string(token))
	}
	if configuration.Namespace != "" {
		request.Header.Set("X-Vault-Namespace", configuration.Namespace)
	}
	response, err := client.Do(request)
	if err != nil {
		return classifyTransportError(err)
	}
	defer response.Body.Close() //nolint:errcheck
	limited := io.LimitReader(response.Body, configuration.MaxResponseSize+1)
	content, err := io.ReadAll(limited)
	if err != nil {
		return providerError(pro_interfaces.SecretProviderErrorUnavailable, "response")
	}
	if int64(len(content)) > configuration.MaxResponseSize {
		return providerError(pro_interfaces.SecretProviderErrorResponseTooLarge, "response")
	}
	switch response.StatusCode {
	case http.StatusOK, http.StatusNoContent:
	case http.StatusUnauthorized:
		return providerError(pro_interfaces.SecretProviderErrorAuthentication, "request")
	case http.StatusForbidden:
		return providerError(pro_interfaces.SecretProviderErrorPermission, "request")
	default:
		if response.StatusCode >= 500 {
			return providerError(pro_interfaces.SecretProviderErrorUnavailable, "request")
		}
		return providerError(pro_interfaces.SecretProviderErrorResponseInvalid, "request")
	}
	if result == nil || len(content) == 0 {
		return nil
	}
	if err = json.Unmarshal(content, result); err != nil {
		return providerError(pro_interfaces.SecretProviderErrorResponseInvalid, "response")
	}
	return nil
}

func providerHTTPClient(configuration pro_interfaces.SecretProviderConfiguration) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if len(configuration.CACertificate) > 0 {
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(configuration.CACertificate) {
			return nil, providerError(pro_interfaces.SecretProviderErrorValidation, "configure")
		}
		tlsConfig.RootCAs = roots
	}
	transport.TLSClientConfig = tlsConfig
	transport.ResponseHeaderTimeout = configuration.Timeout
	return &http.Client{
		Transport: transport,
		Timeout:   configuration.Timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("redirects are disabled")
		},
	}, nil
}

func classifyTransportError(err error) error {
	var netError net.Error
	var unknownAuthority x509.UnknownAuthorityError
	var certificateInvalid x509.CertificateInvalidError
	switch {
	case errors.As(err, &unknownAuthority), errors.As(err, &certificateInvalid), strings.Contains(err.Error(), "tls"):
		return providerError(pro_interfaces.SecretProviderErrorTLS, "request")
	case errors.As(err, &netError) && netError.Timeout():
		return providerError(pro_interfaces.SecretProviderErrorTimeout, "request")
	default:
		return providerError(pro_interfaces.SecretProviderErrorUnavailable, "request")
	}
}

func providerError(category pro_interfaces.SecretProviderErrorCategory, operation string) error {
	return pro_interfaces.SecretProviderError{Category: category, Operation: operation}
}

func errorCategory(err error) pro_interfaces.SecretProviderErrorCategory {
	var providerErr pro_interfaces.SecretProviderError
	if errors.As(err, &providerErr) {
		return providerErr.Category
	}
	return pro_interfaces.SecretProviderErrorUnavailable
}

func stringParam(params db.MapStringAnyField, key string) string {
	value, _ := params[key].(string)
	return strings.TrimSpace(value)
}

func boolParam(params db.MapStringAnyField, key string) bool {
	value, _ := params[key].(bool)
	return value
}

func int64Param(params db.MapStringAnyField, key string) int64 {
	switch value := params[key].(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	default:
		return 0
	}
}

func safeURLSegment(value string) bool {
	if value == "" || strings.ContainsAny(value, "/?#") || value == "." || value == ".." {
		return false
	}
	return true
}

func escapeSecretPath(value string) string {
	parts := strings.Split(value, "/")
	for index := range parts {
		parts[index] = url.PathEscape(parts[index])
	}
	return strings.Join(parts, "/")
}

func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

var _ pro_interfaces.RuntimeSecretResolver = (*VaultAccessKeyDeserializer)(nil)
var _ pro_interfaces.VaultOpenBaoClient = (*vaultOpenBaoClient)(nil)
