package util

type OidcProvider struct {
	ClientID         string       `json:"client_id"`
	ClientIDFile     string       `json:"client_id_file"`
	ClientSecret     string       `json:"client_secret"`
	ClientSecretFile string       `json:"client_secret_file"`
	RedirectURL      string       `json:"redirect_url"`
	Scopes           []string     `json:"scopes"`
	DisplayName      string       `json:"display_name"`
	Color            string       `json:"color"`
	Icon             string       `json:"icon"`
	AutoDiscovery    string       `json:"provider_url"`
	Endpoint         oidcEndpoint `json:"endpoint"`
	UsernameClaim    string       `json:"username_claim" default:"preferred_username"`
	NameClaim        string       `json:"name_claim" default:"preferred_username"`
	EmailClaim       string       `json:"email_claim" default:"email"`
	// GroupClaimPath selects the bounded OIDC claim path used for role mapping.
	GroupClaimPath string `json:"group_claim_path,omitempty"`
	// GroupClaimCaseInsensitive lowercases mapped group values before matching.
	// It defaults to false.
	GroupClaimCaseInsensitive bool   `json:"group_claim_case_insensitive,omitempty"`
	GroupClaimMissingPolicy   string `json:"group_claim_missing_policy,omitempty" default:"preserve"`
	Order                     int    `json:"order"`

	// ReturnViaState passes the return path via the OAuth state parameter. It
	// defaults to true; normal default loading currently treats false as unset.
	ReturnViaState bool `json:"return_via_state" default:"true"`

	// RequireVerifiedEmail requires email_verified=true before email matching.
	// When false, an absent claim is accepted but an explicit false is rejected.
	RequireVerifiedEmail bool `json:"require_verified_email"`
}

type ClaimsProvider interface {
	GetUsernameClaim() string
	GetEmailClaim() string
	GetNameClaim() string
}

func (p *OidcProvider) GetUsernameClaim() string {
	return p.UsernameClaim
}

func (p *OidcProvider) GetEmailClaim() string {
	return p.EmailClaim
}

func (p *OidcProvider) GetNameClaim() string {
	return p.NameClaim
}
