package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/esaiaswestberg/magnet-atlas/internal/config"
	"github.com/esaiaswestberg/magnet-atlas/internal/domain"
	"github.com/esaiaswestberg/magnet-atlas/internal/source"
	"github.com/esaiaswestberg/magnet-atlas/internal/store"
)

// App owns the daemon lifecycle.
type App struct {
	cfg     *config.Config
	repo    store.Repository
	servers []source.Adapter
	logger  *slog.Logger
}

// New builds the application from config and repository.
func New(cfg *config.Config, repo store.Repository, logger *slog.Logger) (*App, error) {
	factory := source.NewFactory()
	adapters := make([]source.Adapter, 0, len(cfg.Sources))
	for _, src := range cfg.Sources {
		if !src.Enabled {
			continue
		}
		adapter, err := factory(src)
		if err != nil {
			return nil, fmt.Errorf("source %q: %w", src.Name, err)
		}
		adapters = append(adapters, adapter)
	}
	return &App{cfg: cfg, repo: repo, servers: adapters, logger: logger}, nil
}

// IngestOnce runs one pass over the enabled adapters.
func (a *App) IngestOnce(ctx context.Context) error {
	for _, adapter := range a.servers {
		torrents, observations, err := adapter.Fetch(ctx)
		if err != nil {
			return fmt.Errorf("adapter %q: %w", adapter.Name(), err)
		}
		for i := range torrents {
			obs := domain.SourceObservation{}
			if i < len(observations) {
				obs = observations[i]
			}
			obs.Source = adapter.Name()
			if err := a.repo.Upsert(ctx, torrents[i], obs); err != nil {
				return fmt.Errorf("upsert %q: %w", torrents[i].InfoHash, err)
			}
		}
	}
	return nil
}

// Run starts the ingest loop and blocks until ctx is done.
func (a *App) Run(ctx context.Context) error {
	if err := a.IngestOnce(ctx); err != nil {
		return err
	}
	if a.cfg.Ingestion.Interval <= 0 {
		<-ctx.Done()
		return ctx.Err()
	}

	ticker := time.NewTicker(a.cfg.Ingestion.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := a.IngestOnce(ctx); err != nil && a.logger != nil {
				a.logger.Error("ingest failed", "error", err)
			}
		}
	}
}
