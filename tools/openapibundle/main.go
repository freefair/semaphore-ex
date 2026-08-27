// Command openapibundle gives legacy API consumers one local Swagger document.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func readDocument(filename string) (*yaml.Node, error) {
	source, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(source))
	var document yaml.Node
	if err = decoder.Decode(&document); err != nil {
		return nil, err
	}
	if len(document.Content) != 1 {
		return nil, fmt.Errorf("expected one document in %s", filename)
	}
	var extra yaml.Node
	if err = decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("expected one document in %s", filename)
	}
	return document.Content[0], nil
}

func lookup(document *yaml.Node, pointer string) (*yaml.Node, error) {
	if !strings.HasPrefix(pointer, "#/") {
		return nil, fmt.Errorf("unsupported reference %s", pointer)
	}
	node := document
	for _, part := range strings.Split(strings.TrimPrefix(pointer, "#/"), "/") {
		key := strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		var next *yaml.Node
		if node.Kind == yaml.MappingNode {
			for i := 0; i < len(node.Content); i += 2 {
				if node.Content[i].Value == key {
					next = node.Content[i+1]
					break
				}
			}
		}
		if next == nil {
			return nil, fmt.Errorf("missing reference %s", pointer)
		}
		node = next
	}
	return node, nil
}

func validateKeys(node *yaml.Node) error {
	if node.Kind == yaml.AliasNode {
		return fmt.Errorf("YAML aliases are unsupported in API fragments")
	}
	if node.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i].Value
			if seen[key] {
				return fmt.Errorf("duplicate API key %s", key)
			}
			seen[key] = true
		}
	}
	for _, child := range node.Content {
		if err := validateKeys(child); err != nil {
			return err
		}
	}
	return nil
}

func bundle(input string) ([]byte, error) {
	root, err := readDocument(input)
	if err != nil {
		return nil, err
	}
	if err = validateKeys(root); err != nil {
		return nil, err
	}
	var companion *yaml.Node
	var expand func(*yaml.Node, map[string]bool) (*yaml.Node, error)
	expand = func(node *yaml.Node, active map[string]bool) (*yaml.Node, error) {
		if node.Kind == yaml.MappingNode {
			for i := 0; i < len(node.Content); i += 2 {
				if node.Content[i].Value != "$ref" {
					continue
				}
				ref := node.Content[i+1].Value
				if strings.HasPrefix(ref, "./api-docs-ex.yml#/") {
					if len(node.Content) != 2 {
						return nil, fmt.Errorf("external reference %s has ambiguous siblings", ref)
					}
					if active[ref] {
						return nil, fmt.Errorf("external reference cycle at %s", ref)
					}
					if companion == nil {
						companion, err = readDocument(filepath.Join(filepath.Dir(input), "api-docs-ex.yml"))
						if err != nil {
							return nil, err
						}
						if err = validateKeys(companion); err != nil {
							return nil, err
						}
					}
					target, err := lookup(companion, strings.TrimPrefix(ref, "./api-docs-ex.yml"))
					if err != nil {
						return nil, err
					}
					active[ref] = true
					result, err := expand(target, active)
					delete(active, ref)
					return result, err
				}
				if !strings.HasPrefix(ref, "#/") && !strings.HasPrefix(ref, "./api-docs.yml#/") {
					return nil, fmt.Errorf("unsupported external reference %s", ref)
				}
			}
		}
		copy := *node
		copy.Content = nil
		for i, child := range node.Content {
			next, err := expand(child, active)
			if err != nil {
				return nil, err
			}
			if node.Kind == yaml.MappingNode && i%2 == 1 && node.Content[i-1].Value == "$ref" {
				next.Value = strings.TrimPrefix(next.Value, "./api-docs.yml")
				if _, err := lookup(root, next.Value); err != nil {
					return nil, err
				}
			}
			copy.Content = append(copy.Content, next)
		}
		return &copy, nil
	}
	output, err := expand(root, map[string]bool{})
	if err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err = encoder.Encode(output); err != nil {
		return nil, err
	}
	if err = encoder.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func run(args []string) error {
	flags := flag.NewFlagSet("openapibundle", flag.ContinueOnError)
	input := flags.String("input", "api-docs.yml", "source Swagger document")
	output := flags.String("output", ".dredd/api-docs.bundled.yml", "generated standalone document")
	if err := flags.Parse(args); err != nil {
		return err
	}
	source, err := filepath.Abs(*input)
	if err != nil {
		return err
	}
	logicalSource := source
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return err
	}
	destination, err := filepath.Abs(*output)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	// Resolve directory links before comparing: rename follows the parent directory.
	sourceDirectory, err := filepath.EvalSymlinks(filepath.Dir(source))
	if err != nil {
		return err
	}
	destinationDirectory, err := filepath.EvalSymlinks(filepath.Dir(destination))
	if err != nil {
		return err
	}
	canonicalDestination := filepath.Join(destinationDirectory, filepath.Base(destination))
	logicalDirectory, err := filepath.EvalSymlinks(filepath.Dir(logicalSource))
	if err != nil {
		return err
	}
	companion := filepath.Join(sourceDirectory, "api-docs-ex.yml")
	resolvedCompanion, companionError := filepath.EvalSymlinks(companion)
	if companionError != nil && !os.IsNotExist(companionError) {
		return companionError
	}
	if canonicalDestination == source || canonicalDestination == filepath.Join(logicalDirectory, filepath.Base(logicalSource)) || canonicalDestination == companion || canonicalDestination == resolvedCompanion {
		return fmt.Errorf("output must not replace an authored API source")
	}
	content, err := bundle(source)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".openapi-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err = temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if err = temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), destination)
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
