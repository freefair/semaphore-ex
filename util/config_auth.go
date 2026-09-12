package util

import (
	"fmt"
	"math"
	"time"
)

// maxSessionLifeHours is the largest whole-hour duration Go can represent.
// Keeping the limit in hours matches the public configuration field while
// preventing a positive setting from overflowing into a disabled duration.
const maxSessionLifeHours = int64(math.MaxInt64) / int64(time.Hour)

// AuthConfig holds settings that apply to every login method.
type AuthConfig struct {
	// MaxSessionLifeHours is the absolute lifetime of a login session in
	// hours, counted from the moment the user logged in. Once exceeded the
	// session is rejected and expired, even if it was active recently, and the
	// user must log in again. 0 (default) means no absolute limit: sessions
	// then only expire after SessionInactivityTimeout without activity.
	MaxSessionLifeHours int `json:"max_session_life_hours,omitempty" rule:"^[0-9]*$" env:"SEMAPHORE_AUTH_MAX_SESSION_LIFE_HOURS"`
}

// MaxSessionLife returns the absolute session lifetime configured by
// auth.max_session_life_hours, or 0 when sessions have no absolute limit.
// It is safe to call when the auth section is not configured.
func (c *ConfigType) MaxSessionLife() time.Duration {
	if c == nil || c.Auth == nil || c.Auth.MaxSessionLifeHours <= 0 {
		return 0
	}
	if int64(c.Auth.MaxSessionLifeHours) > maxSessionLifeHours {
		// ConfigInit rejects this value. Saturating here keeps callers that
		// construct ConfigType directly fail-closed as well.
		return time.Duration(maxSessionLifeHours) * time.Hour
	}
	return time.Duration(c.Auth.MaxSessionLifeHours) * time.Hour
}

func validateAuthConfig(config *AuthConfig) error {
	if config == nil {
		return nil
	}
	if err := validate(config); err != nil {
		return err
	}
	if int64(config.MaxSessionLifeHours) > maxSessionLifeHours {
		return fmt.Errorf(
			"auth.max_session_life_hours must not exceed %d hours",
			maxSessionLifeHours,
		)
	}
	return nil
}

type RecaptchaConfig struct {
	Enabled string `json:"enabled,omitempty" env:"SEMAPHORE_RECAPTCHA_ENABLED"`
	SiteKey string `json:"site_key,omitempty" env:"SEMAPHORE_RECAPTCHA_SITE_KEY"`
}

type EmailAuthConfig struct {
	Enabled                  bool     `json:"enabled" env:"SEMAPHORE_EMAIL_2TP_ENABLED"`
	AllowLoginAsExternalUser bool     `json:"allow_login_as_external_user" env:"SEMAPHORE_EMAIL_2TP_ALLOW_LOGIN_AS_EXTERNAL_USER"`
	AllowCreateExternalUsers bool     `json:"allow_create_external_user" env:"SEMAPHORE_EMAIL_2TP_ALLOW_CREATE_EXTERNAL_USER"`
	AllowedDomains           []string `json:"allowed_domains" env:"SEMAPHORE_EMAIL_2TP_ALLOWED_DOMAINS"`
	DisableForOidc           bool     `json:"disable_for_oidc" env:"SEMAPHORE_EMAIL_2TP_DISABLE_FOR_OIDC"`
}

type MultifactorAuthConfig struct {
	Totp  *TotpConfig      `json:"totp,omitempty"`
	Email *EmailAuthConfig `json:"email,omitempty"`
}
