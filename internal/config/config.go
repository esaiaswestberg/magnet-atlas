package config

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
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
	Name                string        `yaml:"name"`
	Type                string        `yaml:"type"`
	Enabled             bool          `yaml:"enabled"`
	FixturePath         string        `yaml:"fixture_path,omitempty"`
	FeedURL             string        `yaml:"feed_url,omitempty"`
	BaseURL             string        `yaml:"base_url,omitempty"`
	FlareSolverrURL     string        `yaml:"flaresolverr_url,omitempty"`
	SearchQuery         string        `yaml:"search_query,omitempty"`
	ReleasePaths        []string      `yaml:"release_paths,omitempty"`
	Sections            []string      `yaml:"sections,omitempty"`
	Categories          []string      `yaml:"categories,omitempty"`
	PageWindow          int           `yaml:"page_window,omitempty"`
	PageSize            int           `yaml:"page_size,omitempty"`
	MaxPages            int           `yaml:"max_pages,omitempty"`
	Concurrency         int           `yaml:"concurrency,omitempty"`
	RequestDelay        time.Duration `yaml:"request_delay,omitempty"`
	BackoffDelay        time.Duration `yaml:"backoff_delay,omitempty"`
	RequestAttempts     int           `yaml:"request_attempts,omitempty"`
	BindAddress         string        `yaml:"bind_address,omitempty"`
	BootstrapNodes      []string      `yaml:"bootstrap_nodes,omitempty"`
	SeedInfoHashes      []string      `yaml:"seed_infohashes,omitempty"`
	MetadataConcurrency int           `yaml:"metadata_concurrency,omitempty"`
	MetadataTimeout     time.Duration `yaml:"metadata_timeout,omitempty"`
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
		if strings.TrimSpace(cfg.Sources[i].BaseURL) == "" && cfg.Sources[i].Type == "linux-releases" {
			cfg.Sources[i].BaseURL = "https://releases.ubuntu.com/"
		}
		if cfg.Sources[i].Type == "rarbg" && len(cfg.Sources[i].Sections) == 0 {
			cfg.Sources[i].Sections = []string{"movies", "tv", "anime"}
		}
		if cfg.Sources[i].Type == "torznab" && cfg.Sources[i].PageSize <= 0 {
			cfg.Sources[i].PageSize = 100
		}
		if cfg.Sources[i].Type == "torznab" && cfg.Sources[i].PageWindow <= 0 && cfg.Sources[i].MaxPages > 0 {
			cfg.Sources[i].PageWindow = cfg.Sources[i].MaxPages
		}
		if cfg.Sources[i].Type == "torznab" && cfg.Sources[i].PageWindow <= 0 {
			cfg.Sources[i].PageWindow = 20
		}
		if cfg.Sources[i].Type == "rarbg" && cfg.Sources[i].Concurrency <= 0 {
			cfg.Sources[i].Concurrency = 4
		}
		if cfg.Sources[i].Type == "rarbg" && cfg.Sources[i].PageWindow <= 0 {
			cfg.Sources[i].PageWindow = 1
		}
		if cfg.Sources[i].Type == "rarbg" && cfg.Sources[i].RequestDelay <= 0 {
			cfg.Sources[i].RequestDelay = time.Second
		}
		if cfg.Sources[i].Type == "rarbg" && cfg.Sources[i].BackoffDelay <= 0 {
			cfg.Sources[i].BackoffDelay = 750 * time.Millisecond
		}
		if cfg.Sources[i].Type == "rarbg" && cfg.Sources[i].RequestAttempts <= 0 {
			cfg.Sources[i].RequestAttempts = 3
		}
		if cfg.Sources[i].Type == "1337x" && cfg.Sources[i].Concurrency <= 0 {
			cfg.Sources[i].Concurrency = 4
		}
		if cfg.Sources[i].Type == "uindex" && cfg.Sources[i].Concurrency <= 0 {
			cfg.Sources[i].Concurrency = 4
		}
		if cfg.Sources[i].Type == "linux-releases" && cfg.Sources[i].Concurrency <= 0 {
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
		case "linux-releases":
			if strings.TrimSpace(source.BaseURL) == "" {
				errs = append(errs, idx+".base_url is required for linux-releases sources")
			}
		case "torznab":
			if strings.TrimSpace(source.BaseURL) == "" {
				errs = append(errs, idx+".base_url is required for torznab sources")
			}
			for _, category := range source.Categories {
				if _, ok := normalizeTorznabCategory(category); !ok {
					errs = append(errs, idx+".categories contains unsupported torznab category")
					break
				}
			}
		case "rarbg":
			if strings.TrimSpace(source.BaseURL) == "" {
				errs = append(errs, idx+".base_url is required for rarbg sources")
			}
			if strings.TrimSpace(source.FlareSolverrURL) == "" {
				errs = append(errs, idx+".flaresolverr_url is required for rarbg sources")
			}
			for _, section := range source.Sections {
				if strings.TrimSpace(section) == "" {
					errs = append(errs, idx+".sections contains an empty rarbg section")
					break
				}
			}
		default:
			errs = append(errs, idx+".type must be fixture, rss, 1337x, uindex, linux-releases, torznab or rarbg for v1")
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

// NormalizeTorznabCategories maps common category aliases to standard Torznab category IDs.
func NormalizeTorznabCategories(values []string) []int {
	out := make([]int, 0, len(values))
	seen := make(map[int]struct{}, len(values))
	for _, value := range values {
		ids, ok := normalizeTorznabCategory(value)
		if !ok {
			continue
		}
		for _, id := range ids {
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	sort.Ints(out)
	return out
}

func normalizeTorznabCategory(value string) ([]int, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "movie", "movies":
		return []int{2000}, true
	case "music":
		return []int{3000}, true
	case "application", "applications", "app", "apps":
		return []int{4000}, true
	case "game", "games":
		return []int{4050}, true
	case "television", "tv", "series":
		return []int{5000}, true
	case "anime":
		return []int{5070}, true
	case "documentary", "documentaries":
		return []int{5080}, true
	case "xxx":
		return []int{6000}, true
	case "book", "books", "ebook", "ebooks":
		return []int{7000}, true
	case "other", "others":
		return []int{8000}, true
	default:
		id, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || id <= 0 {
			return nil, false
		}
		switch id {
		case 2000, 3000, 4000, 4050, 5000, 5070, 5080, 6000, 7000, 8000:
			return []int{id}, true
		default:
			return nil, false
		}
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

func normalizeInfoHash(value string) (string, bool) {
	value = strings.ToUpper(strings.TrimSpace(value))
	if len(value) != 40 {
		return "", false
	}
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'A' && r <= 'F':
		default:
			return "", false
		}
	}
	return value, true
}
