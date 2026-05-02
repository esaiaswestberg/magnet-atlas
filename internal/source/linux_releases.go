package source

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
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

const defaultLinuxReleasesBaseURL = "https://releases.ubuntu.com/"

var (
	linuxReleaseLinkRe = regexp.MustCompile(`(?is)<a[^>]+href="([^"]+/)"[^>]*>(.*?)</a>`)
	linuxTorrentLinkRe = regexp.MustCompile(`(?is)<a[^>]+href="([^"]+\.torrent(?:\?[^"]*)?)"[^>]*>(.*?)</a>`)
	linuxTitleRe       = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	linuxH1Re          = regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
)

type linuxReleasesAdapter struct {
	name         string
	baseURL      *url.URL
	client       *http.Client
	concurrency  int
	requestDelay time.Duration
	releasePaths []string
}

type linuxReleaseTarget struct {
	name string
	url  string
}

type linuxTorrentCandidate struct {
	releaseName string
	fileName    string
	url         string
}

type linuxTorrentRecord struct {
	torrent domain.Torrent
	obs     domain.SourceObservation
}

type linuxTorrentInfo struct {
	name string
	size int64
}

// NewLinuxReleasesAdapter creates an adapter for official Linux release torrent pages.
func NewLinuxReleasesAdapter(cfg config.SourceConfig) (*linuxReleasesAdapter, error) {
	if strings.TrimSpace(cfg.Name) == "" {
		return nil, fmt.Errorf("source name is required")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = defaultLinuxReleasesBaseURL
	}
	baseURL, err := url.Parse(cfg.BaseURL)
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

	concurrency := cfg.Concurrency
	if concurrency <= 0 {
		concurrency = 4
	}

	return &linuxReleasesAdapter{
		name:         cfg.Name,
		baseURL:      baseURL,
		client:       &http.Client{Timeout: 30 * time.Second},
		concurrency:  concurrency,
		requestDelay: cfg.RequestDelay,
		releasePaths: normalizeLinuxReleasePaths(cfg.ReleasePaths),
	}, nil
}

func (a *linuxReleasesAdapter) Name() string { return a.name }

func (a *linuxReleasesAdapter) Fetch(ctx context.Context, repo store.Repository) (FetchResult, error) {
	if repo == nil {
		return FetchResult{}, fmt.Errorf("repository is required")
	}

	targets, err := a.discoverReleaseTargets(ctx)
	if err != nil {
		return FetchResult{}, err
	}

	candidates := make([]linuxTorrentCandidate, 0)
	for _, target := range targets {
		pageCandidates, err := a.fetchReleasePage(ctx, target)
		if err != nil {
			return FetchResult{}, err
		}
		candidates = append(candidates, pageCandidates...)
	}

	records := a.fetchDetails(ctx, candidates)
	var result FetchResult
	seen := make(map[string]struct{}, len(records))
	for _, rec := range records {
		if rec.torrent.InfoHash == "" {
			continue
		}
		if _, exists := seen[rec.torrent.InfoHash]; exists {
			continue
		}
		known, err := repo.HasInfohash(ctx, rec.torrent.InfoHash)
		if err != nil {
			return FetchResult{}, err
		}
		if known {
			continue
		}
		seen[rec.torrent.InfoHash] = struct{}{}
		result.Torrents = append(result.Torrents, rec.torrent)
		result.Observations = append(result.Observations, rec.obs)
	}
	return result, nil
}

func (a *linuxReleasesAdapter) discoverReleaseTargets(ctx context.Context) ([]linuxReleaseTarget, error) {
	if len(a.releasePaths) > 0 {
		targets := make([]linuxReleaseTarget, 0, len(a.releasePaths))
		seen := make(map[string]struct{}, len(a.releasePaths))
		for _, rel := range a.releasePaths {
			url := a.resolve(rel)
			if _, ok := seen[url]; ok {
				continue
			}
			seen[url] = struct{}{}
			targets = append(targets, linuxReleaseTarget{name: releaseNameFromPath(rel), url: url})
		}
		return targets, nil
	}

	body, status, err := a.get(ctx, a.baseURL.String())
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("release index %s: unexpected status %d", a.baseURL.String(), status)
	}

	matches := linuxReleaseLinkRe.FindAllStringSubmatch(body, -1)
	targets := make([]linuxReleaseTarget, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		href := html.UnescapeString(strings.TrimSpace(match[1]))
		if !isLinuxReleasePath(href) {
			continue
		}
		targetURL := a.resolve(href)
		if _, ok := seen[targetURL]; ok {
			continue
		}
		seen[targetURL] = struct{}{}
		name := html.UnescapeString(strings.TrimSpace(stripHTML(match[2])))
		if name == "" {
			name = releaseNameFromPath(href)
		}
		targets = append(targets, linuxReleaseTarget{name: name, url: targetURL})
	}
	return targets, nil
}

func (a *linuxReleasesAdapter) fetchReleasePage(ctx context.Context, target linuxReleaseTarget) ([]linuxTorrentCandidate, error) {
	body, status, err := a.get(ctx, target.url)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("release page %s: unexpected status %d", target.url, status)
	}

	releaseName := target.name
	if pageTitle := html.UnescapeString(strings.TrimSpace(findMatch(body, linuxTitleRe))); pageTitle != "" {
		releaseName = pageTitle
	}
	if h1 := html.UnescapeString(strings.TrimSpace(findMatch(body, linuxH1Re))); h1 != "" {
		releaseName = h1
	}
	if releaseName == "" {
		releaseName = releaseNameFromPath(target.url)
	}

	matches := linuxTorrentLinkRe.FindAllStringSubmatch(body, -1)
	candidates := make([]linuxTorrentCandidate, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		href := html.UnescapeString(strings.TrimSpace(match[1]))
		if href == "" {
			continue
		}
		torrentURL := resolveAgainst(target.url, href)
		if _, ok := seen[torrentURL]; ok {
			continue
		}
		seen[torrentURL] = struct{}{}
		fileName := strings.TrimSpace(stripHTML(match[2]))
		if fileName == "" {
			fileName = pathBase(href)
		}
		candidates = append(candidates, linuxTorrentCandidate{
			releaseName: releaseName,
			fileName:    fileName,
			url:         torrentURL,
		})
	}
	return candidates, nil
}

func (a *linuxReleasesAdapter) fetchDetails(ctx context.Context, candidates []linuxTorrentCandidate) []linuxTorrentRecord {
	out := make([]linuxTorrentRecord, len(candidates))
	if len(candidates) == 0 {
		return out
	}

	limit := a.concurrency
	if limit <= 0 {
		limit = 1
	}

	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i := range candidates {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			candidate := candidates[i]
			slog.Debug("fetching torrent file", "source", a.name, "release", candidate.releaseName, "file", candidate.fileName, "url", candidate.url)
			record, err := a.fetchDetail(ctx, candidate)
			if err != nil {
				slog.Debug("torrent file failed", "source", a.name, "release", candidate.releaseName, "file", candidate.fileName, "url", candidate.url, "error", err)
				return
			}
			out[i] = record
		}(i)
	}
	wg.Wait()
	return out
}

func (a *linuxReleasesAdapter) fetchDetail(ctx context.Context, candidate linuxTorrentCandidate) (linuxTorrentRecord, error) {
	body, status, err := a.get(ctx, candidate.url)
	if err != nil {
		return linuxTorrentRecord{}, err
	}
	if status != http.StatusOK {
		return linuxTorrentRecord{}, fmt.Errorf("torrent file %s: unexpected status %d", candidate.url, status)
	}

	infohash, info, err := torrentMetadata([]byte(body))
	if err != nil {
		return linuxTorrentRecord{}, err
	}
	if infohash == "" {
		return linuxTorrentRecord{}, errors.New("infohash not found")
	}

	title := strings.TrimSpace(info.name)
	if title == "" {
		title = strings.TrimSpace(strings.TrimSuffix(candidate.fileName, ".torrent"))
	}
	if title == "" {
		title = strings.TrimSpace(candidate.releaseName)
	}
	if title == "" {
		title = infohash
	}
	magnetURI := buildLinuxMagnetURI(infohash, title, info.size, candidate.url)
	extraText := extraTextValues(candidate.releaseName, candidate.fileName)

	raw, _ := json.Marshal(map[string]any{
		"release_name": candidate.releaseName,
		"file_name":    candidate.fileName,
		"torrent_url":  candidate.url,
		"infohash":     infohash,
		"info_name":    info.name,
		"size_bytes":   info.size,
	})

	torrent := domain.Torrent{
		InfoHash:    infohash,
		Title:       title,
		Category:    candidate.releaseName,
		MagnetURI:   magnetURI,
		DownloadURL: candidate.url,
		ExtraText:   extraText,
	}
	obs := domain.SourceObservation{
		Source:      a.name,
		SourceID:    candidate.url,
		SourceURL:   candidate.url,
		Title:       title,
		Category:    candidate.releaseName,
		MagnetURI:   magnetURI,
		DownloadURL: candidate.url,
		ObservedAt:  time.Now().UTC(),
		RawJSON:     string(raw),
		ExtraText:   extraText,
	}
	return linuxTorrentRecord{torrent: torrent, obs: obs}, nil
}

func (a *linuxReleasesAdapter) get(ctx context.Context, pageURL string) (string, int, error) {
	if err := a.wait(ctx); err != nil {
		return "", 0, err
	}
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
	slog.Debug("fetched linux release response", "url", pageURL, "status", resp.StatusCode, "bytes", len(b), "duration", time.Since(start))
	return string(b), resp.StatusCode, nil
}

func (a *linuxReleasesAdapter) wait(ctx context.Context) error {
	if a.requestDelay <= 0 {
		return nil
	}
	timer := time.NewTimer(a.requestDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (a *linuxReleasesAdapter) resolve(rel string) string {
	ref, err := url.Parse(rel)
	if err != nil {
		return rel
	}
	return a.baseURL.ResolveReference(ref).String()
}

func resolveAgainst(baseURL, rel string) string {
	base, err := url.Parse(baseURL)
	if err != nil {
		return rel
	}
	ref, err := url.Parse(rel)
	if err != nil {
		return rel
	}
	return base.ResolveReference(ref).String()
}

func normalizeLinuxReleasePaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}

func isLinuxReleasePath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || strings.HasPrefix(path, "../") {
		return false
	}
	if strings.Contains(path, ".torrent") || strings.Contains(path, ".iso") {
		return false
	}
	return strings.HasSuffix(path, "/")
}

func releaseNameFromPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimSuffix(path, "/")
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return "releases"
	}
	parts := strings.Split(path, "/")
	return parts[len(parts)-1]
}

func pathBase(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	path = strings.TrimSuffix(path, "/")
	parts := strings.Split(path, "/")
	return parts[len(parts)-1]
}

func torrentInfoHash(data []byte) (string, error) {
	infohash, _, err := torrentMetadata(data)
	return infohash, err
}

func torrentMetadata(data []byte) (string, linuxTorrentInfo, error) {
	i := 0
	if len(data) == 0 || data[i] != 'd' {
		return "", linuxTorrentInfo{}, fmt.Errorf("torrent file must be a bencoded dictionary")
	}
	i++
	var meta linuxTorrentInfo
	for i < len(data) && data[i] != 'e' {
		key, err := parseBencodeString(data, &i)
		if err != nil {
			return "", linuxTorrentInfo{}, err
		}
		if key == "info" {
			start := i
			infoMeta, err := parseTorrentInfo(data, &i)
			if err != nil {
				return "", linuxTorrentInfo{}, err
			}
			sum := sha1.Sum(data[start:i])
			if infoMeta.name != "" {
				meta.name = infoMeta.name
			}
			if infoMeta.size > 0 {
				meta.size = infoMeta.size
			}
			return strings.ToUpper(hex.EncodeToString(sum[:])), meta, nil
		}
		if err := skipBencodeValue(data, &i); err != nil {
			return "", linuxTorrentInfo{}, err
		}
	}
	return "", linuxTorrentInfo{}, errors.New("info dictionary not found")
}

func parseTorrentInfo(data []byte, i *int) (linuxTorrentInfo, error) {
	if *i >= len(data) || data[*i] != 'd' {
		return linuxTorrentInfo{}, fmt.Errorf("info value must be a dictionary")
	}
	*i++
	var info linuxTorrentInfo
	for *i < len(data) && data[*i] != 'e' {
		key, err := parseBencodeString(data, i)
		if err != nil {
			return linuxTorrentInfo{}, err
		}
		switch key {
		case "name":
			name, err := parseBencodeString(data, i)
			if err != nil {
				return linuxTorrentInfo{}, err
			}
			info.name = name
		case "length":
			size, err := parseBencodeInt(data, i)
			if err != nil {
				return linuxTorrentInfo{}, err
			}
			info.size = size
		case "files":
			size, err := parseTorrentFilesSize(data, i)
			if err != nil {
				return linuxTorrentInfo{}, err
			}
			if size > 0 {
				info.size = size
			}
		default:
			if err := skipBencodeValue(data, i); err != nil {
				return linuxTorrentInfo{}, err
			}
		}
	}
	if *i >= len(data) {
		return linuxTorrentInfo{}, io.ErrUnexpectedEOF
	}
	*i++
	return info, nil
}

func parseTorrentFilesSize(data []byte, i *int) (int64, error) {
	if *i >= len(data) || data[*i] != 'l' {
		return 0, fmt.Errorf("files value must be a list")
	}
	*i++
	var total int64
	for *i < len(data) && data[*i] != 'e' {
		size, err := parseTorrentFileDictSize(data, i)
		if err != nil {
			return 0, err
		}
		total += size
	}
	if *i >= len(data) {
		return 0, io.ErrUnexpectedEOF
	}
	*i++
	return total, nil
}

func parseTorrentFileDictSize(data []byte, i *int) (int64, error) {
	if *i >= len(data) || data[*i] != 'd' {
		return 0, fmt.Errorf("file entry must be a dictionary")
	}
	*i++
	var size int64
	for *i < len(data) && data[*i] != 'e' {
		key, err := parseBencodeString(data, i)
		if err != nil {
			return 0, err
		}
		switch key {
		case "length":
			size, err = parseBencodeInt(data, i)
			if err != nil {
				return 0, err
			}
		default:
			if err := skipBencodeValue(data, i); err != nil {
				return 0, err
			}
		}
	}
	if *i >= len(data) {
		return 0, io.ErrUnexpectedEOF
	}
	*i++
	return size, nil
}

func parseBencodeInt(data []byte, i *int) (int64, error) {
	if *i >= len(data) || data[*i] != 'i' {
		return 0, fmt.Errorf("expected bencode integer at offset %d", *i)
	}
	*i++
	start := *i
	for *i < len(data) && data[*i] != 'e' {
		*i++
	}
	if *i >= len(data) {
		return 0, io.ErrUnexpectedEOF
	}
	value, err := strconv.ParseInt(string(data[start:*i]), 10, 64)
	if err != nil {
		return 0, err
	}
	*i++
	return value, nil
}

func buildLinuxMagnetURI(infohash, displayName string, sizeBytes int64, sourceURL string) string {
	var b strings.Builder
	b.WriteString("magnet:?xt=urn:btih:")
	b.WriteString(strings.ToUpper(strings.TrimSpace(infohash)))
	if displayName != "" {
		b.WriteString("&dn=")
		b.WriteString(url.QueryEscape(displayName))
	}
	if sizeBytes > 0 {
		b.WriteString("&xl=")
		b.WriteString(strconv.FormatInt(sizeBytes, 10))
	}
	if sourceURL != "" {
		b.WriteString("&xs=")
		b.WriteString(url.QueryEscape(sourceURL))
	}
	return b.String()
}

func parseBencodeString(data []byte, i *int) (string, error) {
	start := *i
	for *i < len(data) && data[*i] != ':' {
		if data[*i] < '0' || data[*i] > '9' {
			return "", fmt.Errorf("invalid bencode string length at offset %d", start)
		}
		*i++
	}
	if *i >= len(data) || data[*i] != ':' {
		return "", fmt.Errorf("invalid bencode string at offset %d", start)
	}
	length, err := strconv.Atoi(string(data[start:*i]))
	if err != nil {
		return "", err
	}
	*i++
	end := *i + length
	if end > len(data) {
		return "", fmt.Errorf("bencode string out of bounds")
	}
	value := string(data[*i:end])
	*i = end
	return value, nil
}

func skipBencodeValue(data []byte, i *int) error {
	if *i >= len(data) {
		return io.ErrUnexpectedEOF
	}
	switch data[*i] {
	case 'i':
		*i++
		for *i < len(data) && data[*i] != 'e' {
			*i++
		}
		if *i >= len(data) {
			return io.ErrUnexpectedEOF
		}
		*i++
		return nil
	case 'l', 'd':
		kind := data[*i]
		*i++
		for *i < len(data) && data[*i] != 'e' {
			if kind == 'd' {
				if _, err := parseBencodeString(data, i); err != nil {
					return err
				}
			}
			if err := skipBencodeValue(data, i); err != nil {
				return err
			}
		}
		if *i >= len(data) {
			return io.ErrUnexpectedEOF
		}
		*i++
		return nil
	default:
		if data[*i] < '0' || data[*i] > '9' {
			return fmt.Errorf("unexpected bencode token %q at offset %d", data[*i], *i)
		}
		_, err := parseBencodeString(data, i)
		return err
	}
}
