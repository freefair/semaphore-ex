// Package debuglog provides validated, reloadable component filters for
// DEBUG-level console, syslog, and structured output.
package debuglog

import (
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/semaphoreui/semaphore/pro_interfaces"
)

const (
	DebugFilterDefaultAll        = "all"
	DebugFilterDefaultConfigured = "configured"
)

var componentPattern = regexp.MustCompile(`^[a-z][a-z0-9_.:-]*$`)

type compiledFilter struct {
	diagnostics    pro_interfaces.DebugFilterDiagnostics
	defaultAll     bool
	exactIncludes  map[string]struct{}
	prefixIncludes []string
	exactExcludes  map[string]struct{}
	prefixExcludes []string
}

// Manager owns one instance's immutable compiled filter. Reload serializes
// compilation while Enabled remains lock-free through an atomic snapshot.
type Manager struct {
	instance string
	reloadMu sync.Mutex
	current  atomic.Pointer[compiledFilter]
}

func NewManager(instance, spec string, loadedAt time.Time) *Manager {
	manager := &Manager{instance: instance}
	manager.Reload(spec, loadedAt)
	return manager
}

func NewManagerWithReloadError(instance, message string, loadedAt time.Time) *Manager {
	manager := &Manager{instance: instance}
	manager.RecordReloadError(message, loadedAt)
	return manager
}

func (m *Manager) Reload(spec string, loadedAt time.Time) pro_interfaces.DebugFilterDiagnostics {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	compiled := compile(m.instance, spec, loadedAt)
	m.current.Store(compiled)
	return cloneDiagnostics(compiled.diagnostics)
}

// RecordReloadError retains the last-known-good decision and reports the
// bounded source failure rather than broadening collection.
func (m *Manager) RecordReloadError(message string, loadedAt time.Time) pro_interfaces.DebugFilterDiagnostics {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	previous := m.current.Load()
	if previous == nil {
		previous = denyAll(m.instance, loadedAt)
	}
	next := *previous
	next.diagnostics = cloneDiagnostics(previous.diagnostics)
	next.diagnostics.ReloadedAt = loadedAt.UTC()
	next.diagnostics.ReloadError = sanitizeReloadError(message)
	m.current.Store(&next)
	return cloneDiagnostics(next.diagnostics)
}

func denyAll(instance string, loadedAt time.Time) *compiledFilter {
	return &compiledFilter{
		diagnostics: pro_interfaces.DebugFilterDiagnostics{
			Instance: instance, Default: DebugFilterDefaultConfigured, ReloadedAt: loadedAt.UTC(),
			Configured: []string{}, Effective: []string{}, Rejected: []pro_interfaces.DebugFilterRejectedEntry{},
		},
		exactIncludes: map[string]struct{}{}, exactExcludes: map[string]struct{}{},
	}
}

func (m *Manager) Enabled(component string) bool {
	current := m.current.Load()
	if current == nil {
		return false
	}
	if current.defaultAll {
		return true
	}
	if !matches(component, current.exactIncludes, current.prefixIncludes) {
		return false
	}
	return !matches(component, current.exactExcludes, current.prefixExcludes)
}

func (m *Manager) Diagnostics() pro_interfaces.DebugFilterDiagnostics {
	current := m.current.Load()
	if current == nil {
		return cloneDiagnostics(denyAll(m.instance, time.Time{}).diagnostics)
	}
	return cloneDiagnostics(current.diagnostics)
}

func compile(instance, spec string, loadedAt time.Time) *compiledFilter {
	tokens := splitSpec(spec)
	compiled := &compiledFilter{
		diagnostics: pro_interfaces.DebugFilterDiagnostics{
			Instance: instance, ReloadedAt: loadedAt.UTC(), Configured: append([]string{}, tokens...),
			Effective: []string{}, Rejected: []pro_interfaces.DebugFilterRejectedEntry{},
		},
		exactIncludes: map[string]struct{}{}, exactExcludes: map[string]struct{}{},
	}
	if len(tokens) == 0 {
		compiled.defaultAll = true
		compiled.diagnostics.Default = DebugFilterDefaultAll
		compiled.diagnostics.Effective = []string{"*"}
		return compiled
	}
	compiled.diagnostics.Default = DebugFilterDefaultConfigured
	seen := map[string]struct{}{}
	for _, token := range tokens {
		negative := strings.HasPrefix(token, "-")
		pattern := strings.TrimPrefix(token, "-")
		prefix, reason := validatePattern(pattern)
		if reason != "" {
			compiled.diagnostics.Rejected = append(compiled.diagnostics.Rejected,
				pro_interfaces.DebugFilterRejectedEntry{Entry: token, Reason: reason})
			continue
		}
		if _, duplicate := seen[token]; duplicate {
			continue
		}
		seen[token] = struct{}{}
		compiled.diagnostics.Effective = append(compiled.diagnostics.Effective, token)
		value := strings.TrimSuffix(pattern, "*")
		switch {
		case negative && prefix:
			compiled.prefixExcludes = append(compiled.prefixExcludes, value)
		case negative:
			compiled.exactExcludes[value] = struct{}{}
		case prefix:
			compiled.prefixIncludes = append(compiled.prefixIncludes, value)
		default:
			compiled.exactIncludes[value] = struct{}{}
		}
	}
	return compiled
}

func splitSpec(spec string) []string {
	return strings.FieldsFunc(spec, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
}

func validatePattern(pattern string) (prefix bool, reason string) {
	if pattern == "*" {
		return true, ""
	}
	if pattern == "" {
		return false, "empty_entry"
	}
	if strings.Count(pattern, "*") > 1 || (strings.Contains(pattern, "*") && !strings.HasSuffix(pattern, "*")) {
		return false, "wildcard_must_be_terminal"
	}
	prefix = strings.HasSuffix(pattern, "*")
	name := strings.TrimSuffix(pattern, "*")
	if !componentPattern.MatchString(name) {
		return false, "invalid_component"
	}
	return prefix, ""
}

func matches(component string, exact map[string]struct{}, prefixes []string) bool {
	if _, ok := exact[component]; ok {
		return true
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(component, prefix) {
			return true
		}
	}
	return false
}

func sanitizeReloadError(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	if len(message) <= 256 {
		return message
	}
	message = message[:256]
	for !utf8.ValidString(message) {
		message = message[:len(message)-1]
	}
	return message
}

func cloneDiagnostics(value pro_interfaces.DebugFilterDiagnostics) pro_interfaces.DebugFilterDiagnostics {
	value.Configured = append([]string{}, value.Configured...)
	value.Effective = append([]string{}, value.Effective...)
	value.Rejected = append([]pro_interfaces.DebugFilterRejectedEntry{}, value.Rejected...)
	return value
}

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
