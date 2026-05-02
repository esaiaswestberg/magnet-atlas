package store

import (
	"context"
	"errors"
	"strings"

	"github.com/esaiaswestberg/magnet-atlas/internal/domain"
)

var ErrSourceStateNotFound = errors.New("source state not found")

// Options select the storage backend and its connection settings.
type Options struct {
	Type string
	Path string
	URL  string
}

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

// Open builds the requested storage backend.
func Open(opts Options) (Repository, error) {
	switch normalizeType(opts.Type) {
	case "", "sqlite":
		return OpenSQLite(opts.Path)
	case "postgres", "postgresql":
		return OpenPostgres(opts.URL)
	default:
		return nil, errors.New("unsupported database type")
	}
}

func normalizeType(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}
