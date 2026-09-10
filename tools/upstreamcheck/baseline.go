package main

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

func checkBaseline(root, ref string) error {
	if ref == "" {
		return nil
	}
	cmd := exec.Command("git", "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	cmd.Dir = root
	b, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("invalid baseline ref: %w", err)
	}
	sha := strings.TrimSpace(string(b))
	for _, name := range []string{"migrations", "contracts"} {
		cmd = exec.Command("git", "show", sha+":maintenance/"+name+".yml")
		cmd.Dir = root
		before, err := cmd.Output()
		if err != nil {
			// The first inventory is bootstrapped on this already verified product commit.
			if sha == "df23a6e43c2c250f9ca194a14a852e9211c662f2" {
				if name == "migrations" {
					if err := checkBootstrapSQL(root, sha); err != nil {
						return err
					}
				}
				continue
			}
			return fmt.Errorf("baseline has no %s inventory; use its last reviewed maintenance commit", name)
		}
		if name == "migrations" {
			var old, current migrationLedger
			if err = yaml.NewDecoder(bytes.NewReader(before)).Decode(&old); err != nil {
				return err
			}
			if err = readYAML(filepath.Join(root, "maintenance/migrations.yml"), &current); err != nil {
				return err
			}
			if err := validateLedgerEvolution(old, current); err != nil {
				return err
			}
		} else {
			var old, current contractInventory
			if err = yaml.NewDecoder(bytes.NewReader(before)).Decode(&old); err != nil {
				return err
			}
			if err = readYAML(filepath.Join(root, "maintenance/contracts.yml"), &current); err != nil {
				return err
			}
			if err := validateContractEvolution(old, current); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkBehaviorReferences(root string, inventory contractInventory) error {
	valid := regexp.MustCompile(`^(.+_test\.go)::(Test[A-Za-z0-9_]+)$`)
	for _, row := range inventory.Symbols {
		for _, reference := range row.BehaviorTests {
			parts := valid.FindStringSubmatch(reference)
			if parts == nil {
				return fmt.Errorf("invalid behavioral test reference %q", reference)
			}
			clean := filepath.Clean(parts[1])
			if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
				return fmt.Errorf("test reference escapes repository")
			}
			content, err := os.ReadFile(filepath.Join(root, clean))
			if err != nil {
				return err
			}
			file, err := parser.ParseFile(token.NewFileSet(), clean, content, 0)
			if err != nil {
				return err
			}
			found := false
			for _, decl := range file.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == parts[2] && fn.Body != nil {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("behavior test %s is missing", reference)
			}
		}
	}
	return nil
}

// The first ledger must describe the shipped baseline, not bless edits made while introducing it.
func checkBootstrapSQL(root, sha string) error {
	var current migrationLedger
	if err := readYAML(filepath.Join(root, "maintenance/migrations.yml"), &current); err != nil {
		return err
	}
	hashes := map[string]string{}
	for _, entry := range current.Entries {
		for name, sum := range entry.Files {
			hashes[name] = sum
		}
	}
	cmd := exec.Command("git", "archive", sha, "db/sql/migrations")
	cmd.Dir = root
	content, err := cmd.Output()
	if err != nil {
		return err
	}
	archive := tar.NewReader(bytes.NewReader(content))
	for {
		header, err := archive.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if !strings.HasSuffix(header.Name, ".sql") {
			continue
		}
		content, err := io.ReadAll(archive)
		if err != nil {
			return err
		}
		if hashes[filepath.Base(header.Name)] != fmt.Sprintf("%x", sha256.Sum256(content)) {
			return fmt.Errorf("bootstrap ledger rewrites shipped SQL %s", header.Name)
		}
	}
}

func validateLedgerEvolution(old, current migrationLedger) error {
	indexed := map[string]migrationEntry{}
	for _, row := range current.Entries {
		indexed[row.ID] = row
	}
	for _, row := range old.Entries {
		if !reflect.DeepEqual(row, indexed[row.ID]) {
			return fmt.Errorf("shipped migration %s ledger was rewritten; append a new entry", row.ID)
		}
	}
	return nil
}

func validateContractEvolution(old, current contractInventory) error {
	currentNames := map[string]bool{}
	for _, row := range current.Symbols {
		currentNames[row.Name] = true
	}
	for _, row := range old.Symbols {
		if !currentNames[row.Name] {
			return fmt.Errorf("baseline exported contract %s was removed; preserve the public seam or explicitly revise its contract policy", row.Name)
		}
	}
	indexed := map[string]contractSymbol{}
	for _, row := range old.Symbols {
		indexed[row.Name] = row
	}
	for _, row := range current.Symbols {
		if previous, ok := indexed[row.Name]; ok && len(previous.BehaviorTests) > 0 && len(row.BehaviorTests) == 0 {
			return fmt.Errorf("behavioral coverage removed for %s", row.Name)
		}
		if previous, ok := indexed[row.Name]; ok && previous.Signature == row.Signature && previous.SelectedSignature == row.SelectedSignature && previous.Implementation == row.Implementation {
			continue
		}
		if (strings.HasPrefix(row.Signature, "func(") || (strings.HasPrefix(row.Signature, "interface{") && strings.Contains(row.Signature, "("))) && len(row.BehaviorTests) == 0 {
			return fmt.Errorf("new or changed callable %s requires behavior_tests", row.Name)
		}
	}
	return nil
}
