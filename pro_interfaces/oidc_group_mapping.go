package pro_interfaces

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/pkg/common_errors"
)

const (
	OIDCGroupClaimMaxValues      = 256
	OIDCGroupClaimMaxValueLength = 256
	OIDCGroupClaimMaxPathDepth   = 8
)

type OIDCMissingClaimPolicy string

const (
	OIDCMissingClaimPreserve OIDCMissingClaimPolicy = "preserve"
	OIDCMissingClaimClear    OIDCMissingClaimPolicy = "clear"
)

type OIDCGroupClaimConfiguration struct {
	Path               string                 `json:"path"`
	CaseInsensitive    bool                   `json:"case_insensitive"`
	MissingClaimPolicy OIDCMissingClaimPolicy `json:"missing_claim_policy"`
}

type OIDCGroupClaimSet struct {
	Trusted  bool     `json:"trusted"`
	Present  bool     `json:"present"`
	Values   []string `json:"values"`
	Revision string   `json:"revision"`
}

type OIDCRoleScope string

const (
	OIDCRoleScopeGlobal  OIDCRoleScope = "global"
	OIDCRoleScopeProject OIDCRoleScope = "project"
)

type OIDCRoleTarget struct {
	Scope     OIDCRoleScope `json:"scope"`
	ProjectID int           `json:"project_id,omitempty"`
	RoleID    string        `json:"role_id"`
}

func (t OIDCRoleTarget) key() string {
	return string(t.Scope) + ":" + strconv.Itoa(t.ProjectID) + ":" + t.RoleID
}

type OIDCGroupMapping struct {
	ID         string         `json:"id"`
	ProviderID string         `json:"provider_id"`
	ClaimValue string         `json:"claim_value"`
	Target     OIDCRoleTarget `json:"target"`
	Enabled    bool           `json:"enabled"`
	Revision   int            `json:"revision"`
	Created    time.Time      `json:"created,omitempty"`
	Updated    time.Time      `json:"updated,omitempty"`
}

type OIDCRoleAssignment struct {
	UserID                 int            `json:"user_id"`
	Target                 OIDCRoleTarget `json:"target"`
	OwnerKind              string         `json:"owner_kind,omitempty"`
	ManagedByProviderID    string         `json:"managed_by_provider_id,omitempty"`
	ManagedByMappingID     string         `json:"managed_by_mapping_id,omitempty"`
	ProtectedAdministrator bool           `json:"protected_administrator,omitempty"`
}

type OIDCGroupAssignmentChange struct {
	MappingID string         `json:"mapping_id"`
	UserID    int            `json:"user_id"`
	Target    OIDCRoleTarget `json:"target"`
}

type OIDCGroupCollision struct {
	UserID    int            `json:"user_id"`
	MappingID string         `json:"mapping_id"`
	Target    OIDCRoleTarget `json:"target"`
	Reason    string         `json:"reason"`
	Conflicts []string       `json:"conflicts,omitempty"`
}

type OIDCProtectedAdminViolation struct {
	UserID    int            `json:"user_id"`
	MappingID string         `json:"mapping_id"`
	Target    OIDCRoleTarget `json:"target"`
	Reason    string         `json:"reason"`
}

type OIDCGroupPreviewInput struct {
	ProviderID          string
	MappingRevision     int
	CapturedAt          time.Time
	Configuration       OIDCGroupClaimConfiguration
	Claim               OIDCGroupClaimSet
	Mappings            []OIDCGroupMapping
	UserID              int
	ExistingAssignments []OIDCRoleAssignment
}

type OIDCGroupPreview struct {
	Token                    string                        `json:"token"`
	ProviderID               string                        `json:"provider_id"`
	MappingRevision          int                           `json:"mapping_revision"`
	ClaimRevision            string                        `json:"claim_revision"`
	CapturedAt               time.Time                     `json:"captured_at"`
	Preserved                bool                          `json:"preserved"`
	Additions                []OIDCGroupAssignmentChange   `json:"additions"`
	Removals                 []OIDCGroupAssignmentChange   `json:"removals"`
	UnknownValues            []string                      `json:"unknown_values"`
	Collisions               []OIDCGroupCollision          `json:"collisions"`
	ProtectedAdminViolations []OIDCProtectedAdminViolation `json:"protected_admin_violations"`
}

var oidcClaimPathSegmentPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,63}$`)

func NormalizeOIDCGroupClaimConfiguration(configuration *OIDCGroupClaimConfiguration) error {
	if configuration == nil {
		return common_errors.NewValidationError("OIDC group claim configuration is required")
	}
	configuration.Path = strings.TrimSpace(configuration.Path)
	segments := strings.Split(configuration.Path, ".")
	if configuration.Path == "" || len(segments) > OIDCGroupClaimMaxPathDepth {
		return common_errors.NewValidationError("invalid OIDC group claim path")
	}
	for _, segment := range segments {
		if !oidcClaimPathSegmentPattern.MatchString(segment) {
			return common_errors.NewValidationError("invalid OIDC group claim path")
		}
	}
	if configuration.MissingClaimPolicy == "" {
		configuration.MissingClaimPolicy = OIDCMissingClaimPreserve
	}
	if configuration.MissingClaimPolicy != OIDCMissingClaimPreserve &&
		configuration.MissingClaimPolicy != OIDCMissingClaimClear {
		return common_errors.NewValidationError("invalid OIDC missing-claim policy")
	}
	return nil
}

func ParseOIDCGroupClaim(
	claims map[string]any,
	configuration OIDCGroupClaimConfiguration,
) (OIDCGroupClaimSet, error) {
	result := OIDCGroupClaimSet{Values: make([]string, 0)}
	if err := NormalizeOIDCGroupClaimConfiguration(&configuration); err != nil {
		return result, err
	}
	var current any = claims
	for _, segment := range strings.Split(configuration.Path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return result, common_errors.NewValidationError("OIDC group claim path has an invalid object shape")
		}
		value, exists := object[segment]
		if !exists {
			result.Trusted = configuration.MissingClaimPolicy == OIDCMissingClaimClear
			result.Revision = oidcClaimRevision(configuration.Path, false, result.Trusted, nil)
			return result, nil
		}
		current = value
	}

	values := make([]string, 0)
	switch value := current.(type) {
	case string:
		values = append(values, value)
	case []string:
		values = append(values, value...)
	case []any:
		if len(value) > OIDCGroupClaimMaxValues {
			return result, common_errors.NewValidationError("OIDC group claim contains too many values")
		}
		for _, item := range value {
			text, ok := item.(string)
			if !ok {
				return result, common_errors.NewValidationError("OIDC group claim array must contain only strings")
			}
			values = append(values, text)
		}
	default:
		return result, common_errors.NewValidationError("OIDC group claim must be a string or string array")
	}
	if len(values) > OIDCGroupClaimMaxValues {
		return result, common_errors.NewValidationError("OIDC group claim contains too many values")
	}
	deduplicated := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > OIDCGroupClaimMaxValueLength {
			return result, common_errors.NewValidationError("OIDC group claim contains an invalid value")
		}
		if configuration.CaseInsensitive {
			value = strings.ToLower(value)
		}
		deduplicated[value] = true
	}
	for value := range deduplicated {
		result.Values = append(result.Values, value)
	}
	sort.Strings(result.Values)
	result.Trusted = true
	result.Present = true
	result.Revision = oidcClaimRevision(configuration.Path, true, true, result.Values)
	return result, nil
}

func NormalizeOIDCGroupMapping(
	mapping *OIDCGroupMapping,
	configuration OIDCGroupClaimConfiguration,
) error {
	if mapping == nil {
		return common_errors.NewValidationError("OIDC group mapping is required")
	}
	if err := NormalizeOIDCGroupClaimConfiguration(&configuration); err != nil {
		return err
	}
	mapping.ID = strings.ToLower(strings.TrimSpace(mapping.ID))
	mapping.ProviderID = strings.ToLower(strings.TrimSpace(mapping.ProviderID))
	mapping.ClaimValue = strings.TrimSpace(mapping.ClaimValue)
	if configuration.CaseInsensitive {
		mapping.ClaimValue = strings.ToLower(mapping.ClaimValue)
	}
	mapping.Target.RoleID = strings.TrimSpace(mapping.Target.RoleID)
	if !ldapMappingIDPattern.MatchString(mapping.ID) || !ldapMappingIDPattern.MatchString(mapping.ProviderID) {
		return common_errors.NewValidationError("invalid OIDC group mapping identity")
	}
	if mapping.ClaimValue == "" || len(mapping.ClaimValue) > OIDCGroupClaimMaxValueLength {
		return common_errors.NewValidationError("invalid OIDC group claim value")
	}
	if mapping.Target.RoleID == "" || len(mapping.Target.RoleID) > 64 {
		return common_errors.NewValidationError("OIDC group mapping requires a role ID")
	}
	switch mapping.Target.Scope {
	case OIDCRoleScopeGlobal:
		if mapping.Target.ProjectID != 0 {
			return common_errors.NewValidationError("global OIDC role target cannot contain a project")
		}
	case OIDCRoleScopeProject:
		if mapping.Target.ProjectID <= 0 {
			return common_errors.NewValidationError("project OIDC role target requires a project")
		}
	default:
		return common_errors.NewValidationError("invalid OIDC role target scope")
	}
	if mapping.Revision <= 0 {
		mapping.Revision = 1
	}
	return nil
}

func ComputeOIDCGroupPreview(input OIDCGroupPreviewInput) (OIDCGroupPreview, error) {
	preview := OIDCGroupPreview{
		ProviderID:      strings.ToLower(strings.TrimSpace(input.ProviderID)),
		MappingRevision: input.MappingRevision, ClaimRevision: input.Claim.Revision,
		CapturedAt: input.CapturedAt, Additions: make([]OIDCGroupAssignmentChange, 0),
		Removals: make([]OIDCGroupAssignmentChange, 0), UnknownValues: make([]string, 0),
		Collisions:               make([]OIDCGroupCollision, 0),
		ProtectedAdminViolations: make([]OIDCProtectedAdminViolation, 0),
	}
	if preview.ProviderID == "" || input.MappingRevision <= 0 || input.UserID <= 0 ||
		preview.ClaimRevision == "" || input.CapturedAt.IsZero() {
		return preview, common_errors.NewValidationError("OIDC group preview metadata is incomplete")
	}
	mappings := append([]OIDCGroupMapping(nil), input.Mappings...)
	mappingByID := make(map[string]OIDCGroupMapping, len(mappings))
	for index := range mappings {
		if err := NormalizeOIDCGroupMapping(&mappings[index], input.Configuration); err != nil {
			return preview, err
		}
		mapping := mappings[index]
		if mapping.ProviderID != preview.ProviderID {
			return preview, common_errors.NewValidationError("OIDC group mapping provider does not match preview")
		}
		if _, exists := mappingByID[mapping.ID]; exists {
			return preview, common_errors.NewValidationError("duplicate OIDC group mapping ID")
		}
		mappingByID[mapping.ID] = mapping
	}
	if !input.Claim.Trusted {
		preview.Preserved = true
		return finalizeOIDCGroupPreview(preview)
	}

	mappingsByValue := make(map[string][]OIDCGroupMapping)
	for _, mapping := range mappings {
		if mapping.Enabled {
			mappingsByValue[mapping.ClaimValue] = append(mappingsByValue[mapping.ClaimValue], mapping)
		}
	}
	desired := make(map[string]OIDCGroupAssignmentChange)
	projectDesired := make(map[int][]OIDCGroupAssignmentChange)
	for _, value := range input.Claim.Values {
		matching := mappingsByValue[value]
		if len(matching) == 0 {
			preview.UnknownValues = append(preview.UnknownValues, value)
			continue
		}
		for _, mapping := range matching {
			change := OIDCGroupAssignmentChange{MappingID: mapping.ID, UserID: input.UserID, Target: mapping.Target}
			desired[oidcAssignmentKey(change.UserID, change.Target)+":"+mapping.ID] = change
			if mapping.Target.Scope == OIDCRoleScopeProject {
				projectDesired[mapping.Target.ProjectID] = append(projectDesired[mapping.Target.ProjectID], change)
			}
		}
	}
	blocked := make(map[string]bool)
	for _, changes := range projectDesired {
		roles := make(map[string]bool)
		for _, change := range changes {
			roles[change.Target.RoleID] = true
		}
		if len(roles) <= 1 {
			continue
		}
		conflicts := make([]string, 0, len(roles))
		for role := range roles {
			conflicts = append(conflicts, role)
		}
		sort.Strings(conflicts)
		for _, change := range changes {
			blocked[oidcAssignmentKey(change.UserID, change.Target)+":"+change.MappingID] = true
			preview.Collisions = append(preview.Collisions, OIDCGroupCollision{
				UserID: change.UserID, MappingID: change.MappingID, Target: change.Target,
				Reason: "multiple_project_roles", Conflicts: conflicts,
			})
		}
	}
	existingByTarget := make(map[string][]OIDCRoleAssignment)
	existingProject := make(map[int][]OIDCRoleAssignment)
	for _, assignment := range input.ExistingAssignments {
		existingByTarget[oidcAssignmentKey(assignment.UserID, assignment.Target)] = append(
			existingByTarget[oidcAssignmentKey(assignment.UserID, assignment.Target)], assignment)
		if assignment.Target.Scope == OIDCRoleScopeProject {
			existingProject[assignment.Target.ProjectID] = append(existingProject[assignment.Target.ProjectID], assignment)
		}
	}
	for desiredKey, change := range desired {
		if blocked[desiredKey] {
			continue
		}
		owned := false
		foreign := ""
		for _, assignment := range existingByTarget[oidcAssignmentKey(change.UserID, change.Target)] {
			if assignment.OwnerKind == "oidc" &&
				assignment.ManagedByProviderID == preview.ProviderID &&
				assignment.ManagedByMappingID == change.MappingID {
				owned = true
			} else {
				foreign = assignment.OwnerKind
				if foreign == "" {
					foreign = "manual"
				}
			}
		}
		if owned {
			continue
		}
		if change.Target.Scope == OIDCRoleScopeProject {
			for _, assignment := range existingProject[change.Target.ProjectID] {
				if assignment.OwnerKind != "oidc" ||
					assignment.ManagedByProviderID != preview.ProviderID ||
					assignment.ManagedByMappingID != change.MappingID {
					foreign = assignment.OwnerKind
					if foreign == "" {
						foreign = "manual"
					}
					break
				}
			}
		}
		if foreign != "" {
			preview.Collisions = append(preview.Collisions, OIDCGroupCollision{
				UserID: change.UserID, MappingID: change.MappingID, Target: change.Target,
				Reason: foreign + "_assignment_preserved",
			})
			continue
		}
		preview.Additions = append(preview.Additions, change)
	}
	for _, assignment := range input.ExistingAssignments {
		if assignment.OwnerKind != "oidc" ||
			assignment.ManagedByProviderID != preview.ProviderID ||
			assignment.ManagedByMappingID == "" {
			continue
		}
		key := oidcAssignmentKey(assignment.UserID, assignment.Target) + ":" + assignment.ManagedByMappingID
		if desired[key].MappingID != "" {
			continue
		}
		if assignment.ProtectedAdministrator {
			preview.ProtectedAdminViolations = append(preview.ProtectedAdminViolations, OIDCProtectedAdminViolation{
				UserID: assignment.UserID, MappingID: assignment.ManagedByMappingID,
				Target: assignment.Target, Reason: "last_administrator",
			})
			continue
		}
		preview.Removals = append(preview.Removals, OIDCGroupAssignmentChange{
			MappingID: assignment.ManagedByMappingID, UserID: assignment.UserID, Target: assignment.Target,
		})
	}
	return finalizeOIDCGroupPreview(preview)
}

func oidcClaimRevision(path string, present bool, trusted bool, values []string) string {
	payload, _ := json.Marshal(struct {
		Path    string
		Present bool
		Trusted bool
		Values  []string
	}{path, present, trusted, values})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func oidcAssignmentKey(userID int, target OIDCRoleTarget) string {
	return strconv.Itoa(userID) + ":" + target.key()
}

func finalizeOIDCGroupPreview(preview OIDCGroupPreview) (OIDCGroupPreview, error) {
	less := func(left, right OIDCGroupAssignmentChange) bool {
		return oidcAssignmentKey(left.UserID, left.Target)+":"+left.MappingID <
			oidcAssignmentKey(right.UserID, right.Target)+":"+right.MappingID
	}
	sort.Slice(preview.Additions, func(i, j int) bool { return less(preview.Additions[i], preview.Additions[j]) })
	sort.Slice(preview.Removals, func(i, j int) bool { return less(preview.Removals[i], preview.Removals[j]) })
	sort.Strings(preview.UnknownValues)
	sort.Slice(preview.Collisions, func(i, j int) bool {
		left, right := preview.Collisions[i], preview.Collisions[j]
		return oidcAssignmentKey(left.UserID, left.Target)+":"+left.MappingID <
			oidcAssignmentKey(right.UserID, right.Target)+":"+right.MappingID
	})
	sort.Slice(preview.ProtectedAdminViolations, func(i, j int) bool {
		left, right := preview.ProtectedAdminViolations[i], preview.ProtectedAdminViolations[j]
		return oidcAssignmentKey(left.UserID, left.Target)+":"+left.MappingID <
			oidcAssignmentKey(right.UserID, right.Target)+":"+right.MappingID
	})
	payload, err := json.Marshal(struct {
		ProviderID      string
		MappingRevision int
		ClaimRevision   string
		Preserved       bool
		Additions       []OIDCGroupAssignmentChange
		Removals        []OIDCGroupAssignmentChange
		Unknown         []string
		Collisions      []OIDCGroupCollision
		Protected       []OIDCProtectedAdminViolation
	}{preview.ProviderID, preview.MappingRevision, preview.ClaimRevision, preview.Preserved,
		preview.Additions, preview.Removals, preview.UnknownValues, preview.Collisions,
		preview.ProtectedAdminViolations})
	if err != nil {
		return preview, fmt.Errorf("encode OIDC group preview: %w", err)
	}
	digest := sha256.Sum256(payload)
	preview.Token = hex.EncodeToString(digest[:])
	return preview, nil
}
