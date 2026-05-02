package source

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/esaiaswestberg/magnet-atlas/internal/config"
)

func Test1337XAdapterFetch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/movie-library/1/", "/cat/Movies/1/":
			_, _ = w.Write([]byte(`<html><body>
<a href="/torrent/1234/example-torrent/">Example Torrent 2025 1080p</a>
</body></html>`))
		case "/movie-library/2/", "/cat/Movies/2/":
			_, _ = w.Write([]byte(`<html><body></body></html>`))
		case "/torrent/1234/example-torrent/":
			_, _ = w.Write([]byte(`<html><body>
<h1>Example Torrent 2025 1080p</h1>
<a href="magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567&dn=Example+Torrent">Magnet Download</a>
Category Movies
Total size 1.5 GB
Seeders 10
Leechers 2
Date uploaded Jan. 19th '24
Infohash : 0123456789ABCDEF0123456789ABCDEF01234567
</body></html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter, err := New1337XAdapter(config.SourceConfig{
		Name:        "1337x",
		Type:        "1337x",
		Enabled:     true,
		BaseURL:     server.URL,
		Categories:  []string{"movies"},
		Concurrency: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	torrents, obs, err := adapter.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(torrents), 1; got != want {
		t.Fatalf("torrents len = %d, want %d", got, want)
	}
	if got, want := len(obs), 1; got != want {
		t.Fatalf("observations len = %d, want %d", got, want)
	}

	torrent := torrents[0]
	if got, want := torrent.InfoHash, "0123456789ABCDEF0123456789ABCDEF01234567"; got != want {
		t.Fatalf("infohash = %q, want %q", got, want)
	}
	if got, want := torrent.Title, "Example Torrent 2025 1080p"; got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
	if got, want := torrent.Category, "Movies"; got != want {
		t.Fatalf("category = %q, want %q", got, want)
	}
	if got, want := torrent.SizeBytes, int64(1610612736); got != want {
		t.Fatalf("size bytes = %d, want %d", got, want)
	}
	if got, want := torrent.Seeders, 10; got != want {
		t.Fatalf("seeders = %d, want %d", got, want)
	}
	if got, want := torrent.Leechers, 2; got != want {
		t.Fatalf("leechers = %d, want %d", got, want)
	}
	if got, want := torrent.MagnetURI, "magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567&dn=Example+Torrent"; got != want {
		t.Fatalf("magnet = %q, want %q", got, want)
	}
	if got, want := torrent.PublishedAt.Year(), 2024; got != want {
		t.Fatalf("published year = %d, want %d", got, want)
	}

	if got, want := obs[0].Source, "1337x"; got != want {
		t.Fatalf("source = %q, want %q", got, want)
	}
	if got, want := obs[0].SourceID, "1234"; got != want {
		t.Fatalf("source id = %q, want %q", got, want)
	}
	if !strings.HasPrefix(obs[0].SourceURL, server.URL+"/torrent/1234/example-torrent/") {
		t.Fatalf("source url = %q", obs[0].SourceURL)
	}
	if got, want := obs[0].ObservedAt.IsZero(), false; got != want {
		t.Fatalf("observed at zero = %v, want %v", got, want)
	}
}

func Test1337XAdapterDedupesRepeatedPages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/movie-library/1/", "/movie-library/2/", "/cat/Movies/1/", "/cat/Movies/2/":
			_, _ = w.Write([]byte(`<html><body>
<a href="/torrent/1234/example-torrent/">Example Torrent 2025 1080p</a>
</body></html>`))
		case "/movie-library/3/", "/cat/Movies/3/":
			_, _ = w.Write([]byte(`<html><body></body></html>`))
		case "/torrent/1234/example-torrent/":
			_, _ = w.Write([]byte(`<html><body>
Category Movies
Total size 1.5 GB
Seeders 10
Leechers 2
Date uploaded Jan. 19th '24
Infohash : 0123456789ABCDEF0123456789ABCDEF01234567
<a href="magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567&dn=Example+Torrent">Magnet Download</a>
</body></html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter, err := New1337XAdapter(config.SourceConfig{
		Name:       "1337x",
		Type:       "1337x",
		Enabled:    true,
		BaseURL:    server.URL,
		Categories: []string{"movies"},
	})
	if err != nil {
		t.Fatal(err)
	}

	torrents, obs, err := adapter.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(torrents), 1; got != want {
		t.Fatalf("torrents len = %d, want %d", got, want)
	}
	if got, want := len(obs), 1; got != want {
		t.Fatalf("observations len = %d, want %d", got, want)
	}
}

func TestParse1337XDate(t *testing.T) {
	ts, err := parse1337XDate("Jan. 19th '24")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ts.Year(), 2024; got != want {
		t.Fatalf("year = %d, want %d", got, want)
	}
	if got, want := ts.Month(), time.January; got != want {
		t.Fatalf("month = %v, want %v", got, want)
	}
}
