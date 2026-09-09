package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	DefaultSigningExpirySeconds = 900

	defaultLFSMaxBatchObjects                    = 1000
	defaultLFSMaxBatchBodyBytes            int64 = 10 * 1024 * 1024
	defaultLFSRequestLimitPerMinute              = 1200
	defaultLFSBandwidthLimitBytesPerMinute int64 = 0
)

func LoadConfig(configFile string) (*Config, error) {
	cfg := defaultConfig()
	if err := loadConfigFile(configFile, cfg); err != nil {
		return nil, err
	}
	if err := applyEnvironmentOverrides(cfg); err != nil {
		return nil, err
	}
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	resolveAuthEnvironment(cfg)
	return cfg, nil
}

func defaultConfig() *Config {
	return &Config{
		Port:     8080,
		Database: DatabaseConfig{},
		Auth:     AuthConfig{},
		Routes: RoutesConfig{
			Docs:     true,
			Ga4gh:    true,
			Metrics:  true,
			Internal: true,
			LFS:      true,
		},
		LFS: LFSConfig{
			MaxBatchObjects:              defaultLFSMaxBatchObjects,
			MaxBatchBodyBytes:            defaultLFSMaxBatchBodyBytes,
			RequestLimitPerMinute:        defaultLFSRequestLimitPerMinute,
			BandwidthLimitBytesPerMinute: defaultLFSBandwidthLimitBytesPerMinute,
		},
		Signing: SigningConfig{DefaultExpirySeconds: DefaultSigningExpirySeconds},
	}
}

func loadConfigFile(configFile string, cfg *Config) error {
	if configFile == "" {
		return nil
	}
	f, err := os.Open(configFile)
	if err != nil {
		return fmt.Errorf("failed to open config file: %w", err)
	}
	defer f.Close()

	switch filepath.Ext(configFile) {
	case ".yaml", ".yml":
		if err := yaml.NewDecoder(f).Decode(cfg); err != nil {
			return fmt.Errorf("failed to decode yaml config: %w", err)
		}
	case ".json":
		if err := json.NewDecoder(f).Decode(cfg); err != nil {
			return fmt.Errorf("failed to decode json config: %w", err)
		}
	default:
		return fmt.Errorf("unsupported config file extension: %s", filepath.Ext(configFile))
	}
	return nil
}
