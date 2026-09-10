// Command upstreamcheck verifies the reviewed ownership and module boundaries of the fork.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("upstreamcheck", flag.ContinueOnError)
	incomingRef := flags.String("incoming-ref", "upstream/develop", "upstream commit/ref for incoming migration assessment")
	baseRef := flags.String("base-ref", "origin/develop", "reviewed prior commit/ref: preserve its migration ledger and require tests for changed callables")
	root := flags.String("root", ".", "repository root")
	mode := flags.String("mode", "check", "check, incoming, migrations, or contracts (candidate YAML on stdout)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	absolute, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	switch *mode {
	case "incoming":
		rows, err := assessIncoming(absolute, *incomingRef)
		if err != nil {
			return err
		}
		return writeYAML(out, rows)
	case "migrations":
		ledger, err := candidateLedger(absolute)
		if err != nil {
			return err
		}
		return writeYAML(out, ledger)
	case "contracts":
		entries, err := inspectContracts(absolute)
		if err != nil {
			return err
		}
		return writeYAML(out, entries)
	case "check":
		if err := checkBaseline(absolute, *baseRef); err != nil {
			return err
		}
		if err := checkLedger(absolute); err != nil {
			return err
		}
		if err := checkContracts(absolute); err != nil {
			return err
		}
		_, err := fmt.Fprintln(out, "Migration ownership and exported module contracts match the reviewed inventories.")
		return err
	default:
		return fmt.Errorf("unknown mode %q", *mode)
	}
}

func readYAML(path string, value any) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	if err = decoder.Decode(value); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("%s: expected exactly one YAML document", path)
	}
	return nil
}

func writeYAML(out io.Writer, value any) error {
	enc := yaml.NewEncoder(out)
	enc.SetIndent(2)
	defer enc.Close()
	return enc.Encode(value)
}
