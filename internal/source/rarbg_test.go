package source

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/esaiaswestberg/magnet-atlas/internal/config"
)

func TestNewRarbgAdapterUsesTSDefaults(t *testing.T) {
	adapter, err := NewRarbgAdapter(config.SourceConfig{
		Name:            "rarbg",
		Type:            "rarbg",
		Enabled:         true,
		BaseURL:         "https://example.test",
		FlareSolverrURL: "http://localhost:8191/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := adapter.sections, []string{"movies", "tv", "anime"}; !equalStrings(got, want) {
		t.Fatalf("sections = %v, want %v", got, want)
	}
	if got, want := adapter.concurrency, 4; got != want {
		t.Fatalf("concurrency = %d, want %d", got, want)
	}
	if got, want := adapter.pageWindow, 1; got != want {
		t.Fatalf("page window = %d, want %d", got, want)
	}
	if got, want := adapter.requestDelay, time.Second; got != want {
		t.Fatalf("request delay = %s, want %s", got, want)
	}
	if got, want := adapter.backoffDelay, 750*time.Millisecond; got != want {
		t.Fatalf("backoff delay = %s, want %s", got, want)
	}
	if got, want := adapter.requestAttempts, 3; got != want {
		t.Fatalf("request attempts = %d, want %d", got, want)
	}
}

func TestRarbgPageURLBuildsSectionPages(t *testing.T) {
	adapter, _ := newTestRarbgAdapter(t, map[string]string{})

	if got, want := adapter.pageURL("movies", 1), "https://example.test/movies/1/"; got != want {
		t.Fatalf("page 1 url = %q, want %q", got, want)
	}
	if got, want := adapter.pageURL("movies", 2), "https://example.test/movies/2/"; got != want {
		t.Fatalf("page 2 url = %q, want %q", got, want)
	}
}

func TestRarbgAdapterFetch(t *testing.T) {
	adapter, transport := newTestRarbgAdapter(t, map[string]string{
		"https://example.test/movies/1/": `<html><body>
<a href="/torrent/the-departed-2006-1080p-ds4k-bluray-x265-10-bit-hdr-aac-5-1-wesley-6634355.html">The Departed (2006) (1080p DS4K BluRay x265 10-bit HDR AAC 5.1) [WeSLeY]</a>
</body></html>`,
		"https://example.test/torrent/the-departed-2006-1080p-ds4k-bluray-x265-10-bit-hdr-aac-5-1-wesley-6634355.html": `<html><body>
<h1>The Departed (2006) (1080p DS4K BluRay x265 10-bit HDR AAC 5.1) [WeSLeY]</h1>
<a href="magnet:?xt=urn:btih:76ED6A73E91B8A14035CA3B1F05E9F885598F0BB&amp;dn=The+Departed+%282006%29+%281080p+DS4K+BluRay+x265+10-bit+HDR+AAC+5.1%29+%5BWeSLeY%5D">Download</a>
<a href="magnet:?xt=urn:btih:76ED6A73E91B8A14035CA3B1F05E9F885598F0BB&amp;dn=The+Departed+%282006%29+%281080p+DS4K+BluRay+x265+10-bit+HDR+AAC+5.1%29+%5BWeSLeY%5D">Mirror</a>
</body></html>`,
	})
	adapter.sections = []string{"movies"}

	result, err := adapter.Fetch(context.Background(), newTestRepository())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(result.Torrents), 1; got != want {
		t.Fatalf("torrents len = %d, want %d", got, want)
	}
	if got, want := len(result.Observations), 1; got != want {
		t.Fatalf("observations len = %d, want %d", got, want)
	}
	if got, want := result.Torrents[0].InfoHash, "76ED6A73E91B8A14035CA3B1F05E9F885598F0BB"; got != want {
		t.Fatalf("infohash = %q, want %q", got, want)
	}
	if got, want := result.Torrents[0].Title, "The Departed (2006) (1080p DS4K BluRay x265 10-bit HDR AAC 5.1) [WeSLeY]"; got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
	if got, want := result.Torrents[0].Category, "movies"; got != want {
		t.Fatalf("category = %q, want %q", got, want)
	}
	if got, want := result.Torrents[0].MagnetURI, "magnet:?xt=urn:btih:76ED6A73E91B8A14035CA3B1F05E9F885598F0BB&dn=The+Departed+%282006%29+%281080p+DS4K+BluRay+x265+10-bit+HDR+AAC+5.1%29+%5BWeSLeY%5D"; got != want {
		t.Fatalf("magnet uri = %q, want %q", got, want)
	}
	if got, want := result.Torrents[0].DownloadURL, result.Torrents[0].MagnetURI; got != want {
		t.Fatalf("download url = %q, want %q", got, want)
	}
	if got, want := result.State, map[string]string{}; len(got) != len(want) {
		t.Fatalf("state = %v, want empty map", got)
	}
	if got, want := transport.targets(), []string{
		"https://example.test/movies/1/",
		"https://example.test/torrent/the-departed-2006-1080p-ds4k-bluray-x265-10-bit-hdr-aac-5-1-wesley-6634355.html",
	}; !equalStrings(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestRarbgRequestDelayUsesHook(t *testing.T) {
	adapter, _ := newTestRarbgAdapter(t, map[string]string{
		"https://example.test/movies/1/": `<html><body></body></html>`,
	})
	var sleeps []time.Duration
	adapter.requestDelay = 250 * time.Millisecond
	adapter.sleep = func(d time.Duration) {
		sleeps = append(sleeps, d)
	}

	if _, err := adapter.get(context.Background(), "https://example.test/movies/1/", true); err != nil {
		t.Fatal(err)
	}
	if got, want := sleeps, []time.Duration{250 * time.Millisecond}; !equalDurations(got, want) {
		t.Fatalf("sleep calls = %v, want %v", got, want)
	}
}

func TestRarbgGetRetriesTransientErrors(t *testing.T) {
	adapter, _ := newTestRarbgAdapter(t, map[string]string{})
	var sleeps []time.Duration
	adapter.sleep = func(d time.Duration) {
		sleeps = append(sleeps, d)
	}
	var attempts int
	adapter.solver.httpClient = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/v1" {
				t.Fatalf("unexpected solver path %q", req.URL.Path)
			}
			var payload flareSolverrRequest
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			attempts++
			if attempts == 1 {
				body := `{"status":"error","message":"Error solving the challenge. Message: unknown error: net::ERR_CONNECTION_RESET"}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(body)),
					Request:    req,
				}, nil
			}
			response := flareSolverrResponse{
				Status: "ok",
				Solution: flareSolverrResult{
					Status:    http.StatusOK,
					Response:  `<html><body>ok</body></html>`,
					UserAgent: "test-agent",
				},
			}
			b, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(string(b))),
				Request:    req,
			}, nil
		}),
	}

	body, err := adapter.get(context.Background(), "https://example.test/movies/1/", false)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := body, `<html><body>ok</body></html>`; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if got, want := attempts, 2; got != want {
		t.Fatalf("attempts = %d, want %d", got, want)
	}
	if got, want := sleeps, []time.Duration{750 * time.Millisecond}; !equalDurations(got, want) {
		t.Fatalf("sleep calls = %v, want %v", got, want)
	}
}

func TestRarbgGetExhaustsTransientErrors(t *testing.T) {
	adapter, _ := newTestRarbgAdapter(t, map[string]string{})
	var sleeps []time.Duration
	adapter.sleep = func(d time.Duration) {
		sleeps = append(sleeps, d)
	}
	adapter.solver.httpClient = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			body := `{"status":"error","message":"Error solving the challenge. Message: unknown error: net::ERR_CONNECTION_RESET"}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    req,
			}, nil
		}),
	}

	if _, err := adapter.get(context.Background(), "https://example.test/movies/1/", false); err == nil {
		t.Fatal("expected transient solver failures to exhaust retries")
	}
	if got, want := sleeps, []time.Duration{750 * time.Millisecond, 1500 * time.Millisecond}; !equalDurations(got, want) {
		t.Fatalf("sleep calls = %v, want %v", got, want)
	}
}

type rarbgSolverTransport struct {
	mu       sync.Mutex
	targetsV []string
	routes   map[string]string
}

func (t *rarbgSolverTransport) record(target string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.targetsV = append(t.targetsV, target)
}

func (t *rarbgSolverTransport) targets() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, len(t.targetsV))
	copy(out, t.targetsV)
	return out
}

func newTestRarbgAdapter(t *testing.T, routes map[string]string) (*rarbgAdapter, *rarbgSolverTransport) {
	t.Helper()
	baseURL, err := url.Parse("https://example.test")
	if err != nil {
		t.Fatal(err)
	}
	solverTransport := &rarbgSolverTransport{routes: routes}
	solverClient := &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/v1" {
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("not found")),
					Request:    req,
				}, nil
			}

			var payload flareSolverrRequest
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			solverTransport.record(payload.URL)

			body, ok := solverTransport.routes[payload.URL]
			if !ok {
				body = ""
			}
			response := flareSolverrResponse{
				Status: "ok",
				Solution: flareSolverrResult{
					Status:    http.StatusOK,
					Response:  body,
					UserAgent: "test-agent",
				},
			}
			b, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(string(b))),
				Request:    req,
			}, nil
		}),
	}

	return &rarbgAdapter{
		name:            "rarbg",
		baseURL:         baseURL,
		solver:          &flareSolverrClient{endpoint: mustParseURL(t, "https://solver.test/v1"), httpClient: solverClient, timeout: defaultFlareSolverrTimeout},
		sections:        []string{"movies"},
		concurrency:     2,
		pageWindow:      1,
		requestDelay:    defaultRARBGRequestDelay,
		backoffDelay:    defaultRARBGBackoffDelay,
		requestAttempts: defaultRARBGRequestAttempts,
		sleep:           time.Sleep,
	}, solverTransport
}

func equalDurations(got, want []time.Duration) bool {
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

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
