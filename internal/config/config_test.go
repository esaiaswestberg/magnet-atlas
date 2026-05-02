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
  path: ""
ingestion:
  interval: 15m
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
	if got, want := cfg.Server.Listen, ":8080"; got != want {
		t.Fatalf("listen = %q, want %q", got, want)
	}
	if got, want := cfg.Database.Path, "./magnet-atlas.db"; got != want {
		t.Fatalf("database path = %q, want %q", got, want)
	}
	if got, want := len(cfg.Sources), 1; got != want {
		t.Fatalf("sources len = %d, want %d", got, want)
	}
}
