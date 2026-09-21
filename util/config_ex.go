package util

import (
	"gopkg.in/natefinch/lumberjack.v2"
)

type DebugLogType struct {
	// Enabled turns on structured debug file export. Requires format=json and
	// logger.filename; log.debug_filter selects the captured components.
	Enabled bool `json:"enabled" env:"SEMAPHORE_DEBUG_LOG_ENABLED"`
	// Format must be json. Empty or other formats are unsupported by the
	// selected structured file writer.
	Format string `json:"format,omitempty" env:"SEMAPHORE_DEBUG_LOG_FORMAT"`
	// Logger configures the debug destination. Requires an absolute, normalized
	// filename; see [Logs](/admin-guide/logs#logger-options) for rotation options.
	Logger *lumberjack.Logger `json:"logger,omitempty" env:"SEMAPHORE_DEBUG_LOGGER"`
}

// GlobalCredentialProviderConfig is global connection/auth metadata for the
// execution-only credential resolver. Bootstrap credentials intentionally do
// not appear here and are read only from the derived process environment name.
type GlobalCredentialProviderConfig struct {
	Type             string `json:"type"`
	URL              string `json:"url"`
	Namespace        string `json:"namespace,omitempty"`
	CACertificate    string `json:"ca_certificate,omitempty"`
	Timeout          string `json:"timeout,omitempty"`
	MaxResponseBytes int64  `json:"max_response_bytes,omitempty"`
	AuthMethod       string `json:"auth_method,omitempty"`
	AuthMount        string `json:"auth_mount,omitempty"`
	RoleID           string `json:"role_id,omitempty"`
	Role             string `json:"role,omitempty"`
}
