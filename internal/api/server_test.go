package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/esaiaswestberg/magnet-atlas/internal/domain"
	"github.com/esaiaswestberg/magnet-atlas/internal/store"
)

type fakeRepo struct {
	stats   store.Stats
	sources []string
	items   []domain.Torrent
	detail  store.Details
}

func (f fakeRepo) Close() error                                                           { return nil }
func (f fakeRepo) Upsert(context.Context, domain.Torrent, domain.SourceObservation) error { return nil }
func (f fakeRepo) Search(context.Context, store.SearchFilter) ([]domain.Torrent, error) {
	return f.items, nil
}
func (f fakeRepo) Get(context.Context, string) (store.Details, error) { return f.detail, nil }
func (f fakeRepo) HasInfohash(context.Context, string) (bool, error)   { return false, nil }
func (f fakeRepo) ListSources(context.Context) ([]string, error)      { return f.sources, nil }
func (f fakeRepo) Stats(context.Context) (store.Stats, error)         { return f.stats, nil }
func (f fakeRepo) GetSourceState(context.Context, string, string) (string, error) {
	return "", store.ErrSourceStateNotFound
}
func (f fakeRepo) SetSourceState(context.Context, string, string, string) error { return nil }

func TestStatusEndpoint(t *testing.T) {
	srv := NewServer(fakeRepo{stats: store.Stats{TorrentCount: 12, SourceCount: 3}}, []byte("openapi: 3.1.0\n"))
	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
}

func TestDocsEndpoint(t *testing.T) {
	srv := NewServer(fakeRepo{}, []byte("openapi: 3.1.0\n"))
	req := httptest.NewRequest(http.MethodGet, "/v1/", nil)
	req.Host = "magnet-atlas.local"
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusOK; got != want {
		t.Fatalf("docs = %d, want %d", got, want)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `url: "openapi.yaml"`) {
		t.Fatalf("docs page does not use a relative spec URL: %s", body)
	}
	if strings.Contains(body, "localhost") {
		t.Fatalf("docs page hard-codes localhost: %s", body)
	}
}

func TestOpenAPISpecEndpoint(t *testing.T) {
	spec, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	srv := NewServer(fakeRepo{}, spec)
	req := httptest.NewRequest(http.MethodGet, "/v1/openapi.yaml", nil)
	req.Host = "example.test"
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusOK; got != want {
		t.Fatalf("spec = %d, want %d", got, want)
	}
	if got := rec.Body.Bytes(); string(got) != string(spec) {
		t.Fatalf("spec body mismatch")
	}
}
