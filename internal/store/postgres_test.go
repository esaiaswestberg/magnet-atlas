package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/esaiaswestberg/magnet-atlas/internal/domain"
)

func TestBuildSearchText(t *testing.T) {
	tests := []struct {
		title, category string
		want            string
	}{
		{title: "Example Torrent", category: "software", want: "Example Torrent software"},
		{title: "Example Torrent", category: "", want: "Example Torrent"},
		{title: "", category: "software", want: "software"},
		{title: " ", category: " ", want: ""},
	}

	for _, tc := range tests {
		if got := buildSearchText(tc.title, tc.category); got != tc.want {
			t.Fatalf("buildSearchText(%q, %q) = %q, want %q", tc.title, tc.category, got, tc.want)
		}
	}
}

func TestPostgresRepositoryIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set")
	}

	repo, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	ctx := context.Background()
	for _, stmt := range []string{
		`DELETE FROM torrent_sources`,
		`DELETE FROM source_state`,
		`DELETE FROM torrents`,
	} {
		if _, err := repo.db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}

	infohash := "fedcba9876543210fedcba9876543210fedcba98"
	err = repo.Upsert(ctx,
		domain.Torrent{
			InfoHash:    infohash,
			Title:       "Example PostgreSQL Torrent",
			Category:    "software",
			SizeBytes:   2048,
			Seeders:     12,
			Leechers:    3,
			PublishedAt: time.Unix(2000, 0).UTC(),
			MagnetURI:   "magnet:?xt=urn:btih:fedcba9876543210fedcba9876543210fedcba98",
		},
		domain.SourceObservation{
			Source:      "postgres-fixture",
			SourceID:    "fixture-1",
			SourceURL:   "https://example.invalid/torrent/1",
			Title:       "Example PostgreSQL Torrent",
			Category:    "software",
			SizeBytes:   2048,
			Seeders:     12,
			Leechers:    3,
			PublishedAt: time.Unix(2000, 0).UTC(),
			ObservedAt:  time.Unix(3000, 0).UTC(),
			RawJSON:     `{"hello":"postgres"}`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	results, err := repo.Search(ctx, SearchFilter{Query: "Example", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(results), 1; got != want {
		t.Fatalf("results len = %d, want %d", got, want)
	}

	details, err := repo.Get(ctx, infohash)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(details.Sources), 1; got != want {
		t.Fatalf("sources len = %d, want %d", got, want)
	}

	stats, err := repo.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := stats.TorrentCount, int64(1); got != want {
		t.Fatalf("torrent count = %d, want %d", got, want)
	}

	if err := repo.SetSourceState(ctx, "1337x", "movies", `{"window_end_page":20}`); err != nil {
		t.Fatal(err)
	}
	state, err := repo.GetSourceState(ctx, "1337x", "movies")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := state, `{"window_end_page":20}`; got != want {
		t.Fatalf("source state = %q, want %q", got, want)
	}
}
