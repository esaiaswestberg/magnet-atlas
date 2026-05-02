package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/lib/pq"

	"github.com/esaiaswestberg/magnet-atlas/internal/domain"
)

// PostgresRepository implements Repository on top of PostgreSQL.
type PostgresRepository struct {
	db *sql.DB
}

// OpenPostgres opens or creates the PostgreSQL database at url.
func OpenPostgres(url string) (*PostgresRepository, error) {
	db, err := sql.Open("postgres", url)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(10)
	r := &PostgresRepository{db: db}
	if err := r.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return r, nil
}

func (r *PostgresRepository) Close() error { return r.db.Close() }

func (r *PostgresRepository) migrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS torrents (
			id BIGSERIAL PRIMARY KEY,
			infohash TEXT NOT NULL UNIQUE,
			title TEXT NOT NULL,
			normalized_title TEXT NOT NULL,
			category TEXT NOT NULL DEFAULT '',
			size_bytes BIGINT NOT NULL DEFAULT 0,
			seeders INTEGER NOT NULL DEFAULT 0,
			leechers INTEGER NOT NULL DEFAULT 0,
			published_at TEXT NOT NULL DEFAULT '',
			magnet_uri TEXT NOT NULL DEFAULT '',
			download_url TEXT NOT NULL DEFAULT '',
			extra_text TEXT NOT NULL DEFAULT '[]',
			search_document TSVECTOR NOT NULL DEFAULT ''::tsvector,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_torrents_category ON torrents(category);`,
		`CREATE INDEX IF NOT EXISTS idx_torrents_updated_at ON torrents(updated_at);`,
		`CREATE INDEX IF NOT EXISTS idx_torrents_search_document ON torrents USING GIN (search_document);`,
		`CREATE TABLE IF NOT EXISTS torrent_sources (
			infohash TEXT NOT NULL,
			source TEXT NOT NULL,
			source_key TEXT NOT NULL,
			source_url TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL DEFAULT '',
			category TEXT NOT NULL DEFAULT '',
			size_bytes BIGINT NOT NULL DEFAULT 0,
			seeders INTEGER NOT NULL DEFAULT 0,
			leechers INTEGER NOT NULL DEFAULT 0,
			published_at TEXT NOT NULL DEFAULT '',
			magnet_uri TEXT NOT NULL DEFAULT '',
			download_url TEXT NOT NULL DEFAULT '',
			extra_text TEXT NOT NULL DEFAULT '[]',
			observed_at TEXT NOT NULL,
			raw_json TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (infohash, source, source_key)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_torrent_sources_source ON torrent_sources(source);`,
		`CREATE INDEX IF NOT EXISTS idx_torrent_sources_infohash ON torrent_sources(infohash);`,
		`CREATE TABLE IF NOT EXISTS source_state (
			source TEXT NOT NULL,
			section TEXT NOT NULL,
			state TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (source, section)
		);`,
		`ALTER TABLE torrents ADD COLUMN IF NOT EXISTS extra_text TEXT NOT NULL DEFAULT '[]';`,
		`ALTER TABLE torrent_sources ADD COLUMN IF NOT EXISTS extra_text TEXT NOT NULL DEFAULT '[]';`,
	}

	for _, stmt := range stmts {
		if _, err := r.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}
	return nil
}

// Upsert stores a canonical torrent and one per-source observation.
func (r *PostgresRepository) Upsert(ctx context.Context, torrent domain.Torrent, obs domain.SourceObservation) error {
	if strings.TrimSpace(torrent.InfoHash) == "" {
		return errors.New("infohash is required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	current, err := r.getTorrent(ctx, torrent.InfoHash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	merged := domain.MergeTorrent(current, torrent)
	if merged.Title == "" {
		merged.Title = torrent.InfoHash
	}
	searchText := buildSearchText(merged.Title, merged.Category, merged.ExtraText)
	extraText := encodeTextList(merged.ExtraText)

	publishedAt := ""
	if !merged.PublishedAt.IsZero() {
		publishedAt = merged.PublishedAt.UTC().Format(time.RFC3339Nano)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO torrents (
			infohash, title, normalized_title, category, size_bytes, seeders, leechers, published_at, magnet_uri, download_url, extra_text, search_document, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, to_tsvector('simple', $12::text), $13, $14)
		ON CONFLICT (infohash) DO UPDATE SET
			title = EXCLUDED.title,
			normalized_title = EXCLUDED.normalized_title,
			category = EXCLUDED.category,
			size_bytes = EXCLUDED.size_bytes,
			seeders = EXCLUDED.seeders,
			leechers = EXCLUDED.leechers,
			published_at = EXCLUDED.published_at,
			magnet_uri = EXCLUDED.magnet_uri,
			download_url = EXCLUDED.download_url,
			extra_text = EXCLUDED.extra_text,
			search_document = EXCLUDED.search_document,
			updated_at = EXCLUDED.updated_at`,
		merged.InfoHash, merged.Title, normalizeTitle(merged.Title), merged.Category, merged.SizeBytes, merged.Seeders, merged.Leechers, publishedAt, merged.MagnetURI, merged.DownloadURL, extraText, searchText, now, now,
	)
	if err != nil {
		return err
	}

	rawJSON := obs.RawJSON
	if strings.TrimSpace(rawJSON) == "" {
		rawJSON = "{}"
	}
	sourceKey := obs.SourceID
	if sourceKey == "" {
		sourceKey = obs.SourceURL
	}
	if sourceKey == "" {
		sourceKey = obs.Source + ":" + merged.InfoHash
	}
	obsPublishedAt := ""
	if !obs.PublishedAt.IsZero() {
		obsPublishedAt = obs.PublishedAt.UTC().Format(time.RFC3339Nano)
	}
	obsObservedAt := obs.ObservedAt.UTC().Format(time.RFC3339Nano)
	if obs.ObservedAt.IsZero() {
		obsObservedAt = now
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO torrent_sources (
			infohash, source, source_key, source_url, title, category, size_bytes, seeders, leechers, published_at, magnet_uri, download_url, extra_text, observed_at, raw_json
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		ON CONFLICT(infohash, source, source_key) DO UPDATE SET
			source_url = EXCLUDED.source_url,
			title = EXCLUDED.title,
			category = EXCLUDED.category,
			size_bytes = EXCLUDED.size_bytes,
			seeders = EXCLUDED.seeders,
			leechers = EXCLUDED.leechers,
			published_at = EXCLUDED.published_at,
			magnet_uri = EXCLUDED.magnet_uri,
			download_url = EXCLUDED.download_url,
			extra_text = EXCLUDED.extra_text,
			observed_at = EXCLUDED.observed_at,
			raw_json = EXCLUDED.raw_json`,
		merged.InfoHash, obs.Source, sourceKey, obs.SourceURL, obs.Title, obs.Category, obs.SizeBytes, obs.Seeders, obs.Leechers, obsPublishedAt, obs.MagnetURI, obs.DownloadURL, encodeTextList(obs.ExtraText), obsObservedAt, rawJSON,
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (r *PostgresRepository) Search(ctx context.Context, filter SearchFilter) ([]domain.Torrent, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	base := `
		SELECT t.infohash, t.title, t.category, t.size_bytes, t.seeders, t.leechers, t.published_at, t.magnet_uri, t.download_url, t.extra_text
		FROM torrents t`
	args := make([]any, 0, 4)
	conds := []string{}
	param := 1
	if strings.TrimSpace(filter.Query) != "" {
		conds = append(conds, fmt.Sprintf(`t.search_document @@ plainto_tsquery('simple', $%d)`, param))
		args = append(args, filter.Query)
		param++
	}
	if filter.Source != "" {
		conds = append(conds, fmt.Sprintf(`EXISTS (SELECT 1 FROM torrent_sources s WHERE s.infohash = t.infohash AND s.source = $%d)`, param))
		args = append(args, filter.Source)
		param++
	}
	if len(filter.Categories) > 0 {
		placeholders := make([]string, 0, len(filter.Categories))
		for range filter.Categories {
			placeholders = append(placeholders, fmt.Sprintf("$%d", param))
			param++
		}
		conds = append(conds, `t.category IN (`+strings.Join(placeholders, ", ")+`)`)
		for _, category := range filter.Categories {
			args = append(args, category)
		}
	} else if filter.Category != "" {
		conds = append(conds, fmt.Sprintf(`t.category = $%d`, param))
		args = append(args, filter.Category)
		param++
	}
	if len(conds) > 0 {
		base += " WHERE " + strings.Join(conds, " AND ")
	}
	base += fmt.Sprintf(` ORDER BY t.seeders DESC, t.updated_at DESC LIMIT $%d OFFSET $%d`, param, param+1)
	args = append(args, limit, offset)

	rows, err := r.db.QueryContext(ctx, base, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var torrents []domain.Torrent
	for rows.Next() {
		t, err := scanTorrent(rows)
		if err != nil {
			return nil, err
		}
		torrents = append(torrents, t)
	}
	return torrents, rows.Err()
}

func (r *PostgresRepository) Get(ctx context.Context, infohash string) (Details, error) {
	t, err := r.getTorrent(ctx, infohash)
	if err != nil {
		return Details{}, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT source, source_key, source_url, title, category, size_bytes, seeders, leechers, published_at, magnet_uri, download_url, extra_text, observed_at, raw_json
		FROM torrent_sources
		WHERE infohash = $1
		ORDER BY observed_at DESC`, infohash)
	if err != nil {
		return Details{}, err
	}
	defer rows.Close()

	var sources []domain.SourceObservation
	for rows.Next() {
		var obs domain.SourceObservation
		var publishedAt, observedAt, extraText string
		if err := rows.Scan(&obs.Source, &obs.SourceID, &obs.SourceURL, &obs.Title, &obs.Category, &obs.SizeBytes, &obs.Seeders, &obs.Leechers, &publishedAt, &obs.MagnetURI, &obs.DownloadURL, &extraText, &observedAt, &obs.RawJSON); err != nil {
			return Details{}, err
		}
		obs.ExtraText = decodeTextList(extraText)
		if publishedAt != "" {
			if ts, err := time.Parse(time.RFC3339Nano, publishedAt); err == nil {
				obs.PublishedAt = ts
			}
		}
		if ts, err := time.Parse(time.RFC3339Nano, observedAt); err == nil {
			obs.ObservedAt = ts
		}
		sources = append(sources, obs)
	}
	return Details{Torrent: t, Sources: sources}, rows.Err()
}

func (r *PostgresRepository) HasInfohash(ctx context.Context, infohash string) (bool, error) {
	var found int
	err := r.db.QueryRowContext(ctx, `SELECT 1 FROM torrents WHERE infohash = $1 LIMIT 1`, infohash).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (r *PostgresRepository) ListSources(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT DISTINCT source FROM torrent_sources ORDER BY source ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sources []string
	for rows.Next() {
		var source string
		if err := rows.Scan(&source); err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	return sources, rows.Err()
}

func (r *PostgresRepository) ListCategories(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT DISTINCT category FROM torrents WHERE category <> '' ORDER BY category ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var categories []string
	for rows.Next() {
		var category string
		if err := rows.Scan(&category); err != nil {
			return nil, err
		}
		categories = append(categories, category)
	}
	return categories, rows.Err()
}

func (r *PostgresRepository) Stats(ctx context.Context) (Stats, error) {
	var stats Stats
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM torrents`).Scan(&stats.TorrentCount); err != nil {
		return Stats{}, err
	}
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT source) FROM torrent_sources`).Scan(&stats.SourceCount); err != nil {
		return Stats{}, err
	}
	if err := r.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(updated_at), '') FROM torrents`).Scan(&stats.LastUpdatedAt); err != nil {
		return Stats{}, err
	}
	return stats, nil
}

func (r *PostgresRepository) GetSourceState(ctx context.Context, source, section string) (string, error) {
	var state string
	err := r.db.QueryRowContext(ctx, `SELECT state FROM source_state WHERE source = $1 AND section = $2`, source, section).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrSourceStateNotFound
	}
	if err != nil {
		return "", err
	}
	return state, nil
}

func (r *PostgresRepository) SetSourceState(ctx context.Context, source, section, state string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO source_state (source, section, state, updated_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT(source, section) DO UPDATE SET
			state = EXCLUDED.state,
			updated_at = EXCLUDED.updated_at`,
		source, section, state, time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (r *PostgresRepository) getTorrent(ctx context.Context, infohash string) (domain.Torrent, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT infohash, title, category, size_bytes, seeders, leechers, published_at, magnet_uri, download_url, extra_text
		FROM torrents
		WHERE infohash = $1`, infohash)
	return scanTorrent(row)
}

// Seed stores a batch of fixture torrents.
func (r *PostgresRepository) Seed(ctx context.Context, source string, torrents []domain.Torrent, observations []domain.SourceObservation) error {
	if len(torrents) != len(observations) {
		return fmt.Errorf("torrents and observations length mismatch")
	}
	for i := range torrents {
		if observations[i].Source == "" {
			observations[i].Source = source
		}
		if err := r.Upsert(ctx, torrents[i], observations[i]); err != nil {
			return err
		}
	}
	return nil
}
