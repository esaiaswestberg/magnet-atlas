package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/esaiaswestberg/magnet-atlas/internal/api"
	"github.com/esaiaswestberg/magnet-atlas/internal/app"
	"github.com/esaiaswestberg/magnet-atlas/internal/config"
	"github.com/esaiaswestberg/magnet-atlas/internal/store"
)

func main() {
	var configPath string
	var ingestOnce bool
	var verbose bool
	flag.StringVar(&configPath, "config", "config.yaml", "path to the YAML configuration file")
	flag.BoolVar(&ingestOnce, "ingest-once", false, "run a single ingestion pass and exit")
	flag.BoolVar(&verbose, "verbose", false, "enable debug logging")
	flag.Parse()

	logger := newLogger(verbose, os.Stdout)
	slog.SetDefault(logger)

	ctx := context.Background()
	var stop func()
	if !ingestOnce {
		ctx, stop = signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
	}

	if err := run(ctx, configPath, ingestOnce, logger); err != nil {
		logger.Error("runtime error", "error", err)
		os.Exit(1)
	}
}

func newLogger(verbose bool, w io.Writer) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}

func run(ctx context.Context, configPath string, ingestOnce bool, logger *slog.Logger) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	repo, err := store.Open(store.Options{
		Type: cfg.Database.Type,
		Path: cfg.Database.Path,
		URL:  cfg.Database.URL,
	})
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer repo.Close()

	daemon, err := app.New(cfg, repo, logger)
	if err != nil {
		return fmt.Errorf("build app: %w", err)
	}

	if ingestOnce {
		logger.Info("starting one-shot ingest")
		if err := daemon.IngestOnce(ctx); err != nil {
			return err
		}
		logger.Info("one-shot ingest complete")
		return nil
	}

	openAPISpec, err := os.ReadFile("api/openapi.yaml")
	if err != nil {
		return fmt.Errorf("read openapi spec: %w", err)
	}

	server := api.NewServer(repo, openAPISpec)
	httpServer := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("starting ingest loop")
		errCh <- daemon.Run(ctx)
	}()
	go func() {
		logger.Info("starting http server", "listen", cfg.Server.Listen)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil {
			return err
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
