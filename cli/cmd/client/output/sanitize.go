package output

import "github.com/semaphoreui/semaphore/pkg/termsafe"

// Sanitize removes terminal control sequences from server text; see termsafe.String.
func Sanitize(text string, keepColor bool) string {
	return termsafe.String(text, keepColor)
}
