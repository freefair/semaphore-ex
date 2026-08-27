package util

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ReadDebugFilterConfig reads only the non-secret debug-filter setting from a
// YAML or JSON configuration file. It does not mutate the active Config.
func ReadDebugFilterConfig(configPath string) (string, error) {
	if configPath == "" {
		return "", nil
	}
	file, err := os.Open(configPath)
	if err != nil {
		return "", fmt.Errorf("read debug filter configuration: %w", err)
	}
	defer file.Close() //nolint:errcheck
	var partial struct {
		Log *struct {
			DebugFilter string `yaml:"debug_filter"`
		} `yaml:"log"`
	}
	if err = yaml.NewDecoder(file).Decode(&partial); err != nil {
		return "", fmt.Errorf("decode debug filter configuration: %w", err)
	}
	if partial.Log == nil {
		return "", nil
	}
	return partial.Log.DebugFilter, nil
}
