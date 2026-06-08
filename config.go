package movoor

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// defaultListenAddress is the HTTP listen address used when none is configured.
const defaultListenAddress = ":8080"

// Config is the top-level application configuration, normally loaded from a
// YAML file via LoadConfig.
type Config struct {
	HTTP HTTPConfig `yaml:"http"`
}

// HTTPConfig configures the HTTP server that serves the embedded web UI and
// the JSON API.
type HTTPConfig struct {
	// ListenAddress is the host:port the HTTP server binds to.
	ListenAddress string `yaml:"listen-address"`
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

	return cfg, nil
}

// ResolveDefaults fills any unset fields with their default values.
func (c *Config) ResolveDefaults() {
	if c.HTTP.ListenAddress == "" {
		c.HTTP.ListenAddress = defaultListenAddress
	}
}
