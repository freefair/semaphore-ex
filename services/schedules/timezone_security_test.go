package schedules

import (
	"strings"
	"testing"
)

func TestValidateTimezoneAcceptsIANAZoneNames(t *testing.T) {
	for _, timezone := range []string{
		"UTC",
		"Europe/Berlin",
		"America/New_York",
		"Etc/GMT+5",
	} {
		t.Run(strings.NewReplacer("/", "_", "+", "plus").Replace(timezone), func(t *testing.T) {
			if err := ValidateTimezone(timezone); err != nil {
				t.Fatalf("ValidateTimezone(%q) returned %v", timezone, err)
			}
		})
	}
}

func TestValidateTimezoneRejectsUnsafeOrAmbiguousValues(t *testing.T) {
	tooLong := strings.Repeat("A", maxTimezoneLength+1)
	for _, timezone := range []string{
		"",
		" UTC",
		"UTC ",
		"Europe/ Berlin",
		"CET",
		"PST",
		"Local",
		"Europe/../UTC",
		"Europe/Berlin\n",
		"America/New_York\x00",
		"Mars/Olympus",
		tooLong,
	} {
		t.Run(strings.NewReplacer("/", "_", " ", "space", "\n", "newline", "\x00", "nul").Replace(timezone), func(t *testing.T) {
			err := ValidateTimezone(timezone)
			if err == nil {
				t.Fatalf("ValidateTimezone(%q) unexpectedly succeeded", timezone)
			}
			if err.Error() != "invalid timezone" {
				t.Fatalf("unsafe error %q", err)
			}
		})
	}
}
