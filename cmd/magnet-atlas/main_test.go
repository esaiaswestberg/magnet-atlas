package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/esaiaswestberg/magnet-atlas/internal/store"
)

func TestRunIngestOnce(t *testing.T) {
	dir := t.TempDir()
	fixturePath, err := filepath.Abs(filepath.Join("..", "..", "testdata", "sample-fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.yaml")
	dbPath := filepath.Join(dir, "magnet-atlas.db")
	config := strings.TrimSpace(`
server:
  listen: ":0"
database:
  path: "` + dbPath + `"
ingestion:
  interval: 30m
sources:
  - name: sample
    type: fixture
    enabled: true
    fixture_path: "` + fixturePath + `"
`)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	if err := run(context.Background(), configPath, true, logger); err != nil {
		t.Fatal(err)
	}

	repo, err := store.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	stats, err := repo.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := stats.TorrentCount, int64(2); got != want {
		t.Fatalf("torrent count = %d, want %d", got, want)
	}
}

func TestRunIngestOnceReturnsErrorOnBadFixture(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	dbPath := filepath.Join(dir, "magnet-atlas.db")
	config := strings.TrimSpace(`
server:
  listen: ":0"
database:
  path: "` + dbPath + `"
ingestion:
  interval: 30m
sources:
  - name: sample
    type: fixture
    enabled: true
    fixture_path: "` + filepath.Join(dir, "missing.json") + `"
`)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	if err := run(context.Background(), configPath, true, logger); err == nil {
		t.Fatal("expected ingest error")
	}
}
