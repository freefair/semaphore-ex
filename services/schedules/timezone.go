package schedules

import (
	"errors"
	"strings"
	"time"
	"unicode"
)

const maxTimezoneLength = 128

var errInvalidTimezone = errors.New("invalid timezone")

// ValidateTimezone accepts a strict, useful subset of IANA timezone names.
// It keeps untrusted timezone values out of CRON_TZ expressions and never
// exposes parser or filesystem details to callers.
func ValidateTimezone(timezone string) error {
	if timezone == "UTC" {
		return nil
	}

	if len(timezone) == 0 || len(timezone) > maxTimezoneLength ||
		strings.TrimSpace(timezone) != timezone ||
		strings.Contains(timezone, "..") ||
		strings.HasPrefix(timezone, "/") ||
		strings.HasSuffix(timezone, "/") ||
		strings.Contains(timezone, "//") {
		return errInvalidTimezone
	}

	for _, character := range timezone {
		if unicode.IsControl(character) || !isTimezoneCharacter(character) {
			return errInvalidTimezone
		}
	}

	if !strings.Contains(timezone, "/") {
		return errInvalidTimezone
	}

	if _, err := time.LoadLocation(timezone); err != nil {
		return errInvalidTimezone
	}

	return nil
}

func isTimezoneCharacter(character rune) bool {
	return character == '/' || character == '_' || character == '-' || character == '+' ||
		(character >= 'a' && character <= 'z') ||
		(character >= 'A' && character <= 'Z') ||
		(character >= '0' && character <= '9')
}
