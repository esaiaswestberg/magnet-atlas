package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/esaiaswestberg/magnet-atlas/internal/config"
	"github.com/esaiaswestberg/magnet-atlas/internal/domain"
	"github.com/esaiaswestberg/magnet-atlas/internal/store"
)

const defaultUIndexBaseURL = "https://uindex.org"

var (
	uindexListingLinkRe = regexp.MustCompile(`(?is)<a[^>]+href="([^"]*?/details\.php\?id=(\d+)[^"]*)"[^>]*>(.*?)</a>`)
	uindexTitleRe       = regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
	uindexPageTitleRe   = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	uindexCategoryRe    = regexp.MustCompile(`(?im)^\s*Category\s*:?\s*(.+?)\s*$`)
	uindexSizeRe        = regexp.MustCompile(`(?im)^\s*(?:Total size|Size|File Size)\s*:?\s*([0-9.]+\s*[A-Za-z]+)\s*$`)
	uindexInfoHashRe    = regexp.MustCompile(`(?im)^\s*Info Hash\s*:?\s*([A-Fa-f0-9]{40})\s*$`)
	uindexAddedAtRe     = regexp.MustCompile(`(?im)^\s*Added\s*:?.*?\((\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})\)\s*$`)
	uindexShareRe       = regexp.MustCompile(`(?im)^\s*(?:Share ratio:\s*)?(\d+)\s+seeders?,\s*(\d+)\s+leechers?\s*$`)
	uindexSeedersRe     = regexp.MustCompile(`(?im)^\s*Seeders\s*:?\s*(\d+)\s*$`)
	uindexLeechersRe    = regexp.MustCompile(`(?im)^\s*Leechers\s*:?\s*(\d+)\s*$`)
)

type uindexAdapter struct {
	name        string
	baseURL     *url.URL
	client      *http.Client
	concurrency int
	pageWindow  int
	sections    []crawlSeed
}

type uindexDetail struct {
	torrent domain.Torrent
	obs     domain.SourceObservation
}

// NewUIndexAdapter creates an adapter for UIndex browse and detail pages.
func NewUIndexAdapter(cfg config.SourceConfig) (*uindexAdapter, error) {
	if strings.TrimSpace(cfg.Name) == "" {
		return nil, fmt.Errorf("source name is required")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = defaultUIndexBaseURL
	}
	baseURL, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base_url: %w", err)
	}
	if !baseURL.IsAbs() {
		return nil, fmt.Errorf("base_url must be absolute")
	}

	sections := []crawlSeed{{name: "latest", path: "/search.php"}}
	if len(cfg.Categories) > 0 {
		categories := config.NormalizeUIndexCategories(cfg.Categories)
		if len(categories) == 0 {
			return nil, fmt.Errorf("no UIndex categories selected")
		}
		sections = append(sections, uindexSeedsForCategories(categories)...)
	}

	concurrency := cfg.Concurrency
	if concurrency <= 0 {
		concurrency = 4
	}
	pageWindow := cfg.PageWindow
	if pageWindow <= 0 {
		pageWindow = cfg.MaxPages
	}
	if pageWindow <= 0 {
		pageWindow = 20
	}

	return &uindexAdapter{
		name:        cfg.Name,
		baseURL:     baseURL,
		client:      &http.Client{Timeout: 30 * time.Second},
		concurrency: concurrency,
		pageWindow:  pageWindow,
		sections:    sections,
	}, nil
}

func (a *uindexAdapter) Name() string { return a.name }

func (a *uindexAdapter) Fetch(ctx context.Context, repo store.Repository) (FetchResult, error) {
	if repo == nil {
		return FetchResult{}, fmt.Errorf("repository is required")
	}

	var out FetchResult
	out.State = make(map[string]string)

	for _, section := range a.sections {
		state, err := a.loadState(ctx, repo, section.name)
		if err != nil {
			return FetchResult{}, err
		}

		if slog.Default().Enabled(ctx, slog.LevelDebug) {
			slog.Debug("starting source crawl", "source", a.name, "section", section.name, "path", section.path, "state", state.WindowEndPage)
		}

		var sectionTorrents []domain.Torrent
		var sectionObservations []domain.SourceObservation
		var nextState int

		if state.WindowEndPage <= 0 {
			res, lastPage, err := a.fetchWindow(ctx, repo, section, 1, a.pageWindow)
			if err != nil {
				return FetchResult{}, err
			}
			sectionTorrents = append(sectionTorrents, res.Torrents...)
			sectionObservations = append(sectionObservations, res.Observations...)
			nextState = lastPage
		} else {
			probeRes, probePages, err := a.probeFrontier(ctx, repo, section, a.pageWindow)
			if err != nil {
				return FetchResult{}, err
			}
			sectionTorrents = append(sectionTorrents, probeRes.Torrents...)
			sectionObservations = append(sectionObservations, probeRes.Observations...)

			start := state.WindowEndPage + probePages + 1
			windowRes, lastPage, err := a.fetchWindow(ctx, repo, section, start, a.pageWindow)
			if err != nil {
				return FetchResult{}, err
			}
			sectionTorrents = append(sectionTorrents, windowRes.Torrents...)
			sectionObservations = append(sectionObservations, windowRes.Observations...)
			nextState = lastPage
		}

		if nextState > 0 {
			out.State[section.name] = encodeState(crawlState{WindowEndPage: nextState})
		}
		out.Torrents = append(out.Torrents, sectionTorrents...)
		out.Observations = append(out.Observations, sectionObservations...)
	}

	return out, nil
}

func (a *uindexAdapter) probeFrontier(ctx context.Context, repo store.Repository, section crawlSeed, limit int) (FetchResult, int, error) {
	var out FetchResult
	pagesBeforeBoundary := 0
	for page := 1; page <= limit; page++ {
		records, empty, foundKnown, err := a.collectPage(ctx, repo, section, page)
		if err != nil {
			return FetchResult{}, 0, err
		}
		if empty {
			return out, pagesBeforeBoundary, nil
		}
		if foundKnown {
			if slog.Default().Enabled(ctx, slog.LevelDebug) {
				slog.Debug("frontier reached", "source", a.name, "section", section.name, "page", page)
			}
			return out, pagesBeforeBoundary, nil
		}
		for _, record := range records {
			out.Torrents = append(out.Torrents, record.torrent)
			out.Observations = append(out.Observations, record.obs)
		}
		pagesBeforeBoundary = page
	}
	return out, pagesBeforeBoundary, nil
}

func (a *uindexAdapter) fetchWindow(ctx context.Context, repo store.Repository, section crawlSeed, startPage, limit int) (FetchResult, int, error) {
	var out FetchResult
	lastPage := 0
	for page := startPage; page < startPage+limit; page++ {
		if slog.Default().Enabled(ctx, slog.LevelDebug) {
			slog.Debug("fetching window page", "source", a.name, "section", section.name, "page", page)
		}
		records, empty, _, err := a.collectPage(ctx, repo, section, page)
		if err != nil {
			return FetchResult{}, 0, err
		}
		if empty {
			break
		}
		for _, record := range records {
			out.Torrents = append(out.Torrents, record.torrent)
			out.Observations = append(out.Observations, record.obs)
		}
		lastPage = page
	}
	return out, lastPage, nil
}

func (a *uindexAdapter) collectPage(ctx context.Context, repo store.Repository, section crawlSeed, page int) ([]uindexDetail, bool, bool, error) {
	listingURL := a.pageURL(section.path, page)
	if slog.Default().Enabled(ctx, slog.LevelDebug) {
		slog.Debug("fetching listing page", "source", a.name, "section", section.name, "page", page, "url", listingURL)
	}
	items, err := a.fetchListingPage(ctx, listingURL, section.name)
	if err != nil {
		return nil, false, false, err
	}
	if len(items) == 0 {
		return nil, true, false, nil
	}
	records := a.fetchDetails(ctx, items)
	out := make([]uindexDetail, 0, len(records))
	foundKnown := false
	for _, rec := range records {
		if rec.torrent.InfoHash == "" {
			continue
		}
		known, err := repo.HasInfohash(ctx, rec.torrent.InfoHash)
		if err != nil {
			return nil, false, false, err
		}
		if known {
			foundKnown = true
		}
		out = append(out, rec)
	}
	return out, false, foundKnown, nil
}

func (a *uindexAdapter) fetchListingPage(ctx context.Context, pageURL string, section string) ([]listingItem, error) {
	body, status, err := a.get(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("listing page %s: unexpected status %d", pageURL, status)
	}
	matches := uindexListingLinkRe.FindAllStringSubmatch(body, -1)
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
			category: section,
		})
	}
	return items, nil
}

func (a *uindexAdapter) fetchDetails(ctx context.Context, items []listingItem) []uindexDetail {
	out := make([]uindexDetail, len(items))
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
			slog.Debug("fetching detail page", "source", a.name, "item_id", item.id, "url", item.url)
			detail, err := a.fetchDetail(ctx, item)
			if err != nil {
				slog.Debug("detail page failed", "source", a.name, "item_id", item.id, "url", item.url, "error", err)
				return
			}
			out[i] = detail
		}(i)
	}
	wg.Wait()
	return out
}

func (a *uindexAdapter) fetchDetail(ctx context.Context, item listingItem) (uindexDetail, error) {
	body, status, err := a.get(ctx, item.url)
	if err != nil {
		return uindexDetail{}, err
	}
	if status != http.StatusOK {
		return uindexDetail{}, fmt.Errorf("detail page %s: unexpected status %d", item.url, status)
	}

	text := stripHTML(body)
	title := html.UnescapeString(strings.TrimSpace(findMatch(body, uindexTitleRe)))
	if title == "" {
		title = html.UnescapeString(strings.TrimSpace(findMatch(body, uindexPageTitleRe)))
	}
	title = html.UnescapeString(strings.TrimSpace(title))
	if title == "" {
		title = item.title
	}
	infohash := strings.ToUpper(findMatch(text, uindexInfoHashRe))
	magnetURI := firstMagnetURI(body)
	if infohash == "" && magnetURI != "" {
		infohash = infohashFromMagnetURI(magnetURI)
	}
	if infohash == "" {
		return uindexDetail{}, errors.New("infohash not found")
	}
	if magnetURI == "" {
		magnetURI = "magnet:?xt=urn:btih:" + infohash
	}

	category := findMatch(text, uindexCategoryRe)
	if category == "" {
		category = item.category
	}

	sizeBytes := int64(0)
	if sizeText := findMatch(text, uindexSizeRe); sizeText != "" {
		if parsed, err := parseHumanSize(sizeText); err == nil {
			sizeBytes = parsed
		}
	}

	seeders := 0
	leechers := 0
	if pair := uindexShareRe.FindStringSubmatch(text); len(pair) >= 3 {
		seeders, _ = strconv.Atoi(pair[1])
		leechers, _ = strconv.Atoi(pair[2])
	} else {
		if s := findMatch(text, uindexSeedersRe); s != "" {
			seeders, _ = strconv.Atoi(s)
		}
		if l := findMatch(text, uindexLeechersRe); l != "" {
			leechers, _ = strconv.Atoi(l)
		}
	}

	publishedAt := time.Time{}
	if added := findMatch(text, uindexAddedAtRe); added != "" {
		if ts, err := time.ParseInLocation("2006-01-02 15:04:05", added, time.UTC); err == nil {
			publishedAt = ts
		}
	}

	raw, _ := json.Marshal(map[string]any{
		"item_id":      item.id,
		"item_title":   item.title,
		"detail_url":   item.url,
		"title":        title,
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
		Title:       title,
		Category:    category,
		SizeBytes:   sizeBytes,
		Seeders:     seeders,
		Leechers:    leechers,
		PublishedAt: publishedAt,
		MagnetURI:   magnetURI,
		DownloadURL: magnetURI,
	}
	obs := domain.SourceObservation{
		Source:      a.name,
		SourceID:    item.id,
		SourceURL:   item.url,
		Title:       title,
		Category:    category,
		SizeBytes:   sizeBytes,
		Seeders:     seeders,
		Leechers:    leechers,
		PublishedAt: publishedAt,
		MagnetURI:   magnetURI,
		DownloadURL: magnetURI,
		ObservedAt:  time.Now().UTC(),
		RawJSON:     string(raw),
	}
	return uindexDetail{torrent: torrent, obs: obs}, nil
}

func (a *uindexAdapter) get(ctx context.Context, pageURL string) (string, int, error) {
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
	slog.Debug("fetched http response", "url", pageURL, "status", resp.StatusCode, "bytes", len(b), "duration", time.Since(start))
	return string(b), resp.StatusCode, nil
}

func (a *uindexAdapter) loadState(ctx context.Context, repo store.Repository, section string) (crawlState, error) {
	state, err := repo.GetSourceState(ctx, a.name, section)
	if errors.Is(err, store.ErrSourceStateNotFound) {
		return crawlState{}, nil
	}
	if err != nil {
		return crawlState{}, err
	}
	return decodeState(state)
}

func (a *uindexAdapter) pageURL(sectionPath string, page int) string {
	u, err := url.Parse(sectionPath)
	if err != nil {
		return a.resolve(sectionPath)
	}
	q := u.Query()
	q.Set("p", strconv.Itoa(page))
	u.RawQuery = q.Encode()
	return a.resolve(u.String())
}

func (a *uindexAdapter) resolve(rel string) string {
	ref, err := url.Parse(rel)
	if err != nil {
		return rel
	}
	return a.baseURL.ResolveReference(ref).String()
}

func uindexSeedsForCategories(categories []string) []crawlSeed {
	sections := make([]crawlSeed, 0, len(categories))
	for _, category := range categories {
		if seed, ok := uindexCategorySeed(category); ok {
			sections = append(sections, seed)
		}
	}
	return sections
}

func uindexCategorySeed(category string) (crawlSeed, bool) {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "movies":
		return crawlSeed{name: "movies", path: "/search.php?c=1"}, true
	case "tv":
		return crawlSeed{name: "tv", path: "/search.php?c=2"}, true
	case "games":
		return crawlSeed{name: "games", path: "/search.php?c=3"}, true
	case "music":
		return crawlSeed{name: "music", path: "/search.php?c=4"}, true
	case "apps":
		return crawlSeed{name: "apps", path: "/search.php?c=5"}, true
	case "xxx":
		return crawlSeed{name: "xxx", path: "/search.php?c=6"}, true
	case "anime":
		return crawlSeed{name: "anime", path: "/search.php?c=7"}, true
	case "other":
		return crawlSeed{name: "other", path: "/search.php?c=8"}, true
	default:
		return crawlSeed{}, false
	}
}
