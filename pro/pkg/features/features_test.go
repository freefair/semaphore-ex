package features

import (
	"testing"

	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
)

func TestCommunityFeaturesAreDisabled(t *testing.T) {
	assert.Equal(t, pro_interfaces.Features{}, GetFeatures(nil, "enhanced"))
}

func TestCommunityCompatibilityMatchesCore(t *testing.T) {
	compatibility := Compatibility()

	assert.Equal(t, pro_interfaces.EditionCommunity, compatibility.Edition)
	assert.Equal(t, pro_interfaces.CoreContractVersion, compatibility.ContractVersion)
	assert.NotEmpty(t, compatibility.Implementation)
}
