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
	"github.com/esaiaswestberg/magnet-atlas/internal/domain"
	"github.com/esaiaswestberg/magnet-atlas/internal/store"
)

func Test1337XAdapterFetchFirstWindow(t *testing.T) {
	adapter, transport := newTest1337XAdapter(t, map[string]string{
		"/movie-library/1/": `<html><body>
<a href="/torrent/1234/example-torrent/">Example Torrent 2025 1080p</a>
</body></html>`,
		"/movie-library/2/": `<html><body>
<a href="/torrent/5678/second-torrent/">Second Torrent 2025 720p</a>
</body></html>`,
		"/movie-library/3/": `<html><body></body></html>`,
		"/torrent/1234/example-torrent/": `<html><body>
<h1>Example Torrent 2025 1080p</h1>
<a href="magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567&dn=Example+Torrent">Magnet Download</a>
Description The rookie follows the detective through another case.
Category Movies
Total size 1.5 GB
Seeders 10
Leechers 2
Date uploaded Jan. 19th '24
Infohash : 0123456789ABCDEF0123456789ABCDEF01234567
</body></html>`,
		"/torrent/5678/second-torrent/": `<html><body>
<h1>Second Torrent 2025 720p</h1>
<a href="magnet:?xt=urn:btih:89abcdef0123456789abcdef0123456789abcdef&dn=Second+Torrent">Magnet Download</a>
Category Movies
Total size 700 MB
Seeders 12
Leechers 1
Date uploaded Jan. 20th '24
Infohash : 89ABCDEF0123456789ABCDEF0123456789ABCDEF
</body></html>`,
	})

	result, err := adapter.Fetch(context.Background(), newTestRepository())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(result.Torrents), 2; got != want {
		t.Fatalf("torrents len = %d, want %d", got, want)
	}
	if got, want := len(result.Observations), 2; got != want {
		t.Fatalf("observations len = %d, want %d", got, want)
	}
	if got, want := result.Torrents[0].ExtraText, []string{"The rookie follows the detective through another case."}; !equalStrings(got, want) {
		t.Fatalf("torrent extra text = %#v, want %#v", got, want)
	}
	if got, want := result.Observations[0].ExtraText, []string{"The rookie follows the detective through another case."}; !equalStrings(got, want) {
		t.Fatalf("observation extra text = %#v, want %#v", got, want)
	}
	state, ok := result.State["movies"]
	if !ok {
		t.Fatalf("missing movies state")
	}
	if got, want := state, `{"window_end_page":2}`; got != want {
		t.Fatalf("state = %s, want %s", got, want)
	}
	if got := transport.paths(); len(got) == 0 || got[0] != "/movie-library/1/" {
		t.Fatalf("unexpected request paths: %v", got)
	}
}

func Test1337XAdapterResumesPerSection(t *testing.T) {
	adapter, transport := newTest1337XAdapter(t, map[string]string{
		"/movie-library/1/": `<html><body>
<a href="/torrent/2001/new-one/">New One</a>
</body></html>`,
		"/movie-library/2/": `<html><body>
<a href="/torrent/2002/new-two/">New Two</a>
</body></html>`,
		"/movie-library/3/": `<html><body>
<a href="/torrent/1001/old-one/">Old One</a>
</body></html>`,
		"/movie-library/5/": `<html><body>
<a href="/torrent/3001/next-one/">Next One</a>
</body></html>`,
		"/movie-library/6/": `<html><body>
<a href="/torrent/3002/next-two/">Next Two</a>
</body></html>`,
		"/movie-library/7/": `<html><body></body></html>`,
		"/torrent/2001/new-one/": `<html><body>
Category Movies
Total size 1 GB
Seeders 5
Leechers 0
Date uploaded Jan. 21st '24
Infohash : 2000000000000000000000000000000000000001
<a href="magnet:?xt=urn:btih:2000000000000000000000000000000000000001&dn=New+One">Magnet</a>
</body></html>`,
		"/torrent/2002/new-two/": `<html><body>
Category Movies
Total size 2 GB
Seeders 6
Leechers 1
Date uploaded Jan. 22nd '24
Infohash : 2000000000000000000000000000000000000002
<a href="magnet:?xt=urn:btih:2000000000000000000000000000000000000002&dn=New+Two">Magnet</a>
</body></html>`,
		"/torrent/1001/old-one/": `<html><body>
Category Movies
Total size 1 GB
Seeders 100
Leechers 1
Date uploaded Jan. 1st '24
Infohash : 1000000000000000000000000000000000000001
<a href="magnet:?xt=urn:btih:1000000000000000000000000000000000000001&dn=Old+One">Magnet</a>
</body></html>`,
		"/torrent/3001/next-one/": `<html><body>
Category Movies
Total size 1 GB
Seeders 1
Leechers 0
Date uploaded Jan. 23rd '24
Infohash : 3000000000000000000000000000000000000001
<a href="magnet:?xt=urn:btih:3000000000000000000000000000000000000001&dn=Next+One">Magnet</a>
</body></html>`,
		"/torrent/3002/next-two/": `<html><body>
Category Movies
Total size 1 GB
Seeders 2
Leechers 0
Date uploaded Jan. 24th '24
Infohash : 3000000000000000000000000000000000000002
<a href="magnet:?xt=urn:btih:3000000000000000000000000000000000000002&dn=Next+Two">Magnet</a>
</body></html>`,
	})

	repo := newTestRepository()
	repo.known["1000000000000000000000000000000000000001"] = true
	repo.states["1337x|movies"] = `{"window_end_page":2}`

	result, err := adapter.Fetch(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := len(result.Torrents), 4; got != want {
		t.Fatalf("torrents len = %d, want %d", got, want)
	}
	if got, want := result.State["movies"], `{"window_end_page":6}`; got != want {
		t.Fatalf("state = %s, want %s", got, want)
	}
	paths := transport.paths()
	if containsPath(paths, "/movie-library/4/") {
		t.Fatalf("unexpected page 4 fetch: %v", paths)
	}
	if !containsPath(paths, "/movie-library/5/") || !containsPath(paths, "/movie-library/6/") {
		t.Fatalf("missing resumed pages: %v", paths)
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

func TestNew1337XAdapterUsesDefaultBaseURL(t *testing.T) {
	adapter, err := New1337XAdapter(config.SourceConfig{
		Name:    "1337x",
		Type:    "1337x",
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := adapter.baseURL.String(), default1337XBaseURL; got != want {
		t.Fatalf("base url = %q, want %q", got, want)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newTest1337XAdapter(t *testing.T, routes map[string]string) (*x1337Adapter, *requestTransport) {
	t.Helper()
	baseURL, err := url.Parse("https://example.test")
	if err != nil {
		t.Fatal(err)
	}
	transport := &requestTransport{routes: routes}
	client := &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			transport.record(req.URL.Path)
			body, ok := routes[req.URL.Path]
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
	return &x1337Adapter{
		name:        "1337x",
		baseURL:     baseURL,
		client:      client,
		concurrency: 2,
		pageWindow:  3,
		seeds:       []crawlSeed{{name: "movies", path: "/movie-library"}},
	}, transport
}

type requestTransport struct {
	mu     sync.Mutex
	pathsV []string
	routes map[string]string
}

func (t *requestTransport) record(path string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pathsV = append(t.pathsV, path)
}

func (t *requestTransport) paths() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, len(t.pathsV))
	copy(out, t.pathsV)
	return out
}

type testRepository struct {
	known  map[string]bool
	states map[string]string
}

func newTestRepository() *testRepository {
	return &testRepository{
		known:  make(map[string]bool),
		states: make(map[string]string),
	}
}

func (r *testRepository) Close() error { return nil }
func (r *testRepository) Upsert(context.Context, domain.Torrent, domain.SourceObservation) error {
	return nil
}
func (r *testRepository) Search(context.Context, store.SearchFilter) ([]domain.Torrent, error) {
	return nil, nil
}
func (r *testRepository) Get(context.Context, string) (store.Details, error) {
	return store.Details{}, nil
}
func (r *testRepository) HasInfohash(_ context.Context, infohash string) (bool, error) {
	return r.known[infohash], nil
}
func (r *testRepository) ListSources(context.Context) ([]string, error)    { return nil, nil }
func (r *testRepository) ListCategories(context.Context) ([]string, error) { return nil, nil }
func (r *testRepository) Stats(context.Context) (store.Stats, error)       { return store.Stats{}, nil }
func (r *testRepository) GetSourceState(_ context.Context, source, section string) (string, error) {
	state, ok := r.states[source+"|"+section]
	if !ok {
		return "", store.ErrSourceStateNotFound
	}
	return state, nil
}
func (r *testRepository) SetSourceState(_ context.Context, source, section, state string) error {
	r.states[source+"|"+section] = state
	return nil
}

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}
	return false
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
