package source

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/esaiaswestberg/magnet-atlas/internal/config"
	"github.com/esaiaswestberg/magnet-atlas/internal/domain"
	"github.com/esaiaswestberg/magnet-atlas/internal/store"
)

// FixtureAdapter replays torrents from a local JSON fixture.
type FixtureAdapter struct {
	name        string
	fixturePath string
}

// NewFixtureAdapter creates a fixture-backed adapter.
func NewFixtureAdapter(cfg config.SourceConfig) (*FixtureAdapter, error) {
	if cfg.Name == "" {
		return nil, fmt.Errorf("source name is required")
	}
	return &FixtureAdapter{name: cfg.Name, fixturePath: cfg.FixturePath}, nil
}

func (a *FixtureAdapter) Name() string { return a.name }

func (a *FixtureAdapter) Fetch(ctx context.Context, _ store.Repository) (FetchResult, error) {
	select {
	case <-ctx.Done():
		return FetchResult{}, ctx.Err()
	default:
	}

	b, err := os.ReadFile(a.fixturePath)
	if err != nil {
		return FetchResult{}, err
	}

	var payload []fixtureTorrent
	if err := json.Unmarshal(b, &payload); err != nil {
		return FetchResult{}, err
	}

	torrents := make([]domain.Torrent, 0, len(payload))
	observations := make([]domain.SourceObservation, 0, len(payload))
	now := time.Now().UTC()
	for _, item := range payload {
		torrents = append(torrents, item.Torrent)
		observations = append(observations, domain.SourceObservation{
			Source:      a.name,
			SourceID:    item.SourceID,
			SourceURL:   item.SourceURL,
			Title:       item.Torrent.Title,
			Category:    item.Torrent.Category,
			SizeBytes:   item.Torrent.SizeBytes,
			Seeders:     item.Torrent.Seeders,
			Leechers:    item.Torrent.Leechers,
			PublishedAt: item.Torrent.PublishedAt,
			MagnetURI:   item.Torrent.MagnetURI,
			DownloadURL: item.Torrent.DownloadURL,
			ObservedAt:  now,
			RawJSON:     string(item.RawJSON),
		})
	}

	return FetchResult{Torrents: torrents, Observations: observations}, nil
}

type fixtureTorrent struct {
	domain.Torrent
	SourceID  string          `json:"source_id"`
	SourceURL string          `json:"source_url"`
	RawJSON   json.RawMessage `json:"raw_json"`
}
