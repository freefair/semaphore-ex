package server

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pkg/infisicalpam"
)

const (
	infisicalParamURL              = "url"
	infisicalParamClientID         = "client_id"
	infisicalParamOrganizationSlug = "organization_slug"
	infisicalParamCACertificate    = "ca_certificate"
	infisicalParamSessionDuration  = "session_duration"
	infisicalParamRoutes           = "pam_routes"

	defaultInfisicalSessionDuration = time.Hour
	maximumInfisicalCACertificate   = 64 << 10
	maximumInfisicalRoutes          = 1000
)

// ValidateInfisicalStorage normalizes and validates an Infisical PAM storage.
// The storage is a connection route, not a secret source, so it is always
// read-only and never synchronized.
func ValidateInfisicalStorage(storage *db.SecretStorage) error {
	if storage == nil || storage.Type != db.SecretStorageTypeInfisical {
		return common_errors.NewValidationError("Infisical storage is required")
	}
	if storage.Params == nil {
		storage.Params = db.MapStringAnyField{}
	}
	config, err := infisicalPAMFromParams(storage.Params)
	if err != nil {
		return err
	}
	storage.Params = db.MapStringAnyField{
		infisicalParamURL:             config.SiteURL,
		infisicalParamClientID:        config.ClientID,
		infisicalParamSessionDuration: config.SessionDuration,
		infisicalParamRoutes:          routesParam(config.Routes),
	}
	if config.OrganizationSlug != "" {
		storage.Params[infisicalParamOrganizationSlug] = config.OrganizationSlug
	}
	if config.CACertificate != "" {
		storage.Params[infisicalParamCACertificate] = config.CACertificate
	}
	if storage.Secret == "" && storage.ID == 0 {
		return common_errors.NewValidationError("client secret is required")
	}
	if storage.SyncEnabled || len(storage.SyncPaths) > 0 ||
		(storage.SyncDirection != "" && storage.SyncDirection != db.SecretSyncDirectionReadOnly) {
		return common_errors.NewValidationError("Infisical storage cannot be synchronized")
	}
	storage.SyncDirection = db.SecretSyncDirectionReadOnly
	storage.SyncEnabled = false
	storage.SyncInterval = 0
	storage.ReadOnly = true
	return nil
}

// InfisicalPAMFromStorage builds the value-free task configuration of a
// stored Infisical storage.
func InfisicalPAMFromStorage(storage db.SecretStorage) (db.TaskInfisicalPAM, error) {
	if storage.Type != db.SecretStorageTypeInfisical {
		return db.TaskInfisicalPAM{}, common_errors.NewValidationError("Infisical storage is required")
	}
	config, err := infisicalPAMFromParams(storage.Params)
	if err != nil {
		return db.TaskInfisicalPAM{}, err
	}
	config.StorageID = storage.ID
	return config, nil
}

// ValidateTaskInfisicalPAM checks a configuration received from the server or
// a runner payload before it is used.
func ValidateTaskInfisicalPAM(config db.TaskInfisicalPAM) error {
	params := db.MapStringAnyField{
		infisicalParamURL:              config.SiteURL,
		infisicalParamClientID:         config.ClientID,
		infisicalParamOrganizationSlug: config.OrganizationSlug,
		infisicalParamCACertificate:    config.CACertificate,
		infisicalParamSessionDuration:  config.SessionDuration,
		infisicalParamRoutes:           routesParam(config.Routes),
	}
	_, err := infisicalPAMFromParams(params)
	return err
}

func infisicalPAMFromParams(params db.MapStringAnyField) (db.TaskInfisicalPAM, error) {
	siteURL, err := infisicalpam.ValidateSiteURL(runtimeSecretStringParam(params, infisicalParamURL))
	if err != nil {
		return db.TaskInfisicalPAM{}, common_errors.NewValidationError("Infisical URL must be an https URL")
	}
	config := db.TaskInfisicalPAM{
		SiteURL:          siteURL.String(),
		ClientID:         runtimeSecretStringParam(params, infisicalParamClientID),
		OrganizationSlug: runtimeSecretStringParam(params, infisicalParamOrganizationSlug),
		CACertificate:    runtimeSecretStringParam(params, infisicalParamCACertificate),
	}
	// Client and organization formats are checked by the client constructor;
	// a placeholder secret lets it validate without the real credential.
	if _, err := infisicalpam.NewClient(infisicalpam.Config{
		SiteURL: config.SiteURL, ClientID: config.ClientID, OrganizationSlug: config.OrganizationSlug,
		CACertificate: []byte(config.CACertificate), ClientSecret: []byte("validation"),
	}); err != nil {
		return db.TaskInfisicalPAM{}, common_errors.NewValidationError(infisicalValidationMessage(err))
	}
	if len(config.CACertificate) > maximumInfisicalCACertificate {
		return db.TaskInfisicalPAM{}, common_errors.NewValidationError("CA certificate is too large")
	}
	duration := defaultInfisicalSessionDuration
	if raw := runtimeSecretStringParam(params, infisicalParamSessionDuration); raw != "" {
		duration, err = time.ParseDuration(raw)
		if err != nil || duration < time.Minute || duration > infisicalpam.MaximumSessionDuration {
			return db.TaskInfisicalPAM{}, common_errors.NewValidationError("session duration must be between 1m and 24h")
		}
	}
	config.SessionDuration = duration.String()
	config.Routes, err = routesFromParam(params[infisicalParamRoutes])
	if err != nil {
		return db.TaskInfisicalPAM{}, err
	}
	return config, nil
}

// infisicalValidationMessage exposes the fixed cause text of a client
// validation error, never configuration values.
func infisicalValidationMessage(err error) string {
	if cause := errors.Unwrap(err); cause != nil {
		return "Infisical " + cause.Error()
	}
	return "Infisical connection settings are invalid"
}

func routesFromParam(value any) ([]db.InfisicalPAMRoute, error) {
	if value == nil {
		return []db.InfisicalPAMRoute{}, nil
	}
	items, ok := value.([]any)
	if !ok {
		if typed, typedOK := value.([]map[string]any); typedOK {
			for _, item := range typed {
				items = append(items, item)
			}
		} else {
			return nil, common_errors.NewValidationError("PAM routes are invalid")
		}
	}
	if len(items) > maximumInfisicalRoutes {
		return nil, common_errors.NewValidationError("too many PAM routes")
	}
	routes := make([]db.InfisicalPAMRoute, 0, len(items))
	pamRoutes := make([]infisicalpam.Route, 0, len(items))
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, common_errors.NewValidationError("PAM routes are invalid")
		}
		host, _ := entry["host"].(string)
		account, _ := entry["account"].(string)
		route := db.InfisicalPAMRoute{Host: strings.ToLower(strings.TrimSpace(host)), Account: strings.TrimSpace(account)}
		routes = append(routes, route)
		pamRoutes = append(pamRoutes, infisicalpam.Route{Host: route.Host, AccountPath: route.Account})
	}
	if err := infisicalpam.ValidateRoutes(pamRoutes); err != nil {
		return nil, common_errors.NewValidationError(routeValidationMessage(err))
	}
	return routes, nil
}

func routeValidationMessage(err error) string {
	message := err.Error()
	if cause := errors.Unwrap(err); cause != nil {
		message = cause.Error()
	}
	switch {
	case strings.Contains(message, "mapped twice"):
		return "a PAM route host is mapped twice"
	case strings.Contains(message, "route host"):
		return "a PAM route host must be a host name, for example web-1.example.com"
	default:
		return "a PAM route account must be a folder/account path"
	}
}

func routesParam(routes []db.InfisicalPAMRoute) []any {
	result := make([]any, 0, len(routes))
	for _, route := range routes {
		result = append(result, map[string]any{"host": route.Host, "account": route.Account})
	}
	return result
}

// LoadProjectInfisicalPAM returns the PAM configuration of every Infisical
// storage in the project with at least one route, each with its encrypted
// credential record. Decryption is left to the caller after the task-start
// claim, as for host mapping credentials.
func LoadProjectInfisicalPAM(
	storages db.SecretStorageRepository,
	keys db.AccessKeyManager,
	projectID int,
) ([]db.TaskInfisicalPAM, error) {
	all, err := storages.GetSecretStorages(projectID)
	if err != nil {
		return nil, err
	}
	result := make([]db.TaskInfisicalPAM, 0)
	seenHosts := map[string]int{}
	for _, storage := range all {
		if storage.Type != db.SecretStorageTypeInfisical {
			continue
		}
		config, err := InfisicalPAMFromStorage(storage)
		if err != nil {
			return nil, fmt.Errorf("Infisical storage %q is invalid: %w", storage.Name, err)
		}
		if len(config.Routes) == 0 {
			continue
		}
		for _, route := range config.Routes {
			if other, exists := seenHosts[route.Host]; exists && other != storage.ID {
				return nil, fmt.Errorf("host %q is routed by more than one Infisical storage", route.Host)
			}
			seenHosts[route.Host] = storage.ID
		}
		credentials, err := keys.GetAccessKeys(projectID, db.GetAccessKeyOptions{
			Owner: db.AccessKeySecretStorage, StorageID: &storage.ID,
		}, db.RetrieveQueryParams{})
		if err != nil {
			return nil, err
		}
		if len(credentials) != 1 {
			return nil, fmt.Errorf("Infisical storage %q has no client secret", storage.Name)
		}
		config.CredentialKeyID = credentials[0].ID
		config.Credential = credentials[0]
		result = append(result, config)
	}
	return result, nil
}
