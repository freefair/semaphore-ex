package db

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Migrate follows one dependency-ordered plan while each namespace retains
// its own registry, SQL assets and history. EX steps follow their exact
// upstream anchor and precede the next upstream step.
func Migrate(d Store, targetVersion *string) error {
	plan, err := orderedMigrations(GetMigrations(d.GetDialect()), GetEXMigrations())
	if err != nil {
		return err
	}
	target, err := migrationTarget(targetVersion, plan)
	if err != nil {
		return err
	}
	return withMigrationLock(d, func() error {
		selected := selectMigrationSteps(plan, target, false)
		if err := validateMigrationSteps(d, selected, false); err != nil {
			return err
		}
		return applyMigrations(d, nil, selected)
	})
}

// Rollback undoes the same dependency plan in reverse, including EX steps
// that depend on an upstream migration being removed.
func Rollback(d Store, targetVersion string) error {
	plan, err := orderedMigrations(GetMigrations(d.GetDialect()), GetEXMigrations())
	if err != nil {
		return err
	}
	target, err := migrationTarget(&targetVersion, plan)
	if err != nil {
		return err
	}
	return withMigrationLock(d, func() error {
		selected := selectMigrationSteps(plan, target, true)
		if err := validateMigrationSteps(d, selected, true); err != nil {
			return err
		}
		return rollbackMigrations(d, *target, selected)
	})
}

func selectMigrationSteps(plan []Migration, target *Migration, undo bool) []Migration {
	selected := make([]Migration, 0, len(plan))
	for _, step := range plan {
		if target == nil || (!undo && step.Compare(*target) <= 0) || (undo && step.Compare(*target) > 0) {
			selected = append(selected, step)
		}
	}
	return selected
}

func validateMigrationSteps(d Store, plan []Migration, undo bool) error {
	if validator, ok := d.(interface{ ValidateMigrationPlan([]Migration, bool) error }); ok {
		return validator.ValidateMigrationPlan(plan, undo)
	}
	return nil
}

func migrationTarget(version *string, plan []Migration) (*Migration, error) {
	if version == nil {
		return nil, nil
	}
	target := Migration{Version: *version}
	if _, err := target.ParseVersion(); err != nil {
		return nil, err
	}
	if strings.Contains(target.Version, "-ex") && !slices.ContainsFunc(plan, func(m Migration) bool { return m.Version == target.Version }) {
		return nil, fmt.Errorf("unknown EX migration target %q", target.Version)
	}
	return &target, nil
}

func orderedMigrations(upstream, ex []Migration) ([]Migration, error) {
	anchors := make(map[string]bool, len(upstream))
	for _, m := range upstream {
		if strings.Contains(m.Version, "-ex") || anchors[m.Version] {
			return nil, fmt.Errorf("invalid upstream migration identity %q", m.Version)
		}
		anchors[m.Version] = true
	}
	groups := make(map[string][]Migration)
	seen := make(map[string]bool)
	for _, m := range ex {
		base, suffix, err := splitEXMigrationVersion(m.Version)
		if err != nil {
			return nil, err
		}
		if len(suffix) == 0 || !anchors[base] {
			return nil, fmt.Errorf("EX migration %q has no registered upstream anchor", m.Version)
		}
		if seen[m.Version] {
			return nil, fmt.Errorf("duplicate EX migration %q", m.Version)
		}
		seen[m.Version] = true
		groups[base] = append(groups[base], m)
	}
	plan := make([]Migration, 0, len(upstream)+len(ex))
	for _, m := range upstream {
		plan = append(plan, m)
		attached := groups[m.Version]
		slices.SortFunc(attached, func(a, b Migration) int { return a.Compare(b) })
		plan = append(plan, attached...)
	}
	return plan, nil
}

func splitEXMigrationVersion(version string) (string, []int, error) {
	base, suffix, found := strings.Cut(version, "-ex")
	if !found {
		return version, nil, nil
	}
	if len(strings.Split(base, ".")) != 3 {
		return "", nil, fmt.Errorf("invalid upstream anchor in EX migration %q", version)
	}
	parts := strings.Split(suffix, ".")
	numbers := make([]int, len(parts))
	for i, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 || strconv.Itoa(number) != part {
			return "", nil, fmt.Errorf("invalid EX migration identity %q", version)
		}
		numbers[i] = number
	}
	if numbers[0] < 1 {
		return "", nil, fmt.Errorf("invalid EX migration identity %q", version)
	}
	return base, numbers, nil
}

func withMigrationLock(d Store, operation func() error) error {
	if locker, ok := d.(interface{ WithMigrationLock(func() error) error }); ok {
		return locker.WithMigrationLock(operation)
	}
	return operation()
}

// CurrentSchemaVersion identifies both independent schema heads for HA checks.
func CurrentSchemaVersion(dialect string) string {
	upstream := GetMigrations(dialect)
	ex := GetEXMigrations()
	return "upstream/" + upstream[len(upstream)-1].Version + ";ex/" + ex[len(ex)-1].Version
}

// GetEXMigrations is the independent EX schema sequence. Its identities never
// occupy the upstream registry or upstream database history table.
func GetEXMigrations() []Migration {
	return []Migration{
		{Version: "2.20.1-ex1.1"},
		{Version: "2.20.1-ex1.2"},
		{Version: "2.20.1-ex1.3"},
		{Version: "2.20.1-ex1.4"},
		{Version: "2.20.1-ex1.5"},
		{Version: "2.20.1-ex1.6"},
		{Version: "2.20.1-ex1.7"},
		{Version: "2.20.1-ex1.8"},
		{Version: "2.20.1-ex1.9"},
		{Version: "2.20.1-ex1.10"},
		{Version: "2.20.1-ex1.11"},
		{Version: "2.20.1-ex1.12"},
		{Version: "2.20.1-ex1.13"},
		{Version: "2.20.1-ex1.14"},
		{Version: "2.20.1-ex1.15"},
		{Version: "2.20.1-ex1.16"},
		{Version: "2.20.1-ex1.17"},
		{Version: "2.20.1-ex1.18"},
		{Version: "2.20.1-ex1.19"},
		{Version: "2.20.1-ex1.20"},
		{Version: "2.20.1-ex1.21"},
		{Version: "2.20.1-ex1.22"},
		{Version: "2.20.1-ex1.23"},
		{Version: "2.20.1-ex1.24"},
		{Version: "2.20.1-ex1.25"},
		{Version: "2.20.1-ex1.26"},
		{Version: "2.20.1-ex1.27"},
		{Version: "2.20.1-ex1.28"},
		{Version: "2.20.1-ex1.29"},
		{Version: "2.20.1-ex1.30"},
		{Version: "2.20.1-ex1.31"},
		{Version: "2.20.1-ex1.32"},
		{Version: "2.20.1-ex1.33"},
		{Version: "2.20.1-ex1.34"},
		{Version: "2.20.1-ex1.35"},
		{Version: "2.20.1-ex1.36"},
		{Version: "2.20.1-ex1.37"},
		{Version: "2.20.1-ex1.38"},
		{Version: "2.20.1-ex1.39"},
		{Version: "2.20.1-ex1.40"},
		{Version: "2.20.1-ex1.41"},
		{Version: "2.20.1-ex1.42"},
		{Version: "2.20.1-ex1.43"},
		{Version: "2.20.1-ex1.44"},
		{Version: "2.20.1-ex1.45"},
		{Version: "2.20.1-ex1.46"},
		{Version: "2.20.1-ex1.47"},
		{Version: "2.20.1-ex1.48"},
		{Version: "2.20.1-ex1.49"},
		{Version: "2.20.1-ex1.50"},
		{Version: "2.20.1-ex1.51"},
		{Version: "2.20.1-ex1.52"},
		{Version: "2.20.1-ex1.53"},
		{Version: "2.20.1-ex1.54"},
		{Version: "2.20.1-ex1.55"},
		{Version: "2.20.1-ex1.56"},
		{Version: "2.20.1-ex1.57"},
		{Version: "2.20.1-ex1.58"},
		{Version: "2.20.1-ex1.59"},
		{Version: "2.20.1-ex1.60"},
		{Version: "2.20.1-ex1.61"},
		{Version: "2.20.1-ex1.62"},
		{Version: "2.20.1-ex1.63"},
		{Version: "2.20.1-ex1.64"},
		{Version: "2.20.2-ex1.1"},
		{Version: "2.20.5-ex1.1"},
		{Version: "2.20.5-ex1.2"},
		{Version: "2.20.5-ex1.3"},
		{Version: "2.20.5-ex1.4"},
	}
}
