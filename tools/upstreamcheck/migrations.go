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
	for _, directory := range []string{"migrations", "migrations_ex"} {
		paths, err := filepath.Glob(filepath.Join(root, "db/sql", directory, "*.sql"))
		if err != nil {
			return nil, err
		}
		for _, source := range paths {
			name := filepath.Base(source)
			stem := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(name, "v"), ".sql"), ".err")
			for _, suffix := range []string{".mysql", ".postgres"} {
				stem = strings.TrimSuffix(stem, suffix)
			}
			if strings.HasSuffix(stem, ".sqlite") && stem != "2.15.1.sqlite" {
				stem = strings.TrimSuffix(stem, ".sqlite")
			}
			if directory == "migrations_ex" && !strings.Contains(stem, "-ex") {
				return nil, fmt.Errorf("EX migration %s lacks upstream anchor", name)
			}
			if directory == "migrations" && strings.Contains(stem, "-ex") {
				return nil, fmt.Errorf("EX migration %s is in upstream directory", name)
			}

			content, err := os.ReadFile(source)
			if err != nil {
				return nil, err
			}
			if result[stem] == nil {
				result[stem] = map[string]string{}
			}
			result[stem][directory+"/"+name] = fmt.Sprintf("%x", sha256.Sum256(content))
		}
	}
	return result, nil
}

func registeredMigrations(root string) (map[string]bool, error) {
	ids := map[string]bool{}
	for _, filename := range []string{"Migration.go", "migration_ex.go"} {
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, "db", filename), nil, 0)
		if err != nil {
			return nil, err
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || (fn.Name.Name != "GetMigrations" && fn.Name.Name != "GetEXMigrations") {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
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
		}
	}
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
	ledger := migrationLedger{Format: 2}
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
	if ledger.Format != 1 && ledger.Format != 2 {
		return fmt.Errorf("unsupported migration ledger format %d", ledger.Format)
	}
	seen := map[string]bool{}
	upstream := map[string]string{}
	for _, entry := range ledger.Entries {
		if ledger.Format == 2 {
			isEX := strings.Contains(entry.ID, "-ex")
			if isEX != (entry.Owner == "fork") {
				return fmt.Errorf("migration %s mixes upstream and EX ownership", entry.ID)
			}
			directory := "migrations/"
			if isEX {
				directory = "migrations_ex/"
			}
			for name := range entry.Files {
				if !strings.HasPrefix(name, directory) {
					return fmt.Errorf("migration %s uses the wrong source directory", entry.ID)
				}
			}
			if !isEX && entry.UpstreamID != entry.ID {
				return fmt.Errorf("upstream migration %s was renumbered", entry.ID)
			}
		}
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
