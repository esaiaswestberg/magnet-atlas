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
		extraText       []string
		want            string
	}{
		{title: "Example Torrent", category: "software", want: "example torrent software"},
		{title: "Example Torrent", category: "", want: "example torrent"},
		{title: "", category: "software", want: "software"},
		{title: " ", category: " ", want: ""},
		{title: "the.rookie.s08e17.720p.hdtv.x264", category: "TV", extraText: []string{"The rookie follows the detective."}, want: "the rookie s08e17 720p hdtv x264 tv the rookie follows the detective"},
	}

	for _, tc := range tests {
		if got := buildSearchText(tc.title, tc.category, tc.extraText); got != tc.want {
			t.Fatalf("buildSearchText(%q, %q, %v) = %q, want %q", tc.title, tc.category, tc.extraText, got, tc.want)
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
			ExtraText:   []string{"The rookie follows the detective."},
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
			ExtraText:   []string{"The rookie follows the detective."},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	results, err := repo.Search(ctx, SearchFilter{Query: "rookie", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(results), 1; got != want {
		t.Fatalf("results len = %d, want %d", got, want)
	}
	if got, want := results[0].InfoHash, infohash; got != want {
		t.Fatalf("result infohash = %q, want %q", got, want)
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
