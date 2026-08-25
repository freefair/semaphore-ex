// Command frontendmaps associates a hidden-source-map build with deterministic production assets.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type options struct {
	production string
	debug      string
	output     string
}

var sourceMapReference = regexp.MustCompile(`(?m)(?:\r?\n)?//[#@][[:space:]]*sourceMappingURL=.*(?:\r?\n|$)|/\*[#@][[:space:]]*sourceMappingURL=.*?\*/`)

func main() {
	var configured options
	flag.StringVar(&configured.production, "production", "api/public", "production frontend without source maps")
	flag.StringVar(&configured.debug, "debug", "", "hidden-source-map frontend build")
	flag.StringVar(&configured.output, "output", "", "normalized source map output")
	flag.Parse()
	if err := run(configured); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(configured options) error {
	if configured.debug == "" || configured.output == "" {
		return errors.New("debug build and output are required")
	}
	if err := requireEmptyOutput(configured.output); err != nil {
		return err
	}
	productionAssets, err := indexProductionAssets(configured.production)
	if err != nil {
		return err
	}
	maps, err := applicationMaps(configured.debug)
	if err != nil {
		return err
	}
	if len(maps) == 0 {
		return errors.New("debug build contains no source maps")
	}
	mappedAssets := make(map[string]struct{})
	for _, sourceMap := range maps {
		debugAsset := strings.TrimSuffix(sourceMap, ".map")
		digest, err := hashFile(debugAsset)
		if err != nil {
			return fmt.Errorf("hash debug asset %s: %w", debugAsset, err)
		}
		relativeDebugAsset, err := filepath.Rel(configured.debug, debugAsset)
		if err != nil {
			return err
		}
		productionAsset := filepath.Join(configured.production, relativeDebugAsset)
		productionDigest, directErr := hashFile(productionAsset)
		if directErr != nil || productionDigest != digest {
			productionAsset = productionAssets[digest]
		}
		_, exists := productionAssets[digest]
		if directErr == nil && productionDigest == digest {
			exists = true
		}
		if !exists {
			return fmt.Errorf("no production asset matches %s", debugAsset)
		}
		if err := normalizeSourceMap(sourceMap, productionAsset, configured.production, configured.output); err != nil {
			return err
		}
		mappedAssets[productionAsset] = struct{}{}
	}
	for productionAsset := range mappedAssets {
		if err := stripSourceMapReference(productionAsset); err != nil {
			return err
		}
	}
	return removeSourceMaps(configured.production)
}

func indexProductionAssets(root string) (map[string]string, error) {
	assets := make(map[string]string)
	for _, directory := range []string{"js", "css"} {
		directoryPath := filepath.Join(root, directory)
		err := filepath.WalkDir(directoryPath, func(path string, entry fs.DirEntry, walkErr error) error {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || filepath.Ext(path) == ".map" {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("unsupported production asset %s", path)
			}
			digest, err := hashFile(path)
			if err != nil {
				return err
			}
			if existing, exists := assets[digest]; exists {
				return fmt.Errorf("production assets %s and %s have identical content", existing, path)
			}
			assets[digest] = path
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return assets, nil
}

func applicationMaps(root string) ([]string, error) {
	var maps []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".map") {
			maps = append(maps, path)
		}
		return nil
	})
	sort.Strings(maps)
	return maps, err
}

func stripSourceMapReference(path string) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read production asset %s: %w", path, err)
	}
	cleaned := sourceMapReference.ReplaceAll(contents, nil)
	if err := os.WriteFile(path, cleaned, 0o644); err != nil {
		return fmt.Errorf("write production asset %s: %w", path, err)
	}
	return nil
}

func removeSourceMaps(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".map") {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("remove production source map %s: %w", path, err)
			}
		}
		return nil
	})
}

func normalizeSourceMap(source string, productionAsset string, productionRoot string, outputRoot string) error {
	encoded, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read source map %s: %w", source, err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		return fmt.Errorf("decode source map %s: %w", source, err)
	}
	relativeAsset, err := filepath.Rel(productionRoot, productionAsset)
	if err != nil {
		return err
	}
	document["file"] = filepath.ToSlash(relativeAsset)
	encoded, err = json.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode source map %s: %w", source, err)
	}
	encoded = append(encoded, '\n')
	target := filepath.Join(outputRoot, relativeAsset+".map")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(target, encoded, 0o644); err != nil {
		return fmt.Errorf("write source map %s: %w", target, err)
	}
	return nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
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
		return fmt.Errorf("source map output directory %s is not empty", path)
	}
	return nil
}
