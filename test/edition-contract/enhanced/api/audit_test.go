package api

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseAuditFilter_RejectsAmbiguousAndUntrustedInput(t *testing.T) {
	for name, query := range map[string]url.Values{
		"unknown":         {"surprise": {"1"}},
		"repeated scalar": {"user": {"1", "2"}},
		"both cursors":    {"before": {"2"}, "after": {"1"}},
		"overflow cursor": {"after": {"9223372036854775807"}},
		"bad kind":        {"kind": {"auth.login"}},
		"partial target":  {"target_type": {"user"}},
		"invalid ip":      {"ip": {"not-an-ip"}},
	} {
		t.Run(name, func(t *testing.T) { _, err := parseAuditFilter(query, false); assert.Error(t, err) })
	}
}

func TestParseAuditFilter_AcceptsAllUIFilters(t *testing.T) {
	f, err := parseAuditFilter(url.Values{"from": {"2026-10-12T10:00:00Z"}, "to": {"2026-10-12T11:00:00Z"}, "user": {"7"}, "project": {"3"}, "kind": {"auth.login/authenticate", "iam.user/create"}, "outcome": {"success"}, "ip": {"127.0.0.1"}, "target_type": {"user"}, "target_id": {"7"}}, false)
	assert.NoError(t, err)
	assert.Len(t, f.Kinds, 2)
	assert.Equal(t, "user", f.TargetType)
}

func TestCSVSafe_PrefixesSpreadsheetFormulaIntroducers(t *testing.T) {
	for _, value := range []string{"=cmd", "+cmd", "-cmd", "@cmd", "\tcmd", "\rcmd", "\ncmd"} {
		assert.Equal(t, "'"+value, csvSafe(value))
	}
}
