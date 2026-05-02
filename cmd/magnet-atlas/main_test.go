package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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

	var logs bytes.Buffer
	logger := newLogger(false, &logs)
	if err := run(context.Background(), configPath, true, logger); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "one-shot ingest complete") {
		t.Fatalf("missing completion log: %s", logs.String())
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

func TestNewLoggerVerboseControlsDebug(t *testing.T) {
	var quiet bytes.Buffer
	quietLogger := newLogger(false, &quiet)
	quietLogger.Debug("debug message")
	if strings.Contains(quiet.String(), "debug message") {
		t.Fatalf("debug message should not be logged without --verbose: %s", quiet.String())
	}

	var verbose bytes.Buffer
	verboseLogger := newLogger(true, &verbose)
	verboseLogger.Debug("debug message")
	if !strings.Contains(verbose.String(), "debug message") {
		t.Fatalf("debug message should be logged with --verbose: %s", verbose.String())
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

	logger := newLogger(false, io.Discard)
	if err := run(context.Background(), configPath, true, logger); err == nil {
		t.Fatal("expected ingest error")
	}
}

func TestRunIngestOnceWithRSSFeed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:tv="https://showrss.info">
  <channel>
    <item>
      <title>The Rookie S08E17 1080p HEVC x265-MeGusta EZTV</title>
      <link>magnet:?xt=urn:btih:3636c60f178912f89eb054f030da0ad9bd593938&amp;dn=The.Rookie.S08E17.1080p.HEVC.x265-MeGusta</link>
      <guid isPermaLink="false">99a8f972ce4ff3453911e5d541cb7a1b8cdc4698</guid>
      <pubDate>Tue, 28 Apr 2026 09:24:13 +0000</pubDate>
      <description>New Episode: The Rookie S08E17 1080p HEVC x265-MeGusta EZTV</description>
      <tv:info_hash>3636c60f178912f89eb054f030da0ad9bd593938</tv:info_hash>
    </item>
  </channel>
</rss>`))
	}))
	defer server.Close()

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
  - name: rss
    type: rss
    enabled: true
    feed_url: "` + server.URL + `"
`)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	logger := newLogger(false, &logs)
	if err := run(context.Background(), configPath, true, logger); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "one-shot ingest complete") {
		t.Fatalf("missing completion log: %s", logs.String())
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
	if got, want := stats.TorrentCount, int64(1); got != want {
		t.Fatalf("torrent count = %d, want %d", got, want)
	}
}
