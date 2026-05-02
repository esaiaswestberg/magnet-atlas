package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the root runtime configuration for Magnet Atlas.
type Config struct {
	Server struct {
		Listen string `yaml:"listen"`
	} `yaml:"server"`
	Database struct {
		Path string `yaml:"path"`
	} `yaml:"database"`
	Ingestion struct {
		Interval time.Duration `yaml:"interval"`
	} `yaml:"ingestion"`
	Sources []SourceConfig `yaml:"sources"`
}

// SourceConfig declares a single source adapter.
type SourceConfig struct {
	Name        string `yaml:"name"`
	Type        string `yaml:"type"`
	Enabled     bool   `yaml:"enabled"`
	FixturePath string `yaml:"fixture_path,omitempty"`
}

// Load reads and validates a YAML config file.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}

	applyDefaults(&cfg)
	if err := validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func applyDefaults(cfg *Config) {
	if strings.TrimSpace(cfg.Server.Listen) == "" {
		cfg.Server.Listen = ":8080"
	}
	if strings.TrimSpace(cfg.Database.Path) == "" {
		cfg.Database.Path = "./magnet-atlas.db"
	}
}

func validate(cfg *Config) error {
	var errs []string
	if cfg.Server.Listen == "" {
		errs = append(errs, "server.listen is required")
	}
	if cfg.Database.Path == "" {
		errs = append(errs, "database.path is required")
	}
	seen := map[string]struct{}{}
	for i, source := range cfg.Sources {
		idx := fmt.Sprintf("sources[%d]", i)
		if strings.TrimSpace(source.Name) == "" {
			errs = append(errs, idx+".name is required")
		}
		if strings.TrimSpace(source.Type) == "" {
			errs = append(errs, idx+".type is required")
		}
		if _, ok := seen[source.Name]; ok {
			errs = append(errs, idx+".name must be unique")
		}
		seen[source.Name] = struct{}{}
		switch source.Type {
		case "fixture":
			if strings.TrimSpace(source.FixturePath) == "" {
				errs = append(errs, idx+".fixture_path is required for fixture sources")
			}
		default:
			errs = append(errs, idx+".type must be fixture for v1")
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}
