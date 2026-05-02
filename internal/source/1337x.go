package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/esaiaswestberg/magnet-atlas/internal/config"
	"github.com/esaiaswestberg/magnet-atlas/internal/domain"
)

const default1337XBaseURL = "https://www.1337xx.to"

var (
	torrentLinkRe = regexp.MustCompile(`(?s)<a[^>]+href="(/torrent/(\d+)/[^"]+/)"[^>]*>(.*?)</a>`)
	magnetLinkRe  = regexp.MustCompile(`href="(magnet:\?xt=urn:btih:[^"]+)"`)
	infohashRe    = regexp.MustCompile(`(?im)^\s*Infohash\s*:?\s*([a-f0-9]{40})\s*$`)
	sizeRe        = regexp.MustCompile(`(?im)^\s*Total size\s+([0-9.]+\s*[A-Za-z]+)\s*$`)
	seedersRe     = regexp.MustCompile(`(?im)^\s*Seeders\s+(\d+)\s*$`)
	leechersRe    = regexp.MustCompile(`(?im)^\s*Leechers\s+(\d+)\s*$`)
	categoryRe    = regexp.MustCompile(`(?im)^\s*Category\s+(.+?)\s*$`)
	uploadedRe    = regexp.MustCompile(`(?im)^\s*Date uploaded\s+(.+?)\s*$`)
	tagStripRe    = regexp.MustCompile(`(?s)<[^>]+>`)
)

type x1337Adapter struct {
	name        string
	baseURL     *url.URL
	client      *http.Client
	concurrency int
	maxPages    int
	seeds       []crawlSeed
}

type crawlSeed struct {
	name string
	path string
}

type listingItem struct {
	id       string
	title    string
	url      string
	category string
}

type torrentRecord struct {
	torrent domain.Torrent
	obs     domain.SourceObservation
}

// New1337XAdapter creates an adapter for the public 1337x browse pages.
func New1337XAdapter(cfg config.SourceConfig) (*x1337Adapter, error) {
	if cfg.Name == "" {
		return nil, fmt.Errorf("source name is required")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = default1337XBaseURL
	}
	baseURL, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base_url: %w", err)
	}
	if !baseURL.IsAbs() {
		return nil, fmt.Errorf("base_url must be absolute")
	}

	seeds := default1337XSeeds()
	if len(cfg.Categories) > 0 {
		seeds = filter1337XSeeds(cfg.Categories)
	}
	if len(seeds) == 0 {
		return nil, fmt.Errorf("no 1337x crawl seeds selected")
	}

	concurrency := cfg.Concurrency
	if concurrency <= 0 {
		concurrency = 4
	}
	client := &http.Client{Timeout: 30 * time.Second}

	return &x1337Adapter{
		name:        cfg.Name,
		baseURL:     baseURL,
		client:      client,
		concurrency: concurrency,
		maxPages:    cfg.MaxPages,
		seeds:       seeds,
	}, nil
}

func (a *x1337Adapter) Name() string { return a.name }

func (a *x1337Adapter) Fetch(ctx context.Context) ([]domain.Torrent, []domain.SourceObservation, error) {
	seen := make(map[string]struct{})
	torrents := make([]domain.Torrent, 0, 256)
	observations := make([]domain.SourceObservation, 0, 256)

	for _, seed := range a.seeds {
		for page := 1; a.maxPages <= 0 || page <= a.maxPages; page++ {
			listingURL := a.pageURL(seed.path, page)
			items, err := a.fetchListingPage(ctx, listingURL, seed.name)
			if err != nil {
				if page == 1 {
					return nil, nil, err
				}
				break
			}
			if len(items) == 0 {
				break
			}
			records := a.fetchDetails(ctx, items)
			for _, record := range records {
				if record.torrent.InfoHash == "" {
					continue
				}
				if _, ok := seen[record.torrent.InfoHash]; ok {
					continue
				}
				seen[record.torrent.InfoHash] = struct{}{}
				torrents = append(torrents, record.torrent)
				observations = append(observations, record.obs)
			}
		}
	}

	return torrents, observations, nil
}

func (a *x1337Adapter) fetchListingPage(ctx context.Context, pageURL string, category string) ([]listingItem, error) {
	body, status, err := a.get(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("listing page %s: unexpected status %d", pageURL, status)
	}
	matches := torrentLinkRe.FindAllStringSubmatch(body, -1)
	items := make([]listingItem, 0, len(matches))
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		title := stripHTML(match[3])
		title = html.UnescapeString(strings.TrimSpace(title))
		if title == "" {
			continue
		}
		items = append(items, listingItem{
			id:       match[2],
			title:    title,
			url:      a.resolve(match[1]),
			category: category,
		})
	}
	return items, nil
}

func (a *x1337Adapter) fetchDetails(ctx context.Context, items []listingItem) []torrentRecord {
	out := make([]torrentRecord, len(items))
	if len(items) == 0 {
		return out
	}

	limit := a.concurrency
	if limit <= 0 {
		limit = 1
	}
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i := range items {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			item := items[i]
			detail, err := a.fetchDetail(ctx, item)
			if err != nil {
				return
			}
			out[i] = detail
		}(i)
	}
	wg.Wait()
	return out
}

func (a *x1337Adapter) fetchDetail(ctx context.Context, item listingItem) (torrentRecord, error) {
	body, status, err := a.get(ctx, item.url)
	if err != nil {
		return torrentRecord{}, err
	}
	if status != http.StatusOK {
		return torrentRecord{}, fmt.Errorf("detail page %s: unexpected status %d", item.url, status)
	}

	text := stripHTML(body)
	infohash := findMatch(text, infohashRe)
	if infohash == "" {
		infohash = infohashFromMagnet(body)
	}
	if infohash == "" {
		return torrentRecord{}, errors.New("infohash not found")
	}

	magnetURI := firstMagnet(body)
	category := findMatch(text, categoryRe)
	if category == "" {
		category = item.category
	}

	sizeBytes := int64(0)
	if sizeText := findMatch(text, sizeRe); sizeText != "" {
		if parsed, err := parseHumanSize(sizeText); err == nil {
			sizeBytes = parsed
		}
	}

	seeders := 0
	if s := findMatch(text, seedersRe); s != "" {
		seeders, _ = strconv.Atoi(s)
	}
	leechers := 0
	if l := findMatch(text, leechersRe); l != "" {
		leechers, _ = strconv.Atoi(l)
	}

	publishedAt := time.Time{}
	if uploaded := findMatch(text, uploadedRe); uploaded != "" {
		if ts, err := parse1337XDate(uploaded); err == nil {
			publishedAt = ts
		}
	}

	raw, _ := json.Marshal(map[string]any{
		"item_id":      item.id,
		"item_title":   item.title,
		"detail_url":   item.url,
		"infohash":     infohash,
		"magnet_uri":   magnetURI,
		"category":     category,
		"size_bytes":   sizeBytes,
		"seeders":      seeders,
		"leechers":     leechers,
		"published_at": publishedAt,
	})

	torrent := domain.Torrent{
		InfoHash:    infohash,
		Title:       item.title,
		Category:    category,
		SizeBytes:   sizeBytes,
		Seeders:     seeders,
		Leechers:    leechers,
		PublishedAt: publishedAt,
		MagnetURI:   magnetURI,
	}
	obs := domain.SourceObservation{
		Source:      a.name,
		SourceID:    item.id,
		SourceURL:   item.url,
		Title:       item.title,
		Category:    category,
		SizeBytes:   sizeBytes,
		Seeders:     seeders,
		Leechers:    leechers,
		PublishedAt: publishedAt,
		MagnetURI:   magnetURI,
		ObservedAt:  time.Now().UTC(),
		RawJSON:     string(raw),
	}
	return torrentRecord{torrent: torrent, obs: obs}, nil
}

func (a *x1337Adapter) get(ctx context.Context, pageURL string) (string, int, error) {
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
	return string(b), resp.StatusCode, nil
}

func (a *x1337Adapter) pageURL(seedPath string, page int) string {
	path := strings.TrimSuffix(seedPath, "/")
	return a.resolve(fmt.Sprintf("%s/%d/", path, page))
}

func (a *x1337Adapter) resolve(rel string) string {
	ref, err := url.Parse(rel)
	if err != nil {
		return rel
	}
	return a.baseURL.ResolveReference(ref).String()
}

func default1337XSeeds() []crawlSeed {
	return []crawlSeed{
		{name: "movie-library", path: "/movie-library"},
		{name: "series-library", path: "/series-library/a"},
		{name: "new-episodes", path: "/new-episodes/day"},
		{name: "movies", path: "/cat/Movies"},
		{name: "tv", path: "/cat/TV"},
		{name: "games", path: "/cat/Games"},
		{name: "music", path: "/cat/Music"},
		{name: "apps", path: "/cat/Apps"},
		{name: "anime", path: "/cat/Anime"},
		{name: "documentaries", path: "/cat/Documentaries"},
		{name: "other", path: "/cat/Other"},
		{name: "xxx", path: "/cat/XXX"},
	}
}

func filter1337XSeeds(categories []string) []crawlSeed {
	allowed := make(map[string]struct{}, len(categories))
	for _, category := range categories {
		for _, alias := range categoryAliases(strings.ToLower(strings.TrimSpace(category))) {
			allowed[alias] = struct{}{}
		}
	}
	seeds := default1337XSeeds()
	out := make([]crawlSeed, 0, len(seeds))
	for _, seed := range seeds {
		if _, ok := allowed[strings.ToLower(seed.name)]; ok {
			out = append(out, seed)
		}
	}
	return out
}

func categoryAliases(name string) []string {
	switch name {
	case "application", "applications", "app", "apps":
		return []string{"apps"}
	case "television", "tv", "series":
		return []string{"tv", "series-library", "new-episodes"}
	case "movie", "movies":
		return []string{"movies", "movie-library"}
	case "documentary", "documentaries":
		return []string{"documentaries"}
	case "game", "games":
		return []string{"games"}
	case "music":
		return []string{"music"}
	case "anime":
		return []string{"anime"}
	case "other", "others":
		return []string{"other"}
	case "xxx":
		return []string{"xxx"}
	default:
		return []string{name}
	}
}

func stripHTML(s string) string {
	replacer := strings.NewReplacer("<br>", "\n", "<br/>", "\n", "<br />", "\n")
	s = replacer.Replace(s)
	s = tagStripRe.ReplaceAllString(s, " ")
	return html.UnescapeString(s)
}

func findMatch(text string, re *regexp.Regexp) string {
	match := re.FindStringSubmatch(text)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func infohashFromMagnet(htmlBody string) string {
	magnet := firstMagnet(htmlBody)
	if magnet == "" {
		return ""
	}
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

func firstMagnet(htmlBody string) string {
	match := magnetLinkRe.FindStringSubmatch(htmlBody)
	if len(match) < 2 {
		return ""
	}
	return html.UnescapeString(match[1])
}

func parseHumanSize(s string) (int64, error) {
	fields := strings.Fields(strings.ToUpper(strings.TrimSpace(s)))
	if len(fields) < 2 {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, err
	}
	unit := fields[1]
	switch unit {
	case "B":
		return int64(value), nil
	case "KB", "KIB":
		return int64(value * 1024), nil
	case "MB", "MIB":
		return int64(value * 1024 * 1024), nil
	case "GB", "GIB":
		return int64(value * 1024 * 1024 * 1024), nil
	case "TB", "TIB":
		return int64(value * 1024 * 1024 * 1024 * 1024), nil
	default:
		return 0, fmt.Errorf("unsupported size unit %q", unit)
	}
}

var ordinalSuffixRe = regexp.MustCompile(`(?i)(\d+)(ST|ND|RD|TH)`)

func parse1337XDate(s string) (time.Time, error) {
	cleaned := strings.TrimSpace(s)
	cleaned = strings.ReplaceAll(cleaned, ".", "")
	cleaned = strings.ReplaceAll(cleaned, ",", "")
	cleaned = strings.ReplaceAll(cleaned, "'", "")
	cleaned = ordinalSuffixRe.ReplaceAllString(cleaned, `$1`)
	layouts := []string{
		"Jan 2 06",
		"Jan 2 2006",
		"January 2 06",
		"January 2 2006",
	}
	for _, layout := range layouts {
		if ts, err := time.ParseInLocation(layout, cleaned, time.UTC); err == nil {
			return ts, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported date %q", s)
}
