package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/esaiaswestberg/magnet-atlas/internal/domain"
)

func TestSQLiteRepositoryRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "magnet-atlas.db")
	repo, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	infohash := "0123456789abcdef0123456789abcdef01234567"
	err = repo.Upsert(context.Background(),
		domain.Torrent{
			InfoHash:    infohash,
			Title:       "The Rookie S08E17 Example Linux ISO",
			Category:    "software",
			SizeBytes:   1024,
			Seeders:     7,
			Leechers:    1,
			PublishedAt: time.Unix(1000, 0).UTC(),
			MagnetURI:   "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
			ExtraText:   []string{"The rookie follows the detective."},
		},
		domain.SourceObservation{
			Source:      "sample",
			SourceID:    "fixture-1",
			SourceURL:   "https://example.invalid/torrent/1",
			Title:       "The Rookie S08E17 Example Linux ISO",
			Category:    "software",
			SizeBytes:   1024,
			Seeders:     7,
			Leechers:    1,
			PublishedAt: time.Unix(1000, 0).UTC(),
			ObservedAt:  time.Unix(2000, 0).UTC(),
			RawJSON:     `{"hello":"world"}`,
			ExtraText:   []string{"The rookie follows the detective."},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	results, err := repo.Search(context.Background(), SearchFilter{Query: "rookie", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(results), 1; got != want {
		t.Fatalf("results len = %d, want %d", got, want)
	}
	if got, want := results[0].InfoHash, infohash; got != want {
		t.Fatalf("result infohash = %q, want %q", got, want)
	}

	seasonResults, err := repo.Search(context.Background(), SearchFilter{Query: "The Rookie S08", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(seasonResults), 1; got != want {
		t.Fatalf("season results len = %d, want %d", got, want)
	}
	if got, want := seasonResults[0].InfoHash, infohash; got != want {
		t.Fatalf("season result infohash = %q, want %q", got, want)
	}

	details, err := repo.Get(context.Background(), infohash)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(details.Sources), 1; got != want {
		t.Fatalf("sources len = %d, want %d", got, want)
	}
	stats, err := repo.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := stats.TorrentCount, int64(1); got != want {
		t.Fatalf("torrent count = %d, want %d", got, want)
	}

	if err := repo.SetSourceState(context.Background(), "1337x", "movies", `{"window_end_page":20}`); err != nil {
		t.Fatal(err)
	}
	state, err := repo.GetSourceState(context.Background(), "1337x", "movies")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := state, `{"window_end_page":20}`; got != want {
		t.Fatalf("source state = %q, want %q", got, want)
	}
}
