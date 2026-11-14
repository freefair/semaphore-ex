package pro_interfaces

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// D1: directory people without a linked account are skipped instead of
// blocking the apply; a mapped group missing from the directory still blocks.
func TestComputeLDAPGroupPreviewSkipsUnlinkedUsersButKeepsMissingGroupsBlocking(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	groupID := "entryuuid:50f1c82a-b773-4d41-a587-7c4cf7f3cd67"
	missingGroupID := "entryuuid:60f1c82a-b773-4d41-a587-7c4cf7f3cd67"
	target := LDAPRoleTarget{Scope: LDAPRoleScopeGlobal, RoleID: "auditors"}
	input := LDAPGroupPreviewInput{
		ProviderID: "corp", MappingRevision: 3, DirectoryRevision: "three", CapturedAt: now,
		Mappings: []LDAPGroupMapping{
			{ID: "auditors", ProviderID: "corp", GroupExternalID: groupID, Target: target, Enabled: true, Revision: 1},
		},
		DirectoryGroupIDs: []string{groupID},
		DirectoryUsers: []LDAPDirectoryUser{
			{ExternalID: "entryuuid:linked", GroupExternalIDs: []string{groupID}},
			{ExternalID: "entryuuid:never-signed-in", GroupExternalIDs: []string{groupID}},
		},
		LinkedUsers: []LDAPLinkedUser{{ExternalID: "entryuuid:linked", UserID: 4}},
	}

	preview, err := ComputeLDAPGroupPreview(input)

	require.NoError(t, err)
	assert.Empty(t, preview.Unresolved)
	require.Len(t, preview.Skipped, 1)
	assert.Equal(t, "entryuuid:never-signed-in", preview.Skipped[0].ExternalID)
	assert.Equal(t, "directory_user_not_linked", preview.Skipped[0].Reason)
	require.Len(t, preview.Additions, 1)
	assert.Equal(t, 4, preview.Additions[0].UserID)

	input.Mappings = append(input.Mappings, LDAPGroupMapping{
		ID: "gone", ProviderID: "corp", GroupExternalID: missingGroupID, Target: target, Enabled: true, Revision: 1,
	})
	blocked, err := ComputeLDAPGroupPreview(input)

	require.NoError(t, err)
	require.Len(t, blocked.Unresolved, 1)
	assert.Equal(t, "directory_group_not_found", blocked.Unresolved[0].Reason)
	assert.Len(t, blocked.Skipped, 1)
}
