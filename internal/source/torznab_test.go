package source

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/esaiaswestberg/magnet-atlas/internal/config"
)

func TestNewTorznabAdapterUsesDefaults(t *testing.T) {
	adapter, err := NewTorznabAdapter(config.SourceConfig{
		Name:       "bitmagnet",
		Type:       "torznab",
		Enabled:    true,
		BaseURL:    "https://example.test/api",
		Categories: []string{"movies", "tv"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := adapter.pageWindow, 20; got != want {
		t.Fatalf("page window = %d, want %d", got, want)
	}
	if got, want := adapter.pageSize, 100; got != want {
		t.Fatalf("page size = %d, want %d", got, want)
	}
	if got, want := adapter.categoryIDs, []int{2000, 5000}; !reflect.DeepEqual(got, want) {
		t.Fatalf("category ids = %#v, want %#v", got, want)
	}
	if got, want := adapter.baseURL.String(), "https://example.test/api"; got != want {
		t.Fatalf("base url = %q, want %q", got, want)
	}
}

func TestNewTorznabAdapterAcceptsExactTorznabEndpointBaseURL(t *testing.T) {
	adapter, err := NewTorznabAdapter(config.SourceConfig{
		Name:    "generic",
		Type:    "torznab",
		Enabled: true,
		BaseURL: "https://example.test/torznab",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := adapter.baseURL.String(), "https://example.test/torznab"; got != want {
		t.Fatalf("base url = %q, want %q", got, want)
	}
}

func TestNewTorznabAdapterRejectsRootBaseURL(t *testing.T) {
	if _, err := NewTorznabAdapter(config.SourceConfig{
		Name:    "generic",
		Type:    "torznab",
		Enabled: true,
		BaseURL: "https://example.test",
	}); err == nil {
		t.Fatal("expected root base url to fail")
	}
}

func TestFactoryBuildsTorznabAdapter(t *testing.T) {
	loaded, err := NewFactory()(config.SourceConfig{
		Name:    "generic",
		Type:    "torznab",
		Enabled: true,
		BaseURL: "https://example.test/api",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.(*torznabAdapter); !ok {
		t.Fatalf("expected *torznabAdapter, got %T", loaded)
	}
}

func TestTorznabAdapterFetch(t *testing.T) {
	adapter, err := NewTorznabAdapter(config.SourceConfig{
		Name:        "bitmagnet",
		Type:        "torznab",
		Enabled:     true,
		BaseURL:     "https://example.test/api",
		SearchQuery: "ubuntu",
		Categories:  []string{"movies", "tv"},
		PageWindow:  2,
		PageSize:    2,
	})
	if err != nil {
		t.Fatal(err)
	}

	var seenOffsets []string
	adapter.client = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/api" {
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("not found")),
					Request:    req,
				}, nil
			}
			q := req.URL.Query()
			seenOffsets = append(seenOffsets, q.Get("offset"))
			if got, want := req.URL.Path, "/api"; got != want {
				t.Fatalf("path = %q, want %q", got, want)
			}
			if got, want := q.Get("q"), "ubuntu"; got != want {
				t.Fatalf("query = %q, want %q", got, want)
			}
			if got, want := q.Get("cat"), "2000,5000"; got != want {
				t.Fatalf("cat = %q, want %q", got, want)
			}
			if got, want := q.Get("limit"), "2"; got != want {
				t.Fatalf("limit = %q, want %q", got, want)
			}

			var body string
			switch q.Get("offset") {
			case "0":
				body = torznabFeedXML(
					torznabItemXML(
						"0123456789abcdef0123456789abcdef01234567",
						"Example Linux ISO",
						"Movies",
						"Tue, 28 Apr 2026 09:24:13 +0000",
						"1073741824",
						"42",
						"3",
						"magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Example+Linux+ISO",
						"https://example.invalid/download/1",
					),
					torznabItemXML(
						"89abcdef0123456789abcdef0123456789abcdef",
						"Example Episode",
						"TV",
						"Tue, 28 Apr 2026 10:24:13 +0000",
						"2048",
						"5",
						"1",
						"magnet:?xt=urn:btih:89abcdef0123456789abcdef0123456789abcdef&dn=Example+Episode",
						"",
					),
				)
			case "2":
				body = torznabFeedXML(
					torznabItemXML(
						"fedcba9876543210fedcba9876543210fedcba98",
						"Example Archive",
						"Movies",
						"Tue, 28 Apr 2026 11:24:13 +0000",
						"4096",
						"7",
						"0",
						"magnet:?xt=urn:btih:fedcba9876543210fedcba9876543210fedcba98&dn=Example+Archive",
						"",
					),
				)
			default:
				body = torznabFeedXML()
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    req,
			}, nil
		}),
	}

	repo := newTestRepository()
	result, err := adapter.Fetch(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(result.Torrents), 3; got != want {
		t.Fatalf("torrents len = %d, want %d", got, want)
	}
	if got, want := len(result.Observations), 3; got != want {
		t.Fatalf("observations len = %d, want %d", got, want)
	}
	if got, want := result.Torrents[0].InfoHash, "0123456789ABCDEF0123456789ABCDEF01234567"; got != want {
		t.Fatalf("infohash = %q, want %q", got, want)
	}
	if got, want := result.Torrents[0].DownloadURL, "https://example.invalid/download/1"; got != want {
		t.Fatalf("download url = %q, want %q", got, want)
	}
	if got, want := result.Observations[0].Source, "bitmagnet"; got != want {
		t.Fatalf("source = %q, want %q", got, want)
	}
	if got, want := result.State["search"], `{"window_end_page":2}`; got != want {
		t.Fatalf("state = %q, want %q", got, want)
	}
	if got, want := seenOffsets, []string{"0", "2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("offsets = %#v, want %#v", got, want)
	}
}

func TestTorznabAdapterResumesFromKnownFrontier(t *testing.T) {
	adapter, err := NewTorznabAdapter(config.SourceConfig{
		Name:       "bitmagnet",
		Type:       "torznab",
		Enabled:    true,
		BaseURL:    "https://example.test/api",
		PageWindow: 2,
		PageSize:   2,
	})
	if err != nil {
		t.Fatal(err)
	}

	var seenOffsets []string
	adapter.client = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			q := req.URL.Query()
			seenOffsets = append(seenOffsets, q.Get("offset"))
			if got, want := req.URL.Path, "/api"; got != want {
				t.Fatalf("path = %q, want %q", got, want)
			}
			var body string
			switch q.Get("offset") {
			case "0":
				body = torznabFeedXML(
					torznabItemXML(
						"0123456789abcdef0123456789abcdef01234567",
						"Known Item",
						"Movies",
						"Tue, 28 Apr 2026 09:24:13 +0000",
						"1073741824",
						"42",
						"3",
						"magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Known+Item",
						"",
					),
				)
			case "2":
				body = torznabFeedXML(
					torznabItemXML(
						"89abcdef0123456789abcdef0123456789abcdef",
						"New Item",
						"TV",
						"Tue, 28 Apr 2026 10:24:13 +0000",
						"2048",
						"5",
						"1",
						"magnet:?xt=urn:btih:89abcdef0123456789abcdef0123456789abcdef&dn=New+Item",
						"",
					),
				)
			default:
				body = torznabFeedXML()
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    req,
			}, nil
		}),
	}

	repo := newTestRepository()
	repo.states["bitmagnet|search"] = encodeState(crawlState{WindowEndPage: 1})
	repo.known["0123456789ABCDEF0123456789ABCDEF01234567"] = true

	result, err := adapter.Fetch(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(result.Torrents), 1; got != want {
		t.Fatalf("torrents len = %d, want %d", got, want)
	}
	if got, want := result.Torrents[0].InfoHash, "89ABCDEF0123456789ABCDEF0123456789ABCDEF"; got != want {
		t.Fatalf("infohash = %q, want %q", got, want)
	}
	if got, want := result.State["search"], `{"window_end_page":2}`; got != want {
		t.Fatalf("state = %q, want %q", got, want)
	}
	if got, want := seenOffsets, []string{"0", "2", "4"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("offsets = %#v, want %#v", got, want)
	}
}

func torznabFeedXML(items ...string) string {
	return `<?xml version="1.0" encoding="utf-8"?>` +
		`<rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel>` +
		strings.Join(items, "") +
		`</channel></rss>`
}

func torznabItemXML(infohash, title, category, pubDate, size, seeders, leechers, magnetURI, enclosureURL string) string {
	enclosure := ""
	if enclosureURL != "" {
		enclosure = fmt.Sprintf(`<enclosure url="%s" length="%s" type="application/x-bittorrent" />`, xmlEscapeAttr(enclosureURL), size)
	}
	return fmt.Sprintf(
		`<item><title>%s</title><guid>%s</guid><link>%s</link><pubDate>%s</pubDate><description>%s</description><category>%s</category>%s<torznab:attr name="seeders" value="%s" /><torznab:attr name="leechers" value="%s" /><torznab:attr name="size" value="%s" /></item>`,
		title,
		infohash,
		xmlEscapeText(magnetURI),
		pubDate,
		title,
		category,
		enclosure,
		seeders,
		leechers,
		size,
	)
}

func xmlEscapeText(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, `"`, "&quot;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	return value
}

func xmlEscapeAttr(value string) string {
	return xmlEscapeText(value)
}
