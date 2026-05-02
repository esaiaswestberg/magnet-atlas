# Magnet Atlas

Magnet Atlas is a Go-based torrent indexing and aggregation engine.

It is designed to ingest metadata from configurable torrent sources, normalize that data into a query-friendly store, and provide a clean backend boundary for downstream consumers such as a future web UI.

## Goals

- Collect torrent metadata from multiple configurable sources.
- Normalize and aggregate data into a single searchable index.
- Keep storage simple for local-first deployments while allowing a path to scale.
- Expose the indexed data through a backend interface rather than coupling the UI directly to source sites.

## Storage

Magnet Atlas is intended to support either SQLite or PostgreSQL.
Choose the backend explicitly with `database.type: sqlite` or `database.type: postgres`, then provide `database.path` or `database.url` accordingly.

For the first version, SQLite is the best default fit:

- Minimal setup and operational overhead.
- Easy local development and single-node deployment.
- Sufficient for a compact indexing engine and early iteration.

PostgreSQL becomes the better choice when the project needs:

- Higher concurrency.
- Larger datasets.
- Multi-process or multi-user deployments.
- More advanced operational controls.

## Architecture

The project is organized around a small set of responsibilities:

- Source adapters fetch torrent listings and metadata from individual sites.
- RSS adapters can poll torrent feeds that already publish magnet metadata.
- The ingestion pipeline normalizes and deduplicates incoming records.
- The storage layer persists the canonical torrent index.
- The query layer serves search and retrieval requests for internal consumers and future APIs.

This separation keeps source-specific behavior isolated and makes it easier to add, remove, or update a source without changing the rest of the system.

## Sources

Source support is configurable. The engine should not assume a fixed set of torrent sites.

Each source can be enabled, disabled, or configured independently.

Supported source types in v1 include `fixture`, `rss`, `1337x`, and `uindex`.

## Future Web UI

Magnet Atlas is intended to serve data to a web UI indirectly through a backend interface. The UI should query the indexed dataset rather than scraping torrent sites itself.

This keeps presentation concerns separate from ingestion and allows the backend to evolve independently.

## Torznab Support

Magnet Atlas also exposes a Torznab-compatible read API at `/api`.

It supports:

- `t=caps`
- `t=search`
- `t=tvsearch`
- `t=movie`
- `t=get`

The endpoint accepts optional API keys through `server.torznab_api_keys` in the YAML config. If no keys are configured, the endpoint is public.

Category filtering uses Torznab category IDs and maps them onto the indexed torrent categories on a best-effort basis.

## Roadmap

- Initial Go implementation of the ingestion pipeline.
- Configurable source adapters.
- SQLite-backed persistence and PostgreSQL backend support.
- Search and filtering endpoints for downstream consumers.
- Web UI built on top of the backend layer.

## Project Layout

Expected repository naming and file references may use `magnet-atlas` where a filename-safe form is needed.
