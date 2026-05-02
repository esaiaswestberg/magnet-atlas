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
- The ingestion pipeline normalizes and deduplicates incoming records.
- The storage layer persists the canonical torrent index.
- The query layer serves search and retrieval requests for internal consumers and future APIs.

This separation keeps source-specific behavior isolated and makes it easier to add, remove, or update a source without changing the rest of the system.

## Sources

Source support is configurable. The engine should not assume a fixed set of torrent sites.

Examples of source types the project may support include:

- rarbg
- 1337x.to
- The Pirate Bay

Each source can be enabled, disabled, or configured independently.

## Future Web UI

Magnet Atlas is intended to serve data to a web UI indirectly through a backend interface. The UI should query the indexed dataset rather than scraping torrent sites itself.

This keeps presentation concerns separate from ingestion and allows the backend to evolve independently.

## Roadmap

- Initial Go implementation of the ingestion pipeline.
- Configurable source adapters.
- SQLite-backed persistence for the first working version.
- PostgreSQL support for larger deployments.
- Search and filtering endpoints for downstream consumers.
- Web UI built on top of the backend layer.

## Project Layout

Expected repository naming and file references may use `magnet-atlas` where a filename-safe form is needed.

