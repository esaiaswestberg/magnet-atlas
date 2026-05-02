package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAppliesDefaultsAndParsesSources(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
database:
  type: sqlite
  path: ""
ingestion:
  interval: 15m
sources:
  - name: sample
    type: fixture
    enabled: true
    fixture_path: ./testdata/sample-fixture.json
  - name: rss
    type: rss
    enabled: true
    feed_url: https://example.invalid/feed.rss
  - name: 1337x
    type: 1337x
    enabled: false
  - name: uindex
    type: uindex
    enabled: false
  - name: linux
    type: linux-releases
    enabled: false
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.Server.Listen, ":8080"; got != want {
		t.Fatalf("listen = %q, want %q", got, want)
	}
	if got, want := cfg.Database.Path, "./magnet-atlas.db"; got != want {
		t.Fatalf("database path = %q, want %q", got, want)
	}
	if got, want := cfg.Database.Type, "sqlite"; got != want {
		t.Fatalf("database type = %q, want %q", got, want)
	}
	if got, want := len(cfg.Sources), 5; got != want {
		t.Fatalf("sources len = %d, want %d", got, want)
	}
	if got, want := cfg.Sources[2].BaseURL, "https://www.1337xx.to"; got != want {
		t.Fatalf("base url = %q, want %q", got, want)
	}
	if got, want := cfg.Sources[2].Concurrency, 4; got != want {
		t.Fatalf("concurrency = %d, want %d", got, want)
	}
	if got, want := cfg.Sources[3].BaseURL, "https://uindex.org"; got != want {
		t.Fatalf("uindex base url = %q, want %q", got, want)
	}
	if got, want := cfg.Sources[3].Concurrency, 4; got != want {
		t.Fatalf("uindex concurrency = %d, want %d", got, want)
	}
	if got, want := cfg.Sources[4].BaseURL, "https://releases.ubuntu.com/"; got != want {
		t.Fatalf("linux releases base url = %q, want %q", got, want)
	}
	if got, want := cfg.Sources[4].Concurrency, 4; got != want {
		t.Fatalf("linux releases concurrency = %d, want %d", got, want)
	}
}

func TestLoadSupportsPostgresDatabaseConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
database:
  type: postgres
  url: postgres://magnet_atlas:secret@localhost:5432/magnet_atlas?sslmode=disable
sources:
  - name: sample
    type: fixture
    enabled: true
    fixture_path: ./testdata/sample-fixture.json
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.Database.Type, "postgres"; got != want {
		t.Fatalf("database type = %q, want %q", got, want)
	}
	if got, want := cfg.Database.URL, "postgres://magnet_atlas:secret@localhost:5432/magnet_atlas?sslmode=disable"; got != want {
		t.Fatalf("database url = %q, want %q", got, want)
	}
}

func TestLoadRejectsMixedDatabaseConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
database:
  type: postgres
  path: ./magnet-atlas.db
  url: postgres://magnet_atlas:secret@localhost:5432/magnet_atlas?sslmode=disable
sources:
  - name: sample
    type: fixture
    enabled: true
    fixture_path: ./testdata/sample-fixture.json
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected mixed database config to fail")
	}
}

func TestLoadRejectsRssSourceWithoutFeedURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
database:
  type: sqlite
  path: ./magnet-atlas.db
sources:
  - name: rss
    type: rss
    enabled: true
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected rss source without feed_url to fail")
	}
}

func TestLoadRejectsUIndexSourceWithInvalidCategory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
database:
  type: sqlite
  path: ./magnet-atlas.db
sources:
  - name: uindex
    type: uindex
    enabled: true
    categories:
      - not-a-real-category
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected uindex source with invalid category to fail")
	}
}
