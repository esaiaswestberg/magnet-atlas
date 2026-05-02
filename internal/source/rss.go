package source

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/esaiaswestberg/magnet-atlas/internal/config"
	"github.com/esaiaswestberg/magnet-atlas/internal/domain"
	"github.com/esaiaswestberg/magnet-atlas/internal/store"
)

type rssAdapter struct {
	name    string
	feedURL *url.URL
	client  *http.Client
}

type rssFeed struct {
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Items []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string       `xml:"title"`
	Link        string       `xml:"link"`
	GUID        string       `xml:"guid"`
	PubDate     string       `xml:"pubDate"`
	Description string       `xml:"description"`
	Enclosure   rssEnclosure `xml:"enclosure"`
	InfoHash    string       `xml:"info_hash"`
	ShowName    string       `xml:"show_name"`
	RawXML      string       `xml:",innerxml"`
}

type rssEnclosure struct {
	URL    string `xml:"url,attr"`
	Type   string `xml:"type,attr"`
	Length string `xml:"length,attr"`
}

// NewRSSAdapter creates an adapter for RSS feeds that expose torrent metadata directly.
func NewRSSAdapter(cfg config.SourceConfig) (*rssAdapter, error) {
	if strings.TrimSpace(cfg.Name) == "" {
		return nil, fmt.Errorf("source name is required")
	}
	feedURL := strings.TrimSpace(cfg.FeedURL)
	if feedURL == "" {
		return nil, fmt.Errorf("feed_url is required")
	}
	parsed, err := url.Parse(feedURL)
	if err != nil {
		return nil, fmt.Errorf("parse feed_url: %w", err)
	}
	if !parsed.IsAbs() {
		return nil, fmt.Errorf("feed_url must be absolute")
	}
	switch parsed.Scheme {
	case "http", "https":
	default:
		return nil, fmt.Errorf("feed_url must use http or https")
	}

	return &rssAdapter{
		name:    cfg.Name,
		feedURL: parsed,
		client:  &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (a *rssAdapter) Name() string { return a.name }

func (a *rssAdapter) Fetch(ctx context.Context, _ store.Repository) (FetchResult, error) {
	select {
	case <-ctx.Done():
		return FetchResult{}, ctx.Err()
	default:
	}

	body, status, err := a.get(ctx, a.feedURL.String())
	if err != nil {
		return FetchResult{}, err
	}
	if status != http.StatusOK {
		return FetchResult{}, fmt.Errorf("feed %s: unexpected status %d", a.feedURL.String(), status)
	}

	var feed rssFeed
	if err := xml.Unmarshal([]byte(body), &feed); err != nil {
		return FetchResult{}, fmt.Errorf("parse rss feed: %w", err)
	}

	now := time.Now().UTC()
	result := FetchResult{}
	seen := make(map[string]struct{}, len(feed.Channel.Items))
	for _, item := range feed.Channel.Items {
		torrent, obs, ok := a.parseItem(item, now)
		if !ok {
			continue
		}
		key := torrent.InfoHash
		if key == "" {
			key = obs.SourceID
		}
		if key != "" {
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
		}
		result.Torrents = append(result.Torrents, torrent)
		result.Observations = append(result.Observations, obs)
	}
	return result, nil
}

func (a *rssAdapter) parseItem(item rssItem, observedAt time.Time) (domain.Torrent, domain.SourceObservation, bool) {
	title := strings.TrimSpace(html.UnescapeString(item.Title))
	description := cleanRSSText(item.Description)
	showName := strings.TrimSpace(html.UnescapeString(item.ShowName))

	magnetURI := firstMagnetURI(item.Link, item.Enclosure.URL, item.Description, item.RawXML)
	infohash := strings.ToUpper(strings.TrimSpace(item.InfoHash))
	if infohash == "" && magnetURI != "" {
		infohash = infohashFromMagnetURI(magnetURI)
	}
	if infohash == "" {
		return domain.Torrent{}, domain.SourceObservation{}, false
	}
	if magnetURI == "" {
		magnetURI = "magnet:?xt=urn:btih:" + infohash
	}
	if title == "" {
		title = infohash
	}

	publishedAt := parseRSSDate(item.PubDate)
	sourceURL := firstNonEmpty(item.Link, item.Enclosure.URL)
	downloadURL := magnetURI
	if downloadURL == "" {
		downloadURL = firstNonEmptyNonMagnet(item.Enclosure.URL, item.Link)
	}

	raw, _ := json.Marshal(map[string]any{
		"title":        title,
		"link":         item.Link,
		"guid":         item.GUID,
		"pub_date":     item.PubDate,
		"description":  item.Description,
		"enclosure":    item.Enclosure,
		"info_hash":    infohash,
		"show_name":    showName,
		"raw_xml":      item.RawXML,
		"magnet_uri":   magnetURI,
		"published_at": publishedAt,
	})

	extraText := extraTextValues(description)
	torrent := domain.Torrent{
		InfoHash:    infohash,
		Title:       title,
		PublishedAt: publishedAt,
		MagnetURI:   magnetURI,
		DownloadURL: downloadURL,
		ExtraText:   extraText,
	}
	obs := domain.SourceObservation{
		Source:      a.name,
		SourceID:    firstNonEmpty(item.GUID, infohash),
		SourceURL:   sourceURL,
		Title:       title,
		PublishedAt: publishedAt,
		MagnetURI:   magnetURI,
		DownloadURL: downloadURL,
		ObservedAt:  observedAt,
		RawJSON:     string(raw),
		ExtraText:   extraText,
	}
	return torrent, obs, true
}

func (a *rssAdapter) get(ctx context.Context, pageURL string) (string, int, error) {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("User-Agent", "Magnet Atlas/1.0")

	resp, err := a.client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", resp.StatusCode, err
	}
	slog.Debug("fetched rss response", "url", pageURL, "status", resp.StatusCode, "bytes", len(b), "duration", time.Since(start))
	return string(b), resp.StatusCode, nil
}

func cleanRSSText(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return strings.TrimSpace(stripHTML(html.UnescapeString(s)))
}

func parseRSSDate(s string) time.Time {
	cleaned := strings.TrimSpace(s)
	if cleaned == "" {
		return time.Time{}
	}
	layouts := []string{
		time.RFC1123Z,
		time.RFC1123,
		time.RFC822Z,
		time.RFC822,
		time.RFC3339Nano,
		time.RFC3339,
		"Mon, 2 Jan 2006 15:04:05 -0700",
	}
	for _, layout := range layouts {
		if ts, err := time.Parse(layout, cleaned); err == nil {
			return ts.UTC()
		}
	}
	return time.Time{}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func firstNonEmptyNonMagnet(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(value), "magnet:?xt=urn:btih:") {
			continue
		}
		return value
	}
	return ""
}

func firstMagnetURI(values ...string) string {
	for _, value := range values {
		if uri := magnetURIFromText(value); uri != "" {
			return uri
		}
	}
	return ""
}

func magnetURIFromText(value string) string {
	value = html.UnescapeString(strings.TrimSpace(value))
	lower := strings.ToLower(value)
	idx := strings.Index(lower, "magnet:?xt=urn:btih:")
	if idx < 0 {
		return ""
	}
	candidate := value[idx:]
	stop := len(candidate)
	for _, sep := range []string{"\n", "\r", "\"", "'", "<", ">"} {
		if p := strings.Index(candidate, sep); p >= 0 && p < stop {
			stop = p
		}
	}
	candidate = strings.TrimSpace(candidate[:stop])
	candidate = strings.Trim(candidate, "\"'<>")
	if candidate == "" {
		return ""
	}
	if _, err := url.Parse(candidate); err != nil {
		return ""
	}
	return candidate
}

func infohashFromMagnetURI(magnet string) string {
	u, err := url.Parse(magnet)
	if err != nil {
		return ""
	}
	xt := strings.ToUpper(strings.TrimSpace(u.Query().Get("xt")))
	if strings.HasPrefix(xt, "URN:BTIH:") {
		return strings.TrimPrefix(xt, "URN:BTIH:")
	}
	return ""
}
