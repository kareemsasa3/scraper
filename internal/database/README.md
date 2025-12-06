# Database Package - SQLite Memory Layer

This package provides persistent storage for web scrape snapshots using SQLite with content-addressable deduplication.

## Overview

The database package implements Arachne's "memory layer" - a persistent store that remembers every page scraped and intelligently deduplicates unchanged content.

## Features

- **Content-Addressable Storage**: Uses SHA256 hashing to detect real content changes
- **Smart Deduplication**: Unchanged pages only update timestamps, not create new rows
- **Fast Lookups**: Indexed queries on URL, domain, content_hash, and timestamps
- **Concurrent Writes**: SQLite WAL mode enables concurrent reads during writes
- **Connection Pooling**: Configurable connection pool (25 max open, 5 idle)
- **Domain Extraction**: Automatic domain parsing with www-prefix removal

## Schema

```sql
CREATE TABLE snapshots (
    id TEXT PRIMARY KEY,              -- UUID
    url TEXT NOT NULL,
    domain TEXT NOT NULL,             -- Extracted from URL
    content_hash TEXT NOT NULL,       -- SHA256 of clean_text
    
    -- Content fields
    title TEXT,
    clean_text TEXT,                  -- Markdown/plain text version
    raw_html TEXT,                    -- Original HTML (optional)
    
    -- Metadata
    scraped_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_checked_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    status_code INTEGER
);

-- Indexes for fast lookups
CREATE INDEX idx_url ON snapshots(url);
CREATE INDEX idx_domain ON snapshots(domain);
CREATE INDEX idx_content_hash ON snapshots(content_hash);
CREATE INDEX idx_scraped_at ON snapshots(scraped_at DESC);
CREATE INDEX idx_url_hash ON snapshots(url, content_hash);
```

## Usage

### Initialize Database

```go
import "github.com/kareemsasa3/arachne/internal/database"

// Create logger (implements database.Logger interface)
logger := &MyLogger{}

// Initialize database
db, err := database.Initialize("/path/to/snapshots.db", logger)
if err != nil {
    log.Fatal(err)
}
defer db.Close()
```

### Save Snapshot

```go
snapshot := &database.Snapshot{
    URL:        "https://example.com/page",
    Title:      "Example Page",
    CleanText:  "This is the page content",
    StatusCode: 200,
}

// Domain and content_hash are computed automatically
err := db.SaveSnapshot(snapshot)
if err != nil {
    log.Fatal(err)
}
```

### Retrieve Latest Snapshot

```go
snapshot, err := db.GetLatestSnapshot("https://example.com/page")
if err != nil {
    log.Fatal(err)
}

if snapshot == nil {
    fmt.Println("URL never scraped")
} else {
    fmt.Printf("Last scraped: %v\n", snapshot.ScrapedAt)
    fmt.Printf("Content hash: %s\n", snapshot.ContentHash)
}
```

### Query by Domain

```go
snapshots, err := db.GetSnapshotsByDomain("example.com", 10)
if err != nil {
    log.Fatal(err)
}

for _, snapshot := range snapshots {
    fmt.Printf("%s - %s\n", snapshot.URL, snapshot.Title)
}
```

### Get Database Statistics

```go
stats, err := db.GetStats()
if err != nil {
    log.Fatal(err)
}

fmt.Printf("Total snapshots: %d\n", stats["total_snapshots"])
fmt.Printf("Unique URLs: %d\n", stats["unique_urls"])
fmt.Printf("Unique domains: %d\n", stats["unique_domains"])
fmt.Printf("DB size: %d bytes\n", stats["db_size_bytes"])
```

## Deduplication Logic

The package implements intelligent deduplication based on content hashing:

```
IF url never seen before:
    → Create new snapshot

ELSE IF content_hash changed:
    → Create new snapshot (content changed)

ELSE:
    → Update last_checked_at only (content unchanged)
```

**Example**: A page scraped 100 times with no changes results in:
- 1 snapshot row (with original content)
- 100 timestamp updates (efficient storage)

## Content Hashing

Content hashes are computed using SHA256:

```go
hash := sha256.Sum256([]byte(cleanText))
contentHash := hex.EncodeToString(hash[:])
// Result: 64-character hex string
```

## Domain Extraction

Domains are automatically extracted from URLs:

| URL | Extracted Domain |
|-----|------------------|
| `https://example.com/path` | `example.com` |
| `https://www.example.com/path` | `example.com` |
| `https://subdomain.example.com/path` | `subdomain.example.com` |
| `https://news.ycombinator.com/item?id=123` | `news.ycombinator.com` |

Rules:
- `www.` prefix is removed
- Subdomains are preserved
- Query parameters and paths are ignored

## Logger Interface

The database package requires a logger that implements:

```go
type Logger interface {
    Info(format string, v ...interface{})
    Debug(format string, v ...interface{})
    Warn(format string, v ...interface{})
    Error(format string, v ...interface{})
}
```

## Performance

- **WAL Mode**: Enables concurrent reads during writes
- **Connection Pool**: 25 max open connections, 5 idle
- **Prepared Statements**: All queries use prepared statements
- **Indexed Queries**: Fast lookups on URL, domain, hash, timestamp

## Testing

Run the test suite:

```bash
go test -v ./internal/database/
```

All tests use temporary databases that are cleaned up automatically.

## Docker Integration

The database is designed to persist across container restarts:

```yaml
services:
  scraper:
    volumes:
      - ./data:/app/data
    environment:
      - SCRAPER_DB_PATH=/app/data/snapshots.db
```

The `data/` directory should be in `.gitignore` but tracked with `.gitkeep`:

```gitignore
data/*.db
data/*.db-shm
data/*.db-wal
```

## API Integration

The database integrates with Arachne's API via the `/memory/lookup` endpoint:

```bash
# Check if URL exists in memory
curl "http://localhost:8080/memory/lookup?url=https%3A%2F%2Fexample.com"
```

Response:

```json
{
  "found": true,
  "snapshot": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "url": "https://example.com",
    "domain": "example.com",
    "title": "Example Domain",
    "content_hash": "a3f5b2c1d4e5f6...",
    "scraped_at": "2025-12-06T12:00:00Z",
    "last_checked_at": "2025-12-06T14:30:00Z",
    "age_hours": 2.5,
    "status_code": 200
  }
}
```

## Future Enhancements (Phase 2+)

- **Full-Text Search**: SQLite FTS5 for semantic search
- **Diff Endpoint**: Compare snapshots across time
- **Retention Policies**: Automatic cleanup of old snapshots
- **Vector Search**: pgvector integration for semantic queries
- **Migration to Postgres**: When scaling beyond 100k snapshots

## Architecture Principles

This implementation follows the core architectural principles:

1. **Separation of Concerns**: Database logic isolated from scraping logic
2. **API-First Design**: Accessed via REST endpoints, not direct DB access
3. **Content-Addressable**: Efficient deduplication via hashing
4. **Incremental Complexity**: Simple SQLite first, migrate to Postgres later

## License

Part of the Arachne web scraping engine.

