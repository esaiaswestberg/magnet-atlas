package source

import (
	"context"

	"github.com/esaiaswestberg/magnet-atlas/internal/config"
	"github.com/esaiaswestberg/magnet-atlas/internal/domain"
	"github.com/esaiaswestberg/magnet-atlas/internal/store"
)

// Adapter fetches torrents from a single configured source.
type Adapter interface {
	Name() string
	Fetch(ctx context.Context, repo store.Repository) (FetchResult, error)
}

// Factory builds adapters from config.
type Factory func(cfg config.SourceConfig) (Adapter, error)

// FetchResult contains one ingest batch from a source adapter.
type FetchResult struct {
	Torrents     []domain.Torrent
	Observations []domain.SourceObservation
	State        map[string]string
}
