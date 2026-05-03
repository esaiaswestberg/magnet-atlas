package source

import (
	"context"

	"github.com/esaiaswestberg/magnet-atlas/internal/config"
	"github.com/esaiaswestberg/magnet-atlas/internal/domain"
	"github.com/esaiaswestberg/magnet-atlas/internal/store"
)

// Source identifies a configured ingest source.
type Source interface {
	Name() string
}

// Adapter fetches torrents from a single configured source.
type Adapter interface {
	Source
	Fetch(ctx context.Context, repo store.Repository) (FetchResult, error)
}

// Daemon runs continuously in the background and writes directly to the repository.
type Daemon interface {
	Source
	Run(ctx context.Context, repo store.Repository) error
}

// Factory builds sources from config.
type Factory func(cfg config.SourceConfig) (Source, error)

// FetchResult contains one ingest batch from a source adapter.
type FetchResult struct {
	Torrents     []domain.Torrent
	Observations []domain.SourceObservation
	State        map[string]string
}
