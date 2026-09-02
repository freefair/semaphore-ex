package db

import (
	"fmt"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"strings"
	"unicode/utf8"
)

// MaxTemplateSearchLength bounds list filtering work and request size. The
// limit is measured in Unicode code points so non-ASCII names are not treated
// as disproportionately large input.
const MaxTemplateSearchLength = 256

// ValidateSearch rejects malformed or unbounded search input before it reaches
// the repository. Control characters remain valid literal input; the SQL
// candidate query is parameterized and Go remains the final matcher.
func (f TemplateFilter) ValidateSearch() error {
	if !utf8.ValidString(f.Search) {
		return common_errors.NewValidationError("template search must be valid UTF-8")
	}
	if len([]rune(f.Search)) > MaxTemplateSearchLength {
		return common_errors.NewValidationError(
			fmt.Sprintf("template search must be at most %d characters", MaxTemplateSearchLength),
		)
	}
	return nil
}

// NormalizeTemplateSearch applies the matching normalization used by both the
// query and every searchable field. It trims incidental outer whitespace while
// preserving embedded whitespace and control characters as literal input.
func NormalizeTemplateSearch(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// MatchesSearch reports whether a normalized literal term occurs in any field
// exposed by template list search. An empty term is a match so the historical
// unfiltered list behavior remains unchanged.
func (tpl Template) MatchesSearch(normalizedSearch string) bool {
	if normalizedSearch == "" {
		return true
	}

	fields := []string{tpl.Name, tpl.Playbook}
	if tpl.Description != nil {
		fields = append(fields, *tpl.Description)
	}
	if tpl.RunnerTag != nil {
		fields = append(fields, *tpl.RunnerTag)
	}
	fields = append(fields, tpl.RunnerTags...)

	for _, field := range fields {
		if strings.Contains(NormalizeTemplateSearch(field), normalizedSearch) {
			return true
		}
	}

	return false
}

// ResolveExecutorImage validates persisted legacy data at the task-start boundary.
func (tpl *Template) ResolveExecutorImage() (*string, error) {
	if tpl.ExecutorImage == nil {
		return nil, nil
	}
	return NormalizeExecutorImage(*tpl.ExecutorImage)
}

// EffectiveRunnerTags returns the multi-tag policy with a legacy single-tag fallback.
func (tpl Template) EffectiveRunnerTags() []string {
	if tags := NormalizeRunnerTags(tpl.RunnerTags); len(tags) > 0 {
		return tags
	}
	if tpl.RunnerTag != nil {
		return NormalizeRunnerTags([]string{*tpl.RunnerTag})
	}
	return nil
}

// EffectiveRunnerTagMatchMode returns the validated match mode default.
func (tpl Template) EffectiveRunnerTagMatchMode() RunnerTagMatchMode {
	if tpl.RunnerTagMatchMode == RunnerTagMatchAny {
		return RunnerTagMatchAny
	}
	return RunnerTagMatchAll
}
