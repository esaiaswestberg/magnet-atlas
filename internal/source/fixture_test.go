package source

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/esaiaswestberg/magnet-atlas/internal/config"
)

func TestFixtureAdapterFetch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.json")
	data, err := os.ReadFile(filepath.Clean("../../testdata/sample-fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	adapter, err := NewFixtureAdapter(config.SourceConfig{
		Name:        "sample",
		Type:        "fixture",
		Enabled:     true,
		FixturePath: path,
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := adapter.Fetch(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(result.Torrents), 2; got != want {
		t.Fatalf("torrents len = %d, want %d", got, want)
	}
	if got, want := len(result.Observations), 2; got != want {
		t.Fatalf("observations len = %d, want %d", got, want)
	}
	if got, want := result.Torrents[0].InfoHash, "0123456789abcdef0123456789abcdef01234567"; got != want {
		t.Fatalf("infohash = %q, want %q", got, want)
	}
	if got, want := result.Observations[0].Source, "sample"; got != want {
		t.Fatalf("source = %q, want %q", got, want)
	}
}
