package features

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	"golang.org/x/crypto/bcrypt"
)

const (
	ldapReadinessMaxAge = 15 * time.Minute
	ldapFailureWindow   = 5 * time.Minute
	ldapFailureBlock    = 5 * time.Minute
	ldapMaxFailures     = 5
)

var ldapProviderIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

type ldapService struct {
	repository db.Store
	client     pro_interfaces.LDAPClient
}

func NewLDAPService(
	repository db.Store,
	_ pro_interfaces.CapabilityProvider,
	client pro_interfaces.LDAPClient,
) pro_interfaces.LDAPService {
	return &ldapService{repository: repository, client: client}
}

func (s *ldapService) Initialize(context.Context) error {
	providers, err := s.repository.GetLDAPProviders()
	if err != nil {
		return fmt.Errorf("load LDAP providers: %w", err)
	}
	if len(providers) != 0 && !util.Config.OptionEncryptionEnabled() {
		return errors.New("LDAP providers require option or access-key encryption")
	}
	for _, provider := range providers {
		secret, decryptErr := util.Config.DecryptOption(provider.EncryptedBindPassword)
		zeroLDAPBytes(secret)
		if decryptErr != nil {
			return fmt.Errorf("decrypt LDAP provider %q bind credential: %w", provider.ID, decryptErr)
		}
	}
	return nil
}

func (s *ldapService) LoginProviders(context.Context) ([]pro_interfaces.LDAPLoginProvider, error) {
	providers, err := s.repository.GetLDAPProviders()
	if err != nil {
		return nil, fmt.Errorf("load LDAP login providers: %w", err)
	}
	result := make([]pro_interfaces.LDAPLoginProvider, 0, len(providers))
	for _, provider := range providers {
		state := pro_interfaces.LDAPState(provider.State)
		if state != pro_interfaces.LDAPStateActive && state != pro_interfaces.LDAPStateSelectedUsers {
			continue
		}
		result = append(result, pro_interfaces.LDAPLoginProvider{
			ID: provider.ID, Name: provider.DisplayName, State: state,
		})
	}
	return result, nil
}

func (s *ldapService) AllowLocalRecovery(_ context.Context, login string) (bool, error) {
	providers, err := s.repository.GetLDAPProviders()
	if err != nil {
		return false, err
	}
	login = strings.ToLower(strings.TrimSpace(login))
	for _, provider := range providers {
		state := pro_interfaces.LDAPState(provider.State)
		if (state != pro_interfaces.LDAPStateActive && state != pro_interfaces.LDAPStateSelectedUsers) ||
			provider.RecoveryAdminUserID == nil {
			continue
		}
		user, userErr := s.repository.GetUser(*provider.RecoveryAdminUserID)
		if userErr != nil {
			return false, userErr
		}
		if !user.External && user.Admin &&
			(strings.EqualFold(user.Username, login) || strings.EqualFold(user.Email, login)) {
			return true, nil
		}
	}
	return false, nil
}

func (s *ldapService) Authenticate(
	ctx context.Context,
	request pro_interfaces.LDAPAuthenticationRequest,
) (db.User, error) {
	if request.Now.IsZero() {
		return db.User{}, common_errors.NewValidationError("LDAP authentication time is required")
	}
	provider, err := s.repository.GetLDAPProvider(request.ProviderID)
	if errors.Is(err, db.ErrNotFound) {
		return db.User{}, pro_interfaces.ErrLDAPProviderNotFound
	}
	if err != nil {
		return db.User{}, err
	}
	state := pro_interfaces.LDAPState(provider.State)
	if state != pro_interfaces.LDAPStateActive && state != pro_interfaces.LDAPStateSelectedUsers {
		return db.User{}, pro_interfaces.ErrLDAPDisabled
	}

	subjectHash := ldapSubjectHash(request.Username)
	if blocked, blockErr := s.isBlocked(provider.ID, subjectHash, request.Now); blockErr != nil {
		return db.User{}, blockErr
	} else if blocked {
		return db.User{}, pro_interfaces.ErrLDAPThrottled
	}
	clientConfiguration, cleanup, err := s.clientConfiguration(provider)
	if err != nil {
		return db.User{}, err
	}
	defer cleanup()
	clientResult, err := s.client.Authenticate(ctx, pro_interfaces.NewLDAPClientRequest(
		clientConfiguration, request.Username, request.Password,
	))
	if err != nil {
		if errors.Is(err, pro_interfaces.ErrLDAPInvalidCredentials) {
			attempt, recordErr := s.repository.RecordLDAPAuthFailure(
				provider.ID, subjectHash, request.Now,
				ldapFailureWindow, ldapMaxFailures, ldapFailureBlock,
			)
			if recordErr != nil {
				return db.User{}, recordErr
			}
			if attempt.BlockedUntil != nil && request.Now.Before(*attempt.BlockedUntil) {
				return db.User{}, pro_interfaces.ErrLDAPThrottled
			}
		}
		return db.User{}, err
	}
	user, err := s.resolveIdentity(provider.ID, clientResult.Identity, state)
	if err != nil {
		return db.User{}, err
	}
	if err = s.repository.ClearLDAPAuthFailures(provider.ID, subjectHash); err != nil {
		return db.User{}, err
	}
	if mappings, mappingErr := s.repository.GetLDAPGroupMappings(provider.ID); mappingErr == nil && len(mappings) != 0 {
		userID := user.ID
		_, _ = s.ReconcileGroupMappings(ctx, pro_interfaces.LDAPGroupPreviewRequest{
			ProviderID: provider.ID, Source: "login", UserID: &userID, Now: request.Now,
		})
	}
	return user, nil
}

func (s *ldapService) isBlocked(providerID string, subjectHash string, now time.Time) (bool, error) {
	attempt, err := s.repository.GetLDAPAuthAttempt(providerID, subjectHash)
	if errors.Is(err, db.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return attempt.BlockedUntil != nil && now.Before(*attempt.BlockedUntil), nil
}

func (s *ldapService) Link(ctx context.Context, request pro_interfaces.LDAPLinkRequest) error {
	if request.Now.IsZero() {
		return common_errors.NewValidationError("LDAP link time is required")
	}
	provider, err := s.repository.GetLDAPProvider(request.ProviderID)
	if errors.Is(err, db.ErrNotFound) {
		return pro_interfaces.ErrLDAPProviderNotFound
	}
	if err != nil {
		return err
	}
	state := pro_interfaces.LDAPState(provider.State)
	if state != pro_interfaces.LDAPStateActive && state != pro_interfaces.LDAPStateSelectedUsers {
		return pro_interfaces.ErrLDAPDisabled
	}
	actor, err := s.repository.GetUser(request.ActorID)
	if err != nil {
		return err
	}
	configuration, cleanup, err := s.clientConfiguration(provider)
	if err != nil {
		return err
	}
	defer cleanup()
	result, err := s.client.Authenticate(ctx, pro_interfaces.NewLDAPClientRequest(
		configuration, request.Username, request.Password,
	))
	if err != nil {
		return err
	}
	if !strings.EqualFold(result.Identity.Username, actor.Username) &&
		!strings.EqualFold(result.Identity.Email, actor.Email) {
		return pro_interfaces.ErrLDAPForbidden
	}
	existing, err := s.repository.GetExternalIdentity(
		db.IdentityTypeLdap, provider.ID, result.Identity.ExternalID)
	switch {
	case err == nil && existing.UserID == actor.ID:
		return nil
	case err == nil:
		return pro_interfaces.ErrLDAPIdentityCollision
	case !errors.Is(err, db.ErrNotFound):
		return err
	}
	identities, err := s.repository.GetUserExternalIdentities(actor.ID)
	if err != nil {
		return err
	}
	for _, identity := range identities {
		if identity.Type == db.IdentityTypeLdap && identity.Provider == provider.ID {
			return pro_interfaces.ErrLDAPIdentityCollision
		}
	}
	_, err = s.repository.CreateExternalIdentity(db.UserExternalIdentity{
		UserID: actor.ID, Type: db.IdentityTypeLdap, Provider: provider.ID,
		ExternalUID: result.Identity.ExternalID, Created: request.Now,
	})
	return err
}

func (s *ldapService) Providers(context.Context) ([]pro_interfaces.LDAPProviderConfiguration, error) {
	providers, err := s.repository.GetLDAPProviders()
	if err != nil {
		return nil, err
	}
	result := make([]pro_interfaces.LDAPProviderConfiguration, 0, len(providers))
	for _, provider := range providers {
		configuration, configErr := s.providerConfiguration(provider)
		if configErr != nil {
			return nil, configErr
		}
		result = append(result, configuration)
	}
	return result, nil
}

func (s *ldapService) Configure(
	_ context.Context,
	request pro_interfaces.LDAPConfigureRequest,
) (pro_interfaces.LDAPProviderConfiguration, error) {
	if !request.ActorIsAdmin {
		return pro_interfaces.LDAPProviderConfiguration{}, pro_interfaces.ErrLDAPForbidden
	}
	if request.Now.IsZero() {
		return pro_interfaces.LDAPProviderConfiguration{}, common_errors.NewValidationError("LDAP configuration time is required")
	}
	input := request.Provider
	input.ID = strings.ToLower(strings.TrimSpace(input.ID))
	if !ldapProviderIDPattern.MatchString(input.ID) {
		return pro_interfaces.LDAPProviderConfiguration{}, common_errors.NewValidationError("invalid LDAP provider ID")
	}
	if strings.TrimSpace(input.DisplayName) == "" || len(input.DisplayName) > 255 {
		return pro_interfaces.LDAPProviderConfiguration{}, common_errors.NewValidationError("invalid LDAP display name")
	}
	if !util.Config.OptionEncryptionEnabled() {
		return pro_interfaces.LDAPProviderConfiguration{}, errors.New("LDAP bind credentials require option or access-key encryption")
	}

	existing, existingErr := s.repository.GetLDAPProvider(input.ID)
	if existingErr != nil && !errors.Is(existingErr, db.ErrNotFound) {
		return pro_interfaces.LDAPProviderConfiguration{}, existingErr
	}
	if existingErr == nil && (existing.State == string(pro_interfaces.LDAPStateActive) ||
		existing.State == string(pro_interfaces.LDAPStateSelectedUsers)) {
		return pro_interfaces.LDAPProviderConfiguration{},
			pro_interfaces.ErrLDAPReconfigurationRequiresInactive
	}
	bindSecret := []byte(input.BindPassword)
	if len(bindSecret) == 0 && existingErr == nil {
		bindSecret, existingErr = util.Config.DecryptOption(existing.EncryptedBindPassword)
		if existingErr != nil {
			return pro_interfaces.LDAPProviderConfiguration{}, existingErr
		}
	}
	defer zeroLDAPBytes(bindSecret)
	if len(bindSecret) == 0 {
		return pro_interfaces.LDAPProviderConfiguration{}, common_errors.NewValidationError("LDAP bind password is required")
	}
	clientConfiguration := clientConfigurationFromInput(input, string(bindSecret))
	if err := s.client.Validate(clientConfiguration); err != nil {
		return pro_interfaces.LDAPProviderConfiguration{}, common_errors.NewValidationError(err.Error())
	}
	encryptedSecret, err := util.Config.EncryptOption(bindSecret)
	if err != nil {
		return pro_interfaces.LDAPProviderConfiguration{}, fmt.Errorf("encrypt LDAP bind password: %w", err)
	}
	created := request.Now
	state := string(pro_interfaces.LDAPStateDisabled)
	if existingErr == nil {
		created = existing.Created
		state = existing.State
	}
	provider := db.LDAPProvider{
		ID: input.ID, DisplayName: strings.TrimSpace(input.DisplayName), State: state,
		ServerURL: strings.TrimSpace(input.ServerURL), TLSMode: string(input.TLSMode),
		TrustMode: string(input.TrustMode), CAPEM: strings.TrimSpace(input.CAPEM),
		BindDN: strings.TrimSpace(input.BindDN), SearchBaseDN: strings.TrimSpace(input.SearchBaseDN),
		UserFilter: strings.TrimSpace(input.UserFilter), IdentityAttribute: strings.TrimSpace(input.IdentityAttribute),
		UsernameAttribute: strings.TrimSpace(input.UsernameAttribute), NameAttribute: strings.TrimSpace(input.NameAttribute),
		EmailAttribute:    strings.TrimSpace(input.EmailAttribute),
		GroupSearchBaseDN: strings.TrimSpace(input.GroupSearchBaseDN),
		GroupUserFilter:   strings.TrimSpace(input.GroupUserFilter), GroupFilter: strings.TrimSpace(input.GroupFilter),
		GroupIdentityAttribute: strings.TrimSpace(input.GroupIdentityAttribute),
		GroupMemberAttribute:   strings.TrimSpace(input.GroupMemberAttribute), GroupMaxDepth: input.GroupMaxDepth,
		ReadinessStatus: string(pro_interfaces.LDAPReadinessUntested), ReadinessCode: "configuration_changed",
		Created: created, Updated: request.Now,
	}
	provider.EncryptedBindPassword = encryptedSecret
	if err = s.repository.SaveLDAPProvider(provider); err != nil {
		return pro_interfaces.LDAPProviderConfiguration{}, err
	}
	return s.providerConfiguration(provider)
}

func (s *ldapService) Test(
	ctx context.Context,
	request pro_interfaces.LDAPTestRequest,
) (pro_interfaces.LDAPReadiness, error) {
	if !request.ActorIsAdmin {
		return pro_interfaces.LDAPReadiness{}, pro_interfaces.ErrLDAPForbidden
	}
	if request.Now.IsZero() {
		return pro_interfaces.LDAPReadiness{}, common_errors.NewValidationError("LDAP readiness time is required")
	}
	provider, err := s.repository.GetLDAPProvider(request.ProviderID)
	if errors.Is(err, db.ErrNotFound) {
		return pro_interfaces.LDAPReadiness{}, pro_interfaces.ErrLDAPProviderNotFound
	}
	if err != nil {
		return pro_interfaces.LDAPReadiness{}, err
	}
	configuration, cleanup, err := s.clientConfiguration(provider)
	if err != nil {
		return pro_interfaces.LDAPReadiness{}, err
	}
	defer cleanup()
	result, testErr := s.client.Authenticate(ctx, pro_interfaces.NewLDAPClientRequest(
		configuration, request.Username, request.Password,
	))
	result.Readiness.CheckedAt = &request.Now
	if testErr != nil {
		_ = s.repository.SaveLDAPReadiness(
			provider.ID, string(pro_interfaces.LDAPReadinessFailed),
			result.Readiness.Code, request.Now, nil, provider.ConfigVersion,
		)
		return result.Readiness, testErr
	}
	recoveryAdmin, err := s.repository.GetUser(request.RecoveryAdminUserID)
	if err != nil || recoveryAdmin.External || !recoveryAdmin.Admin ||
		bcrypt.CompareHashAndPassword(
			[]byte(recoveryAdmin.Password), []byte(request.RecoveryAdminPassword),
		) != nil {
		result.Readiness.Status = pro_interfaces.LDAPReadinessFailed
		result.Readiness.Code = "local_recovery_failed"
		_ = s.repository.SaveLDAPReadiness(
			provider.ID, string(result.Readiness.Status), result.Readiness.Code, request.Now, nil,
			provider.ConfigVersion,
		)
		return result.Readiness, pro_interfaces.ErrLDAPReadiness
	}
	result.Readiness.Recovery = true
	result.Readiness.Status = pro_interfaces.LDAPReadinessReady
	result.Readiness.Code = "ready"
	if err = s.repository.SaveLDAPReadiness(
		provider.ID, string(result.Readiness.Status), result.Readiness.Code,
		request.Now, &request.RecoveryAdminUserID, provider.ConfigVersion,
	); err != nil {
		return pro_interfaces.LDAPReadiness{}, err
	}
	return result.Readiness, nil
}

func (s *ldapService) SetState(
	_ context.Context,
	request pro_interfaces.LDAPStateRequest,
) (pro_interfaces.LDAPProviderConfiguration, error) {
	if !request.ActorIsAdmin {
		return pro_interfaces.LDAPProviderConfiguration{}, pro_interfaces.ErrLDAPForbidden
	}
	if request.Now.IsZero() {
		return pro_interfaces.LDAPProviderConfiguration{}, common_errors.NewValidationError("LDAP state change time is required")
	}
	switch request.State {
	case pro_interfaces.LDAPStateDisabled, pro_interfaces.LDAPStateShadow,
		pro_interfaces.LDAPStateSelectedUsers, pro_interfaces.LDAPStateActive:
	default:
		return pro_interfaces.LDAPProviderConfiguration{}, common_errors.NewValidationError("unsupported LDAP state")
	}
	if err := s.repository.ConfigureLDAPProvider(
		request.ProviderID, string(request.State), request.SelectedUserIDs,
		request.ActorID, request.Now, ldapReadinessMaxAge,
	); err != nil {
		if errors.Is(err, db.ErrLDAPReadiness) {
			return pro_interfaces.LDAPProviderConfiguration{}, pro_interfaces.ErrLDAPReadiness
		}
		if errors.Is(err, db.ErrNotFound) {
			return pro_interfaces.LDAPProviderConfiguration{}, pro_interfaces.ErrLDAPProviderNotFound
		}
		return pro_interfaces.LDAPProviderConfiguration{}, err
	}
	provider, err := s.repository.GetLDAPProvider(request.ProviderID)
	if err != nil {
		return pro_interfaces.LDAPProviderConfiguration{}, err
	}
	return s.providerConfiguration(provider)
}

func (s *ldapService) Transitions(
	_ context.Context,
	providerID string,
) ([]db.LDAPCapabilityTransition, error) {
	return s.repository.GetLDAPCapabilityTransitions(providerID)
}

func (s *ldapService) resolveIdentity(
	providerID string,
	identity pro_interfaces.LDAPIdentity,
	state pro_interfaces.LDAPState,
) (db.User, error) {
	existingIdentity, err := s.repository.GetExternalIdentity(
		db.IdentityTypeLdap, providerID, identity.ExternalID)
	if err == nil {
		user, userErr := s.repository.GetUser(existingIdentity.UserID)
		if userErr != nil {
			return db.User{}, userErr
		}
		if state == pro_interfaces.LDAPStateSelectedUsers {
			selected, selectedErr := s.repository.IsLDAPUserSelected(providerID, user.ID)
			if selectedErr != nil {
				return db.User{}, selectedErr
			}
			if !selected {
				return db.User{}, pro_interfaces.ErrLDAPForbidden
			}
		}
		return s.syncLDAPUser(user, identity)
	}
	if !errors.Is(err, db.ErrNotFound) {
		return db.User{}, err
	}
	if state == pro_interfaces.LDAPStateSelectedUsers {
		return db.User{}, pro_interfaces.ErrLDAPForbidden
	}
	if _, err = s.repository.GetUserByLoginOrEmail(identity.Username, identity.Email); err == nil {
		return db.User{}, pro_interfaces.ErrLDAPIdentityCollision
	} else if !errors.Is(err, db.ErrNotFound) {
		return db.User{}, err
	}
	user := db.User{
		Username: identity.Username, Name: identity.Name, Email: identity.Email,
		External: true,
	}
	if err = db.ValidateUser(user); err != nil {
		return db.User{}, pro_interfaces.ErrLDAPIdentityCollision
	}
	user, err = s.repository.CreateUserWithoutPassword(user)
	if err != nil {
		return db.User{}, pro_interfaces.ErrLDAPIdentityCollision
	}
	_, err = s.repository.CreateExternalIdentity(db.UserExternalIdentity{
		UserID: user.ID, Type: db.IdentityTypeLdap, Provider: providerID,
		ExternalUID: identity.ExternalID,
	})
	if err != nil {
		_ = s.repository.DeleteUser(user.ID)
		return db.User{}, pro_interfaces.ErrLDAPIdentityCollision
	}
	return user, nil
}

func (s *ldapService) syncLDAPUser(user db.User, identity pro_interfaces.LDAPIdentity) (db.User, error) {
	if !user.External {
		return user, nil
	}
	if user.Name == identity.Name && user.Email == identity.Email {
		return user, nil
	}
	user.Name = identity.Name
	user.Email = identity.Email
	if err := s.repository.UpdateUser(db.UserWithPwd{User: user}); err != nil {
		return db.User{}, pro_interfaces.ErrLDAPIdentityCollision
	}
	return user, nil
}

func (s *ldapService) providerConfiguration(
	provider db.LDAPProvider,
) (pro_interfaces.LDAPProviderConfiguration, error) {
	selected, err := s.repository.GetLDAPSelectedUsers(provider.ID)
	if err != nil {
		return pro_interfaces.LDAPProviderConfiguration{}, err
	}
	eligible, err := s.repository.GetLDAPLinkedUserIDs(provider.ID)
	if err != nil {
		return pro_interfaces.LDAPProviderConfiguration{}, err
	}
	readiness := pro_interfaces.LDAPReadiness{
		Status: pro_interfaces.LDAPReadinessStatus(provider.ReadinessStatus),
		Code:   provider.ReadinessCode, CheckedAt: provider.ReadinessCheckedAt,
	}
	if readiness.Status == pro_interfaces.LDAPReadinessReady {
		readiness.Connection = true
		readiness.Search = true
		readiness.Bind = true
		readiness.Recovery = provider.RecoveryAdminUserID != nil && provider.RecoveryCheckedAt != nil
	}
	return pro_interfaces.LDAPProviderConfiguration{
		ID: provider.ID, DisplayName: provider.DisplayName,
		State: pro_interfaces.LDAPState(provider.State), ServerURL: provider.ServerURL,
		TLSMode:   pro_interfaces.LDAPTLSMode(provider.TLSMode),
		TrustMode: pro_interfaces.LDAPTrustMode(provider.TrustMode), CAPEM: provider.CAPEM,
		BindDN: provider.BindDN, BindPasswordConfigured: provider.EncryptedBindPassword != "",
		SearchBaseDN: provider.SearchBaseDN, UserFilter: provider.UserFilter,
		IdentityAttribute: provider.IdentityAttribute, UsernameAttribute: provider.UsernameAttribute,
		NameAttribute: provider.NameAttribute, EmailAttribute: provider.EmailAttribute,
		GroupSearchBaseDN: provider.GroupSearchBaseDN, GroupUserFilter: provider.GroupUserFilter,
		GroupFilter: provider.GroupFilter, GroupIdentityAttribute: provider.GroupIdentityAttribute,
		GroupMemberAttribute: provider.GroupMemberAttribute, GroupMaxDepth: provider.GroupMaxDepth,
		SelectedUserIDs: selected, EligibleUserIDs: eligible,
		RecoveryAdminUserID: provider.RecoveryAdminUserID,
		Readiness:           readiness, Created: provider.Created, Updated: provider.Updated,
	}, nil
}

func (s *ldapService) clientConfiguration(
	provider db.LDAPProvider,
) (pro_interfaces.LDAPClientConfiguration, func(), error) {
	secret, err := util.Config.DecryptOption(provider.EncryptedBindPassword)
	if err != nil {
		return pro_interfaces.LDAPClientConfiguration{}, func() {}, err
	}
	configuration := pro_interfaces.LDAPClientConfiguration{
		ServerURL: provider.ServerURL, TLSMode: pro_interfaces.LDAPTLSMode(provider.TLSMode),
		TrustMode: pro_interfaces.LDAPTrustMode(provider.TrustMode), CAPEM: provider.CAPEM,
		BindDN: provider.BindDN, SearchBaseDN: provider.SearchBaseDN,
		UserFilter: provider.UserFilter, IdentityAttribute: provider.IdentityAttribute,
		UsernameAttribute: provider.UsernameAttribute, NameAttribute: provider.NameAttribute,
		EmailAttribute:    provider.EmailAttribute,
		GroupSearchBaseDN: provider.GroupSearchBaseDN, GroupUserFilter: provider.GroupUserFilter,
		GroupFilter: provider.GroupFilter, GroupIdentityAttribute: provider.GroupIdentityAttribute,
		GroupMemberAttribute: provider.GroupMemberAttribute, GroupMaxDepth: provider.GroupMaxDepth,
	}
	configuration.BindPassword = string(secret)
	return configuration, func() {
		zeroLDAPBytes(secret)
		configuration.BindPassword = ""
	}, nil
}

func clientConfigurationFromInput(
	input pro_interfaces.LDAPProviderInput,
	bindSecret string,
) pro_interfaces.LDAPClientConfiguration {
	configuration := pro_interfaces.LDAPClientConfiguration{
		ServerURL: strings.TrimSpace(input.ServerURL), TLSMode: input.TLSMode,
		TrustMode: input.TrustMode, CAPEM: strings.TrimSpace(input.CAPEM),
		BindDN: strings.TrimSpace(input.BindDN), SearchBaseDN: strings.TrimSpace(input.SearchBaseDN),
		UserFilter: strings.TrimSpace(input.UserFilter), IdentityAttribute: strings.TrimSpace(input.IdentityAttribute),
		UsernameAttribute: strings.TrimSpace(input.UsernameAttribute), NameAttribute: strings.TrimSpace(input.NameAttribute),
		EmailAttribute:    strings.TrimSpace(input.EmailAttribute),
		GroupSearchBaseDN: strings.TrimSpace(input.GroupSearchBaseDN), GroupUserFilter: strings.TrimSpace(input.GroupUserFilter),
		GroupFilter: strings.TrimSpace(input.GroupFilter), GroupIdentityAttribute: strings.TrimSpace(input.GroupIdentityAttribute),
		GroupMemberAttribute: strings.TrimSpace(input.GroupMemberAttribute), GroupMaxDepth: input.GroupMaxDepth,
	}
	configuration.BindPassword = bindSecret
	return configuration
}

var _ pro_interfaces.LDAPService = (*ldapService)(nil)

func ldapSubjectHash(username string) string {
	digest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(username))))
	return hex.EncodeToString(digest[:])
}

func zeroLDAPBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
