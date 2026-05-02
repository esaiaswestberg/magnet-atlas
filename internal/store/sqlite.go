package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/esaiaswestberg/magnet-atlas/internal/domain"
)

// SQLiteRepository implements Repository on top of SQLite.
type SQLiteRepository struct {
	db *sql.DB
}

// OpenSQLite opens or creates the SQLite database at path.
func OpenSQLite(path string) (*SQLiteRepository, error) {
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	r := &SQLiteRepository{db: db}
	if err := r.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return r, nil
}

func (r *SQLiteRepository) Close() error { return r.db.Close() }

func (r *SQLiteRepository) migrate(ctx context.Context) error {
	stmts := []string{
		`PRAGMA foreign_keys = ON;`,
		`CREATE TABLE IF NOT EXISTS torrents (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			infohash TEXT NOT NULL UNIQUE,
			title TEXT NOT NULL,
			normalized_title TEXT NOT NULL,
			category TEXT NOT NULL DEFAULT '',
			size_bytes INTEGER NOT NULL DEFAULT 0,
			seeders INTEGER NOT NULL DEFAULT 0,
			leechers INTEGER NOT NULL DEFAULT 0,
			published_at TEXT NOT NULL DEFAULT '',
			magnet_uri TEXT NOT NULL DEFAULT '',
			download_url TEXT NOT NULL DEFAULT '',
			extra_text TEXT NOT NULL DEFAULT '[]',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_torrents_category ON torrents(category);`,
		`CREATE INDEX IF NOT EXISTS idx_torrents_updated_at ON torrents(updated_at);`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS torrents_fts USING fts5(
			infohash UNINDEXED,
			title,
			category,
			extra_text,
			content=''
		);`,
		`CREATE TABLE IF NOT EXISTS torrent_sources (
			infohash TEXT NOT NULL,
			source TEXT NOT NULL,
			source_key TEXT NOT NULL,
			source_url TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL DEFAULT '',
			category TEXT NOT NULL DEFAULT '',
			size_bytes INTEGER NOT NULL DEFAULT 0,
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
		`CREATE TABLE IF NOT EXISTS source_state (
			source TEXT NOT NULL,
			section TEXT NOT NULL,
			state TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (source, section)
		);`,
		`DROP TRIGGER IF EXISTS torrents_ai;`,
		`DROP TRIGGER IF EXISTS torrents_ad;`,
		`DROP TRIGGER IF EXISTS torrents_au;`,
		`CREATE TRIGGER torrents_ai AFTER INSERT ON torrents BEGIN
			INSERT INTO torrents_fts(rowid, infohash, title, category, extra_text) VALUES (new.id, new.infohash, new.normalized_title, new.category, new.extra_text);
		END;`,
		`CREATE TRIGGER torrents_ad AFTER DELETE ON torrents BEGIN
			INSERT INTO torrents_fts(torrents_fts, rowid, infohash, title, category, extra_text) VALUES ('delete', old.id, old.infohash, old.normalized_title, old.category, old.extra_text);
		END;`,
		`CREATE TRIGGER torrents_au AFTER UPDATE ON torrents BEGIN
			INSERT INTO torrents_fts(torrents_fts, rowid, infohash, title, category, extra_text) VALUES ('delete', old.id, old.infohash, old.normalized_title, old.category, old.extra_text);
			INSERT INTO torrents_fts(rowid, infohash, title, category, extra_text) VALUES (new.id, new.infohash, new.normalized_title, new.category, new.extra_text);
		END;`,
	}

	for _, stmt := range stmts {
		if _, err := r.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}
	if err := r.ensureSQLiteColumn(ctx, "torrents", "extra_text", `ALTER TABLE torrents ADD COLUMN extra_text TEXT NOT NULL DEFAULT '[]';`); err != nil {
		return err
	}
	if err := r.ensureSQLiteColumn(ctx, "torrent_sources", "extra_text", `ALTER TABLE torrent_sources ADD COLUMN extra_text TEXT NOT NULL DEFAULT '[]';`); err != nil {
		return err
	}
	if err := r.ensureSQLiteFTS(ctx); err != nil {
		return err
	}
	return nil
}

func (r *SQLiteRepository) ensureSQLiteColumn(ctx context.Context, table, column, stmt string) error {
	rows, err := r.db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull int
		var dfltValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dfltValue, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if _, err := r.db.ExecContext(ctx, stmt); err != nil {
		return err
	}
	return nil
}

func (r *SQLiteRepository) ensureSQLiteFTS(ctx context.Context) error {
	hasExtra, err := r.sqliteFTSHasColumn(ctx, "extra_text")
	if err != nil {
		return err
	}
	if hasExtra {
		return nil
	}
	stmts := []string{
		`DROP TABLE IF EXISTS torrents_fts;`,
		`CREATE VIRTUAL TABLE torrents_fts USING fts5(
			infohash UNINDEXED,
			title,
			category,
			extra_text,
			content=''
		);`,
		`INSERT INTO torrents_fts(rowid, infohash, title, category, extra_text)
			SELECT id, infohash, normalized_title, category, extra_text FROM torrents;`,
	}
	for _, stmt := range stmts {
		if _, err := r.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (r *SQLiteRepository) sqliteFTSHasColumn(ctx context.Context, column string) (bool, error) {
	rows, err := r.db.QueryContext(ctx, `PRAGMA table_info(torrents_fts)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull int
		var dfltValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dfltValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// Upsert stores a canonical torrent and one per-source observation.
func (r *SQLiteRepository) Upsert(ctx context.Context, torrent domain.Torrent, obs domain.SourceObservation) error {
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

	if current.InfoHash == "" {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO torrents (infohash, title, normalized_title, category, size_bytes, seeders, leechers, published_at, magnet_uri, download_url, extra_text, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			merged.InfoHash, merged.Title, normalizeTitle(merged.Title), merged.Category, merged.SizeBytes, merged.Seeders, merged.Leechers, publishedAt, merged.MagnetURI, merged.DownloadURL, extraText, now, now,
		)
		if err != nil {
			return err
		}
		if _, err := res.LastInsertId(); err != nil {
			return err
		}
	} else {
		_, err := tx.ExecContext(ctx, `
			UPDATE torrents
			SET title = ?, normalized_title = ?, category = ?, size_bytes = ?, seeders = ?, leechers = ?, published_at = ?, magnet_uri = ?, download_url = ?, extra_text = ?, updated_at = ?
			WHERE infohash = ?`,
			merged.Title, normalizeTitle(merged.Title), merged.Category, merged.SizeBytes, merged.Seeders, merged.Leechers, publishedAt, merged.MagnetURI, merged.DownloadURL, extraText, now, merged.InfoHash,
		)
		if err != nil {
			return err
		}
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
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(infohash, source, source_key) DO UPDATE SET
			source_url = excluded.source_url,
			title = excluded.title,
			category = excluded.category,
			size_bytes = excluded.size_bytes,
			seeders = excluded.seeders,
			leechers = excluded.leechers,
			published_at = excluded.published_at,
			magnet_uri = excluded.magnet_uri,
			download_url = excluded.download_url,
			extra_text = excluded.extra_text,
			observed_at = excluded.observed_at,
			raw_json = excluded.raw_json`,
		merged.InfoHash, obs.Source, sourceKey, obs.SourceURL, obs.Title, obs.Category, obs.SizeBytes, obs.Seeders, obs.Leechers, obsPublishedAt, obs.MagnetURI, obs.DownloadURL, encodeTextList(obs.ExtraText), obsObservedAt, rawJSON,
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (r *SQLiteRepository) Search(ctx context.Context, filter SearchFilter) ([]domain.Torrent, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	base := `
		SELECT DISTINCT t.infohash, t.title, t.category, t.size_bytes, t.seeders, t.leechers, t.published_at, t.magnet_uri, t.download_url, t.extra_text
		FROM torrents t`
	args := make([]any, 0, 4)
	conds := []string{}
	if strings.TrimSpace(filter.Query) != "" {
		base += ` JOIN torrents_fts fts ON fts.rowid = t.id`
		conds = append(conds, `torrents_fts MATCH ?`)
		args = append(args, rewriteSearchQuery(filter.Query))
	}
	if filter.Source != "" {
		conds = append(conds, `EXISTS (SELECT 1 FROM torrent_sources s WHERE s.infohash = t.infohash AND s.source = ?)`)
		args = append(args, filter.Source)
	}
	if len(filter.Categories) > 0 {
		placeholders := make([]string, 0, len(filter.Categories))
		for range filter.Categories {
			placeholders = append(placeholders, "?")
		}
		conds = append(conds, `t.category IN (`+strings.Join(placeholders, ", ")+`)`)
		for _, category := range filter.Categories {
			args = append(args, category)
		}
	} else if filter.Category != "" {
		conds = append(conds, `t.category = ?`)
		args = append(args, filter.Category)
	}
	if len(conds) > 0 {
		base += " WHERE " + strings.Join(conds, " AND ")
	}
	base += ` ORDER BY t.seeders DESC, t.updated_at DESC LIMIT ? OFFSET ?`
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

func (r *SQLiteRepository) Get(ctx context.Context, infohash string) (Details, error) {
	t, err := r.getTorrent(ctx, infohash)
	if err != nil {
		return Details{}, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT source, source_key, source_url, title, category, size_bytes, seeders, leechers, published_at, magnet_uri, download_url, extra_text, observed_at, raw_json
		FROM torrent_sources
		WHERE infohash = ?
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

func (r *SQLiteRepository) HasInfohash(ctx context.Context, infohash string) (bool, error) {
	var found int
	err := r.db.QueryRowContext(ctx, `SELECT 1 FROM torrents WHERE infohash = ? LIMIT 1`, infohash).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (r *SQLiteRepository) ListSources(ctx context.Context) ([]string, error) {
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

func (r *SQLiteRepository) ListCategories(ctx context.Context) ([]string, error) {
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

func (r *SQLiteRepository) Stats(ctx context.Context) (Stats, error) {
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

func (r *SQLiteRepository) GetSourceState(ctx context.Context, source, section string) (string, error) {
	var state string
	err := r.db.QueryRowContext(ctx, `SELECT state FROM source_state WHERE source = ? AND section = ?`, source, section).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrSourceStateNotFound
	}
	if err != nil {
		return "", err
	}
	return state, nil
}

func (r *SQLiteRepository) SetSourceState(ctx context.Context, source, section, state string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO source_state (source, section, state, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(source, section) DO UPDATE SET
			state = excluded.state,
			updated_at = excluded.updated_at`,
		source, section, state, time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (r *SQLiteRepository) getTorrent(ctx context.Context, infohash string) (domain.Torrent, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT infohash, title, category, size_bytes, seeders, leechers, published_at, magnet_uri, download_url, extra_text
		FROM torrents
		WHERE infohash = ?`, infohash)
	return scanTorrent(row)
}

func scanTorrent(scanner interface {
	Scan(dest ...any) error
}) (domain.Torrent, error) {
	var t domain.Torrent
	var publishedAt, extraText string
	if err := scanner.Scan(&t.InfoHash, &t.Title, &t.Category, &t.SizeBytes, &t.Seeders, &t.Leechers, &publishedAt, &t.MagnetURI, &t.DownloadURL, &extraText); err != nil {
		return domain.Torrent{}, err
	}
	t.ExtraText = decodeTextList(extraText)
	if publishedAt != "" {
		ts, err := time.Parse(time.RFC3339Nano, publishedAt)
		if err == nil {
			t.PublishedAt = ts
		}
	}
	return t, nil
}

// Seed stores a batch of fixture torrents.
func (r *SQLiteRepository) Seed(ctx context.Context, source string, torrents []domain.Torrent, observations []domain.SourceObservation) error {
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

// MarshalJSON is intentionally not used by the store, but having a helper here makes tests simpler.
func MarshalJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
