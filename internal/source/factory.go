package source

import (
	"fmt"

	"github.com/esaiaswestberg/magnet-atlas/internal/config"
)

// NewFactory returns the built-in adapter factory for v1.
func NewFactory() Factory {
	return func(cfg config.SourceConfig) (Adapter, error) {
		switch cfg.Type {
		case "fixture":
			return NewFixtureAdapter(cfg)
		default:
			return nil, fmt.Errorf("unsupported source type %q", cfg.Type)
		}
	}
}
