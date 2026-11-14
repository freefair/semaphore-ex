package features

import (
	"context"
	"errors"
	"strings"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	log "github.com/sirupsen/logrus"
)

// requireLDAPGroupAccess is intentionally called before resolveIdentity: an
// unmatched directory identity must never create or link a Semaphore account.
func (s *ldapService) requireLDAPGroupAccess(
	ctx context.Context,
	provider db.LDAPProvider,
	configuration pro_interfaces.LDAPClientConfiguration,
	identity pro_interfaces.LDAPIdentity,
) error {
	mappings, err := s.repository.GetLDAPGroupMappings(provider.ID)
	if err != nil {
		return err
	}
	enabledGroups := make(map[string]bool, len(mappings))
	for _, mapping := range mappings {
		if mapping.Enabled {
			enabledGroups[strings.ToLower(strings.TrimSpace(mapping.GroupExternalID))] = true
		}
	}
	if len(enabledGroups) == 0 {
		return pro_interfaces.ErrLDAPNoGroupMapping
	}
	snapshot, err := s.client.ReadGroupSnapshot(ctx, configuration)
	if err != nil {
		logLDAPOperationFailure(provider.ID, "group_search", "group_search_failed", err, configuration)
		return err
	}
	for _, user := range snapshot.Users {
		if !strings.EqualFold(strings.TrimSpace(user.ExternalID), strings.TrimSpace(identity.ExternalID)) {
			continue
		}
		for _, groupID := range user.GroupExternalIDs {
			if enabledGroups[strings.ToLower(strings.TrimSpace(groupID))] {
				return nil
			}
		}
		return pro_interfaces.ErrLDAPNoGroupAccess
	}
	return pro_interfaces.ErrLDAPNoGroupAccess
}

func logLDAPFailure(providerID string, readiness pro_interfaces.LDAPReadiness, err error) {
	stage := readiness.Code
	switch readiness.Code {
	case "connection_failed":
		stage = "connect"
	case "service_bind_failed":
		stage = "service_bind"
	case "search_failed", "duplicate_identity", "referral_rejected":
		stage = "search"
	case "invalid_credentials":
		stage = "user_bind"
	case "identity_attribute_missing", "identity_not_uuid", "required_attribute_missing", "invalid_identity":
		stage = "identity_extraction"
	}
	fields := log.Fields{"provider_id": providerID, "stage": stage, "code": readiness.Code}
	if readiness.Detail != "" {
		fields["detail"] = readiness.Detail
	}
	if err != nil {
		fields["error_type"] = "LDAP operation error"
	}
	log.WithFields(fields).Error("LDAP operation failed")
}

func logLDAPOperationFailure(
	providerID string,
	stage string,
	code string,
	err error,
	configuration pro_interfaces.LDAPClientConfiguration,
) {
	detail := "LDAP operation failed"
	if err != nil {
		detail = err.Error()
		for _, value := range []string{configuration.BindDN, configuration.BindPassword} {
			if value != "" {
				detail = strings.ReplaceAll(detail, value, "[redacted]")
			}
		}
	}
	log.WithFields(log.Fields{
		"provider_id": providerID, "stage": stage, "code": code, "detail": detail,
	}).Error("LDAP operation failed")
}

func ldapGroupAccessRevocations(
	directoryUsers []pro_interfaces.LDAPDirectoryUser,
	linkedUsers []pro_interfaces.LDAPLinkedUser,
	mappings []pro_interfaces.LDAPGroupMapping,
) []int {
	enabledGroups := make(map[string]bool, len(mappings))
	for _, mapping := range mappings {
		if mapping.Enabled {
			enabledGroups[strings.ToLower(strings.TrimSpace(mapping.GroupExternalID))] = true
		}
	}
	linked := make(map[string]int, len(linkedUsers))
	for _, linkedUser := range linkedUsers {
		linked[strings.ToLower(strings.TrimSpace(linkedUser.ExternalID))] = linkedUser.UserID
	}
	revocations := make([]int, 0)
	for _, directoryUser := range directoryUsers {
		userID, isLinked := linked[strings.ToLower(strings.TrimSpace(directoryUser.ExternalID))]
		if !isLinked || userID <= 0 {
			continue
		}
		allowed := false
		for _, groupID := range directoryUser.GroupExternalIDs {
			if enabledGroups[strings.ToLower(strings.TrimSpace(groupID))] {
				allowed = true
				break
			}
		}
		if !allowed {
			revocations = append(revocations, userID)
		}
	}
	return revocations
}

func (s *ldapService) SearchGroups(
	ctx context.Context,
	providerID string,
	query string,
) ([]pro_interfaces.LDAPDirectoryGroup, error) {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	if providerID == "" || strings.TrimSpace(query) == "" {
		return nil, common_errors.NewValidationError("LDAP group search provider and query are required")
	}
	provider, err := s.repository.GetLDAPProvider(providerID)
	if errors.Is(err, db.ErrNotFound) {
		return nil, pro_interfaces.ErrLDAPProviderNotFound
	}
	if err != nil {
		return nil, err
	}
	configuration, cleanup, err := s.clientConfiguration(provider)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	groups, err := s.client.SearchGroups(ctx, configuration, query)
	if err != nil {
		logLDAPOperationFailure(provider.ID, "group_search", "group_search_failed", err, configuration)
		return nil, err
	}
	return groups, nil
}
