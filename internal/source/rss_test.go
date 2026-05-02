package source

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/esaiaswestberg/magnet-atlas/internal/config"
)

func TestRSSAdapterFetch(t *testing.T) {
	adapter, err := NewRSSAdapter(config.SourceConfig{
		Name:    "rss",
		Type:    "rss",
		Enabled: true,
		FeedURL: "https://example.test/feed.rss",
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter.client = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/feed.rss" {
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
				Body: io.NopCloser(strings.NewReader(strings.TrimSpace(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:tv="https://showrss.info">
  <channel>
    <title>showRSS personal feed</title>
    <link>https://showrss.info</link>
    <description>showRSS personal feed</description>
    <item>
      <title>The Rookie S08E17 1080p HEVC x265-MeGusta EZTV</title>
      <link>magnet:?xt=urn:btih:3636c60f178912f89eb054f030da0ad9bd593938&amp;dn=The.Rookie.S08E17.1080p.HEVC.x265-MeGusta</link>
      <guid isPermaLink="false">99a8f972ce4ff3453911e5d541cb7a1b8cdc4698</guid>
      <pubDate>Tue, 28 Apr 2026 09:24:13 +0000</pubDate>
      <description>New Episode: The Rookie S08E17 1080p HEVC x265-MeGusta EZTV</description>
      <tv:show_name>The Rookie</tv:show_name>
      <tv:info_hash>3636c60f178912f89eb054f030da0ad9bd593938</tv:info_hash>
    </item>
    <item>
      <title>The Amazing Race S31E06 Who Wants a Rolex 1080p</title>
      <link>https://showrss.info/shows/18/</link>
      <guid isPermaLink="false">f5b9c5dbf3524422f635844ae2e56b1389b67442</guid>
      <pubDate>Sat, 02 May 2026 00:15:02 +0000</pubDate>
      <description>New episode: The Amazing Race S31E06 Who Wants a Rolex 1080p. Link: &lt;a href=&quot;magnet:?xt=urn:btih:45F0AD848F2F729801BF9D35FF8AC0728EFEA008&amp;dn=The+Amazing+Race&quot;&gt;magnet:?xt=urn:btih:45F0AD848F2F729801BF9D35FF8AC0728EFEA008&amp;dn=The+Amazing+Race&lt;/a&gt;</description>
      <tv:show_name>The Amazing Race</tv:show_name>
      <tv:info_hash>45F0AD848F2F729801BF9D35FF8AC0728EFEA008</tv:info_hash>
      <enclosure url="magnet:?xt=urn:btih:45F0AD848F2F729801BF9D35FF8AC0728EFEA008&amp;dn=The+Amazing+Race+S31E06+Who+Wants+a+Rolex+1080p" length="0" type="application/x-bittorrent" />
    </item>
  </channel>
</rss>`))),
				Request: req,
			}, nil
		}),
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
	if got, want := result.Torrents[0].InfoHash, "3636C60F178912F89EB054F030DA0AD9BD593938"; got != want {
		t.Fatalf("infohash = %q, want %q", got, want)
	}
	if got, want := result.Observations[0].SourceID, "99a8f972ce4ff3453911e5d541cb7a1b8cdc4698"; got != want {
		t.Fatalf("source id = %q, want %q", got, want)
	}
	if got, want := result.Torrents[1].MagnetURI, "magnet:?xt=urn:btih:45F0AD848F2F729801BF9D35FF8AC0728EFEA008&dn=The+Amazing+Race+S31E06+Who+Wants+a+Rolex+1080p"; got != want {
		t.Fatalf("magnet uri = %q, want %q", got, want)
	}
	if got, want := result.Observations[1].SourceURL, "https://showrss.info/shows/18/"; got != want {
		t.Fatalf("source url = %q, want %q", got, want)
	}
	if got := result.Observations[0].ObservedAt; got.IsZero() {
		t.Fatal("observed_at should be set")
	}
	if got := result.State; len(got) != 0 {
		t.Fatalf("state should be empty, got %#v", got)
	}
	if got := result.Observations[1].ExtraText; len(got) != 1 || !strings.Contains(got[0], "New episode: The Amazing Race S31E06 Who Wants a Rolex 1080p.") || !strings.Contains(got[0], "magnet:?xt=urn:btih:45F0AD848F2F729801BF9D35FF8AC0728EFEA008&dn=The+Amazing+Race") {
		t.Fatalf("extra text = %#v", got)
	}
}

func TestRSSAdapterSkipsMalformedItems(t *testing.T) {
	adapter, err := NewRSSAdapter(config.SourceConfig{
		Name:    "rss",
		Type:    "rss",
		Enabled: true,
		FeedURL: "https://example.test/feed.rss",
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter.client = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <item>
      <title>Broken item</title>
      <guid>broken-1</guid>
    </item>
    <item>
      <title>Working item</title>
      <guid>working-1</guid>
      <link>magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567&amp;dn=Working+Item</link>
      <description>Example</description>
    </item>
  </channel>
</rss>`)),
				Request: req,
			}, nil
		}),
	}

	result, err := adapter.Fetch(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(result.Torrents), 1; got != want {
		t.Fatalf("torrents len = %d, want %d", got, want)
	}
	if got, want := result.Torrents[0].InfoHash, "0123456789ABCDEF0123456789ABCDEF01234567"; got != want {
		t.Fatalf("infohash = %q, want %q", got, want)
	}
	if got, want := result.Observations[0].SourceID, "working-1"; got != want {
		t.Fatalf("source id = %q, want %q", got, want)
	}
}

func TestParseRSSDate(t *testing.T) {
	ts := parseRSSDate("Tue, 28 Apr 2026 09:24:13 +0000")
	if got, want := ts.Year(), 2026; got != want {
		t.Fatalf("year = %d, want %d", got, want)
	}
	if got, want := ts.Month(), time.April; got != want {
		t.Fatalf("month = %v, want %v", got, want)
	}
}
