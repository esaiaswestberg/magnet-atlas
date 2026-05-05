package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/esaiaswestberg/magnet-atlas/internal/config"
	"github.com/esaiaswestberg/magnet-atlas/internal/domain"
	"github.com/esaiaswestberg/magnet-atlas/internal/store"
)

const (
	defaultRARBGRequestDelay    = time.Second
	defaultRARBGBackoffDelay    = 750 * time.Millisecond
	defaultRARBGRequestAttempts = 3
	defaultRARBGPageWindow      = 1
)

var (
	rarbgTorrentLinkRe = regexp.MustCompile(`(?is)<a[^>]+href="(/torrent/[^"]+-\d+\.html)"[^>]*>(.*?)</a>`)
	rarbgMagnetLinkRe  = regexp.MustCompile(`(?is)href="(magnet:\?[^"]+)"`)
	rarbgTorrentIDRe   = regexp.MustCompile(`-(\d+)\.html$`)
)

type rarbgAdapter struct {
	name            string
	baseURL         *url.URL
	solver          *flareSolverrClient
	sections        []string
	concurrency     int
	pageWindow      int
	requestDelay    time.Duration
	backoffDelay    time.Duration
	requestAttempts int
	sleep           func(time.Duration)
}

// NewRarbgAdapter creates an adapter that crawls fixed sections and extracts
// magnet links from torrent detail pages behind FlareSolverr.
func NewRarbgAdapter(cfg config.SourceConfig) (*rarbgAdapter, error) {
	if strings.TrimSpace(cfg.Name) == "" {
		return nil, fmt.Errorf("source name is required")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("base_url is required")
	}
	baseURL, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil {
		return nil, fmt.Errorf("parse base_url: %w", err)
	}
	if !baseURL.IsAbs() {
		return nil, fmt.Errorf("base_url must be absolute")
	}
	switch baseURL.Scheme {
	case "http", "https":
	default:
		return nil, fmt.Errorf("base_url must use http or https")
	}

	solver, err := newFlareSolverrClient(cfg.FlareSolverrURL)
	if err != nil {
		return nil, err
	}

	sections := normalizeRARBGSections(cfg.Sections)
	if len(sections) == 0 {
		sections = []string{"movies", "tv", "anime"}
	}

	concurrency := cfg.Concurrency
	if concurrency <= 0 {
		concurrency = 4
	}
	pageWindow := cfg.PageWindow
	if pageWindow <= 0 {
		pageWindow = defaultRARBGPageWindow
	}
	requestDelay := cfg.RequestDelay
	if requestDelay <= 0 {
		requestDelay = defaultRARBGRequestDelay
	}
	backoffDelay := cfg.BackoffDelay
	if backoffDelay <= 0 {
		backoffDelay = defaultRARBGBackoffDelay
	}
	requestAttempts := cfg.RequestAttempts
	if requestAttempts <= 0 {
		requestAttempts = defaultRARBGRequestAttempts
	}

	return &rarbgAdapter{
		name:            cfg.Name,
		baseURL:         baseURL,
		solver:          solver,
		sections:        sections,
		concurrency:     concurrency,
		pageWindow:      pageWindow,
		requestDelay:    requestDelay,
		backoffDelay:    backoffDelay,
		requestAttempts: requestAttempts,
		sleep:           time.Sleep,
	}, nil
}

func (a *rarbgAdapter) Name() string { return a.name }

func (a *rarbgAdapter) Fetch(ctx context.Context, repo store.Repository) (FetchResult, error) {
	if repo == nil {
		return FetchResult{}, fmt.Errorf("repository is required")
	}

	var out FetchResult
	out.State = map[string]string{}

	for _, section := range a.sections {
		if slog.Default().Enabled(ctx, slog.LevelDebug) {
			slog.Debug("starting source crawl", "source", a.name, "section", section, "page_window", a.pageWindow)
		}

		sectionPages, err := a.fetchSectionPages(ctx, section)
		if err != nil {
			return FetchResult{}, err
		}

		torrentURLs := a.extractTorrentURLs(ctx, sectionPages)
		if slog.Default().Enabled(ctx, slog.LevelDebug) {
			slog.Debug("parsed section pages", "source", a.name, "section", section, "pages", len(sectionPages), "torrent_urls", len(torrentURLs))
		}
		if len(torrentURLs) == 0 {
			continue
		}

		records, err := a.fetchTorrentPages(ctx, section, torrentURLs)
		if err != nil {
			return FetchResult{}, err
		}
		for _, record := range records {
			out.Torrents = append(out.Torrents, record.torrent)
			out.Observations = append(out.Observations, record.obs)
		}
	}

	return out, nil
}

func (a *rarbgAdapter) fetchSectionPages(ctx context.Context, section string) ([]string, error) {
	pages := make([]string, a.pageWindow)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for page := 1; page <= a.pageWindow; page++ {
		page := page
		wg.Add(1)
		go func() {
			defer wg.Done()
			pageURL := a.pageURL(section, page)
			if slog.Default().Enabled(ctx, slog.LevelDebug) {
				slog.Debug("fetching section page", "source", a.name, "section", section, "page", page, "url", pageURL)
			}
			body, err := a.get(ctx, pageURL, true)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			pages[page-1] = body
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return pages, nil
}

func (a *rarbgAdapter) extractTorrentURLs(ctx context.Context, pages []string) []listingItem {
	items := make([]listingItem, 0)
	seen := make(map[string]struct{})
	for _, body := range pages {
		matches := rarbgTorrentLinkRe.FindAllStringSubmatch(body, -1)
		if slog.Default().Enabled(ctx, slog.LevelDebug) {
			slog.Debug("parsed listing page", "source", a.name, "items", len(matches))
		}
		for _, match := range matches {
			if len(match) < 3 {
				continue
			}
			href := html.UnescapeString(strings.TrimSpace(match[1]))
			if href == "" {
				continue
			}
			url := a.resolve(href)
			if _, ok := seen[url]; ok {
				continue
			}
			seen[url] = struct{}{}
			title := html.UnescapeString(strings.TrimSpace(stripHTML(match[2])))
			if title == "" {
				title = url
			}
			items = append(items, listingItem{
				id:       rarbgItemIDFromURL(url),
				title:    title,
				url:      url,
				category: "",
			})
		}
	}
	return items
}

func (a *rarbgAdapter) fetchTorrentPages(ctx context.Context, section string, items []listingItem) ([]torrentRecord, error) {
	if len(items) == 0 {
		return nil, nil
	}

	limit := a.concurrency
	if limit <= 0 {
		limit = 1
	}
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := make(map[string]struct{}, len(items))
	records := make([]torrentRecord, 0, len(items))
	var firstErr error

	for _, item := range items {
		item := item
		wg.Add(1)
		go func() {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				mu.Lock()
				if firstErr == nil {
					firstErr = ctx.Err()
				}
				mu.Unlock()
				return
			}
			defer func() { <-sem }()

			if slog.Default().Enabled(ctx, slog.LevelDebug) {
				slog.Debug("fetching detail page", "source", a.name, "section", section, "item_id", item.id, "url", item.url)
			}
			body, err := a.get(ctx, item.url, false)
			if err != nil {
				if slog.Default().Enabled(ctx, slog.LevelDebug) {
					slog.Debug("detail page failed", "source", a.name, "section", section, "item_id", item.id, "url", item.url, "error", err)
				}
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}

			magnets := rarbgMagnetLinks(body)
			if slog.Default().Enabled(ctx, slog.LevelDebug) {
				slog.Debug("parsed detail page", "source", a.name, "section", section, "item_id", item.id, "url", item.url, "magnet_count", len(magnets))
			}
			if len(magnets) == 0 {
				if slog.Default().Enabled(ctx, slog.LevelDebug) {
					slog.Debug("detail page yielded no magnet links", "source", a.name, "section", section, "item_id", item.id, "url", item.url, "snippet", rarbgDetailSnippet(body))
				}
				return
			}

			now := time.Now().UTC()
			pageRecords := make([]torrentRecord, 0, len(magnets))
			for idx, magnetURI := range magnets {
				infohash := infohashFromMagnetURI(magnetURI)
				if infohash == "" {
					continue
				}
				key := infohash
				mu.Lock()
				if _, ok := seen[key]; ok {
					mu.Unlock()
					continue
				}
				seen[key] = struct{}{}
				mu.Unlock()

				title := rarbgTitleFromMagnet(magnetURI)
				if title == "" {
					title = item.title
				}
				if title == "" {
					title = item.url
				}
				raw, _ := json.Marshal(map[string]any{
					"section":    section,
					"item_id":    item.id,
					"item_url":   item.url,
					"magnet_uri": magnetURI,
					"index":      idx,
				})
				record := torrentRecord{
					torrent: domain.Torrent{
						InfoHash:    infohash,
						Title:       title,
						Category:    section,
						MagnetURI:   magnetURI,
						DownloadURL: magnetURI,
					},
					obs: domain.SourceObservation{
						Source:      a.name,
						SourceID:    item.id,
						SourceURL:   item.url,
						Title:       title,
						Category:    section,
						MagnetURI:   magnetURI,
						DownloadURL: magnetURI,
						ObservedAt:  now,
						RawJSON:     string(raw),
					},
				}
				pageRecords = append(pageRecords, record)
			}

			if len(pageRecords) == 0 {
				if slog.Default().Enabled(ctx, slog.LevelDebug) {
					slog.Debug("detail page yielded no usable magnet links", "source", a.name, "section", section, "item_id", item.id, "url", item.url, "snippet", rarbgDetailSnippet(body))
				}
				return
			}

			mu.Lock()
			records = append(records, pageRecords...)
			mu.Unlock()
		}()
	}

	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return records, nil
}

func (a *rarbgAdapter) get(ctx context.Context, targetURL string, applyRequestDelay bool) (string, error) {
	backoff := a.backoffDelay
	if backoff <= 0 {
		backoff = defaultRARBGBackoffDelay
	}
	attempts := a.requestAttempts
	if attempts <= 0 {
		attempts = defaultRARBGRequestAttempts
	}

	for attempt := 0; attempt < attempts; attempt++ {
		if applyRequestDelay && attempt == 0 {
			if err := a.wait(ctx, a.requestDelay); err != nil {
				return "", err
			}
		}

		start := time.Now()
		body, _, err := a.solver.get(ctx, targetURL)
		if err == nil {
			if slog.Default().Enabled(ctx, slog.LevelDebug) {
				slog.Debug("fetched flaresolverr response", "source", a.name, "url", targetURL, "bytes", len(body), "duration", time.Since(start))
			}
			return body, nil
		}

		if attempt == attempts-1 || !isTransientRARBGError(err) {
			return "", err
		}
		if slog.Default().Enabled(ctx, slog.LevelDebug) {
			slog.Debug("retrying rarbg fetch", "source", a.name, "url", targetURL, "attempt", attempt+1, "backoff", backoff, "error", err)
		}
		if err := a.wait(ctx, backoff); err != nil {
			return "", err
		}
		backoff *= 2
	}

	return "", fmt.Errorf("rarbg fetch failed after %d attempts", attempts)
}

func (a *rarbgAdapter) wait(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	sleep := a.sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	sleep(delay)
	return nil
}

func (a *rarbgAdapter) pageURL(section string, page int) string {
	base := strings.TrimRight(a.baseURL.String(), "/")
	section = strings.Trim(section, "/")
	if section == "" {
		return base
	}
	return fmt.Sprintf("%s/%s/%d/", base, section, page)
}

func (a *rarbgAdapter) resolve(rel string) string {
	ref, err := url.Parse(rel)
	if err != nil {
		return rel
	}
	return a.baseURL.ResolveReference(ref).String()
}

func rarbgMagnetLinks(body string) []string {
	matches := rarbgMagnetLinkRe.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		uri := html.UnescapeString(strings.TrimSpace(match[1]))
		if uri == "" {
			continue
		}
		if _, ok := seen[uri]; ok {
			continue
		}
		seen[uri] = struct{}{}
		out = append(out, uri)
	}
	return out
}

func rarbgTitleFromMagnet(magnetURI string) string {
	u, err := url.Parse(magnetURI)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(u.Query().Get("dn"))
}

func rarbgItemIDFromURL(raw string) string {
	matches := rarbgTorrentIDRe.FindStringSubmatch(raw)
	if len(matches) >= 2 {
		return matches[1]
	}
	return raw
}

func rarbgDetailSnippet(body string) string {
	if body == "" {
		return ""
	}
	lower := strings.ToLower(body)
	for _, needle := range []string{"href=\"magnet:?xt=urn:btih:", "magnet:?xt=urn:btih:"} {
		idx := strings.Index(lower, needle)
		if idx < 0 {
			continue
		}
		start := idx - 120
		if start < 0 {
			start = 0
		}
		end := idx + 260
		if end > len(body) {
			end = len(body)
		}
		snippet := strings.TrimSpace(body[start:end])
		snippet = strings.ReplaceAll(snippet, "\n", " ")
		snippet = strings.ReplaceAll(snippet, "\r", " ")
		return snippet
	}
	if len(body) > 320 {
		body = body[:320]
	}
	body = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(body, "\n", " "), "\r", " "))
	return body
}

func normalizeRARBGSections(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		value = strings.Trim(value, "/")
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func isTransientRARBGError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, token := range []string{
		"connection reset",
		"reset by peer",
		"broken pipe",
		"temporarily unavailable",
		"timeout",
		"timed out",
		"error solving the challenge",
		"unknown error",
	} {
		if strings.Contains(msg, token) {
			return true
		}
	}
	return false
}
