package features

import (
	"testing"

	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
)

func TestLDAPGroupAccessRevocationsFailClosed(t *testing.T) {
	mapped := "entryuuid:aaaaaaaa-0000-0000-0000-000000000001"
	unmapped := "entryuuid:aaaaaaaa-0000-0000-0000-000000000002"
	disabledOnly := "entryuuid:aaaaaaaa-0000-0000-0000-000000000003"
	mappings := []pro_interfaces.LDAPGroupMapping{
		{ID: "ops", GroupExternalID: mapped, Enabled: true},
		{ID: "old", GroupExternalID: disabledOnly, Enabled: false},
	}
	directory := []pro_interfaces.LDAPDirectoryUser{
		{ExternalID: "entryuuid:in-mapped", GroupExternalIDs: []string{unmapped, mapped}},
		{ExternalID: "entryuuid:in-unmapped", GroupExternalIDs: []string{unmapped}},
		{ExternalID: "entryuuid:in-disabled", GroupExternalIDs: []string{disabledOnly}},
		{ExternalID: "entryuuid:never-linked", GroupExternalIDs: []string{unmapped}},
	}
	linked := []pro_interfaces.LDAPLinkedUser{
		{ExternalID: "ENTRYUUID:IN-MAPPED", UserID: 1},
		{ExternalID: "entryuuid:in-unmapped", UserID: 2},
		{ExternalID: "entryuuid:in-disabled", UserID: 3},
		{ExternalID: "entryuuid:removed-from-directory", UserID: 4},
		{ExternalID: "entryuuid:invalid", UserID: 0},
	}

	revocations := ldapGroupAccessRevocations(directory, linked, mappings)

	assert.ElementsMatch(t, []int{2, 3, 4}, revocations,
		"unmapped, disabled-only and vanished people lose access; mapped people keep it")
}
