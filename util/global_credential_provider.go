package util

import (
	"errors"
	"regexp"
	"strings"
)

var globalCredentialProviderIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// GlobalCredentialProviderCredentialEnv derives the only permitted bootstrap
// credential environment variable for a configured global provider. The
// normalized suffix is intentionally deterministic; registries must reject
// collisions such as "primary-vault" and "primary_vault".
func GlobalCredentialProviderCredentialEnv(providerID string) (string, error) {
	if !globalCredentialProviderIDPattern.MatchString(providerID) {
		return "", errors.New("global credential provider ID is invalid")
	}
	var normalized strings.Builder
	for _, char := range providerID {
		switch {
		case char >= 'a' && char <= 'z':
			normalized.WriteRune(char - ('a' - 'A'))
		case char >= '0' && char <= '9':
			normalized.WriteRune(char)
		default:
			normalized.WriteByte('_')
		}
	}
	return "SEMAPHORE_GLOBAL_CREDENTIAL_PROVIDER_" + normalized.String() + "_CREDENTIAL", nil
}
