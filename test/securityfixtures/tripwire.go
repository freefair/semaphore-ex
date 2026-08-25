package securityfixtures

import (
	"strings"
	"testing"
)

var TripwireValues = []string{
	"tripwire-private-material-a7f26c",
	"tripwire-bearer-material-f91d42",
	"tripwire-connection-material-6bd883",
}

func AssertTripwiresAbsent(t testing.TB, outputs ...string) {
	t.Helper()
	if ContainsTripwire(outputs...) {
		t.Errorf("security tripwire appeared in observable output")
	}
}

func ContainsTripwire(outputs ...string) bool {
	for _, output := range outputs {
		for _, tripwire := range TripwireValues {
			if strings.Contains(output, tripwire) {
				return true
			}
		}
	}
	return false
}
