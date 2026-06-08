package movoor

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultLogging         = "info"
	defaultMetricsAddr     = ":9090"
	defaultHealthCheckAddr = ":8081"
	defaultFrontendAddr    = ":8080"
	defaultQueryTimeout    = 30 * time.Second
	defaultDialTimeout     = 5 * time.Second
)

var (
	errNoClickHouseNodes = errors.New("clickhouse.nodes must contain at least one node")
	errNoWatches         = errors.New("watches must contain at least one table")
)

// Config is the top-level application configuration, normally loaded from a
// YAML file via LoadConfig.
//
//nolint:tagliatelle // Existing config keys intentionally use lower camel case.
type Config struct {
	Logging         string           `yaml:"logging"`
	MetricsAddr     string           `yaml:"metricsAddr"`
	HealthCheckAddr string           `yaml:"healthCheckAddr"`
	ClickHouse      ClickHouseConfig `yaml:"clickhouse"`
	Watches         []WatchConfig    `yaml:"watches"`
	Frontend        FrontendConfig   `yaml:"frontend"`
}

// ClickHouseConfig configures per-node ClickHouse connections. Each node entry
// must identify exactly one physical ClickHouse server.
//
//nolint:tagliatelle // Existing config keys intentionally use lower camel case.
type ClickHouseConfig struct {
	QueryTimeout time.Duration          `yaml:"queryTimeout"`
	DialTimeout  time.Duration          `yaml:"dialTimeout"`
	Nodes        []ClickHouseNodeConfig `yaml:"nodes"`
}

// ClickHouseNodeConfig configures one physical ClickHouse node.
type ClickHouseNodeConfig struct {
	Name    string `yaml:"name"`
	Shard   string `yaml:"shard"`
	Replica string `yaml:"replica"`
	DSN     string `yaml:"dsn"`
}

// WatchConfig identifies a table movoor should monitor.
type WatchConfig struct {
	Database string `yaml:"database"`
	Table    string `yaml:"table"`
}

// FrontendConfig configures the HTTP server that serves the embedded web UI
// and JSON API.
type FrontendConfig struct {
	Enabled *bool  `yaml:"enabled"`
	Addr    string `yaml:"addr"`
}

// DefaultConfig returns a Config populated with sensible defaults.
func DefaultConfig() Config {
	cfg := Config{}
	cfg.ResolveDefaults()

	return cfg
}

// LoadConfig reads and parses a YAML config file from path.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err = yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}

	cfg.ResolveDefaults()
	if err = cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate config: %w", err)
	}

	return cfg, nil
}

// ResolveDefaults fills any unset fields with their default values.
func (c *Config) ResolveDefaults() {
	if c.Logging == "" {
		c.Logging = defaultLogging
	}

	if c.MetricsAddr == "" {
		c.MetricsAddr = defaultMetricsAddr
	}

	if c.HealthCheckAddr == "" {
		c.HealthCheckAddr = defaultHealthCheckAddr
	}

	if c.ClickHouse.QueryTimeout == 0 {
		c.ClickHouse.QueryTimeout = defaultQueryTimeout
	}

	if c.ClickHouse.DialTimeout == 0 {
		c.ClickHouse.DialTimeout = defaultDialTimeout
	}

	if c.Frontend.Addr == "" {
		c.Frontend.Addr = defaultFrontendAddr
	}
}

// Validate checks the application configuration for required identity and
// connection fields.
//
//nolint:gocognit // Validation stays explicit so field-specific errors remain precise.
func (c Config) Validate() error {
	if err := validateLogging(c.Logging); err != nil {
		return err
	}

	if len(c.ClickHouse.Nodes) == 0 {
		return errNoClickHouseNodes
	}

	seenNodes := make(map[string]struct{}, len(c.ClickHouse.Nodes))
	for i, node := range c.ClickHouse.Nodes {
		if node.Name == "" {
			return fmt.Errorf("clickhouse.nodes[%d].name is required", i)
		}
		if _, ok := seenNodes[node.Name]; ok {
			return fmt.Errorf("clickhouse.nodes[%d].name %q is duplicated", i, node.Name)
		}
		seenNodes[node.Name] = struct{}{}

		if node.Shard == "" {
			return fmt.Errorf("clickhouse.nodes[%d].shard is required", i)
		}
		if node.Replica == "" {
			return fmt.Errorf("clickhouse.nodes[%d].replica is required", i)
		}
		if err := validateClickHouseNodeDSN(node.DSN); err != nil {
			return fmt.Errorf("clickhouse.nodes[%d].dsn: %w", i, err)
		}
	}

	if len(c.Watches) == 0 {
		return errNoWatches
	}

	seenWatches := make(map[string]struct{}, len(c.Watches))
	for i, watch := range c.Watches {
		if watch.Database == "" {
			return fmt.Errorf("watches[%d].database is required", i)
		}
		if watch.Table == "" {
			return fmt.Errorf("watches[%d].table is required", i)
		}

		key := watch.Database + "." + watch.Table
		if _, ok := seenWatches[key]; ok {
			return fmt.Errorf("watches[%d] %q is duplicated", i, key)
		}
		seenWatches[key] = struct{}{}
	}

	if c.Frontend.IsEnabled() && c.Frontend.Addr == "" {
		return errors.New("frontend.addr is required when frontend.enabled is true")
	}

	return nil
}

// IsEnabled reports whether the frontend/API server should start. It defaults
// to true when frontend.enabled is omitted.
func (c FrontendConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

func validateLogging(level string) error {
	switch strings.ToLower(level) {
	case "debug", "info", "warn", "warning", "error":
		return nil
	default:
		return errors.New("logging must be one of debug, info, warn, or error")
	}
}

func validateClickHouseNodeDSN(raw string) error {
	if raw == "" {
		return errors.New("is required")
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}

	if parsed.Scheme != "clickhouse" {
		return errors.New("must use clickhouse:// native protocol DSN")
	}

	if parsed.Host == "" {
		return errors.New("host is required")
	}

	if strings.Contains(parsed.Host, ",") {
		return errors.New("must identify exactly one ClickHouse host, not a failover list")
	}

	return nil
}
