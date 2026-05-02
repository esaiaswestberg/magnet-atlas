package store

import (
	"context"
	"errors"

	"github.com/esaiaswestberg/magnet-atlas/internal/domain"
)

var ErrSourceStateNotFound = errors.New("source state not found")

// SearchFilter narrows torrent search results.
type SearchFilter struct {
	Query    string
	Source   string
	Category string
	Limit    int
	Offset   int
}

// Stats summarizes the current index state.
type Stats struct {
	TorrentCount  int64  `json:"torrent_count"`
	SourceCount   int64  `json:"source_count"`
	LastUpdatedAt string `json:"last_updated_at,omitempty"`
}

// Details returns a canonical torrent and all provenance observations.
type Details struct {
	Torrent domain.Torrent             `json:"torrent"`
	Sources []domain.SourceObservation `json:"sources"`
}

// Repository is the storage interface used by ingestion and the API layer.
type Repository interface {
	Close() error
	Upsert(ctx context.Context, torrent domain.Torrent, obs domain.SourceObservation) error
	Search(ctx context.Context, filter SearchFilter) ([]domain.Torrent, error)
	Get(ctx context.Context, infohash string) (Details, error)
	HasInfohash(ctx context.Context, infohash string) (bool, error)
	ListSources(ctx context.Context) ([]string, error)
	Stats(ctx context.Context) (Stats, error)
	GetSourceState(ctx context.Context, source, section string) (string, error)
	SetSourceState(ctx context.Context, source, section, state string) error
}
