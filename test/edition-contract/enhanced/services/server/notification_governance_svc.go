// Package server contains the private Enhanced-edition notification governance
// implementation. Provider adapters and dispatch loops remain outside Slice 056.
package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
)

type credentialCipher interface {
	OptionEncryptionEnabled() bool
	EncryptOption([]byte) (string, error)
}

type governanceService struct {
	repository db.NotificationRepository
	cipher     credentialCipher
	now        func() time.Time
}

var providerPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

const pagerDutyProviderName = "pagerduty"

// NewGovernanceService creates the provider-neutral notification use-case
// boundary. The production path encrypts credentials through util.Config;
// tests can supply a deterministic cipher without mutating global config.
func NewGovernanceService(repository db.NotificationRepository, ciphers ...credentialCipher) pro_interfaces.NotificationGovernanceServiceFacade {
	cipher := credentialCipher(util.Config)
	if len(ciphers) > 0 && ciphers[0] != nil {
		cipher = ciphers[0]
	}
	return &governanceService{repository: repository, cipher: cipher, now: func() time.Time { return time.Now().UTC() }}
}

// NewNotificationGovernanceService is the Enhanced bootstrap entry point.
func NewNotificationGovernanceService(repository db.NotificationRepository) pro_interfaces.NotificationGovernanceServiceFacade {
	return NewGovernanceService(repository)
}

func (s *governanceService) CreateDestination(ctx context.Context, projectID *int, input pro_interfaces.NotificationDestinationInput) (pro_interfaces.NotificationDestinationDTO, error) {
	if err := contextError(ctx); err != nil {
		return pro_interfaces.NotificationDestinationDTO{}, err
	}
	if err := validateDestinationInput(projectID, input); err != nil {
		return pro_interfaces.NotificationDestinationDTO{}, err
	}
	credential, configured, err := s.encryptCredential(input.Credential)
	if err != nil {
		return pro_interfaces.NotificationDestinationDTO{}, err
	}
	providerConfig, err := canonicalProviderConfig(input)
	if err != nil {
		return pro_interfaces.NotificationDestinationDTO{}, err
	}
	destination, err := s.repository.CreateNotificationDestination(db.NotificationDestination{
		ProjectID: projectID, Name: input.Name, Provider: input.Provider, Environment: input.Environment, Region: string(input.Region),
		ProviderConfig: providerConfig, EncryptedCredential: credential, CredentialConfigured: configured, Enabled: input.Enabled,
	})
	if err != nil {
		return pro_interfaces.NotificationDestinationDTO{}, mapRepositoryError(err)
	}
	return destinationDTO(destination), nil
}

func (s *governanceService) GetDestination(ctx context.Context, projectID *int, id int) (pro_interfaces.NotificationDestinationDTO, error) {
	if err := contextError(ctx); err != nil {
		return pro_interfaces.NotificationDestinationDTO{}, err
	}
	destination, err := s.destination(projectID, id)
	if err != nil {
		return pro_interfaces.NotificationDestinationDTO{}, err
	}
	return destinationDTO(destination), nil
}

func (s *governanceService) ListDestinations(ctx context.Context, projectID *int, params db.RetrieveQueryParams) ([]pro_interfaces.NotificationDestinationDTO, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if err := validatePagination(params); err != nil {
		return nil, err
	}
	destinations, err := s.repository.GetNotificationDestinations(projectID, params)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	result := make([]pro_interfaces.NotificationDestinationDTO, len(destinations))
	for index, destination := range destinations {
		result[index] = destinationDTO(destination)
	}
	return result, nil
}

func (s *governanceService) UpdateDestination(ctx context.Context, projectID *int, id, expectedRevision int, input pro_interfaces.NotificationDestinationInput) (pro_interfaces.NotificationDestinationDTO, error) {
	if err := contextError(ctx); err != nil {
		return pro_interfaces.NotificationDestinationDTO{}, err
	}
	if id <= 0 || expectedRevision <= 0 || validateDestinationInput(projectID, input) != nil {
		return pro_interfaces.NotificationDestinationDTO{}, pro_interfaces.ErrNotificationInvalidInput
	}
	current, err := s.destination(projectID, id)
	if err != nil {
		return pro_interfaces.NotificationDestinationDTO{}, err
	}
	if current.Revision != expectedRevision {
		return pro_interfaces.NotificationDestinationDTO{}, pro_interfaces.ErrNotificationRevisionConflict
	}
	credential, configured, err := s.updatedCredential(current, input.Provider, input.Credential)
	if err != nil {
		return pro_interfaces.NotificationDestinationDTO{}, err
	}
	providerConfig, err := canonicalProviderConfig(input)
	if err != nil {
		return pro_interfaces.NotificationDestinationDTO{}, err
	}
	updated, err := s.repository.UpdateNotificationDestination(db.NotificationDestination{
		ID: id, ProjectID: projectID, Name: input.Name, Provider: input.Provider, Environment: input.Environment, Region: string(input.Region),
		ProviderConfig: providerConfig, EncryptedCredential: credential, CredentialConfigured: configured, Enabled: input.Enabled, Paused: current.Paused,
		Revision: expectedRevision,
	}, expectedRevision)
	if err != nil {
		return pro_interfaces.NotificationDestinationDTO{}, mapRepositoryError(err)
	}
	return destinationDTO(updated), nil
}

func (s *governanceService) DeleteDestination(ctx context.Context, projectID *int, id, expectedRevision int) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if id <= 0 || expectedRevision <= 0 {
		return pro_interfaces.ErrNotificationInvalidInput
	}
	current, err := s.destination(projectID, id)
	if err != nil {
		return err
	}
	if current.Revision != expectedRevision {
		return pro_interfaces.ErrNotificationRevisionConflict
	}
	if err = s.repository.DeleteNotificationDestination(projectID, id, expectedRevision); err != nil {
		return mapRepositoryError(err)
	}
	return nil
}

func (s *governanceService) SetDestinationPaused(ctx context.Context, projectID *int, id, expectedRevision int, paused bool) (pro_interfaces.NotificationDestinationDTO, error) {
	if err := contextError(ctx); err != nil {
		return pro_interfaces.NotificationDestinationDTO{}, err
	}
	if id <= 0 || expectedRevision <= 0 {
		return pro_interfaces.NotificationDestinationDTO{}, pro_interfaces.ErrNotificationInvalidInput
	}
	current, err := s.destination(projectID, id)
	if err != nil {
		return pro_interfaces.NotificationDestinationDTO{}, err
	}
	if current.Revision != expectedRevision {
		return pro_interfaces.NotificationDestinationDTO{}, pro_interfaces.ErrNotificationRevisionConflict
	}
	updated, err := s.repository.SetNotificationDestinationPaused(db.NotificationDestination{
		ID: id, ProjectID: projectID, Paused: paused, Revision: expectedRevision,
	}, expectedRevision)
	if err != nil {
		return pro_interfaces.NotificationDestinationDTO{}, mapRepositoryError(err)
	}
	if !paused {
		if err := s.repository.ResumePausedNotificationDeliveries(updated.ID, s.now()); err != nil {
			return pro_interfaces.NotificationDestinationDTO{}, mapRepositoryError(err)
		}
	}
	return destinationDTO(updated), nil
}

func (s *governanceService) CreateRule(ctx context.Context, projectID *int, input pro_interfaces.NotificationRuleInput) (pro_interfaces.NotificationRuleDTO, error) {
	if err := contextError(ctx); err != nil {
		return pro_interfaces.NotificationRuleDTO{}, err
	}
	rule, err := ruleFromInput(projectID, input)
	if err != nil {
		return pro_interfaces.NotificationRuleDTO{}, err
	}
	if _, err = s.destination(projectID, input.DestinationID); err != nil {
		return pro_interfaces.NotificationRuleDTO{}, err
	}
	created, err := s.repository.CreateNotificationRule(rule)
	if err != nil {
		return pro_interfaces.NotificationRuleDTO{}, mapRepositoryError(err)
	}
	return ruleDTO(created), nil
}

func (s *governanceService) ListRules(ctx context.Context, projectID *int, params db.RetrieveQueryParams) ([]pro_interfaces.NotificationRuleDTO, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if err := validatePagination(params); err != nil {
		return nil, err
	}
	rules, err := s.repository.GetNotificationRules(projectID, params)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	result := make([]pro_interfaces.NotificationRuleDTO, len(rules))
	for index, rule := range rules {
		result[index] = ruleDTO(rule)
	}
	return result, nil
}

func (s *governanceService) UpdateRule(ctx context.Context, projectID *int, id, expectedRevision int, input pro_interfaces.NotificationRuleInput) (pro_interfaces.NotificationRuleDTO, error) {
	if err := contextError(ctx); err != nil {
		return pro_interfaces.NotificationRuleDTO{}, err
	}
	if id <= 0 || expectedRevision <= 0 {
		return pro_interfaces.NotificationRuleDTO{}, pro_interfaces.ErrNotificationInvalidInput
	}
	rule, err := ruleFromInput(projectID, input)
	if err != nil {
		return pro_interfaces.NotificationRuleDTO{}, err
	}
	current, err := s.repository.GetNotificationRule(projectID, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, db.ErrNotFound) {
			return pro_interfaces.NotificationRuleDTO{}, pro_interfaces.ErrNotificationRuleMissing
		}
		return pro_interfaces.NotificationRuleDTO{}, mapRepositoryError(err)
	}
	if current.Revision != expectedRevision {
		return pro_interfaces.NotificationRuleDTO{}, pro_interfaces.ErrNotificationRevisionConflict
	}
	if _, err = s.destination(projectID, input.DestinationID); err != nil {
		return pro_interfaces.NotificationRuleDTO{}, err
	}
	rule.ID = id
	rule.Revision = expectedRevision
	updated, err := s.repository.UpdateNotificationRule(rule, expectedRevision)
	if err != nil {
		return pro_interfaces.NotificationRuleDTO{}, mapRepositoryError(err)
	}
	return ruleDTO(updated), nil
}

func (s *governanceService) DeleteRule(ctx context.Context, projectID *int, id, expectedRevision int) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if id <= 0 || expectedRevision <= 0 {
		return pro_interfaces.ErrNotificationInvalidInput
	}
	current, err := s.repository.GetNotificationRule(projectID, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, db.ErrNotFound) {
			return pro_interfaces.ErrNotificationRuleMissing
		}
		return mapRepositoryError(err)
	}
	if current.Revision != expectedRevision {
		return pro_interfaces.ErrNotificationRevisionConflict
	}
	if err = s.repository.DeleteNotificationRule(projectID, id, expectedRevision); err != nil {
		return mapRepositoryError(err)
	}
	return nil
}

func (s *governanceService) PreviewRouting(ctx context.Context, projectID *int, event pro_interfaces.NotificationEvent) ([]pro_interfaces.NotificationRoutingPreviewDTO, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if !sameScope(projectID, event.ProjectID) || event.Validate() != nil {
		return nil, pro_interfaces.ErrNotificationInvalidInput
	}
	rules, err := s.repository.GetNotificationRules(projectID, db.RetrieveQueryParams{})
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	seen := make(map[int]struct{})
	result := make([]pro_interfaces.NotificationRoutingPreviewDTO, 0, len(rules))
	for _, rule := range rules {
		if !routingRule(rule).Matches(event) {
			continue
		}
		destination, destinationErr := s.destination(projectID, rule.DestinationID)
		if destinationErr != nil {
			continue
		}
		if !destination.Enabled {
			continue
		}
		if _, exists := seen[destination.ID]; exists {
			continue
		}
		seen[destination.ID] = struct{}{}
		result = append(result, pro_interfaces.NotificationRoutingPreviewDTO{
			DestinationID: destination.ID, Name: destination.Name, Provider: destination.Provider, Environment: destination.Environment,
		})
	}
	return result, nil
}

func (s *governanceService) EnqueueTestDelivery(ctx context.Context, projectID *int, destinationID int) (pro_interfaces.NotificationDeliveryDTO, error) {
	if err := contextError(ctx); err != nil {
		return pro_interfaces.NotificationDeliveryDTO{}, err
	}
	destination, err := s.destination(projectID, destinationID)
	if err != nil {
		return pro_interfaces.NotificationDeliveryDTO{}, err
	}
	if !destination.Enabled {
		return pro_interfaces.NotificationDeliveryDTO{}, pro_interfaces.ErrNotificationDestinationDisabled
	}
	if destination.Paused {
		return pro_interfaces.NotificationDeliveryDTO{}, pro_interfaces.ErrNotificationDestinationPaused
	}
	if !destination.CredentialConfigured || destination.EncryptedCredential == "" {
		return pro_interfaces.NotificationDeliveryDTO{}, pro_interfaces.ErrNotificationDestinationNotConfigured
	}
	testID, err := randomTestSourceID(destinationID)
	if err != nil {
		return pro_interfaces.NotificationDeliveryDTO{}, err
	}
	scope := pro_interfaces.NotificationScopeGlobal
	if projectID != nil {
		scope = pro_interfaces.NotificationScopeProject
	}
	event, err := (pro_interfaces.NotificationEvent{
		SourceRevision: 1, Scope: scope, ProjectID: projectID,
		Source:      pro_interfaces.NotificationSource{Kind: pro_interfaces.NotificationSourceSystem, ID: testID},
		LifecycleID: testID, Severity: pro_interfaces.NotificationSeverityInfo,
		LifecycleAction: pro_interfaces.NotificationLifecycleTrigger,
	}).EnsureIdentity(s.now())
	if err != nil {
		return pro_interfaces.NotificationDeliveryDTO{}, pro_interfaces.ErrNotificationInvalidInput
	}
	created, err := s.repository.CreateNotificationEventWithRouting(notificationEvent(event, db.NotificationRoutingRouted), []db.NotificationDelivery{{
		DestinationID: destination.ID, DestinationRevision: destination.Revision, DestinationConfigurationRevision: destination.ConfigurationRevision, DestinationName: destination.Name,
		DestinationProvider: destination.Provider, DestinationEnvironment: destination.Environment, DestinationRegion: destination.Region,
	}})
	if err != nil {
		return pro_interfaces.NotificationDeliveryDTO{}, mapRepositoryError(err)
	}
	deliveries, err := s.repository.GetNotificationDeliveries(projectID, db.RetrieveQueryParams{Count: 1})
	if err != nil {
		return pro_interfaces.NotificationDeliveryDTO{}, mapRepositoryError(err)
	}
	for _, delivery := range deliveries {
		if delivery.EventID == created.EventID && delivery.DestinationID == destination.ID {
			return deliveryDTO(delivery), nil
		}
	}
	return pro_interfaces.NotificationDeliveryDTO{}, fmt.Errorf("notification test delivery was not persisted")
}

func (s *governanceService) DeliveryHistory(ctx context.Context, projectID *int, params db.RetrieveQueryParams) ([]pro_interfaces.NotificationDeliveryDTO, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if err := validatePagination(params); err != nil {
		return nil, err
	}
	deliveries, err := s.repository.GetNotificationDeliveries(projectID, params)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	result := make([]pro_interfaces.NotificationDeliveryDTO, len(deliveries))
	for index, delivery := range deliveries {
		result[index] = deliveryDTO(delivery)
	}
	return result, nil
}

func (s *governanceService) EventHistory(ctx context.Context, projectID *int, params db.RetrieveQueryParams) ([]pro_interfaces.NotificationEventHistoryDTO, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if err := validatePagination(params); err != nil {
		return nil, err
	}
	events, err := s.repository.GetNotificationEventHistory(projectID, params)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	result := make([]pro_interfaces.NotificationEventHistoryDTO, len(events))
	for index, event := range events {
		result[index] = eventHistoryDTO(event)
	}
	return result, nil
}

func (s *governanceService) RetryDelivery(ctx context.Context, projectID *int, id int) (pro_interfaces.NotificationDeliveryDTO, error) {
	if err := contextError(ctx); err != nil {
		return pro_interfaces.NotificationDeliveryDTO{}, err
	}
	if id <= 0 {
		return pro_interfaces.NotificationDeliveryDTO{}, pro_interfaces.ErrNotificationInvalidInput
	}
	delivery, err := s.repository.GetNotificationDelivery(projectID, id)
	if err != nil {
		return pro_interfaces.NotificationDeliveryDTO{}, mapRepositoryError(err)
	}
	destination, err := s.destination(projectID, delivery.DestinationID)
	if err != nil {
		return pro_interfaces.NotificationDeliveryDTO{}, err
	}
	if err := s.repository.RetryNotificationDelivery(id, destination.ID, destination.ConfigurationRevision, s.now()); err != nil {
		if errors.Is(err, db.ErrNotificationDeliveryNotClaimed) {
			return pro_interfaces.NotificationDeliveryDTO{}, pro_interfaces.ErrNotificationRetryUnavailable
		}
		return pro_interfaces.NotificationDeliveryDTO{}, mapRepositoryError(err)
	}
	delivery, err = s.repository.GetNotificationDelivery(projectID, id)
	if err != nil {
		return pro_interfaces.NotificationDeliveryDTO{}, mapRepositoryError(err)
	}
	return deliveryDTO(delivery), nil
}

func (s *governanceService) destination(projectID *int, id int) (db.NotificationDestination, error) {
	if id <= 0 {
		return db.NotificationDestination{}, pro_interfaces.ErrNotificationInvalidInput
	}
	destination, err := s.repository.GetNotificationDestination(projectID, id)
	if err != nil {
		return db.NotificationDestination{}, mapRepositoryError(err)
	}
	return destination, nil
}

func (s *governanceService) encryptCredential(credential *string) (string, bool, error) {
	if credential == nil {
		return "", false, nil
	}
	if *credential == "" || len(*credential) > 16*1024 {
		return "", false, pro_interfaces.ErrNotificationInvalidInput
	}
	if s.cipher == nil || !s.cipher.OptionEncryptionEnabled() {
		return "", false, pro_interfaces.ErrNotificationEncryptionRequired
	}
	encrypted, err := s.cipher.EncryptOption([]byte(*credential))
	if err != nil || encrypted == "" {
		return "", false, pro_interfaces.ErrNotificationEncryptionRequired
	}
	return encrypted, true, nil
}

func (s *governanceService) updatedCredential(current db.NotificationDestination, provider string, credential *string) (string, bool, error) {
	if credential == nil {
		if current.Provider != provider {
			return "", false, pro_interfaces.ErrNotificationInvalidInput
		}
		return current.EncryptedCredential, current.CredentialConfigured, nil
	}
	return s.encryptCredential(credential)
}

func validateDestinationInput(projectID *int, input pro_interfaces.NotificationDestinationInput) error {
	if projectID != nil && *projectID <= 0 || strings.TrimSpace(input.Name) == "" || input.Name != strings.TrimSpace(input.Name) || len(input.Name) > 128 ||
		!providerPattern.MatchString(input.Provider) || len(input.Environment) > 64 {
		return pro_interfaces.ErrNotificationInvalidInput
	}
	if input.Provider == pagerDutyProviderName {
		if input.Opsgenie != nil || !validPagerDutyRegion(input.Region) || input.Credential != nil && !validPagerDutyRoutingKey(*input.Credential) {
			return pro_interfaces.ErrNotificationInvalidInput
		}
	}
	if input.Provider == opsgenieProviderName {
		if !validOpsgenieRegion(input.Region) || input.Credential != nil && !validOpsgenieKey(*input.Credential) || !validOpsgenieConfiguration(input.Opsgenie) {
			return pro_interfaces.ErrNotificationInvalidInput
		}
	}
	if input.Provider != pagerDutyProviderName && input.Provider != opsgenieProviderName && (input.Region != "" || input.Opsgenie != nil) {
		return pro_interfaces.ErrNotificationInvalidInput
	}
	return nil
}

func validOpsgenieRegion(region pro_interfaces.NotificationProviderRegion) bool {
	return region == pro_interfaces.NotificationProviderRegionUS || region == pro_interfaces.NotificationProviderRegionEU
}

func validOpsgenieKey(value string) bool {
	if len(value) == 0 || len(value) > opsgenieMaxCredentialBytes {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

func validOpsgenieConfiguration(configuration *pro_interfaces.NotificationOpsgenieConfiguration) bool {
	if configuration == nil {
		return true
	}
	if configuration.Priority != "" && configuration.Priority != pro_interfaces.NotificationOpsgeniePriorityP1 && configuration.Priority != pro_interfaces.NotificationOpsgeniePriorityP2 && configuration.Priority != pro_interfaces.NotificationOpsgeniePriorityP3 && configuration.Priority != pro_interfaces.NotificationOpsgeniePriorityP4 && configuration.Priority != pro_interfaces.NotificationOpsgeniePriorityP5 || len(configuration.Responders) > 50 {
		return false
	}
	for _, responder := range configuration.Responders {
		count := 0
		for _, value := range []string{responder.ID, responder.Name, responder.Username} {
			if value != "" {
				count++
				if len(value) > 256 || strings.TrimSpace(value) != value {
					return false
				}
			}
		}
		if count != 1 {
			return false
		}
		switch responder.Type {
		case pro_interfaces.NotificationOpsgenieResponderTeam, pro_interfaces.NotificationOpsgenieResponderEscalation, pro_interfaces.NotificationOpsgenieResponderSchedule:
			if responder.Username != "" {
				return false
			}
		case pro_interfaces.NotificationOpsgenieResponderUser:
			if responder.Name != "" {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func canonicalProviderConfig(input pro_interfaces.NotificationDestinationInput) (string, error) {
	if input.Provider != opsgenieProviderName {
		return "", nil
	}
	if !validOpsgenieConfiguration(input.Opsgenie) {
		return "", pro_interfaces.ErrNotificationInvalidInput
	}
	if input.Opsgenie == nil {
		return "", nil
	}
	encoded, err := json.Marshal(input.Opsgenie)
	if err != nil || len(encoded) > 16*1024 {
		return "", pro_interfaces.ErrNotificationInvalidInput
	}
	return string(encoded), nil
}

func opsgenieConfiguration(value string) (*pro_interfaces.NotificationOpsgenieConfiguration, bool) {
	if value == "" {
		return nil, true
	}
	var configuration pro_interfaces.NotificationOpsgenieConfiguration
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&configuration) != nil || decoder.Decode(&struct{}{}) != io.EOF || !validOpsgenieConfiguration(&configuration) {
		return nil, false
	}
	return &configuration, true
}

func validPagerDutyRegion(region pro_interfaces.NotificationProviderRegion) bool {
	return region == pro_interfaces.NotificationProviderRegionUS || region == pro_interfaces.NotificationProviderRegionEU
}

func validPagerDutyRoutingKey(credential string) bool {
	return pagerDutyRoutingKeyPattern.MatchString(credential)
}

func ruleFromInput(projectID *int, input pro_interfaces.NotificationRuleInput) (db.NotificationRule, error) {
	if input.DestinationID <= 0 || !validSourceKinds(input.SourceKinds) || !validActions(input.LifecycleActions) || !validSeverity(input.MinimumSeverity) {
		return db.NotificationRule{}, pro_interfaces.ErrNotificationInvalidInput
	}
	return db.NotificationRule{
		ProjectID: projectID, DestinationID: input.DestinationID,
		SourceKinds:      strings.Join(notificationSourceKinds(input.SourceKinds), ","),
		LifecycleActions: strings.Join(notificationActions(input.LifecycleActions), ","),
		MinimumSeverity:  string(input.MinimumSeverity), Enabled: input.Enabled,
	}, nil
}

func validSourceKinds(values []pro_interfaces.NotificationSourceKind) bool {
	return len(values) > 0 && !slices.Contains(values, "") && noDuplicates(values) && slices.ContainsFunc(values, func(value pro_interfaces.NotificationSourceKind) bool {
		return value != pro_interfaces.NotificationSourceTask && value != pro_interfaces.NotificationSourceWorkflow && value != pro_interfaces.NotificationSourceApproval && value != pro_interfaces.NotificationSourceSystem
	}) == false
}

func validActions(values []pro_interfaces.NotificationLifecycleAction) bool {
	return len(values) > 0 && !slices.Contains(values, "") && noDuplicates(values) && slices.ContainsFunc(values, func(value pro_interfaces.NotificationLifecycleAction) bool {
		return value != pro_interfaces.NotificationLifecycleTrigger && value != pro_interfaces.NotificationLifecycleUpdate && value != pro_interfaces.NotificationLifecycleResolve
	}) == false
}

func validSeverity(value pro_interfaces.NotificationSeverity) bool {
	return value == pro_interfaces.NotificationSeverityInfo || value == pro_interfaces.NotificationSeverityWarning || value == pro_interfaces.NotificationSeverityError || value == pro_interfaces.NotificationSeverityCritical
}

func noDuplicates[T comparable](values []T) bool {
	seen := make(map[T]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func routingRule(rule db.NotificationRule) pro_interfaces.NotificationRoutingRule {
	return pro_interfaces.NotificationRoutingRule{
		SourceKinds: notificationSourceKindsFromString(rule.SourceKinds), Actions: notificationActionsFromString(rule.LifecycleActions),
		MinimumSeverity: pro_interfaces.NotificationSeverity(rule.MinimumSeverity), Enabled: rule.Enabled,
	}
}

func notificationSourceKinds(values []pro_interfaces.NotificationSourceKind) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}

func notificationActions(values []pro_interfaces.NotificationLifecycleAction) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}

func notificationSourceKindsFromString(value string) []pro_interfaces.NotificationSourceKind {
	parts := strings.Split(value, ",")
	result := make([]pro_interfaces.NotificationSourceKind, len(parts))
	for index, part := range parts {
		result[index] = pro_interfaces.NotificationSourceKind(part)
	}
	return result
}

func notificationActionsFromString(value string) []pro_interfaces.NotificationLifecycleAction {
	parts := strings.Split(value, ",")
	result := make([]pro_interfaces.NotificationLifecycleAction, len(parts))
	for index, part := range parts {
		result[index] = pro_interfaces.NotificationLifecycleAction(part)
	}
	return result
}

func destinationDTO(destination db.NotificationDestination) pro_interfaces.NotificationDestinationDTO {
	configuration, _ := opsgenieConfiguration(destination.ProviderConfig)
	return pro_interfaces.NotificationDestinationDTO{
		ID: destination.ID, ProjectID: destination.ProjectID, Name: destination.Name, Provider: destination.Provider, Environment: destination.Environment, Region: pro_interfaces.NotificationProviderRegion(destination.Region),
		CredentialConfigured: destination.CredentialConfigured, Opsgenie: configuration, Enabled: destination.Enabled, Paused: destination.Paused, Revision: destination.Revision,
		CreatedAt: destination.Created, UpdatedAt: destination.Updated,
	}
}

func ruleDTO(rule db.NotificationRule) pro_interfaces.NotificationRuleDTO {
	return pro_interfaces.NotificationRuleDTO{
		ID: rule.ID, ProjectID: rule.ProjectID, DestinationID: rule.DestinationID, SourceKinds: notificationSourceKindsFromString(rule.SourceKinds),
		LifecycleActions: notificationActionsFromString(rule.LifecycleActions), MinimumSeverity: pro_interfaces.NotificationSeverity(rule.MinimumSeverity),
		Enabled: rule.Enabled, Revision: rule.Revision, CreatedAt: rule.Created, UpdatedAt: rule.Updated,
	}
}

func deliveryDTO(delivery db.NotificationDelivery) pro_interfaces.NotificationDeliveryDTO {
	return pro_interfaces.NotificationDeliveryDTO{
		ID: delivery.ID, EventID: delivery.EventID, DestinationID: delivery.DestinationID, DestinationRevision: delivery.DestinationRevision,
		DestinationName: delivery.DestinationName, DestinationProvider: delivery.DestinationProvider, DestinationEnvironment: delivery.DestinationEnvironment, DestinationRegion: pro_interfaces.NotificationProviderRegion(delivery.DestinationRegion),
		IncidentKey: delivery.IncidentKey, IdempotencyKey: delivery.IdempotencyKey, ProviderRequestID: delivery.ProviderRequestID, Status: delivery.Status, Attempts: delivery.Attempts,
		NextAttempt: delivery.NextAttempt, LastReason: delivery.LastReason, CreatedAt: delivery.Created, UpdatedAt: delivery.Updated, DeliveredAt: delivery.DeliveredAt,
		SourceKind: pro_interfaces.NotificationSourceKind(delivery.SourceKind), SourceID: delivery.SourceID,
		LifecycleAction: pro_interfaces.NotificationLifecycleAction(delivery.LifecycleAction), Severity: pro_interfaces.NotificationSeverity(delivery.Severity), OccurredAt: delivery.OccurredAt,
	}
}

func eventHistoryDTO(event db.NotificationEventHistory) pro_interfaces.NotificationEventHistoryDTO {
	return pro_interfaces.NotificationEventHistoryDTO{
		EventID: event.EventID, SourceKind: pro_interfaces.NotificationSourceKind(event.SourceKind), SourceID: event.SourceID,
		LifecycleID: event.LifecycleID, Severity: pro_interfaces.NotificationSeverity(event.Severity),
		LifecycleAction: pro_interfaces.NotificationLifecycleAction(event.LifecycleAction), IncidentKey: event.IncidentKey,
		RoutingOutcome: event.RoutingOutcome, OccurredAt: event.OccurredAt, CreatedAt: event.Created,
	}
}

func notificationEvent(event pro_interfaces.NotificationEvent, outcome db.NotificationRoutingOutcome) db.NotificationEvent {
	return db.NotificationEvent{
		SchemaVersion: event.SchemaVersion, EventID: event.EventID, SourceEventKey: event.SourceEventKey, SourceRevision: event.SourceRevision,
		ProjectID: event.ProjectID, SourceKind: string(event.Source.Kind), SourceID: event.Source.ID, LifecycleID: event.LifecycleID,
		Severity: string(event.Severity), LifecycleAction: string(event.LifecycleAction), IncidentKey: event.IncidentKey,
		Details: "{}", RoutingOutcome: outcome, OccurredAt: event.OccurredAt,
	}
}

func randomTestSourceID(destinationID int) (string, error) {
	if destinationID <= 0 {
		return "", pro_interfaces.ErrNotificationInvalidInput
	}
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate notification test identity: %w", err)
	}
	return fmt.Sprintf("system:notification_test-%d-%s", destinationID, hex.EncodeToString(bytes)), nil
}

func sameScope(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func validatePagination(params db.RetrieveQueryParams) error {
	if params.Count < 0 || params.Offset < 0 || params.Count > 500 {
		return pro_interfaces.ErrNotificationInvalidInput
	}
	return nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return pro_interfaces.ErrNotificationInvalidInput
	}
	return ctx.Err()
}

func mapRepositoryError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, db.ErrNotFound) {
		return pro_interfaces.ErrNotificationDestinationMissing
	}
	if errors.Is(err, db.ErrNotificationDestinationRevisionConflict) || errors.Is(err, db.ErrNotificationRuleRevisionConflict) {
		return pro_interfaces.ErrNotificationRevisionConflict
	}
	if errors.Is(err, db.ErrInvalidOperation) {
		return pro_interfaces.ErrNotificationInvalidInput
	}
	return err
}

var _ pro_interfaces.NotificationGovernanceServiceFacade = (*governanceService)(nil)
