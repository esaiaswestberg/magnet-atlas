package source

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/esaiaswestberg/magnet-atlas/internal/config"
	"github.com/esaiaswestberg/magnet-atlas/internal/domain"
	"github.com/esaiaswestberg/magnet-atlas/internal/store"
)

const defaultTorznabPageSize = 100

type torznabAdapter struct {
	name        string
	baseURL     *url.URL
	searchQuery string
	categoryIDs []int
	client      *http.Client
	pageWindow  int
	pageSize    int
}

type torznabFeed struct {
	Channel torznabChannel `xml:"channel"`
}

type torznabChannel struct {
	Items []torznabItem `xml:"item"`
}

type torznabItem struct {
	Title       string           `xml:"title"`
	GUID        string           `xml:"guid"`
	Link        string           `xml:"link"`
	PubDate     string           `xml:"pubDate"`
	Description string           `xml:"description"`
	Enclosure   torznabEnclosure `xml:"enclosure"`
	Categories  []string         `xml:"category"`
	Attrs       []torznabAttr    `xml:"attr"`
}

type torznabEnclosure struct {
	URL    string `xml:"url,attr"`
	Type   string `xml:"type,attr"`
	Length string `xml:"length,attr"`
}

type torznabAttr struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

// NewTorznabAdapter creates an adapter for a Torznab-compatible upstream indexer.
func NewTorznabAdapter(cfg config.SourceConfig) (*torznabAdapter, error) {
	if strings.TrimSpace(cfg.Name) == "" {
		return nil, fmt.Errorf("source name is required")
	}
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		return nil, fmt.Errorf("base_url is required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base_url: %w", err)
	}
	if !parsed.IsAbs() {
		return nil, fmt.Errorf("base_url must be absolute")
	}
	switch parsed.Scheme {
	case "http", "https":
	default:
		return nil, fmt.Errorf("base_url must use http or https")
	}
	if strings.TrimSpace(parsed.Path) == "" || strings.TrimSpace(parsed.Path) == "/" {
		return nil, fmt.Errorf("base_url must include the exact torznab endpoint path")
	}

	categories := config.NormalizeTorznabCategories(cfg.Categories)
	if len(cfg.Categories) > 0 && len(categories) == 0 {
		return nil, fmt.Errorf("no torznab categories selected")
	}

	pageWindow := cfg.PageWindow
	if pageWindow <= 0 {
		pageWindow = cfg.MaxPages
	}
	if pageWindow <= 0 {
		pageWindow = 20
	}
	pageSize := cfg.PageSize
	if pageSize <= 0 {
		pageSize = defaultTorznabPageSize
	}

	return &torznabAdapter{
		name:        cfg.Name,
		baseURL:     parsed,
		searchQuery: strings.TrimSpace(cfg.SearchQuery),
		categoryIDs: categories,
		client:      &http.Client{Timeout: 30 * time.Second},
		pageWindow:  pageWindow,
		pageSize:    pageSize,
	}, nil
}

func (a *torznabAdapter) Name() string { return a.name }

func (a *torznabAdapter) Fetch(ctx context.Context, repo store.Repository) (FetchResult, error) {
	if repo == nil {
		return FetchResult{}, fmt.Errorf("repository is required")
	}

	state, err := a.loadState(ctx, repo, "search")
	if err != nil {
		return FetchResult{}, err
	}
	if slog.Default().Enabled(ctx, slog.LevelDebug) {
		slog.Debug("starting source crawl", "source", a.name, "section", "search", "query", a.searchQuery, "state", state.WindowEndPage)
	}

	var out FetchResult
	out.State = make(map[string]string)

	collect := func(startPage, limit int) (FetchResult, int, error) {
		return a.fetchWindow(ctx, repo, startPage, limit)
	}

	if state.WindowEndPage <= 0 {
		res, lastPage, err := collect(1, a.pageWindow)
		if err != nil {
			return FetchResult{}, err
		}
		out.Torrents = append(out.Torrents, res.Torrents...)
		out.Observations = append(out.Observations, res.Observations...)
		if lastPage > 0 {
			out.State["search"] = encodeState(crawlState{WindowEndPage: lastPage})
		}
		return out, nil
	}

	probeRes, probePages, err := a.probeFrontier(ctx, repo, a.pageWindow)
	if err != nil {
		return FetchResult{}, err
	}
	out.Torrents = append(out.Torrents, probeRes.Torrents...)
	out.Observations = append(out.Observations, probeRes.Observations...)

	start := state.WindowEndPage + probePages + 1
	windowRes, lastPage, err := collect(start, a.pageWindow)
	if err != nil {
		return FetchResult{}, err
	}
	out.Torrents = append(out.Torrents, windowRes.Torrents...)
	out.Observations = append(out.Observations, windowRes.Observations...)
	if lastPage > 0 {
		out.State["search"] = encodeState(crawlState{WindowEndPage: lastPage})
	}
	return out, nil
}

func (a *torznabAdapter) probeFrontier(ctx context.Context, repo store.Repository, limit int) (FetchResult, int, error) {
	var out FetchResult
	pagesBeforeBoundary := 0
	for page := 1; page <= limit; page++ {
		records, empty, foundKnown, err := a.collectPage(ctx, repo, page)
		if err != nil {
			return FetchResult{}, 0, err
		}
		if empty {
			return out, pagesBeforeBoundary, nil
		}
		if foundKnown {
			if slog.Default().Enabled(ctx, slog.LevelDebug) {
				slog.Debug("frontier reached", "source", a.name, "section", "search", "page", page)
			}
			return out, pagesBeforeBoundary, nil
		}
		for _, record := range records {
			out.Torrents = append(out.Torrents, record.record.torrent)
			out.Observations = append(out.Observations, record.record.obs)
		}
		pagesBeforeBoundary = page
	}
	return out, pagesBeforeBoundary, nil
}

func (a *torznabAdapter) fetchWindow(ctx context.Context, repo store.Repository, startPage, limit int) (FetchResult, int, error) {
	var out FetchResult
	lastPage := 0
	for page := startPage; page < startPage+limit; page++ {
		if slog.Default().Enabled(ctx, slog.LevelDebug) {
			slog.Debug("fetching window page", "source", a.name, "section", "search", "page", page)
		}
		records, empty, _, err := a.collectPage(ctx, repo, page)
		if err != nil {
			return FetchResult{}, 0, err
		}
		if empty {
			break
		}
		for _, record := range records {
			if record.known {
				continue
			}
			out.Torrents = append(out.Torrents, record.record.torrent)
			out.Observations = append(out.Observations, record.record.obs)
		}
		lastPage = page
	}
	return out, lastPage, nil
}

func (a *torznabAdapter) collectPage(ctx context.Context, repo store.Repository, page int) ([]pageRecord, bool, bool, error) {
	listingURL := a.pageURL(page)
	if slog.Default().Enabled(ctx, slog.LevelDebug) {
		slog.Debug("fetching listing page", "source", a.name, "section", "search", "page", page, "url", listingURL)
	}
	items, err := a.fetchSearchPage(ctx, page)
	if err != nil {
		return nil, false, false, err
	}
	if len(items) == 0 {
		return nil, true, false, nil
	}
	records := a.fetchDetails(items)
	out := make([]pageRecord, 0, len(records))
	foundKnown := false
	seen := make(map[string]struct{}, len(records))
	for _, rec := range records {
		if rec.torrent.InfoHash == "" {
			continue
		}
		if _, exists := seen[rec.torrent.InfoHash]; exists {
			continue
		}
		seen[rec.torrent.InfoHash] = struct{}{}
		known, err := repo.HasInfohash(ctx, rec.torrent.InfoHash)
		if err != nil {
			return nil, false, false, err
		}
		if known {
			foundKnown = true
		}
		out = append(out, pageRecord{record: rec, known: known})
	}
	return out, false, foundKnown, nil
}

func (a *torznabAdapter) fetchDetails(items []torznabItem) []torrentRecord {
	records := make([]torrentRecord, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	observedAt := time.Now().UTC()
	for _, item := range items {
		torrent, obs, ok := a.parseItem(item, observedAt)
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
		records = append(records, torrentRecord{torrent: torrent, obs: obs})
	}
	return records
}

func (a *torznabAdapter) parseItem(item torznabItem, observedAt time.Time) (domain.Torrent, domain.SourceObservation, bool) {
	title := strings.TrimSpace(html.UnescapeString(item.Title))
	infohash := normalizeTorznabInfoHash(item.GUID)
	magnetURI := firstMagnetURI(item.Link, item.Enclosure.URL, item.Description)
	if infohash == "" && magnetURI != "" {
		infohash = infohashFromMagnetURI(magnetURI)
	}
	if infohash == "" {
		return domain.Torrent{}, domain.SourceObservation{}, false
	}
	if title == "" {
		title = infohash
	}
	if magnetURI == "" {
		magnetURI = "magnet:?xt=urn:btih:" + infohash
	}

	category := torznabItemCategory(item)
	categories := torznabItemCategories(item)
	if category == "" && len(categories) > 0 {
		category = categories[0]
	}

	seeders := torznabIntValue(item, "seeders")
	leechers := torznabIntValue(item, "leechers")
	sizeBytes := torznabSizeValue(item)
	downloadURL := firstNonEmptyNonMagnet(item.Enclosure.URL, item.Link)
	if downloadURL == "" {
		downloadURL = magnetURI
	}

	raw, _ := json.Marshal(map[string]any{
		"title":       item.Title,
		"guid":        item.GUID,
		"link":        item.Link,
		"pub_date":    item.PubDate,
		"description": item.Description,
		"enclosure":   item.Enclosure,
		"categories":  categories,
		"attrs":       item.Attrs,
		"magnet_uri":  magnetURI,
	})

	torrent := domain.Torrent{
		InfoHash:    infohash,
		Title:       title,
		Category:    category,
		SizeBytes:   sizeBytes,
		Seeders:     seeders,
		Leechers:    leechers,
		PublishedAt: parseRSSDate(item.PubDate),
		MagnetURI:   magnetURI,
		DownloadURL: downloadURL,
		ExtraText:   extraTextValues(strings.TrimSpace(html.UnescapeString(item.Description)), strings.Join(categories, " ")),
	}
	obs := domain.SourceObservation{
		Source:      a.name,
		SourceID:    firstNonEmpty(item.GUID, infohash),
		SourceURL:   firstNonEmpty(item.Link, item.Enclosure.URL),
		Title:       title,
		Category:    category,
		SizeBytes:   sizeBytes,
		Seeders:     seeders,
		Leechers:    leechers,
		PublishedAt: torrent.PublishedAt,
		MagnetURI:   magnetURI,
		DownloadURL: downloadURL,
		ObservedAt:  observedAt,
		RawJSON:     string(raw),
		ExtraText:   append([]string(nil), torrent.ExtraText...),
	}
	return torrent, obs, true
}

func (a *torznabAdapter) fetchSearchPage(ctx context.Context, page int) ([]torznabItem, error) {
	body, status, err := a.get(ctx, a.pageURL(page))
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("search page %s: unexpected status %d", a.pageURL(page), status)
	}

	var feed torznabFeed
	if err := xml.Unmarshal([]byte(body), &feed); err != nil {
		return nil, fmt.Errorf("parse torznab feed: %w", err)
	}
	return feed.Channel.Items, nil
}

func (a *torznabAdapter) get(ctx context.Context, pageURL string) (string, int, error) {
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
	slog.Debug("fetched torznab response", "source", a.name, "url", pageURL, "status", resp.StatusCode, "bytes", len(b), "duration", time.Since(start))
	return string(b), resp.StatusCode, nil
}

func (a *torznabAdapter) loadState(ctx context.Context, repo store.Repository, section string) (crawlState, error) {
	state, err := repo.GetSourceState(ctx, a.name, section)
	if errors.Is(err, store.ErrSourceStateNotFound) {
		return crawlState{}, nil
	}
	if err != nil {
		return crawlState{}, err
	}
	return decodeState(state)
}

func (a *torznabAdapter) pageURL(page int) string {
	if page <= 0 {
		page = 1
	}
	u := *a.baseURL
	q := u.Query()
	q.Set("t", "search")
	q.Set("limit", strconv.Itoa(a.pageSize))
	q.Set("offset", strconv.Itoa((page-1)*a.pageSize))
	if a.searchQuery != "" {
		q.Set("q", a.searchQuery)
	}
	if len(a.categoryIDs) > 0 {
		q.Set("cat", torznabIDsString(a.categoryIDs))
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func torznabIDsString(ids []int) string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, strconv.Itoa(id))
	}
	return strings.Join(out, ",")
}

func torznabItemCategories(item torznabItem) []string {
	seen := make(map[string]struct{})
	var out []string
	appendCategory := func(value string) {
		value = strings.TrimSpace(html.UnescapeString(value))
		if value == "" {
			return
		}
		value = torznabCategoryName(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	for _, value := range item.Categories {
		appendCategory(value)
	}
	for _, attr := range item.Attrs {
		if !strings.EqualFold(attr.Name, "category") {
			continue
		}
		appendCategory(attr.Value)
	}
	return out
}

func torznabItemCategory(item torznabItem) string {
	categories := torznabItemCategories(item)
	if len(categories) == 0 {
		return ""
	}
	return categories[0]
}

func torznabIntValue(item torznabItem, name string) int {
	for _, attr := range item.Attrs {
		if !strings.EqualFold(attr.Name, name) {
			continue
		}
		if v, err := strconv.Atoi(strings.TrimSpace(attr.Value)); err == nil && v >= 0 {
			return v
		}
	}
	return 0
}

func torznabSizeValue(item torznabItem) int64 {
	for _, attr := range item.Attrs {
		if !strings.EqualFold(attr.Name, "size") {
			continue
		}
		if v, err := strconv.ParseInt(strings.TrimSpace(attr.Value), 10, 64); err == nil && v >= 0 {
			return v
		}
	}
	if v, err := strconv.ParseInt(strings.TrimSpace(item.Enclosure.Length), 10, 64); err == nil && v >= 0 {
		return v
	}
	return 0
}

func torznabCategoryName(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "2000", "movie", "movies":
		return "movies"
	case "3000", "music":
		return "music"
	case "4000", "app", "apps", "application", "applications":
		return "apps"
	case "4050", "game", "games":
		return "games"
	case "5000", "tv", "television", "series":
		return "tv"
	case "5070", "anime":
		return "anime"
	case "5080", "documentary", "documentaries":
		return "documentaries"
	case "6000", "xxx":
		return "xxx"
	case "7000", "book", "books", "ebook", "ebooks":
		return "books"
	case "8000", "other", "others":
		return "other"
	default:
		return strings.TrimSpace(value)
	}
}

func normalizeTorznabInfoHash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "magnet:?") {
		return infohashFromMagnetURI(value)
	}
	if strings.HasPrefix(lower, "urn:btih:") {
		value = value[len("urn:btih:"):]
	}
	if idx := strings.LastIndex(value, "/"); idx >= 0 && idx < len(value)-1 {
		value = value[idx+1:]
	}
	value = strings.ToUpper(strings.TrimSpace(value))
	if len(value) != 40 {
		return ""
	}
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'A' && r <= 'F':
		default:
			return ""
		}
	}
	return value
}
