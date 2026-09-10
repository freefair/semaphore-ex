package main

import (
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

type migrationLedger struct {
	Format  int              `yaml:"format"`
	Entries []migrationEntry `yaml:"migrations"`
}
type migrationEntry struct {
	Registered bool              `yaml:"registered"`
	ID         string            `yaml:"id"`
	Owner      string            `yaml:"owner"`
	UpstreamID string            `yaml:"upstream_id,omitempty"`
	Rationale  string            `yaml:"rationale,omitempty"`
	Files      map[string]string `yaml:"files"`
}

func migrationFiles(root string) (map[string]map[string]string, error) {
	result := map[string]map[string]string{}
	paths, err := filepath.Glob(filepath.Join(root, "db/sql/migrations/*.sql"))
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		name := filepath.Base(path)
		stem := strings.TrimSuffix(strings.TrimPrefix(name, "v"), ".sql")
		stem = strings.TrimSuffix(stem, ".err")
		// Keep the SQLite bootstrap identifier; other dialect suffixes are variants of one migration.
		for _, suffix := range []string{".mysql", ".postgres"} {
			stem = strings.TrimSuffix(stem, suffix)
		}
		if strings.HasSuffix(stem, ".sqlite") && stem != "2.15.1.sqlite" {
			stem = strings.TrimSuffix(stem, ".sqlite")
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if result[stem] == nil {
			result[stem] = map[string]string{}
		}
		result[stem][name] = fmt.Sprintf("%x", sha256.Sum256(b))
	}
	return result, nil
}

func registeredMigrations(root string) (map[string]bool, error) {
	f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, "db/Migration.go"), nil, 0)
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		pair, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, ok := pair.Key.(*ast.Ident)
		if !ok || key.Name != "Version" {
			return true
		}
		value, ok := pair.Value.(*ast.BasicLit)
		if !ok || value.Kind != token.STRING {
			return true
		}
		id, err := strconv.Unquote(value.Value)
		if err == nil {
			ids[id] = true
		}
		return true
	})
	return ids, nil
}

func candidateLedger(root string) (migrationLedger, error) {
	files, err := migrationFiles(root)
	if err != nil {
		return migrationLedger{}, err
	}
	registry, err := registeredMigrations(root)
	if err != nil {
		return migrationLedger{}, err
	}
	ledger := migrationLedger{Format: 1}
	var ids []string
	for id := range files {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		ledger.Entries = append(ledger.Entries, migrationEntry{ID: id, Owner: "REVIEW", Registered: registry[id], Files: files[id]})
	}
	return ledger, nil
}

func validateLedger(ledger migrationLedger, files map[string]map[string]string, registered map[string]bool) error {
	if ledger.Format != 1 {
		return fmt.Errorf("unsupported migration ledger format %d", ledger.Format)
	}
	seen := map[string]bool{}
	upstream := map[string]string{}
	for _, entry := range ledger.Entries {
		if seen[entry.ID] {
			return fmt.Errorf("duplicate migration ledger ID %s", entry.ID)
		}
		seen[entry.ID] = true
		if entry.Registered != registered[entry.ID] {
			return fmt.Errorf("migration %s registration changed", entry.ID)
		}
		if entry.Owner != "upstream" && entry.Owner != "fork" && entry.Owner != "upstream-adapted" {
			return fmt.Errorf("migration %s needs an explicit owner", entry.ID)
		}
		if entry.Owner != "fork" && entry.UpstreamID == "" {
			return fmt.Errorf("migration %s lacks upstream identity", entry.ID)
		}
		if entry.Owner == "fork" && entry.UpstreamID != "" {
			return fmt.Errorf("fork migration %s claims an upstream identity", entry.ID)
		}
		if entry.Owner == "upstream-adapted" && strings.TrimSpace(entry.Rationale) == "" {
			return fmt.Errorf("adapted migration %s requires rationale", entry.ID)
		}
		if entry.UpstreamID != "" {
			if old, ok := upstream[entry.UpstreamID]; ok {
				return fmt.Errorf("upstream migration %s mapped twice: %s, %s", entry.UpstreamID, old, entry.ID)
			}
			upstream[entry.UpstreamID] = entry.ID
		}
		actual, ok := files[entry.ID]
		if !ok || !reflect.DeepEqual(actual, entry.Files) {
			return fmt.Errorf("migration %s SQL changed or is missing; preserve shipped SQL and append a new migration", entry.ID)
		}
	}
	for id := range files {
		if !seen[id] {
			return fmt.Errorf("migration %s has no ownership ledger entry", id)
		}
	}
	for id := range registered {
		if !seen[id] {
			return fmt.Errorf("registered migration %s has no ownership ledger entry", id)
		}
	}

	return nil
}
func checkLedger(root string) error {
	var ledger migrationLedger
	if err := readYAML(filepath.Join(root, "maintenance/migrations.yml"), &ledger); err != nil {
		return err
	}
	files, err := migrationFiles(root)
	if err != nil {
		return err
	}
	registry, err := registeredMigrations(root)
	if err != nil {
		return err
	}
	return validateLedger(ledger, files, registry)
}
