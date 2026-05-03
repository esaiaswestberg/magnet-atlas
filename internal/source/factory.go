package source

import (
	"fmt"

	"github.com/esaiaswestberg/magnet-atlas/internal/config"
)

// NewFactory returns the built-in adapter factory for v1.
func NewFactory() Factory {
	return func(cfg config.SourceConfig) (Source, error) {
		switch cfg.Type {
		case "fixture":
			return NewFixtureAdapter(cfg)
		case "rss":
			return NewRSSAdapter(cfg)
		case "1337x":
			return New1337XAdapter(cfg)
		case "uindex":
			return NewUIndexAdapter(cfg)
		case "linux-releases":
			return NewLinuxReleasesAdapter(cfg)
		case "torznab":
			return NewTorznabAdapter(cfg)
		default:
			return nil, fmt.Errorf("unsupported source type %q", cfg.Type)
		}
	}
}
