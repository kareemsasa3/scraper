# Quick Start: Memory Layer

This guide shows you how to use Arachne's new SQLite memory layer in 5 minutes.

## Prerequisites

- Docker and Docker Compose installed
- `curl` and `jq` for testing (optional)

## Step 1: Start Arachne

```bash
cd arachne
docker-compose up
```

You should see:
```
✅ Memory database initialized at /app/data/snapshots.db
🚀 Starting API server on port 8080
```

## Step 2: Check Memory (Empty)

```bash
curl "http://localhost:8080/memory/lookup?url=https%3A%2F%2Fexample.com" | jq
```

Response:
```json
{
  "found": false
}
```

## Step 3: Scrape a URL

```bash
curl -X POST http://localhost:8080/scrape \
  -H "Content-Type: application/json" \
  -d '{"urls": ["https://example.com"]}' | jq
```

Response:
```json
{
  "job_id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "accepted",
  "results": []
}
```

Wait a few seconds for the job to complete.

## Step 4: Check Memory Again (Found!)

```bash
curl "http://localhost:8080/memory/lookup?url=https%3A%2F%2Fexample.com" | jq
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
    "content_hash": "a3f5b2c1d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1",
    "scraped_at": "2025-12-06T12:00:00Z",
    "last_checked_at": "2025-12-06T12:00:00Z",
    "age_hours": 0.0,
    "status_code": 200
  }
}
```

## Step 5: Test Deduplication

Scrape the same URL again:

```bash
curl -X POST http://localhost:8080/scrape \
  -H "Content-Type: application/json" \
  -d '{"urls": ["https://example.com"]}' | jq
```

Wait a few seconds, then check memory:

```bash
curl "http://localhost:8080/memory/lookup?url=https%3A%2F%2Fexample.com" | jq
```

Notice:
- Same `id` (no new snapshot created)
- Same `scraped_at` (original scrape time)
- Updated `last_checked_at` (just now)
- Same `content_hash` (content unchanged)

**This is deduplication in action!** Unchanged content doesn't create duplicate rows.

## Step 6: Verify Persistence

Stop and restart Arachne:

```bash
docker-compose down
docker-compose up
```

Check memory again:

```bash
curl "http://localhost:8080/memory/lookup?url=https%3A%2F%2Fexample.com" | jq
```

The snapshot is still there! Data persists in `./data/snapshots.db`.

## Automated Test

Run the included test script:

```bash
./test_memory.sh
```

This tests:
1. Health check
2. Memory lookup (empty)
3. Scraping
4. Memory lookup (found)
5. Re-scraping (deduplication)
6. Final memory check (updated timestamp)

## Understanding the Response

### When URL Not Found
```json
{
  "found": false
}
```

### When URL Found
```json
{
  "found": true,
  "snapshot": {
    "id": "uuid",                    // Unique snapshot ID
    "url": "https://example.com",    // Original URL
    "domain": "example.com",         // Extracted domain
    "title": "Page Title",           // Page title
    "content_hash": "sha256...",     // SHA256 of content
    "scraped_at": "timestamp",       // First scrape time
    "last_checked_at": "timestamp",  // Most recent check
    "age_hours": 2.5,                // Hours since first scrape
    "status_code": 200               // HTTP status
  }
}
```

## Key Concepts

### Content Hashing
- Every snapshot has a SHA256 hash of its content
- If content changes, hash changes → new snapshot
- If content unchanged, hash same → timestamp update only

### Domain Extraction
- Automatically extracts domain from URL
- Removes `www.` prefix
- Preserves subdomains
- Examples:
  - `https://www.example.com/path` → `example.com`
  - `https://news.ycombinator.com/item?id=123` → `news.ycombinator.com`

### Deduplication
- Saves storage by not duplicating unchanged content
- 100 scrapes of unchanged page = 1 content entry + 100 timestamp updates
- Efficient for monitoring pages that rarely change

## Database Location

The SQLite database is stored at:
```
./data/snapshots.db
```

You can inspect it with:
```bash
sqlite3 ./data/snapshots.db "SELECT url, title, scraped_at FROM snapshots LIMIT 10;"
```

## Environment Variables

Configure the database path:
```bash
export SCRAPER_DB_PATH=/custom/path/snapshots.db
```

Default: `/app/data/snapshots.db`

## Troubleshooting

### Database Not Initializing
Check logs:
```bash
docker-compose logs scraper | grep -i database
```

### Permission Issues
Ensure `./data` directory is writable:
```bash
chmod 755 ./data
```

### Database Locked
SQLite uses WAL mode for concurrent access. If you see "database locked" errors, check:
1. No other processes are accessing the database
2. The `./data` directory is on a local filesystem (not NFS)

## Next Steps

- **Phase 2**: Full-text search with `/memory/search?q=X`
- **Phase 3**: Diff snapshots with `/memory/diff?url=X&v1=ID&v2=ID`
- **Phase 4**: Automated scraping with scheduled jobs

## API Reference

### GET /memory/lookup

**Parameters:**
- `url` (required): URL-encoded URL to look up

**Response:**
- `found` (boolean): Whether URL exists in memory
- `snapshot` (object, optional): Snapshot data if found

**Example:**
```bash
curl "http://localhost:8080/memory/lookup?url=https%3A%2F%2Fexample.com"
```

### POST /scrape

**Body:**
```json
{
  "urls": ["https://example.com", "https://example.org"]
}
```

**Response:**
```json
{
  "job_id": "uuid",
  "status": "accepted"
}
```

Snapshots are automatically saved after successful scrapes.

## Learn More

- `internal/database/README.md` - Package documentation
- `MEMORY_LAYER_IMPLEMENTATION.md` - Implementation details
- `PHASE_1_COMPLETE.md` - Completion summary
- `CORE_ARCHITECTURAL_PRINCIPLES.md` - Architecture overview

---

**Questions?** Check the documentation or open an issue.

