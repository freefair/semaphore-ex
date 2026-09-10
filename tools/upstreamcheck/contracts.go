package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

type contractInventory struct {
	Format  int              `yaml:"format"`
	Symbols []contractSymbol `yaml:"symbols"`
}
type contractSymbol struct {
	Name              string `yaml:"name"`
	Signature         string `yaml:"signature"`
	SelectedSignature string `yaml:"selected_signature,omitempty"`
	Implementation    string `yaml:"implementation"`
	// The reviewed inventory explains deliberately retained scaffolding; candidates never approve it.
	Rationale     string   `yaml:"rationale,omitempty"`
	BehaviorTests []string `yaml:"behavior_tests,omitempty"`
}
type exportPackage struct {
	ImportPath, Export, Dir string
	GoFiles                 []string
}

func declaration(obj types.Object) string {
	if value, ok := obj.(*types.Const); ok {
		return canonicalType(obj.Type()) + " = " + value.Val().ExactString()
	}
	if _, ok := obj.(*types.TypeName); ok {
		underlying := obj.Type().Underlying()
		if record, ok := underlying.(*types.Struct); ok {
			var fields []*types.Var
			var tags []string
			for i := 0; i < record.NumFields(); i++ {
				if record.Field(i).Exported() {
					fields = append(fields, record.Field(i))
					tags = append(tags, record.Tag(i))
				}
			}
			underlying = types.NewStruct(fields, tags)
		}
		return canonicalType(underlying)
	}
	return canonicalType(obj.Type())
}

func canonicalType(t types.Type) string {
	return types.TypeString(t, func(p *types.Package) string {
		s := p.Path()
		s = strings.ReplaceAll(s, "github.com/semaphoreui/semaphore/community-pro", "pro")
		s = strings.ReplaceAll(s, "github.com/semaphoreui/semaphore/pro", "pro")
		return strings.TrimPrefix(s, "github.com/semaphoreui/semaphore/")
	})
}

func methodFor(pkg *types.Package, receiver, name string) *types.Selection {
	obj := pkg.Scope().Lookup(receiver)
	if obj == nil {
		return nil
	}
	t := types.Unalias(obj.Type())
	if _, ok := t.Underlying().(*types.Interface); ok {
		return types.NewMethodSet(t).Lookup(pkg, name)
	}
	return types.NewMethodSet(types.NewPointer(t)).Lookup(pkg, name)
}

func classifyMethod(method *types.Selection) string {
	if method == nil {
		return "missing"
	}
	if strings.Contains(method.Obj().Pkg().Path(), "/community-pro/") {
		return "community"
	}
	return "local"
}

func inventoryPackages(stub, selected *types.Package) []contractSymbol {
	var entries []contractSymbol
	add := func(name string, obj types.Object, method *types.Selection) {
		row := contractSymbol{Name: strings.TrimPrefix(stub.Path(), "github.com/semaphoreui/semaphore/community-pro/") + ":" + name, Signature: declaration(obj), Implementation: "missing"}
		if selected != nil {
			if method != nil {
				row.Implementation = classifyMethod(method)
				row.SelectedSignature = canonicalType(method.Obj().Type())
			} else if !strings.Contains(name, ".") {
				if other := selected.Scope().Lookup(name); other != nil {
					row.Implementation = "local"
					row.SelectedSignature = declaration(other)
					if typeName, ok := other.(*types.TypeName); ok {
						target := types.Unalias(typeName.Type())
						if named, ok := target.(*types.Named); ok && strings.Contains(named.Obj().Pkg().Path(), "/community-pro/") {
							row.Implementation = "community"
						}
					}
				}
			}
		}
		entries = append(entries, row)
	}
	for _, name := range stub.Scope().Names() {
		obj := stub.Scope().Lookup(name)
		if !obj.Exported() {
			continue
		}
		add(name, obj, nil)
		tn, ok := obj.(*types.TypeName)
		if !ok {
			continue
		}
		t := types.Unalias(tn.Type())
		if _, ok := t.Underlying().(*types.Interface); ok {
			continue
		}
		set := types.NewMethodSet(types.NewPointer(t))
		for i := 0; i < set.Len(); i++ {
			m := set.At(i).Obj()
			if !m.Exported() {
				continue
			}
			var selectedMethod *types.Selection
			if selected != nil {
				selectedMethod = methodFor(selected, name, m.Name())
			}
			add(name+"."+m.Name(), m, selectedMethod)
		}
	}
	return entries
}

func inspectContracts(root string) (contractInventory, error) {
	cmd := exec.Command("go", "list", "-mod=readonly", "-export", "-deps", "-json", "github.com/semaphoreui/semaphore/community-pro/...", "github.com/semaphoreui/semaphore/pro/...", "github.com/semaphoreui/semaphore/pro_interfaces", "github.com/semaphoreui/semaphore/db")
	cmd.Dir = filepath.Join(root, "test/edition-contract/enhanced")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return contractInventory{}, fmt.Errorf("load module exports: %w: %s", err, stderr.String())
	}
	packages := map[string]string{}
	sources := map[string]exportPackage{}
	dec := json.NewDecoder(&stdout)
	for {
		var p exportPackage
		err := dec.Decode(&p)
		if err == io.EOF {
			break
		}
		if err != nil {
			return contractInventory{}, err
		}
		packages[p.ImportPath] = p.Export
		sources[p.ImportPath] = p
	}
	imp := importer.ForCompiler(token.NewFileSet(), "gc", func(path string) (io.ReadCloser, error) {
		file, ok := packages[path]
		if !ok || file == "" {
			return nil, fmt.Errorf("missing export data for %s", path)
		}
		return os.Open(file)
	})
	result := contractInventory{Format: 1}
	for path := range packages {
		if !strings.HasPrefix(path, "github.com/semaphoreui/semaphore/community-pro/") {
			continue
		}
		stub, err := imp.Import(path)
		if err != nil {
			return result, err
		}
		selectedPath := strings.Replace(path, "/community-pro/", "/pro/", 1)
		var selected *types.Package
		if _, ok := packages[selectedPath]; ok {
			selected, err = imp.Import(selectedPath)
			if err != nil {
				return result, err
			}
		}
		rows := inventoryPackages(stub, selected)
		aliases, err := communityFunctionAliases(sources[selectedPath])
		if err != nil {
			return result, err
		}
		for i := range rows {
			symbol := strings.SplitN(rows[i].Name, ":", 2)[1]
			if aliases[symbol] {
				rows[i].Implementation = "community"
			}
		}
		result.Symbols = append(result.Symbols, rows...)
	}
	for _, corePath := range []string{"pro_interfaces", "db"} {
		core, err := imp.Import("github.com/semaphoreui/semaphore/" + corePath)
		if err != nil {
			return result, err
		}
		for _, name := range core.Scope().Names() {
			obj := core.Scope().Lookup(name)
			if obj.Exported() {
				result.Symbols = append(result.Symbols, contractSymbol{Name: corePath + ":" + name, Signature: declaration(obj), Implementation: "core"})
			}
		}
	}
	sort.Slice(result.Symbols, func(i, j int) bool { return result.Symbols[i].Name < result.Symbols[j].Name })
	return result, nil
}

func validateContracts(expected, actual contractInventory) error {
	if expected.Format != 1 {
		return fmt.Errorf("unsupported contract inventory format %d", expected.Format)
	}
	approved := map[string]contractSymbol{}
	for _, row := range expected.Symbols {
		if _, ok := approved[row.Name]; ok {
			return fmt.Errorf("duplicate contract %s", row.Name)
		}
		if (row.Implementation == "community" || row.Implementation == "missing") && strings.TrimSpace(row.Rationale) == "" {
			return fmt.Errorf("contract %s needs reviewed scaffolding rationale", row.Name)
		}
		approved[row.Name] = row
	}
	for _, row := range actual.Symbols {
		old, ok := approved[row.Name]
		if !ok {
			return fmt.Errorf("new exported contract %s: implement and add behavioral coverage before reviewing the inventory", row.Name)
		}
		old.Rationale = ""
		old.BehaviorTests = nil
		if !reflect.DeepEqual(old, row) {
			return fmt.Errorf("contract %s changed (implementation %s -> %s): inspect signatures and inherited placeholders", row.Name, old.Implementation, row.Implementation)
		}
		delete(approved, row.Name)
	}
	if len(approved) > 0 {
		return fmt.Errorf("%d inventoried contracts disappeared", len(approved))
	}
	return nil
}
func checkContracts(root string) error {
	var expected contractInventory
	if err := readYAML(filepath.Join(root, "maintenance/contracts.yml"), &expected); err != nil {
		return err
	}
	if err := checkBehaviorReferences(root, expected); err != nil {
		return err
	}
	actual, err := inspectContracts(root)
	if err != nil {
		return err
	}
	return validateContracts(expected, actual)
}

// Function-valued variable aliases have no method-selection origin in export data.
func communityFunctionAliases(pkg exportPackage) (map[string]bool, error) {
	aliases := map[string]bool{}
	for _, name := range pkg.GoFiles {
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(pkg.Dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		imports := map[string]bool{}
		for _, spec := range f.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return nil, err
			}
			if !strings.Contains(path, "/community-pro/") {
				continue
			}
			alias := filepath.Base(path)
			if spec.Name != nil {
				alias = spec.Name.Name
			}
			imports[alias] = true
		}
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				value := spec.(*ast.ValueSpec)
				for i, expr := range value.Values {
					selector, ok := expr.(*ast.SelectorExpr)
					if !ok || i >= len(value.Names) {
						continue
					}
					identifier, ok := selector.X.(*ast.Ident)
					if ok && imports[identifier.Name] {
						aliases[value.Names[i].Name] = true
					}
				}
			}
		}
	}
	return aliases, nil
}
