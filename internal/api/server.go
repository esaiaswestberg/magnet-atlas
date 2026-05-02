package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/esaiaswestberg/magnet-atlas/internal/domain"
	"github.com/esaiaswestberg/magnet-atlas/internal/store"
)

// Server exposes the HTTP API over a Repository.
type Server struct {
	repo store.Repository
	mux  *http.ServeMux
}

// NewServer builds the HTTP routes.
func NewServer(repo store.Repository) *Server {
	s := &Server{repo: repo, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("/healthz", s.healthz)
	s.mux.HandleFunc("/v1/status", s.status)
	s.mux.HandleFunc("/v1/sources", s.sources)
	s.mux.HandleFunc("/v1/torrents", s.torrents)
	s.mux.HandleFunc("/v1/torrents/", s.torrentDetails)
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	stats, err := s.repo.Stats(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, StatusResponse{
		Status:      ptrString("ok"),
		Torrents:    ptrInt64(stats.TorrentCount),
		Sources:     ptrInt64(stats.SourceCount),
		LastUpdated: ptrString(stats.LastUpdatedAt),
	})
}

func (s *Server) sources(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	items, err := s.repo.ListSources(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, SourcesResponse{Items: ptrStrings(items)})
}

func (s *Server) torrents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	filter := store.SearchFilter{
		Query:    r.URL.Query().Get("query"),
		Source:   r.URL.Query().Get("source"),
		Category: r.URL.Query().Get("category"),
		Limit:    limit,
		Offset:   offset,
	}
	items, err := s.repo.Search(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, TorrentsResponse{Items: ptrGeneratedTorrents(items), Total: ptrInt(len(items))})
}

func (s *Server) torrentDetails(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	infohash := strings.TrimPrefix(r.URL.Path, "/v1/torrents/")
	if infohash == "" || strings.Contains(infohash, "/") {
		http.NotFound(w, r)
		return
	}
	details, err := s.repo.Get(r.Context(), infohash)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, TorrentDetailsResponse{
		Torrent: ptrGeneratedTorrent(details.Torrent),
		Sources: ptrGeneratedSources(details.Sources),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func ptrString(v string) *string { return &v }

func ptrInt(v int) *int { return &v }

func ptrInt64(v int64) *int64 { return &v }

func ptrStrings(v []string) *[]string { return &v }

func ptrGeneratedTorrents(v []domain.Torrent) *[]Torrent {
	out := make([]Torrent, 0, len(v))
	for _, item := range v {
		out = append(out, *ptrGeneratedTorrent(item))
	}
	return &out
}

func ptrGeneratedTorrent(v domain.Torrent) *Torrent {
	t := Torrent{}
	t.Infohash = ptrString(v.InfoHash)
	t.Title = ptrString(v.Title)
	if v.Category != "" {
		t.Category = ptrString(v.Category)
	}
	if v.SizeBytes != 0 {
		t.SizeBytes = ptrInt64(v.SizeBytes)
	}
	if v.Seeders != 0 {
		t.Seeders = ptrInt(v.Seeders)
	}
	if v.Leechers != 0 {
		t.Leechers = ptrInt(v.Leechers)
	}
	if !v.PublishedAt.IsZero() {
		ts := v.PublishedAt.UTC()
		t.PublishedAt = &ts
	}
	if v.MagnetURI != "" {
		t.MagnetUri = ptrString(v.MagnetURI)
	}
	if v.DownloadURL != "" {
		t.DownloadUrl = ptrString(v.DownloadURL)
	}
	if len(v.Tags) > 0 {
		tags := append([]string(nil), v.Tags...)
		t.Tags = &tags
	}
	return &t
}

func ptrGeneratedSources(v []domain.SourceObservation) *[]SourceObservation {
	out := make([]SourceObservation, 0, len(v))
	for _, item := range v {
		out = append(out, ptrGeneratedSource(item))
	}
	return &out
}

func ptrGeneratedSource(v domain.SourceObservation) SourceObservation {
	s := SourceObservation{}
	s.Source = ptrString(v.Source)
	if v.SourceID != "" {
		s.SourceId = ptrString(v.SourceID)
	}
	if v.SourceURL != "" {
		s.SourceUrl = ptrString(v.SourceURL)
	}
	if v.Title != "" {
		s.Title = ptrString(v.Title)
	}
	if v.Category != "" {
		s.Category = ptrString(v.Category)
	}
	if v.SizeBytes != 0 {
		s.SizeBytes = ptrInt64(v.SizeBytes)
	}
	if v.Seeders != 0 {
		s.Seeders = ptrInt(v.Seeders)
	}
	if v.Leechers != 0 {
		s.Leechers = ptrInt(v.Leechers)
	}
	if !v.PublishedAt.IsZero() {
		ts := v.PublishedAt.UTC()
		s.PublishedAt = &ts
	}
	if v.MagnetURI != "" {
		s.MagnetUri = ptrString(v.MagnetURI)
	}
	if v.DownloadURL != "" {
		s.DownloadUrl = ptrString(v.DownloadURL)
	}
	if !v.ObservedAt.IsZero() {
		ts := v.ObservedAt.UTC()
		s.ObservedAt = &ts
	}
	if v.RawJSON != "" {
		s.RawJson = ptrString(v.RawJSON)
	}
	return s
}
