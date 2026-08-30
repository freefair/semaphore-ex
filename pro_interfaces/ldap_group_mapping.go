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

// LDAPRoleScope identifies the namespace in which a mapping grants a role.
type LDAPRoleScope string

const (
	LDAPRoleScopeGlobal  LDAPRoleScope = "global"
	LDAPRoleScopeProject LDAPRoleScope = "project"
)

// LDAPRoleTarget addresses a role by its immutable identifier.
type LDAPRoleTarget struct {
	Scope     LDAPRoleScope `json:"scope"`
	ProjectID int           `json:"project_id,omitempty"`
	RoleID    string        `json:"role_id"`
}

func (t LDAPRoleTarget) key() string {
	return string(t.Scope) + ":" + strconv.Itoa(t.ProjectID) + ":" + t.RoleID
}

// LDAPGroupMapping maps one immutable directory group identity to one role.
type LDAPGroupMapping struct {
	ID              string         `json:"id"`
	ProviderID      string         `json:"provider_id"`
	GroupExternalID string         `json:"group_external_id"`
	Target          LDAPRoleTarget `json:"target"`
	Enabled         bool           `json:"enabled"`
	Revision        int            `json:"revision"`
	Created         time.Time      `json:"created,omitempty"`
	Updated         time.Time      `json:"updated,omitempty"`
}

type LDAPDirectoryUser struct {
	ExternalID       string   `json:"external_id"`
	GroupExternalIDs []string `json:"group_external_ids"`
}

type LDAPLinkedUser struct {
	ExternalID string `json:"external_id"`
	UserID     int    `json:"user_id"`
}

// LDAPRoleAssignment is the role state used by the pure reconciliation model.
// ManagedByMappingID is empty for a manual assignment.
type LDAPRoleAssignment struct {
	UserID                 int            `json:"user_id"`
	Target                 LDAPRoleTarget `json:"target"`
	ManagedByMappingID     string         `json:"managed_by_mapping_id,omitempty"`
	ProtectedAdministrator bool           `json:"protected_administrator,omitempty"`
}

type LDAPGroupAssignmentChange struct {
	MappingID string         `json:"mapping_id"`
	UserID    int            `json:"user_id"`
	Target    LDAPRoleTarget `json:"target"`
}

type LDAPGroupUnresolvedItem struct {
	Kind       string `json:"kind"`
	ExternalID string `json:"external_id"`
	MappingID  string `json:"mapping_id,omitempty"`
	Reason     string `json:"reason"`
}

type LDAPGroupCollision struct {
	UserID    int            `json:"user_id"`
	MappingID string         `json:"mapping_id"`
	Target    LDAPRoleTarget `json:"target"`
	Reason    string         `json:"reason"`
	Conflicts []string       `json:"conflicts,omitempty"`
}

type LDAPProtectedAdminViolation struct {
	UserID    int            `json:"user_id"`
	MappingID string         `json:"mapping_id"`
	Target    LDAPRoleTarget `json:"target"`
	Reason    string         `json:"reason"`
}

type LDAPGroupPreviewInput struct {
	ProviderID          string
	MappingRevision     int
	DirectoryRevision   string
	CapturedAt          time.Time
	Mappings            []LDAPGroupMapping
	DirectoryGroupIDs   []string
	DirectoryUsers      []LDAPDirectoryUser
	LinkedUsers         []LDAPLinkedUser
	ExistingAssignments []LDAPRoleAssignment
}

type LDAPGroupPreview struct {
	Token                    string                        `json:"token"`
	ProviderID               string                        `json:"provider_id"`
	MappingRevision          int                           `json:"mapping_revision"`
	DirectoryRevision        string                        `json:"directory_revision"`
	CapturedAt               time.Time                     `json:"captured_at"`
	Additions                []LDAPGroupAssignmentChange   `json:"additions"`
	Removals                 []LDAPGroupAssignmentChange   `json:"removals"`
	Unresolved               []LDAPGroupUnresolvedItem     `json:"unresolved"`
	Collisions               []LDAPGroupCollision          `json:"collisions"`
	ProtectedAdminViolations []LDAPProtectedAdminViolation `json:"protected_admin_violations"`
}

func (p LDAPGroupPreview) IsFresh(mappingRevision int, directoryRevision string) bool {
	return p.MappingRevision == mappingRevision && p.DirectoryRevision == directoryRevision
}

var ldapMappingIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
var ldapImmutableIdentityPattern = regexp.MustCompile(
	`^(entryuuid|objectguid|nsuniqueid|ipauniqueid):[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
)

// NormalizeLDAPGroupMapping canonicalizes external identifiers and validates
// that the target is explicit and immutable.
func NormalizeLDAPGroupMapping(mapping *LDAPGroupMapping) error {
	if mapping == nil {
		return common_errors.NewValidationError("LDAP group mapping is required")
	}
	mapping.ID = strings.ToLower(strings.TrimSpace(mapping.ID))
	mapping.ProviderID = strings.ToLower(strings.TrimSpace(mapping.ProviderID))
	mapping.GroupExternalID = strings.ToLower(strings.TrimSpace(mapping.GroupExternalID))
	mapping.Target.RoleID = strings.TrimSpace(mapping.Target.RoleID)
	if !ldapMappingIDPattern.MatchString(mapping.ID) {
		return common_errors.NewValidationError("invalid LDAP group mapping ID")
	}
	if !ldapMappingIDPattern.MatchString(mapping.ProviderID) {
		return common_errors.NewValidationError("invalid LDAP provider ID")
	}
	if !ldapImmutableIdentityPattern.MatchString(mapping.GroupExternalID) {
		return common_errors.NewValidationError("LDAP group identity must use an allow-listed immutable UUID attribute")
	}
	if mapping.Target.RoleID == "" || len(mapping.Target.RoleID) > 64 {
		return common_errors.NewValidationError("LDAP group mapping requires a role ID")
	}
	switch mapping.Target.Scope {
	case LDAPRoleScopeGlobal:
		if mapping.Target.ProjectID != 0 {
			return common_errors.NewValidationError("global LDAP role target cannot contain a project")
		}
	case LDAPRoleScopeProject:
		if mapping.Target.ProjectID <= 0 {
			return common_errors.NewValidationError("project LDAP role target requires a project")
		}
	default:
		return common_errors.NewValidationError("invalid LDAP role target scope")
	}
	if mapping.Revision <= 0 {
		mapping.Revision = 1
	}
	return nil
}

type ldapDesiredAssignment struct {
	change LDAPGroupAssignmentChange
}

// ComputeLDAPGroupPreview produces a deterministic, side-effect-free diff.
// It never adopts or removes manual role assignments.
func ComputeLDAPGroupPreview(input LDAPGroupPreviewInput) (LDAPGroupPreview, error) {
	preview := LDAPGroupPreview{
		ProviderID:               strings.ToLower(strings.TrimSpace(input.ProviderID)),
		MappingRevision:          input.MappingRevision,
		DirectoryRevision:        strings.TrimSpace(input.DirectoryRevision),
		CapturedAt:               input.CapturedAt,
		Additions:                make([]LDAPGroupAssignmentChange, 0),
		Removals:                 make([]LDAPGroupAssignmentChange, 0),
		Unresolved:               make([]LDAPGroupUnresolvedItem, 0),
		Collisions:               make([]LDAPGroupCollision, 0),
		ProtectedAdminViolations: make([]LDAPProtectedAdminViolation, 0),
	}
	if preview.ProviderID == "" || input.MappingRevision <= 0 || preview.DirectoryRevision == "" || input.CapturedAt.IsZero() {
		return preview, common_errors.NewValidationError("LDAP group preview metadata is incomplete")
	}

	mappings := append([]LDAPGroupMapping(nil), input.Mappings...)
	mappingByID := make(map[string]LDAPGroupMapping, len(mappings))
	for index := range mappings {
		if err := NormalizeLDAPGroupMapping(&mappings[index]); err != nil {
			return preview, err
		}
		mapping := mappings[index]
		if mapping.ProviderID != preview.ProviderID {
			return preview, common_errors.NewValidationError("LDAP group mapping provider does not match preview")
		}
		if _, exists := mappingByID[mapping.ID]; exists {
			return preview, common_errors.NewValidationError("duplicate LDAP group mapping ID")
		}
		mappingByID[mapping.ID] = mapping
	}

	groups := make(map[string]bool, len(input.DirectoryGroupIDs))
	for _, groupID := range input.DirectoryGroupIDs {
		groups[strings.ToLower(strings.TrimSpace(groupID))] = true
	}
	linked := make(map[string]int, len(input.LinkedUsers))
	for _, user := range input.LinkedUsers {
		externalID := strings.ToLower(strings.TrimSpace(user.ExternalID))
		if externalID != "" && user.UserID > 0 {
			linked[externalID] = user.UserID
		}
	}

	desired := make(map[string]ldapDesiredAssignment)
	projectDesired := make(map[string][]ldapDesiredAssignment)
	for _, directoryUser := range input.DirectoryUsers {
		externalID := strings.ToLower(strings.TrimSpace(directoryUser.ExternalID))
		userID, found := linked[externalID]
		if !found {
			preview.Unresolved = append(preview.Unresolved, LDAPGroupUnresolvedItem{
				Kind: "user", ExternalID: externalID, Reason: "directory_user_not_linked",
			})
			continue
		}
		memberships := make(map[string]bool, len(directoryUser.GroupExternalIDs))
		for _, groupID := range directoryUser.GroupExternalIDs {
			memberships[strings.ToLower(strings.TrimSpace(groupID))] = true
		}
		for _, mapping := range mappings {
			if !mapping.Enabled || !memberships[mapping.GroupExternalID] {
				continue
			}
			entry := ldapDesiredAssignment{change: LDAPGroupAssignmentChange{
				MappingID: mapping.ID, UserID: userID, Target: mapping.Target,
			}}
			key := assignmentKey(userID, mapping.Target)
			desired[key+":"+mapping.ID] = entry
			if mapping.Target.Scope == LDAPRoleScopeProject {
				projectKey := strconv.Itoa(userID) + ":" + strconv.Itoa(mapping.Target.ProjectID)
				projectDesired[projectKey] = append(projectDesired[projectKey], entry)
			}
		}
	}
	for _, mapping := range mappings {
		if mapping.Enabled && len(groups) > 0 && !groups[mapping.GroupExternalID] {
			preview.Unresolved = append(preview.Unresolved, LDAPGroupUnresolvedItem{
				Kind: "group", ExternalID: mapping.GroupExternalID, MappingID: mapping.ID,
				Reason: "directory_group_not_found",
			})
		}
	}

	blockedDesired := make(map[string]bool)
	for _, entries := range projectDesired {
		roles := make(map[string]bool)
		for _, entry := range entries {
			roles[entry.change.Target.RoleID] = true
		}
		if len(roles) <= 1 {
			continue
		}
		conflicts := make([]string, 0, len(roles))
		for roleID := range roles {
			conflicts = append(conflicts, roleID)
		}
		sort.Strings(conflicts)
		for _, entry := range entries {
			blockedDesired[assignmentKey(entry.change.UserID, entry.change.Target)+":"+entry.change.MappingID] = true
			preview.Collisions = append(preview.Collisions, LDAPGroupCollision{
				UserID: entry.change.UserID, MappingID: entry.change.MappingID, Target: entry.change.Target,
				Reason: "multiple_project_roles", Conflicts: conflicts,
			})
		}
	}

	existingByTarget := make(map[string][]LDAPRoleAssignment)
	existingProject := make(map[string][]LDAPRoleAssignment)
	for _, assignment := range input.ExistingAssignments {
		existingByTarget[assignmentKey(assignment.UserID, assignment.Target)] = append(
			existingByTarget[assignmentKey(assignment.UserID, assignment.Target)], assignment)
		if assignment.Target.Scope == LDAPRoleScopeProject {
			projectKey := strconv.Itoa(assignment.UserID) + ":" + strconv.Itoa(assignment.Target.ProjectID)
			existingProject[projectKey] = append(existingProject[projectKey], assignment)
		}
	}
	for desiredKey, entry := range desired {
		if blockedDesired[desiredKey] {
			continue
		}
		existing := existingByTarget[assignmentKey(entry.change.UserID, entry.change.Target)]
		owned := false
		manual := false
		for _, assignment := range existing {
			owned = owned || assignment.ManagedByMappingID == entry.change.MappingID
			manual = manual || assignment.ManagedByMappingID == ""
		}
		if owned {
			continue
		}
		if entry.change.Target.Scope == LDAPRoleScopeProject {
			projectKey := strconv.Itoa(entry.change.UserID) + ":" + strconv.Itoa(entry.change.Target.ProjectID)
			for _, assignment := range existingProject[projectKey] {
				if assignment.ManagedByMappingID != entry.change.MappingID {
					preview.Collisions = append(preview.Collisions, LDAPGroupCollision{
						UserID: entry.change.UserID, MappingID: entry.change.MappingID, Target: entry.change.Target,
						Reason: "existing_project_membership_preserved",
						Conflicts: []string{assignment.Target.RoleID},
					})
					manual = false
					owned = true
					break
				}
			}
			if owned {
				continue
			}
		}
		if manual {
			preview.Collisions = append(preview.Collisions, LDAPGroupCollision{
				UserID: entry.change.UserID, MappingID: entry.change.MappingID, Target: entry.change.Target,
				Reason: "manual_assignment_preserved",
			})
			continue
		}
		preview.Additions = append(preview.Additions, entry.change)
	}

	for _, assignment := range input.ExistingAssignments {
		if assignment.ManagedByMappingID == "" {
			continue
		}
		mapping, mappingExists := mappingByID[assignment.ManagedByMappingID]
		desiredKey := assignmentKey(assignment.UserID, assignment.Target) + ":" + assignment.ManagedByMappingID
		if mappingExists && mapping.Enabled && desired[desiredKey].change.MappingID != "" {
			continue
		}
		if assignment.ProtectedAdministrator {
			preview.ProtectedAdminViolations = append(preview.ProtectedAdminViolations, LDAPProtectedAdminViolation{
				UserID: assignment.UserID, MappingID: assignment.ManagedByMappingID,
				Target: assignment.Target, Reason: "last_administrator",
			})
			continue
		}
		preview.Removals = append(preview.Removals, LDAPGroupAssignmentChange{
			MappingID: assignment.ManagedByMappingID, UserID: assignment.UserID, Target: assignment.Target,
		})
	}

	sortLDAPGroupPreview(&preview)
	payload, err := json.Marshal(struct {
		ProviderID        string
		MappingRevision   int
		DirectoryRevision string
		Additions         []LDAPGroupAssignmentChange
		Removals          []LDAPGroupAssignmentChange
		Unresolved        []LDAPGroupUnresolvedItem
		Collisions        []LDAPGroupCollision
		Protected         []LDAPProtectedAdminViolation
	}{preview.ProviderID, preview.MappingRevision, preview.DirectoryRevision, preview.Additions,
		preview.Removals, preview.Unresolved, preview.Collisions, preview.ProtectedAdminViolations})
	if err != nil {
		return preview, fmt.Errorf("encode LDAP group preview: %w", err)
	}
	digest := sha256.Sum256(payload)
	preview.Token = hex.EncodeToString(digest[:])
	return preview, nil
}

func assignmentKey(userID int, target LDAPRoleTarget) string {
	return strconv.Itoa(userID) + ":" + target.key()
}

func sortLDAPGroupPreview(preview *LDAPGroupPreview) {
	changeLess := func(left, right LDAPGroupAssignmentChange) bool {
		return assignmentKey(left.UserID, left.Target)+":"+left.MappingID <
			assignmentKey(right.UserID, right.Target)+":"+right.MappingID
	}
	sort.Slice(preview.Additions, func(i, j int) bool { return changeLess(preview.Additions[i], preview.Additions[j]) })
	sort.Slice(preview.Removals, func(i, j int) bool { return changeLess(preview.Removals[i], preview.Removals[j]) })
	sort.Slice(preview.Unresolved, func(i, j int) bool {
		left, right := preview.Unresolved[i], preview.Unresolved[j]
		return left.Kind+":"+left.ExternalID+":"+left.MappingID < right.Kind+":"+right.ExternalID+":"+right.MappingID
	})
	sort.Slice(preview.Collisions, func(i, j int) bool {
		left, right := preview.Collisions[i], preview.Collisions[j]
		return assignmentKey(left.UserID, left.Target)+":"+left.MappingID < assignmentKey(right.UserID, right.Target)+":"+right.MappingID
	})
	sort.Slice(preview.ProtectedAdminViolations, func(i, j int) bool {
		left, right := preview.ProtectedAdminViolations[i], preview.ProtectedAdminViolations[j]
		return assignmentKey(left.UserID, left.Target)+":"+left.MappingID < assignmentKey(right.UserID, right.Target)+":"+right.MappingID
	})
}
