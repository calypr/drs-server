package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	DefaultSigningExpirySeconds = 900

	defaultLFSMaxBatchObjects                       = 1000
	defaultLFSMaxBatchBodyBytes               int64 = 10 * 1024 * 1024
	defaultLFSRequestLimitPerMinute                 = 1200
	defaultLFSBandwidthLimitBytesPerMinute    int64 = 0
	defaultDRSMaxBulkRequestLength                  = 1000
	defaultMultipartCleanupIntervalSeconds          = 60
	defaultMultipartInactiveTimeoutSeconds          = 24 * 60 * 60
	defaultMultipartCompletedRetentionSeconds       = 60 * 60
	defaultMultipartBatchSize                       = 100
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
		Profile:  ProfileDevelopment,
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
		Service: ServiceConfig{
			ID:               "drs-service-calypr",
			Name:             "Calypr DRS Server",
			Description:      "Calypr-backed DRS server",
			Environment:      "dev",
			Organization:     "Calypr",
			OrganizationURL:  "https://github.com/calypr/syfon",
			ContactURL:       "https://github.com/calypr/syfon/issues",
			DocumentationURL: "https://github.com/calypr/syfon",
		},
		DRS: DRSConfig{MaxBulkRequestLength: defaultDRSMaxBulkRequestLength},
		Multipart: MultipartConfig{
			CleanupIntervalSeconds:    defaultMultipartCleanupIntervalSeconds,
			InactiveTimeoutSeconds:    defaultMultipartInactiveTimeoutSeconds,
			CompletedRetentionSeconds: defaultMultipartCompletedRetentionSeconds,
			BatchSize:                 defaultMultipartBatchSize,
		},
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
		decoder := yaml.NewDecoder(f)
		decoder.KnownFields(true)
		if err := decoder.Decode(cfg); err != nil {
			return fmt.Errorf("failed to decode yaml config: %w", err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			if err == nil {
				return fmt.Errorf("failed to decode yaml config: multiple documents are not allowed")
			}
			return fmt.Errorf("failed to decode trailing yaml config: %w", err)
		}
	case ".json":
		decoder := json.NewDecoder(f)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(cfg); err != nil {
			return fmt.Errorf("failed to decode json config: %w", err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			if err == nil {
				return fmt.Errorf("failed to decode json config: trailing values are not allowed")
			}
			return fmt.Errorf("failed to decode trailing json config: %w", err)
		}
	default:
		return fmt.Errorf("unsupported config file extension: %s", filepath.Ext(configFile))
	}
	return nil
}
