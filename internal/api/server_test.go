package api

import (
	"context"
	"net/http"
	"net/http/httptest"
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
func (f fakeRepo) ListSources(context.Context) ([]string, error)      { return f.sources, nil }
func (f fakeRepo) Stats(context.Context) (store.Stats, error)         { return f.stats, nil }

func TestStatusEndpoint(t *testing.T) {
	srv := NewServer(fakeRepo{stats: store.Stats{TorrentCount: 12, SourceCount: 3}})
	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
}
