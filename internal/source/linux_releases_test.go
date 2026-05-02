package source

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/esaiaswestberg/magnet-atlas/internal/config"
)

func TestLinuxReleasesAdapterFetch(t *testing.T) {
	desktopTorrent, desktopHash := buildTorrentFixture("ubuntu-24.04.4-desktop-amd64.iso")
	serverTorrent, serverHash := buildTorrentFixture("ubuntu-24.04.4-live-server-amd64.iso")

	adapter, transport := newTestLinuxReleasesAdapter(t, map[string]string{
		"/": `<html><body>
<a href="releases/24.04/">Ubuntu 24.04.4 LTS</a>
</body></html>`,
		"/releases/24.04/": `<html><body>
<title>Ubuntu 24.04.4 (Noble Numbat)</title>
<h1>Ubuntu 24.04.4 (Noble Numbat)</h1>
<a href="ubuntu-24.04.4-desktop-amd64.iso.torrent">ubuntu-24.04.4-desktop-amd64.iso.torrent</a>
<a href="ubuntu-24.04.4-live-server-amd64.iso.torrent">ubuntu-24.04.4-live-server-amd64.iso.torrent</a>
</body></html>`,
		"/releases/24.04/ubuntu-24.04.4-desktop-amd64.iso.torrent":     string(desktopTorrent),
		"/releases/24.04/ubuntu-24.04.4-live-server-amd64.iso.torrent": string(serverTorrent),
	})

	repo := newTestRepository()
	targets, err := adapter.discoverReleaseTargets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(targets), 1; got != want {
		t.Fatalf("targets len = %d, want %d", got, want)
	}
	candidates, err := adapter.fetchReleasePage(context.Background(), targets[0])
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(candidates), 2; got != want {
		t.Fatalf("candidates len = %d, want %d", got, want)
	}
	firstRecord, err := adapter.fetchDetail(context.Background(), candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	if firstRecord.torrent.InfoHash == "" {
		t.Fatal("expected first record infohash")
	}
	result, err := adapter.Fetch(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(result.Torrents), 2; got != want {
		t.Fatalf("torrents len = %d, want %d", got, want)
	}
	if got, want := len(result.Observations), 2; got != want {
		t.Fatalf("observations len = %d, want %d", got, want)
	}
	if got, want := result.Torrents[0].InfoHash, desktopHash; got != want {
		t.Fatalf("desktop infohash = %q, want %q", got, want)
	}
	if got, want := result.Torrents[0].Title, "ubuntu-24.04.4-desktop-amd64.iso"; got != want {
		t.Fatalf("desktop title = %q, want %q", got, want)
	}
	if got, want := result.Torrents[0].Category, "Ubuntu 24.04.4 (Noble Numbat)"; got != want {
		t.Fatalf("category = %q, want %q", got, want)
	}
	wantMagnet := "magnet:?xt=urn:btih:" + desktopHash +
		"&dn=" + url.QueryEscape("ubuntu-24.04.4-desktop-amd64.iso") +
		"&xl=123456789" +
		"&xs=" + url.QueryEscape("https://releases.ubuntu.com/releases/24.04/ubuntu-24.04.4-desktop-amd64.iso.torrent")
	if got, want := result.Torrents[0].MagnetURI, wantMagnet; got != want {
		t.Fatalf("magnet uri = %q, want %q", got, want)
	}
	if got, want := result.Observations[1].SourceURL, "https://releases.ubuntu.com/releases/24.04/ubuntu-24.04.4-live-server-amd64.iso.torrent"; got != want {
		t.Fatalf("source url = %q, want %q", got, want)
	}
	if got, want := result.Torrents[1].InfoHash, serverHash; got != want {
		t.Fatalf("server infohash = %q, want %q", got, want)
	}
	if got := result.State; len(got) != 0 {
		t.Fatalf("state should be empty, got %#v", got)
	}
	paths := transport.paths()
	if !containsString(paths, "/") || !containsString(paths, "/releases/24.04/") {
		t.Fatalf("unexpected request paths: %v", paths)
	}
}

func TestLinuxReleasesAdapterSkipsKnownInfohash(t *testing.T) {
	torrentBytes, hash := buildTorrentFixture("ubuntu-24.04.4-desktop-amd64.iso")
	adapter, _ := newTestLinuxReleasesAdapter(t, map[string]string{
		"/": `<html><body>
<a href="releases/24.04/">Ubuntu 24.04.4 LTS</a>
</body></html>`,
		"/releases/24.04/": `<html><body>
<a href="ubuntu-24.04.4-desktop-amd64.iso.torrent">ubuntu-24.04.4-desktop-amd64.iso.torrent</a>
</body></html>`,
		"/releases/24.04/ubuntu-24.04.4-desktop-amd64.iso.torrent": string(torrentBytes),
	})

	repo := newTestRepository()
	repo.known[hash] = true

	result, err := adapter.Fetch(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(result.Torrents), 0; got != want {
		t.Fatalf("torrents len = %d, want %d", got, want)
	}
}

func TestTorrentInfoHash(t *testing.T) {
	torrentBytes, want := buildTorrentFixture("ubuntu-24.04.4-desktop-amd64.iso")
	got, err := torrentInfoHash(torrentBytes)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("infohash = %q, want %q", got, want)
	}
}

func TestNewLinuxReleasesAdapterUsesDefaultBaseURL(t *testing.T) {
	adapter, err := NewLinuxReleasesAdapter(config.SourceConfig{
		Name:    "ubuntu",
		Type:    "linux-releases",
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := adapter.baseURL.String(), defaultLinuxReleasesBaseURL; got != want {
		t.Fatalf("base url = %q, want %q", got, want)
	}
}

type linuxTestTransport struct {
	routes map[string]string
	pathsV []string
}

func newTestLinuxReleasesAdapter(t *testing.T, routes map[string]string) (*linuxReleasesAdapter, *linuxTestTransport) {
	t.Helper()
	baseURL, err := url.Parse("https://releases.ubuntu.com/")
	if err != nil {
		t.Fatal(err)
	}
	transport := &linuxTestTransport{routes: routes}
	client := &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			transport.pathsV = append(transport.pathsV, req.URL.RequestURI())
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
	return &linuxReleasesAdapter{
		name:        "ubuntu",
		baseURL:     baseURL,
		client:      client,
		concurrency: 2,
	}, transport
}

func (t *linuxTestTransport) paths() []string {
	out := make([]string, len(t.pathsV))
	copy(out, t.pathsV)
	return out
}

func buildTorrentFixture(name string) ([]byte, string) {
	info := map[string]any{
		"length":       123456789,
		"name":         name,
		"piece length": 262144,
		"pieces":       strings.Repeat("a", 20),
	}
	infoBytes := encodeBencode(info)
	torrent := encodeBencode(map[string]any{
		"announce": "https://tracker.invalid/announce",
		"info":     info,
	})
	sum := sha1.Sum(infoBytes)
	return torrent, strings.ToUpper(hex.EncodeToString(sum[:]))
}

func encodeBencode(value any) []byte {
	switch v := value.(type) {
	case string:
		return []byte(fmt.Sprintf("%d:%s", len(v), v))
	case []byte:
		return []byte(fmt.Sprintf("%d:", len(v)) + string(v))
	case int:
		return []byte(fmt.Sprintf("i%de", v))
	case int64:
		return []byte(fmt.Sprintf("i%de", v))
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteByte('d')
		for _, k := range keys {
			b.WriteString(fmt.Sprintf("%d:%s", len(k), k))
			b.Write(encodeBencode(v[k]))
		}
		b.WriteByte('e')
		return []byte(b.String())
	default:
		return []byte{}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
