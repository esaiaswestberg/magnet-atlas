package domain

import "time"

// Torrent is the canonical record served by the index and stored in the database.
type Torrent struct {
	InfoHash    string    `json:"infohash"`
	Title       string    `json:"title"`
	Category    string    `json:"category,omitempty"`
	SizeBytes   int64     `json:"size_bytes,omitempty"`
	Seeders     int       `json:"seeders,omitempty"`
	Leechers    int       `json:"leechers,omitempty"`
	PublishedAt time.Time `json:"published_at,omitempty"`
	MagnetURI   string    `json:"magnet_uri,omitempty"`
	DownloadURL string    `json:"download_url,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
}

// SourceObservation stores per-source provenance for a canonical torrent.
type SourceObservation struct {
	Source      string    `json:"source"`
	SourceID    string    `json:"source_id,omitempty"`
	SourceURL   string    `json:"source_url,omitempty"`
	Title       string    `json:"title,omitempty"`
	Category    string    `json:"category,omitempty"`
	SizeBytes   int64     `json:"size_bytes,omitempty"`
	Seeders     int       `json:"seeders,omitempty"`
	Leechers    int       `json:"leechers,omitempty"`
	PublishedAt time.Time `json:"published_at,omitempty"`
	MagnetURI   string    `json:"magnet_uri,omitempty"`
	DownloadURL string    `json:"download_url,omitempty"`
	ObservedAt  time.Time `json:"observed_at"`
	RawJSON     string    `json:"raw_json,omitempty"`
}

// MergeTorrent fills missing fields on dst from src and prefers richer metadata.
func MergeTorrent(dst Torrent, src Torrent) Torrent {
	if dst.InfoHash == "" {
		dst.InfoHash = src.InfoHash
	}
	if dst.Title == "" {
		dst.Title = src.Title
	}
	if dst.Category == "" {
		dst.Category = src.Category
	}
	if dst.SizeBytes == 0 {
		dst.SizeBytes = src.SizeBytes
	}
	if dst.Seeders < src.Seeders {
		dst.Seeders = src.Seeders
	}
	if dst.Leechers < src.Leechers {
		dst.Leechers = src.Leechers
	}
	if dst.PublishedAt.IsZero() || (!src.PublishedAt.IsZero() && src.PublishedAt.Before(dst.PublishedAt)) {
		if !src.PublishedAt.IsZero() {
			dst.PublishedAt = src.PublishedAt
		}
	}
	if dst.MagnetURI == "" {
		dst.MagnetURI = src.MagnetURI
	}
	if dst.DownloadURL == "" {
		dst.DownloadURL = src.DownloadURL
	}
	if len(dst.Tags) == 0 && len(src.Tags) > 0 {
		dst.Tags = append([]string(nil), src.Tags...)
	}
	return dst
}
