package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

func loadConfigFile(configFile string, cfg *Config) error {
	// 2. Load from file if provided
	if configFile != "" {
		f, err := os.Open(configFile)
		if err != nil {
			return fmt.Errorf("failed to open config file: %w", err)
		}
		defer f.Close()

		ext := filepath.Ext(configFile)
		if ext == ".yaml" || ext == ".yml" {
			if err := yaml.NewDecoder(f).Decode(cfg); err != nil {
				return fmt.Errorf("failed to decode yaml config: %w", err)
			}
		} else if ext == ".json" {
			if err := json.NewDecoder(f).Decode(cfg); err != nil {
				return fmt.Errorf("failed to decode json config: %w", err)
			}
		} else {
			return fmt.Errorf("unsupported config file extension: %s", ext)
		}
	}
	return nil
}
