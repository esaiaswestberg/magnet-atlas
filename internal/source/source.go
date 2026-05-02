package source

import (
	"context"

	"github.com/esaiaswestberg/magnet-atlas/internal/config"
	"github.com/esaiaswestberg/magnet-atlas/internal/domain"
)

// Adapter fetches torrents from a single configured source.
type Adapter interface {
	Name() string
	Fetch(ctx context.Context) ([]domain.Torrent, []domain.SourceObservation, error)
}

// Factory builds adapters from config.
type Factory func(cfg config.SourceConfig) (Adapter, error)
