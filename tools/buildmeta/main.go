// Command buildmeta assembles deterministic edition artifacts and metadata.
package main

import (
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type options struct {
	binary             string
	frontend           string
	frontendLock       string
	sourceMaps         string
	output             string
	edition            string
	coreRevision       string
	enhancedRevision   string
	implementation     string
	sourceDateEpochRaw string
}

type artifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type manifest struct {
	SchemaVersion    int        `json:"schema_version"`
	Edition          string     `json:"edition"`
	ContractVersion  string     `json:"contract_version"`
	Implementation   string     `json:"implementation_version"`
	CoreRevision     string     `json:"core_revision"`
	EnhancedRevision string     `json:"enhanced_revision,omitempty"`
	SourceDateEpoch  int64      `json:"source_date_epoch"`
	Artifacts        []artifact `json:"artifacts"`
}

type spdxDocument struct {
	SPDXVersion       string        `json:"spdxVersion"`
	DataLicense       string        `json:"dataLicense"`
	SPDXID            string        `json:"SPDXID"`
	Name              string        `json:"name"`
	DocumentNamespace string        `json:"documentNamespace"`
	CreationInfo      creationInfo  `json:"creationInfo"`
	Packages          []spdxPackage `json:"packages"`
}

type creationInfo struct {
	Created  string   `json:"created"`
	Creators []string `json:"creators"`
}

type spdxPackage struct {
	Name             string `json:"name"`
	SPDXID           string `json:"SPDXID"`
	VersionInfo      string `json:"versionInfo,omitempty"`
	DownloadLocation string `json:"downloadLocation"`
	FilesAnalyzed    bool   `json:"filesAnalyzed"`
	LicenseConcluded string `json:"licenseConcluded"`
	LicenseDeclared  string `json:"licenseDeclared"`
	CopyrightText    string `json:"copyrightText"`
}

type npmLock struct {
	Packages map[string]npmPackage `json:"packages"`
}

type npmPackage struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Dev     bool   `json:"dev"`
	Link    bool   `json:"link"`
}

type provenance struct {
	Type          string              `json:"_type"`
	Subject       []provenanceSubject `json:"subject"`
	PredicateType string              `json:"predicateType"`
	Predicate     provenancePredicate `json:"predicate"`
}

type provenanceSubject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

type provenancePredicate struct {
	BuildDefinition buildDefinition `json:"buildDefinition"`
	RunDetails      runDetails      `json:"runDetails"`
}

type buildDefinition struct {
	BuildType          string            `json:"buildType"`
	ExternalParameters map[string]string `json:"externalParameters"`
}

type runDetails struct {
	Builder builder `json:"builder"`
}

type builder struct {
	ID string `json:"id"`
}

func main() {
	configured := parseFlags()
	if err := run(configured); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseFlags() options {
	var configured options
	flag.StringVar(&configured.binary, "binary", "bin/semaphore", "source Semaphore binary")
	flag.StringVar(&configured.frontend, "frontend", "api/public", "built frontend directory")
	flag.StringVar(&configured.frontendLock, "frontend-lock", "web/package-lock.json", "frontend package-lock.json")
	flag.StringVar(&configured.sourceMaps, "source-maps", "", "normalized application source maps")
	flag.StringVar(&configured.output, "output", "dist/community", "artifact output directory")
	flag.StringVar(&configured.edition, "edition", "community", "community or enhanced")
	flag.StringVar(&configured.coreRevision, "core-revision", "", "full core Git revision")
	flag.StringVar(&configured.enhancedRevision, "enhanced-revision", "", "full enhanced Git revision")
	flag.StringVar(&configured.implementation, "implementation", "", "edition implementation identifier")
	flag.StringVar(&configured.sourceDateEpochRaw, "source-date-epoch", "", "reproducible Unix build timestamp")
	flag.Parse()
	return configured
}

func run(configured options) error {
	if configured.edition != string(pro_interfaces.EditionCommunity) && configured.edition != string(pro_interfaces.EditionEnhanced) {
		return fmt.Errorf("unsupported edition %q", configured.edition)
	}
	if configured.coreRevision == "" {
		return errors.New("core revision is required")
	}
	if configured.implementation == "" {
		return errors.New("implementation identifier is required")
	}
	if configured.edition == string(pro_interfaces.EditionEnhanced) && configured.enhancedRevision == "" {
		return errors.New("enhanced revision is required for enhanced artifacts")
	}
	sourceDateEpoch, err := strconv.ParseInt(configured.sourceDateEpochRaw, 10, 64)
	if err != nil {
		return fmt.Errorf("parse source date epoch: %w", err)
	}

	if err := requireEmptyOutput(configured.output); err != nil {
		return fmt.Errorf("create artifact directory: %w", err)
	}

	for _, name := range []string{"semaphore-server", "semaphore-runner"} {
		if err := copyFile(configured.binary, filepath.Join(configured.output, name), 0o755); err != nil {
			return err
		}
	}
	if err := copyDirectory(configured.frontend, filepath.Join(configured.output, "web")); err != nil {
		return err
	}
	if configured.sourceMaps != "" {
		if err := copyDirectory(configured.sourceMaps, filepath.Join(configured.output, "debug-source-maps")); err != nil {
			return err
		}
	}

	artifacts, err := collectArtifacts(configured.output)
	if err != nil {
		return err
	}
	manifestDocument := manifest{
		SchemaVersion:    1,
		Edition:          configured.edition,
		ContractVersion:  pro_interfaces.CoreContractVersion,
		Implementation:   configured.implementation,
		CoreRevision:     configured.coreRevision,
		EnhancedRevision: configured.enhancedRevision,
		SourceDateEpoch:  sourceDateEpoch,
		Artifacts:        artifacts,
	}
	if err := writeJSON(filepath.Join(configured.output, "manifest.json"), manifestDocument); err != nil {
		return err
	}

	sbom, err := makeSBOM(configured, sourceDateEpoch)
	if err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(configured.output, "sbom.spdx.json"), sbom); err != nil {
		return err
	}

	subjects := make([]provenanceSubject, 0, len(artifacts))
	for _, currentArtifact := range artifacts {
		subjects = append(subjects, provenanceSubject{
			Name:   currentArtifact.Path,
			Digest: map[string]string{"sha256": currentArtifact.SHA256},
		})
	}
	provenanceDocument := provenance{
		Type:          "https://in-toto.io/Statement/v1",
		Subject:       subjects,
		PredicateType: "https://slsa.dev/provenance/v1",
		Predicate: provenancePredicate{
			BuildDefinition: buildDefinition{
				BuildType: "https://semaphoreui.com/build/edition/v1",
				ExternalParameters: map[string]string{
					"contract_version":  pro_interfaces.CoreContractVersion,
					"core_revision":     configured.coreRevision,
					"edition":           configured.edition,
					"enhanced_revision": configured.enhancedRevision,
					"implementation":    configured.implementation,
					"source_date_epoch": configured.sourceDateEpochRaw,
				},
			},
			RunDetails: runDetails{Builder: builder{ID: "https://taskfile.dev/Taskfile.yml#build:edition"}},
		},
	}
	return writeJSON(filepath.Join(configured.output, "provenance.json"), provenanceDocument)
}

func copyFile(source string, target string, mode fs.FileMode) error {
	sourceFile, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open %s: %w", source, err)
	}
	defer sourceFile.Close()

	targetFile, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("create %s: %w", target, err)
	}
	if _, err = io.Copy(targetFile, sourceFile); err != nil {
		targetFile.Close()
		return fmt.Errorf("copy %s: %w", source, err)
	}
	if err := targetFile.Close(); err != nil {
		return fmt.Errorf("close %s: %w", target, err)
	}
	return nil
}

func copyDirectory(source string, target string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relativePath, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		targetPath := filepath.Join(target, relativePath)
		if entry.IsDir() {
			return os.MkdirAll(targetPath, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported frontend artifact %s", path)
		}
		return copyFile(path, targetPath, info.Mode().Perm())
	})
}

func collectArtifacts(root string) ([]artifact, error) {
	var artifacts []artifact
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		checksum, size, err := hashFile(path)
		if err != nil {
			return err
		}
		relativePath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		artifacts = append(artifacts, artifact{Path: filepath.ToSlash(relativePath), SHA256: checksum, Size: size})
		return nil
	})
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })
	return artifacts, err
}

func hashFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func makeSBOM(configured options, sourceDateEpoch int64) (spdxDocument, error) {
	info, err := buildinfo.ReadFile(configured.binary)
	if err != nil {
		return spdxDocument{}, fmt.Errorf("read binary build info: %w", err)
	}
	packages := []spdxPackage{newSPDXPackage("go", info.Main.Path, info.Main.Version)}
	for _, dependency := range info.Deps {
		selected := dependency
		if dependency.Replace != nil {
			selected = dependency.Replace
		}
		packages = append(packages, newSPDXPackage("go", selected.Path, selected.Version))
	}
	npmPackages, err := readNPMProductionPackages(configured.frontendLock)
	if err != nil {
		return spdxDocument{}, err
	}
	packages = append(packages, npmPackages...)
	sort.Slice(packages, func(i, j int) bool {
		if packages[i].Name != packages[j].Name {
			return packages[i].Name < packages[j].Name
		}
		if packages[i].VersionInfo != packages[j].VersionInfo {
			return packages[i].VersionInfo < packages[j].VersionInfo
		}
		return packages[i].SPDXID < packages[j].SPDXID
	})
	namespaceRevision := configured.coreRevision
	if configured.enhancedRevision != "" {
		namespaceRevision += "-" + configured.enhancedRevision
	}
	return spdxDocument{
		SPDXVersion:       "SPDX-2.3",
		DataLicense:       "CC0-1.0",
		SPDXID:            "SPDXRef-DOCUMENT",
		Name:              "semaphore-" + configured.edition,
		DocumentNamespace: "https://semaphoreui.com/sbom/" + namespaceRevision,
		CreationInfo: creationInfo{
			Created:  time.Unix(sourceDateEpoch, 0).UTC().Format(time.RFC3339),
			Creators: []string{"Tool: semaphore-buildmeta-1"},
		},
		Packages: packages,
	}, nil
}

func requireEmptyOutput(path string) error {
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(path, 0o755)
	}
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("output directory %s is not empty", path)
	}
	return nil
}

func readNPMProductionPackages(path string) ([]spdxPackage, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read frontend lockfile: %w", err)
	}
	var lock npmLock
	if err := json.Unmarshal(encoded, &lock); err != nil {
		return nil, fmt.Errorf("decode frontend lockfile: %w", err)
	}
	if lock.Packages == nil {
		return nil, errors.New("frontend lockfile does not contain a packages tree")
	}
	seen := make(map[string]struct{})
	packages := make([]spdxPackage, 0)
	for location, descriptor := range lock.Packages {
		if location == "" || descriptor.Dev || descriptor.Link {
			continue
		}
		name := descriptor.Name
		if name == "" {
			const marker = "node_modules/"
			index := strings.LastIndex(filepath.ToSlash(location), marker)
			if index >= 0 {
				name = filepath.ToSlash(location)[index+len(marker):]
			}
		}
		if name == "" || descriptor.Version == "" {
			return nil, fmt.Errorf("frontend package %q has no exact name and version", location)
		}
		key := name + "@" + descriptor.Version
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		packages = append(packages, newSPDXPackage("npm", name, descriptor.Version))
	}
	return packages, nil
}

func newSPDXPackage(ecosystem string, name string, version string) spdxPackage {
	return spdxPackage{
		Name:             name,
		SPDXID:           spdxID(ecosystem, name, version),
		VersionInfo:      version,
		DownloadLocation: "NOASSERTION",
		FilesAnalyzed:    false,
		LicenseConcluded: "NOASSERTION",
		LicenseDeclared:  "NOASSERTION",
		CopyrightText:    "NOASSERTION",
	}
}

func spdxID(ecosystem string, name string, version string) string {
	key := ecosystem + ":" + name + "@" + version
	digest := sha256.Sum256([]byte(key))
	var normalized strings.Builder
	for _, current := range ecosystem + "-" + name + "-" + version {
		if current >= 'a' && current <= 'z' || current >= 'A' && current <= 'Z' || current >= '0' && current <= '9' || current == '.' || current == '-' {
			normalized.WriteRune(current)
		} else {
			normalized.WriteByte('-')
		}
	}
	return fmt.Sprintf("SPDXRef-Package-%s-%x", normalized.String(), digest[:6])
}

func writeJSON(path string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
