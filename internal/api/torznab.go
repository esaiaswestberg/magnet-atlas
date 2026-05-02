package api

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/esaiaswestberg/magnet-atlas/internal/domain"
	"github.com/esaiaswestberg/magnet-atlas/internal/store"
)

const torznabNS = "http://torznab.com/schemas/2015/feed"

type torznabCategory struct {
	ID       int
	Name     string
	Title    string
	Internal []string
}

type torznabCatalog struct {
	categories       []torznabCategory
	internalToIDs    map[string][]int
	customByName     map[string]int
	knownCategoryIDs map[int]struct{}
}

func (s *Server) torznab(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeTorznabError(w, torznabError{Code: 201, Description: "Incorrect parameter"})
		return
	}

	if err := s.requireTorznabAPIKey(r); err != nil {
		writeTorznabError(w, torznabError{Code: 100, Description: "Incorrect user credentials"})
		return
	}

	mode := strings.ToLower(strings.TrimSpace(queryValueFold(r.URL.Query(), "t")))
	switch mode {
	case "caps":
		s.writeTorznabCaps(w, r)
	case "search", "tvsearch", "movie":
		s.writeTorznabSearch(w, r)
	case "get":
		s.writeTorznabGet(w, r)
	default:
		writeTorznabError(w, torznabError{Code: 201, Description: "Incorrect parameter"})
	}
}

func (s *Server) requireTorznabAPIKey(r *http.Request) error {
	if len(s.torznabKeys) == 0 {
		return nil
	}
	key := strings.TrimSpace(queryValueFold(r.URL.Query(), "apikey"))
	if key == "" {
		return fmt.Errorf("missing key")
	}
	if _, ok := s.torznabKeys[key]; !ok {
		return fmt.Errorf("invalid key")
	}
	return nil
}

func (s *Server) writeTorznabCaps(w http.ResponseWriter, r *http.Request) {
	catalog, err := s.buildTorznabCatalog(r.Context())
	if err != nil {
		writeTorznabError(w, torznabError{Code: 201, Description: "Incorrect parameter"})
		return
	}
	writeXML(w, http.StatusOK, s.renderTorznabCaps(r, catalog))
}

func (s *Server) writeTorznabSearch(w http.ResponseWriter, r *http.Request) {
	limit, err := parseTorznabInt(queryValueFold(r.URL.Query(), "limit"), 100, 100)
	if err != nil {
		writeTorznabError(w, torznabError{Code: 201, Description: "Incorrect parameter"})
		return
	}
	offset, err := parseTorznabInt(queryValueFold(r.URL.Query(), "offset"), 0, -1)
	if err != nil {
		writeTorznabError(w, torznabError{Code: 201, Description: "Incorrect parameter"})
		return
	}

	catalog, err := s.buildTorznabCatalog(r.Context())
	if err != nil {
		writeTorznabError(w, torznabError{Code: 201, Description: "Incorrect parameter"})
		return
	}

	catIDs, err := parseTorznabCategories(queryValueFold(r.URL.Query(), "cat"), catalog)
	if err != nil {
		writeTorznabError(w, torznabError{Code: 201, Description: "Incorrect parameter"})
		return
	}
	rawCat := strings.TrimSpace(queryValueFold(r.URL.Query(), "cat"))
	if rawCat != "" && len(catIDs) == 0 {
		writeXML(w, http.StatusOK, s.renderTorznabSearch(r, catalog, nil, offset))
		return
	}

	filter := store.SearchFilter{
		Query:      queryValueFold(r.URL.Query(), "q"),
		Limit:      limit,
		Offset:     offset,
		Categories: catalog.categoriesForIDs(catIDs),
	}
	items, err := s.repo.Search(r.Context(), filter)
	if err != nil {
		writeTorznabError(w, torznabError{Code: 201, Description: "Incorrect parameter"})
		return
	}

	writeXML(w, http.StatusOK, s.renderTorznabSearch(r, catalog, items, offset))
}

func (s *Server) writeTorznabGet(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(queryValueFold(r.URL.Query(), "id"))
	if id == "" {
		id = strings.TrimSpace(queryValueFold(r.URL.Query(), "guid"))
	}
	if id == "" {
		writeTorznabError(w, torznabError{Code: 200, Description: "Missing parameter: id"})
		return
	}
	infohash := normalizeTorznabGUID(id)
	details, err := s.repo.Get(r.Context(), infohash)
	if err != nil {
		writeTorznabError(w, torznabError{Code: 300, Description: "No such GUID"})
		return
	}

	target := details.Torrent.DownloadURL
	if target == "" {
		target = details.Torrent.MagnetURI
	}
	if target == "" {
		writeTorznabError(w, torznabError{Code: 300, Description: "No such GUID"})
		return
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) buildTorznabCatalog(ctx context.Context) (*torznabCatalog, error) {
	categories, err := s.repo.ListCategories(ctx)
	if err != nil {
		return nil, err
	}

	catalog := &torznabCatalog{
		internalToIDs:    make(map[string][]int),
		customByName:     make(map[string]int),
		knownCategoryIDs: make(map[int]struct{}),
	}

	base := []torznabCategory{
		{ID: 2000, Name: "Movies", Title: "Movies", Internal: []string{"movie-library", "movie", "movies", "video"}},
		{ID: 3000, Name: "Audio", Title: "Audio", Internal: []string{"music"}},
		{ID: 4000, Name: "PC", Title: "PC", Internal: []string{"apps", "application", "applications", "games", "software"}},
		{ID: 4050, Name: "PC/Games", Title: "Games", Internal: []string{"games"}},
		{ID: 5000, Name: "TV", Title: "TV", Internal: []string{"anime", "documentaries", "documentary", "new-episodes", "series", "series-library", "television", "tv"}},
		{ID: 5070, Name: "TV/Anime", Title: "Anime", Internal: []string{"anime"}},
		{ID: 5080, Name: "TV/Documentary", Title: "Documentary", Internal: []string{"documentary", "documentaries"}},
		{ID: 6000, Name: "XXX", Title: "XXX", Internal: []string{"xxx"}},
		{ID: 7000, Name: "Books", Title: "Books", Internal: []string{"book", "books", "ebook", "ebooks"}},
		{ID: 8000, Name: "Other", Title: "Other", Internal: []string{"other", "others"}},
	}
	for _, cat := range base {
		catalog.addCategory(cat)
	}

	customNameToID := make(map[string]int)
	nextCustomID := 100000
	for _, category := range categories {
		normalized := normalizeCategoryName(category)
		if normalized == "" {
			continue
		}
		if ids := catalog.internalToIDs[normalized]; len(ids) > 0 {
			continue
		}
		if _, ok := customNameToID[normalized]; ok {
			continue
		}
		customNameToID[normalized] = nextCustomID
		catalog.customByName[normalized] = nextCustomID
		catalog.addCategory(torznabCategory{
			ID:       nextCustomID,
			Name:     category,
			Title:    category,
			Internal: []string{normalized},
		})
		nextCustomID++
	}

	sort.Slice(catalog.categories, func(i, j int) bool {
		if catalog.categories[i].ID == catalog.categories[j].ID {
			return catalog.categories[i].Title < catalog.categories[j].Title
		}
		return catalog.categories[i].ID < catalog.categories[j].ID
	})

	return catalog, nil
}

func (c *torznabCatalog) addCategory(cat torznabCategory) {
	c.categories = append(c.categories, cat)
	c.knownCategoryIDs[cat.ID] = struct{}{}
	for _, internal := range cat.Internal {
		internal = normalizeCategoryName(internal)
		if internal == "" {
			continue
		}
		c.internalToIDs[internal] = appendUniqueInts(c.internalToIDs[internal], cat.ID)
	}
}

func (c *torznabCatalog) categoriesForIDs(ids []int) []string {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	var out []string
	for _, id := range ids {
		for _, internal := range c.internalForID(id) {
			if _, ok := seen[internal]; ok {
				continue
			}
			seen[internal] = struct{}{}
			out = append(out, internal)
		}
	}
	return out
}

func (c *torznabCatalog) internalForID(id int) []string {
	var out []string
	for _, cat := range c.categories {
		if cat.ID != id {
			continue
		}
		out = append(out, cat.Internal...)
	}
	return out
}

func (c *torznabCatalog) titleForID(id int) string {
	for _, cat := range c.categories {
		if cat.ID == id {
			return cat.Title
		}
	}
	return ""
}

func (c *torznabCatalog) idsForCategory(category string) []int {
	normalized := normalizeCategoryName(category)
	if normalized == "" {
		return nil
	}
	return append([]int(nil), c.internalToIDs[normalized]...)
}

func (c *torznabCatalog) allCategories() []torznabCategory {
	out := make([]torznabCategory, len(c.categories))
	copy(out, c.categories)
	return out
}

func parseTorznabCategories(raw string, catalog *torznabCatalog) ([]int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	seen := make(map[int]struct{})
	var out []int
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("invalid category")
		}
		id, err := strconv.Atoi(part)
		if err != nil || id < 0 {
			return nil, fmt.Errorf("invalid category")
		}
		if _, ok := catalog.knownCategoryIDs[id]; !ok {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

func parseTorznabInt(raw string, defaultValue int, maxValue int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultValue, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("invalid integer")
	}
	if maxValue >= 0 && v > maxValue {
		return maxValue, nil
	}
	return v, nil
}

func queryValueFold(values url.Values, key string) string {
	for k, v := range values {
		if strings.EqualFold(k, key) && len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

func normalizeTorznabGUID(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "urn:btih:")
	if idx := strings.LastIndex(raw, "/"); idx >= 0 && idx < len(raw)-1 {
		raw = raw[idx+1:]
	}
	return raw
}

func normalizeCategoryName(category string) string {
	category = strings.TrimSpace(strings.ToLower(category))
	category = strings.ReplaceAll(category, "_", "-")
	category = strings.ReplaceAll(category, " ", "-")
	category = strings.ReplaceAll(category, "/", "-")
	for strings.Contains(category, "--") {
		category = strings.ReplaceAll(category, "--", "-")
	}
	return strings.Trim(category, "-")
}

func appendUniqueInts(dst []int, value int) []int {
	for _, existing := range dst {
		if existing == value {
			return dst
		}
	}
	return append(dst, value)
}

func writeTorznabError(w http.ResponseWriter, err torznabError) {
	writeXML(w, http.StatusOK, renderTorznabError(err))
}

func renderTorznabError(err torznabError) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>`)
	b.WriteString(`<rss version="2.0" xmlns:torznab="`)
	b.WriteString(torznabNS)
	b.WriteString(`"><channel><error code="`)
	b.WriteString(strconv.Itoa(err.Code))
	b.WriteString(`" description="`)
	xmlEscape(&b, err.Description)
	b.WriteString(`" /></channel></rss>`)
	return []byte(b.String())
}

func (s *Server) renderTorznabCaps(r *http.Request, catalog *torznabCatalog) []byte {
	var b strings.Builder
	origin := requestOrigin(r)
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>`)
	b.WriteString(`<caps xmlns:torznab="`)
	b.WriteString(torznabNS)
	b.WriteString(`">`)
	b.WriteString(`<server title="Magnet Atlas" strapline="Torrent index" url="`)
	xmlEscape(&b, origin)
	b.WriteString(`" />`)
	b.WriteString(`<limits default="100" max="100" />`)
	b.WriteString(`<searching>`)
	b.WriteString(`<search available="yes" supportedParams="q" />`)
	b.WriteString(`<tv-search available="yes" supportedParams="q" />`)
	b.WriteString(`<movie-search available="yes" supportedParams="q" />`)
	b.WriteString(`</searching>`)
	b.WriteString(`<categories>`)
	for _, cat := range catalog.allCategories() {
		b.WriteString(`<category id="`)
		b.WriteString(strconv.Itoa(cat.ID))
		b.WriteString(`" name="`)
		xmlEscape(&b, cat.Name)
		b.WriteString(`"`)
		if len(cat.Internal) == 0 {
			b.WriteString(` />`)
			continue
		}
		b.WriteString(`>`)
		if cat.ID == 4000 {
			b.WriteString(`<subcat id="4050" name="Games" />`)
		}
		if cat.ID == 5000 {
			b.WriteString(`<subcat id="5070" name="Anime" />`)
			b.WriteString(`<subcat id="5080" name="Documentary" />`)
		}
		b.WriteString(`</category>`)
	}
	b.WriteString(`</categories>`)
	b.WriteString(`</caps>`)
	return []byte(b.String())
}

func (s *Server) renderTorznabSearch(r *http.Request, catalog *torznabCatalog, items []domain.Torrent, offset int) []byte {
	var b strings.Builder
	origin := requestOrigin(r)
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>`)
	b.WriteString(`<rss version="2.0" xmlns:torznab="`)
	b.WriteString(torznabNS)
	b.WriteString(`"><channel>`)
	b.WriteString(`<title>Magnet Atlas</title>`)
	b.WriteString(`<description>Torznab search results</description>`)
	b.WriteString(`<link>`)
	xmlEscape(&b, origin)
	b.WriteString(`/api</link>`)
	b.WriteString(`<torznab:response offset="`)
	b.WriteString(strconv.Itoa(offset))
	b.WriteString(`" />`)
	for _, item := range items {
		s.writeTorznabItem(&b, origin, catalog, item)
	}
	b.WriteString(`</channel></rss>`)
	return []byte(b.String())
}

func (s *Server) writeTorznabItem(b *strings.Builder, origin string, catalog *torznabCatalog, item domain.Torrent) {
	categories := catalog.idsForCategory(item.Category)
	if len(categories) == 0 && strings.TrimSpace(item.Category) != "" {
		categories = []int{100000}
	}
	b.WriteString(`<item>`)
	b.WriteString(`<title>`)
	xmlEscape(b, item.Title)
	b.WriteString(`</title>`)
	b.WriteString(`<guid isPermaLink="false">`)
	xmlEscape(b, item.InfoHash)
	b.WriteString(`</guid>`)
	b.WriteString(`<link>`)
	xmlEscape(b, origin+"/v1/torrents/"+url.PathEscape(item.InfoHash))
	b.WriteString(`</link>`)
	if !item.PublishedAt.IsZero() {
		b.WriteString(`<pubDate>`)
		b.WriteString(item.PublishedAt.UTC().Format(time.RFC1123Z))
		b.WriteString(`</pubDate>`)
	}
	if enclosureURL := firstNonEmpty(item.DownloadURL, item.MagnetURI); enclosureURL != "" {
		b.WriteString(`<enclosure url="`)
		xmlEscape(b, enclosureURL)
		b.WriteString(`" length="`)
		b.WriteString(strconv.FormatInt(item.SizeBytes, 10))
		b.WriteString(`" type="application/x-bittorrent" />`)
	}
	seeders := item.Seeders
	leechers := item.Leechers
	peers := seeders + leechers
	b.WriteString(`<torznab:attr name="seeders" value="`)
	b.WriteString(strconv.Itoa(seeders))
	b.WriteString(`" />`)
	b.WriteString(`<torznab:attr name="leechers" value="`)
	b.WriteString(strconv.Itoa(leechers))
	b.WriteString(`" />`)
	b.WriteString(`<torznab:attr name="peers" value="`)
	b.WriteString(strconv.Itoa(peers))
	b.WriteString(`" />`)
	b.WriteString(`<torznab:attr name="size" value="`)
	b.WriteString(strconv.FormatInt(item.SizeBytes, 10))
	b.WriteString(`" />`)
	for _, category := range categories {
		title := catalog.titleForID(category)
		if title == "" {
			title = item.Category
		}
		b.WriteString(`<torznab:attr name="category" value="`)
		b.WriteString(strconv.Itoa(category))
		b.WriteString(`" />`)
		b.WriteString(`<category>`)
		xmlEscape(b, title)
		b.WriteString(`</category>`)
	}
	b.WriteString(`</item>`)
}

func writeXML(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func requestOrigin(r *http.Request) string {
	scheme := "http"
	if proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); proto != "" {
		scheme = proto
	} else if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = "localhost"
	}
	return scheme + "://" + host
}

func xmlEscape(b *strings.Builder, value string) {
	_ = xml.EscapeText(b, []byte(value))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type torznabError struct {
	Code        int
	Description string
}
