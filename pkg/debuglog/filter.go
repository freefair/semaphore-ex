// Package debuglog provides validated, reloadable component filters for
// DEBUG-level console, syslog, and structured output.
package debuglog

import (
	"regexp"
	"time"
)

var componentPattern = regexp.MustCompile(`^[a-z][a-z0-9_.:-]*$`)

// Filter preserves the focused baseline API while delegating to the validated
// manager. New runtime code should own a Manager directly.
type Filter struct{ manager *Manager }

func Parse(spec string) *Filter {
	return &Filter{manager: NewManager("", spec, time.Now().UTC())}
}

func (f *Filter) Enabled(namespace string) bool {
	return f != nil && f.manager.Enabled(namespace)
}

func (f *Filter) Active() bool {
	return f != nil && len(f.manager.Diagnostics().Configured) > 0
}
