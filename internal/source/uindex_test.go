package source

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/esaiaswestberg/magnet-atlas/internal/config"
)

func TestUIndexAdapterFetchFirstWindow(t *testing.T) {
	adapter, transport := newTestUIndexAdapter(t, map[string]string{
		"/search.php?p=1": `<html><body>
<a href="/details.php?id=1001">Example Torrent 2026 1080p</a>
</body></html>`,
		"/search.php?p=2": `<html><body>
<a href="/details.php?id=1002">Second Torrent 2026 720p</a>
</body></html>`,
		"/search.php?p=3": `<html><body></body></html>`,
		"/details.php?id=1001": `<html><body>
<h1>Example Torrent 2026 1080p</h1>
Category Movies
Size 1.5 GB
Added 1 day ago (2026-01-13 19:31:28)
Share ratio: 10 seeders, 1 leechers
Info Hash 0123456789ABCDEF0123456789ABCDEF01234567
<a href="magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567&dn=Example+Torrent">Download Torrent</a>
</body></html>`,
		"/details.php?id=1002": `<html><body>
<h1>Second Torrent 2026 720p</h1>
Category Movies
Size 700 MB
Added 2 days ago (2026-01-12 08:09:10)
Share ratio: 12 seeders, 2 leechers
Info Hash 89abcdef0123456789abcdef0123456789abcdef
<a href="magnet:?xt=urn:btih:89abcdef0123456789abcdef0123456789abcdef&dn=Second+Torrent">Download Torrent</a>
</body></html>`,
	})

	result, err := adapter.Fetch(context.Background(), newTestRepository())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(result.Torrents), 2; got != want {
		t.Fatalf("torrents len = %d, want %d", got, want)
	}
	if got, want := result.Torrents[0].InfoHash, "0123456789ABCDEF0123456789ABCDEF01234567"; got != want {
		t.Fatalf("infohash = %q, want %q", got, want)
	}
	if got, want := result.Torrents[0].Category, "Movies"; got != want {
		t.Fatalf("category = %q, want %q", got, want)
	}
	if got, want := result.Torrents[0].SizeBytes, int64(1610612736); got != want {
		t.Fatalf("size bytes = %d, want %d", got, want)
	}
	if got, want := result.Torrents[0].Seeders, 10; got != want {
		t.Fatalf("seeders = %d, want %d", got, want)
	}
	if got, want := result.Torrents[0].Leechers, 1; got != want {
		t.Fatalf("leechers = %d, want %d", got, want)
	}
	if got, want := result.Torrents[0].PublishedAt.UTC(), time.Date(2026, time.January, 13, 19, 31, 28, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("published_at = %s, want %s", got, want)
	}
	if got, want := result.State["latest"], `{"window_end_page":2}`; got != want {
		t.Fatalf("state = %s, want %s", got, want)
	}
	uris := transport.uris()
	if len(uris) == 0 || uris[0] != "/search.php?p=1" {
		t.Fatalf("unexpected request uris: %v", uris)
	}
}

func TestUIndexAdapterFetchCategoriesIndependently(t *testing.T) {
	adapter, transport := newTestUIndexAdapter(t, map[string]string{
		"/search.php?p=1": `<html><body></body></html>`,
		"/search.php?c=1&p=1": `<html><body>
<a href="/details.php?id=2001">Movie Torrent</a>
</body></html>`,
		"/search.php?c=2&p=1": `<html><body>
<a href="/details.php?id=2002">TV Torrent</a>
</body></html>`,
		"/search.php?c=1&p=2": `<html><body></body></html>`,
		"/search.php?c=2&p=2": `<html><body></body></html>`,
		"/details.php?id=2001": `<html><body>
<h1>Movie Torrent</h1>
Category Movies
Size 2 GB
Added 2026-01-14 01:02:03
Seeders 5
Leechers 0
Info Hash 2000000000000000000000000000000000000001
</body></html>`,
		"/details.php?id=2002": `<html><body>
<h1>TV Torrent</h1>
Category TV
Size 3 GB
Added 2026-01-14 04:05:06
Seeders 7
Leechers 1
Info Hash 2000000000000000000000000000000000000002
</body></html>`,
	})

	adapter.sections = []crawlSeed{
		{name: "latest", path: "/search.php"},
		{name: "movies", path: "/search.php?c=1"},
		{name: "tv", path: "/search.php?c=2"},
	}

	repo := newTestRepository()
	result, err := adapter.Fetch(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(result.Torrents), 2; got != want {
		t.Fatalf("torrents len = %d, want %d", got, want)
	}
	if got, want := result.State["movies"], `{"window_end_page":1}`; got != want {
		t.Fatalf("movies state = %s, want %s", got, want)
	}
	if got, want := result.State["tv"], `{"window_end_page":1}`; got != want {
		t.Fatalf("tv state = %s, want %s", got, want)
	}
	if _, ok := result.State["latest"]; ok {
		t.Fatalf("expected no latest state for empty latest crawl")
	}
	if !containsURI(transport.uris(), "/search.php?c=1&p=1") || !containsURI(transport.uris(), "/search.php?c=2&p=1") {
		t.Fatalf("missing category requests: %v", transport.uris())
	}
}

func TestNewUIndexAdapterUsesDefaultBaseURL(t *testing.T) {
	adapter, err := NewUIndexAdapter(config.SourceConfig{
		Name:    "uindex",
		Type:    "uindex",
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := adapter.baseURL.String(), defaultUIndexBaseURL; got != want {
		t.Fatalf("base url = %q, want %q", got, want)
	}
}

type uindexRequestTransport struct {
	mu     sync.Mutex
	urisV  []string
	routes map[string]string
}

func (t *uindexRequestTransport) record(uri string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.urisV = append(t.urisV, uri)
}

func (t *uindexRequestTransport) uris() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, len(t.urisV))
	copy(out, t.urisV)
	return out
}

func newTestUIndexAdapter(t *testing.T, routes map[string]string) (*uindexAdapter, *uindexRequestTransport) {
	t.Helper()
	baseURL, err := url.Parse("https://example.test")
	if err != nil {
		t.Fatal(err)
	}
	transport := &uindexRequestTransport{routes: routes}
	client := &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			transport.record(req.URL.RequestURI())
			body, ok := routes[req.URL.RequestURI()]
			if !ok {
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("not found")),
					Request:    req,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    req,
			}, nil
		}),
	}
	return &uindexAdapter{
		name:        "uindex",
		baseURL:     baseURL,
		client:      client,
		concurrency: 2,
		pageWindow:  2,
		sections:    []crawlSeed{{name: "latest", path: "/search.php"}},
	}, transport
}

func containsURI(uris []string, want string) bool {
	for _, uri := range uris {
		if uri == want {
			return true
		}
	}
	return false
}
