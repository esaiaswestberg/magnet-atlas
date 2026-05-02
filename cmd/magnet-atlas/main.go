package main

import (
	"context"
	"flag"
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
	flag.StringVar(&configPath, "config", "config.yaml", "path to the YAML configuration file")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Error("load config", "error", err)
		os.Exit(1)
	}

	repo, err := store.OpenSQLite(cfg.Database.Path)
	if err != nil {
		logger.Error("open store", "error", err)
		os.Exit(1)
	}
	defer repo.Close()

	openAPISpec, err := os.ReadFile("api/openapi.yaml")
	if err != nil {
		logger.Error("read openapi spec", "error", err)
		os.Exit(1)
	}

	daemon, err := app.New(cfg, repo, logger)
	if err != nil {
		logger.Error("build app", "error", err)
		os.Exit(1)
	}

	server := api.NewServer(repo, openAPISpec)
	httpServer := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
			logger.Error("runtime error", "error", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown", "error", err)
	}
}
