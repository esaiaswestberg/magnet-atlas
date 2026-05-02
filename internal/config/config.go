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
		Listen         string   `yaml:"listen"`
		TorznabAPIKeys []string `yaml:"torznab_api_keys,omitempty"`
	} `yaml:"server"`
	Database struct {
		Type string `yaml:"type,omitempty"`
		Path string `yaml:"path,omitempty"`
		URL  string `yaml:"url,omitempty"`
	} `yaml:"database"`
	Ingestion struct {
		Interval time.Duration `yaml:"interval"`
	} `yaml:"ingestion"`
	Sources []SourceConfig `yaml:"sources"`
}

// SourceConfig declares a single source adapter.
type SourceConfig struct {
	Name         string        `yaml:"name"`
	Type         string        `yaml:"type"`
	Enabled      bool          `yaml:"enabled"`
	FixturePath  string        `yaml:"fixture_path,omitempty"`
	FeedURL      string        `yaml:"feed_url,omitempty"`
	BaseURL      string        `yaml:"base_url,omitempty"`
	Categories   []string      `yaml:"categories,omitempty"`
	PageWindow   int           `yaml:"page_window,omitempty"`
	MaxPages     int           `yaml:"max_pages,omitempty"`
	Concurrency  int           `yaml:"concurrency,omitempty"`
	RequestDelay time.Duration `yaml:"request_delay,omitempty"`
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
	cfg.Server.TorznabAPIKeys = normalizeStrings(cfg.Server.TorznabAPIKeys)
	if strings.TrimSpace(cfg.Database.Type) == "" {
		cfg.Database.Type = "sqlite"
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Database.Type)) {
	case "postgresql":
		cfg.Database.Type = "postgres"
	case "sqlite", "postgres":
		cfg.Database.Type = strings.ToLower(strings.TrimSpace(cfg.Database.Type))
	}
	if cfg.Database.Type == "sqlite" && strings.TrimSpace(cfg.Database.Path) == "" {
		cfg.Database.Path = "./magnet-atlas.db"
	}
	for i := range cfg.Sources {
		if strings.TrimSpace(cfg.Sources[i].BaseURL) == "" && cfg.Sources[i].Type == "1337x" {
			cfg.Sources[i].BaseURL = "https://www.1337xx.to"
		}
		if strings.TrimSpace(cfg.Sources[i].BaseURL) == "" && cfg.Sources[i].Type == "uindex" {
			cfg.Sources[i].BaseURL = "https://uindex.org"
		}
		if cfg.Sources[i].Type == "1337x" && cfg.Sources[i].Concurrency <= 0 {
			cfg.Sources[i].Concurrency = 4
		}
		if cfg.Sources[i].Type == "uindex" && cfg.Sources[i].Concurrency <= 0 {
			cfg.Sources[i].Concurrency = 4
		}
		if cfg.Sources[i].Type == "1337x" && cfg.Sources[i].PageWindow <= 0 && cfg.Sources[i].MaxPages > 0 {
			cfg.Sources[i].PageWindow = cfg.Sources[i].MaxPages
		}
		if cfg.Sources[i].Type == "uindex" && cfg.Sources[i].PageWindow <= 0 && cfg.Sources[i].MaxPages > 0 {
			cfg.Sources[i].PageWindow = cfg.Sources[i].MaxPages
		}
	}
}

func validate(cfg *Config) error {
	var errs []string
	if cfg.Server.Listen == "" {
		errs = append(errs, "server.listen is required")
	}
	if cfg.Database.Path == "" {
		if cfg.Database.Type == "sqlite" {
			errs = append(errs, "database.path is required for sqlite")
		}
	}
	switch cfg.Database.Type {
	case "sqlite":
		if strings.TrimSpace(cfg.Database.URL) != "" {
			errs = append(errs, "database.url must be empty for sqlite")
		}
	case "postgres":
		if strings.TrimSpace(cfg.Database.URL) == "" {
			errs = append(errs, "database.url is required for postgres")
		}
		if strings.TrimSpace(cfg.Database.Path) != "" {
			errs = append(errs, "database.path must be empty for postgres")
		}
	default:
		errs = append(errs, "database.type must be sqlite or postgres")
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
		case "rss":
			if strings.TrimSpace(source.FeedURL) == "" {
				errs = append(errs, idx+".feed_url is required for rss sources")
			}
		case "1337x":
			if strings.TrimSpace(source.BaseURL) == "" {
				errs = append(errs, idx+".base_url is required for 1337x sources")
			}
		case "uindex":
			if strings.TrimSpace(source.BaseURL) == "" {
				errs = append(errs, idx+".base_url is required for uindex sources")
			}
			for _, category := range source.Categories {
				if _, ok := normalizeUIndexCategory(category); !ok {
					errs = append(errs, idx+".categories contains unsupported uindex category")
					break
				}
			}
		default:
			errs = append(errs, idx+".type must be fixture, rss, 1337x or uindex for v1")
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// NormalizeUIndexCategories maps common category aliases to UIndex section names.
func NormalizeUIndexCategories(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		cat, ok := normalizeUIndexCategory(value)
		if !ok {
			continue
		}
		if _, exists := seen[cat]; exists {
			continue
		}
		seen[cat] = struct{}{}
		out = append(out, cat)
	}
	return out
}

func normalizeUIndexCategory(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "movie", "movies":
		return "movies", true
	case "television", "tv", "series":
		return "tv", true
	case "music":
		return "music", true
	case "anime":
		return "anime", true
	case "game", "games":
		return "games", true
	case "application", "applications", "app", "apps":
		return "apps", true
	case "xxx":
		return "xxx", true
	case "other", "others":
		return "other", true
	default:
		return "", false
	}
}

func normalizeStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
