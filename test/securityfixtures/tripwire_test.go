package securityfixtures

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestContainsTripwire(t *testing.T) {
	assert.True(t, ContainsTripwire("prefix "+TripwireValues[1]+" suffix"))
	assert.False(t, ContainsTripwire("stable safe context"))
}
