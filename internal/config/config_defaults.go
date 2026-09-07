package config

const (
	DefaultS3Region = "us-east-1"

	DefaultSigningExpirySeconds = 900 // 15 minutes

	DefaultLFSMaxBatchObjects                    = 1000
	DefaultLFSMaxBatchBodyBytes            int64 = 10 * 1024 * 1024 // 10 MiB
	DefaultLFSRequestLimitPerMinute              = 1200             // 20 req/sec average per client
	DefaultLFSBandwidthLimitBytesPerMinute int64 = 0                // disabled by default
)

func defaultConfig() *Config {
	// 1. Default Config
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
			MaxBatchObjects:              DefaultLFSMaxBatchObjects,
			MaxBatchBodyBytes:            DefaultLFSMaxBatchBodyBytes,
			RequestLimitPerMinute:        DefaultLFSRequestLimitPerMinute,
			BandwidthLimitBytesPerMinute: DefaultLFSBandwidthLimitBytesPerMinute,
		},
		Signing: SigningConfig{
			DefaultExpirySeconds: DefaultSigningExpirySeconds,
		},
	}
}
