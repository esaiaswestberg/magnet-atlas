package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/esaiaswestberg/magnet-atlas/internal/domain"
	"github.com/esaiaswestberg/magnet-atlas/internal/store"
)

type torznabRepo struct {
	categories    []string
	items         []domain.Torrent
	details       map[string]store.Details
	searchFilters []store.SearchFilter
}

func (r *torznabRepo) Close() error { return nil }

func (r *torznabRepo) Upsert(context.Context, domain.Torrent, domain.SourceObservation) error {
	return nil
}

func (r *torznabRepo) Search(_ context.Context, filter store.SearchFilter) ([]domain.Torrent, error) {
	r.searchFilters = append(r.searchFilters, filter)
	if len(filter.Categories) == 0 {
		return append([]domain.Torrent(nil), r.items...), nil
	}
	allowed := make(map[string]struct{}, len(filter.Categories))
	for _, category := range filter.Categories {
		allowed[strings.ToLower(category)] = struct{}{}
	}
	var out []domain.Torrent
	for _, item := range r.items {
		if _, ok := allowed[strings.ToLower(item.Category)]; ok {
			out = append(out, item)
		}
	}
	return out, nil
}

func (r *torznabRepo) Get(_ context.Context, infohash string) (store.Details, error) {
	if details, ok := r.details[infohash]; ok {
		return details, nil
	}
	return store.Details{}, store.ErrSourceStateNotFound
}

func (r *torznabRepo) HasInfohash(context.Context, string) (bool, error) { return false, nil }
func (r *torznabRepo) ListSources(context.Context) ([]string, error)     { return nil, nil }
func (r *torznabRepo) ListCategories(context.Context) ([]string, error)  { return r.categories, nil }
func (r *torznabRepo) Stats(context.Context) (store.Stats, error)        { return store.Stats{}, nil }
func (r *torznabRepo) GetSourceState(context.Context, string, string) (string, error) {
	return "", store.ErrSourceStateNotFound
}
func (r *torznabRepo) SetSourceState(context.Context, string, string, string) error { return nil }

func TestTorznabCapsSearchAndGet(t *testing.T) {
	repo := &torznabRepo{
		categories: []string{"games", "software", "video"},
		items: []domain.Torrent{
			{
				InfoHash:    "0123456789abcdef0123456789abcdef01234567",
				Title:       "Example Linux ISO",
				Category:    "software",
				SizeBytes:   1073741824,
				Seeders:     42,
				Leechers:    3,
				PublishedAt: time.Unix(1000, 0).UTC(),
				MagnetURI:   "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
				DownloadURL: "https://example.invalid/download/1",
			},
			{
				InfoHash:    "89abcdef0123456789abcdef0123456789abcdef",
				Title:       "Example Game",
				Category:    "games",
				SizeBytes:   2048,
				Seeders:     5,
				Leechers:    1,
				PublishedAt: time.Unix(2000, 0).UTC(),
				MagnetURI:   "magnet:?xt=urn:btih:89abcdef0123456789abcdef0123456789abcdef",
			},
		},
		details: map[string]store.Details{
			"0123456789abcdef0123456789abcdef01234567": {
				Torrent: domain.Torrent{
					InfoHash:    "0123456789abcdef0123456789abcdef01234567",
					DownloadURL: "https://example.invalid/download/1",
				},
			},
		},
	}
	srv := NewServer(repo, []byte("openapi: 3.1.0\n"), nil)

	capsReq := httptest.NewRequest(http.MethodGet, "/api?t=caps", nil)
	capsRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(capsRec, capsReq)

	if got, want := capsRec.Code, http.StatusOK; got != want {
		t.Fatalf("caps = %d, want %d", got, want)
	}
	capsBody := capsRec.Body.String()
	if !strings.Contains(capsBody, `category id="2000" name="Movies"`) {
		t.Fatalf("caps response missing Movies category: %s", capsBody)
	}
	if !strings.Contains(capsBody, `subcat id="4050" name="Games"`) {
		t.Fatalf("caps response missing Games subcategory: %s", capsBody)
	}

	searchReq := httptest.NewRequest(http.MethodGet, "/api?t=search&q=Example&cat=4000&limit=20", nil)
	searchRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(searchRec, searchReq)

	if got, want := searchRec.Code, http.StatusOK; got != want {
		t.Fatalf("search = %d, want %d", got, want)
	}
	if len(repo.searchFilters) == 0 {
		t.Fatal("search filter was not recorded")
	}
	filter := repo.searchFilters[0]
	if !containsString(filter.Categories, "games") || !containsString(filter.Categories, "software") {
		t.Fatalf("unexpected category mapping: %#v", filter.Categories)
	}
	if !strings.Contains(searchRec.Body.String(), `<title>Example Linux ISO</title>`) {
		t.Fatalf("search response missing item: %s", searchRec.Body.String())
	}
	if !strings.Contains(searchRec.Body.String(), `torznab:attr name="category" value="4000"`) {
		t.Fatalf("search response missing category attr: %s", searchRec.Body.String())
	}

	emptyReq := httptest.NewRequest(http.MethodGet, "/api?t=search&q=Example&cat=999999", nil)
	emptyRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(emptyRec, emptyReq)
	if strings.Contains(emptyRec.Body.String(), "<item>") {
		t.Fatalf("unexpected item for unknown category: %s", emptyRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api?t=get&id=0123456789abcdef0123456789abcdef01234567", nil)
	getRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getRec, getReq)
	if got, want := getRec.Code, http.StatusFound; got != want {
		t.Fatalf("get = %d, want %d", got, want)
	}
	if got, want := getRec.Header().Get("Location"), "https://example.invalid/download/1"; got != want {
		t.Fatalf("get redirect = %q, want %q", got, want)
	}
}

func TestTorznabAPIKeyProtection(t *testing.T) {
	repo := &torznabRepo{}
	srv := NewServer(repo, []byte("openapi: 3.1.0\n"), []string{"secret"})

	req := httptest.NewRequest(http.MethodGet, "/api?t=search&q=Example", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusOK; got != want {
		t.Fatalf("auth search = %d, want %d", got, want)
	}
	if !strings.Contains(rec.Body.String(), `code="100"`) {
		t.Fatalf("missing auth error in response: %s", rec.Body.String())
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
