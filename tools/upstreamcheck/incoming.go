package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type incomingMigration struct {
	UpstreamID string `yaml:"upstream_id"`
	LocalID    string `yaml:"local_id,omitempty"`
	Action     string `yaml:"action"`
}

// assessIncoming reports mapping decisions; it never allocates an ID or edits SQL.
func assessIncoming(root, ref string) ([]incomingMigration, error) {
	command := exec.Command("git", "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	command.Dir = root
	raw, err := command.Output()
	if err != nil {
		return nil, err
	}
	sha := strings.TrimSpace(string(raw))
	command = exec.Command("git", "show", sha+":db/Migration.go")
	command.Dir = root
	raw, err = command.Output()
	if err != nil {
		return nil, err
	}
	source, err := parser.ParseFile(token.NewFileSet(), "Migration.go", raw, 0)
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	ast.Inspect(source, func(node ast.Node) bool {
		pair, ok := node.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, ok := pair.Key.(*ast.Ident)
		if !ok || key.Name != "Version" {
			return true
		}
		value, ok := pair.Value.(*ast.BasicLit)
		if ok && value.Kind == token.STRING {
			if id, err := strconv.Unquote(value.Value); err == nil {
				ids[id] = true
			}
		}
		return true
	})
	var ledger migrationLedger
	if err = readYAML(filepath.Join(root, "maintenance/migrations.yml"), &ledger); err != nil {
		return nil, err
	}
	return unmappedMigrations(ledger, ids), nil
}

func unmappedMigrations(ledger migrationLedger, ids map[string]bool) []incomingMigration {
	owned := map[string]bool{}
	mapped := map[string]bool{}
	for _, entry := range ledger.Entries {
		owned[entry.ID] = true
		if entry.UpstreamID != "" {
			mapped[entry.UpstreamID] = true
		}
	}
	rows := []incomingMigration{}
	for id := range ids {
		if mapped[id] {
			continue
		}
		row := incomingMigration{UpstreamID: id, Action: "Add the canonical upstream ID in the upstream registry and source directory; the EX sequence is independent."}
		if owned[id] {
			row.LocalID = id
			row.Action = "Collision in legacy mixed history: migrate the development source layout to separate upstream and EX namespaces."
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].UpstreamID < rows[j].UpstreamID })
	return rows
}
