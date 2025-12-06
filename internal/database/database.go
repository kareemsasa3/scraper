package database

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

// Logger interface for database logging
type Logger interface {
	Info(format string, v ...interface{})
	Debug(format string, v ...interface{})
	Warn(format string, v ...interface{})
	Error(format string, v ...interface{})
}

// DB wraps the SQLite database connection
type DB struct {
	conn   *sql.DB
	logger Logger
}

// Snapshot represents a web page snapshot in the database
type Snapshot struct {
	ID              string    `json:"id"`
	URL             string    `json:"url"`
	Domain          string    `json:"domain"`
	ContentHash     string    `json:"content_hash"`
	Title           string    `json:"title"`
	CleanText       string    `json:"clean_text"`
	RawHTML         string    `json:"raw_html,omitempty"`
	Summary         string    `json:"summary,omitempty"`
	ScrapedAt       time.Time `json:"scraped_at"`
	LastCheckedAt   time.Time `json:"last_checked_at"`
	StatusCode      int       `json:"status_code"`
}

// Initialize creates a new database connection and runs migrations
func Initialize(dbPath string, log Logger) (*DB, error) {
	// Create database connection
	conn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool
	conn.SetMaxOpenConns(25)
	conn.SetMaxIdleConns(5)
	conn.SetConnMaxLifetime(time.Hour)

	// Enable WAL mode for better concurrent write performance
	if _, err := conn.Exec("PRAGMA journal_mode=WAL"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to enable WAL mode: %w", err)
	}

	db := &DB{
		conn:   conn,
		logger: log,
	}

	// Run schema migrations
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	db.logger.Info("Database initialized successfully at %s", dbPath)
	return db, nil
}

// migrate creates the schema if it doesn't exist
func (db *DB) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS snapshots (
		id TEXT PRIMARY KEY,
		url TEXT NOT NULL,
		domain TEXT NOT NULL,
		content_hash TEXT NOT NULL,
		
		-- Content fields
		title TEXT,
		clean_text TEXT,
		raw_html TEXT,
		summary TEXT,
		
		-- Metadata
		scraped_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		last_checked_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		status_code INTEGER
	);

	-- Indexes for fast lookups
	CREATE INDEX IF NOT EXISTS idx_url ON snapshots(url);
	CREATE INDEX IF NOT EXISTS idx_domain ON snapshots(domain);
	CREATE INDEX IF NOT EXISTS idx_content_hash ON snapshots(content_hash);
	CREATE INDEX IF NOT EXISTS idx_scraped_at ON snapshots(scraped_at DESC);
	CREATE INDEX IF NOT EXISTS idx_url_hash ON snapshots(url, content_hash);
	`

	if _, err := db.conn.Exec(schema); err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	// Migration: Add summary column if it doesn't exist (for existing databases)
	addSummaryColumn := `
	ALTER TABLE snapshots ADD COLUMN summary TEXT;
	`
	
	// Try to add the column - will fail silently if it already exists
	_, err := db.conn.Exec(addSummaryColumn)
	if err != nil {
		// Check if error is "duplicate column name" which is expected
		if !strings.Contains(err.Error(), "duplicate column") {
			db.logger.Warn("Note: summary column may already exist or migration skipped: %v", err)
		}
	} else {
		db.logger.Info("Added summary column to snapshots table")
	}

	return nil
}

// SaveSnapshot saves or updates a snapshot in the database
// If the URL exists with the same content_hash, only last_checked_at is updated
// If the content_hash differs, a new snapshot is created
func (db *DB) SaveSnapshot(snapshot *Snapshot) error {
	// Compute content hash if not provided
	if snapshot.ContentHash == "" {
		snapshot.ContentHash = computeContentHash(snapshot.CleanText)
	}

	// Extract domain if not provided
	if snapshot.Domain == "" {
		var err error
		snapshot.Domain, err = extractDomain(snapshot.URL)
		if err != nil {
			return fmt.Errorf("failed to extract domain: %w", err)
		}
	}

	// Check if we already have this exact content
	existing, err := db.GetLatestSnapshot(snapshot.URL)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("failed to check existing snapshot: %w", err)
	}

	// If we have the same content, just update last_checked_at
	if existing != nil && existing.ContentHash == snapshot.ContentHash {
		query := `UPDATE snapshots SET last_checked_at = ? WHERE id = ?`
		now := time.Now()
		if _, err := db.conn.Exec(query, now, existing.ID); err != nil {
			return fmt.Errorf("failed to update last_checked_at: %w", err)
		}
		db.logger.Info("Updated last_checked_at for URL: %s (content unchanged)", snapshot.URL)
		return nil
	}

	// Content is new or changed - insert new snapshot
	if snapshot.ID == "" {
		snapshot.ID = uuid.New().String()
	}

	now := time.Now()
	snapshot.ScrapedAt = now
	snapshot.LastCheckedAt = now

	query := `
		INSERT INTO snapshots (
			id, url, domain, content_hash, title, clean_text, raw_html, summary,
			scraped_at, last_checked_at, status_code
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err = db.conn.Exec(
		query,
		snapshot.ID,
		snapshot.URL,
		snapshot.Domain,
		snapshot.ContentHash,
		snapshot.Title,
		snapshot.CleanText,
		snapshot.RawHTML,
		snapshot.Summary,
		snapshot.ScrapedAt,
		snapshot.LastCheckedAt,
		snapshot.StatusCode,
	)

	if err != nil {
		return fmt.Errorf("failed to insert snapshot: %w", err)
	}

	if existing != nil {
		db.logger.Info("Saved new snapshot for URL: %s (content changed, hash: %s)", snapshot.URL, snapshot.ContentHash[:8])
	} else {
		db.logger.Info("Saved first snapshot for URL: %s (hash: %s)", snapshot.URL, snapshot.ContentHash[:8])
	}

	return nil
}

// GetLatestSnapshot retrieves the most recent snapshot for a given URL
func (db *DB) GetLatestSnapshot(urlStr string) (*Snapshot, error) {
	query := `
		SELECT id, url, domain, content_hash, title, clean_text, raw_html, summary,
		       scraped_at, last_checked_at, status_code
		FROM snapshots
		WHERE url = ?
		ORDER BY scraped_at DESC
		LIMIT 1
	`

	var snapshot Snapshot
	var summary sql.NullString
	err := db.conn.QueryRow(query, urlStr).Scan(
		&snapshot.ID,
		&snapshot.URL,
		&snapshot.Domain,
		&snapshot.ContentHash,
		&snapshot.Title,
		&snapshot.CleanText,
		&snapshot.RawHTML,
		&summary,
		&snapshot.ScrapedAt,
		&snapshot.LastCheckedAt,
		&snapshot.StatusCode,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("failed to query snapshot: %w", err)
	}

	if summary.Valid {
		snapshot.Summary = summary.String
	}

	return &snapshot, nil
}

// GetSnapshotByID retrieves a snapshot by its ID
func (db *DB) GetSnapshotByID(id string) (*Snapshot, error) {
	query := `
		SELECT id, url, domain, content_hash, title, clean_text, raw_html, summary,
		       scraped_at, last_checked_at, status_code
		FROM snapshots
		WHERE id = ?
	`

	var snapshot Snapshot
	var summary sql.NullString
	err := db.conn.QueryRow(query, id).Scan(
		&snapshot.ID,
		&snapshot.URL,
		&snapshot.Domain,
		&snapshot.ContentHash,
		&snapshot.Title,
		&snapshot.CleanText,
		&snapshot.RawHTML,
		&summary,
		&snapshot.ScrapedAt,
		&snapshot.LastCheckedAt,
		&snapshot.StatusCode,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("failed to query snapshot: %w", err)
	}

	if summary.Valid {
		snapshot.Summary = summary.String
	}

	return &snapshot, nil
}

// GetSnapshotsByDomain retrieves all snapshots for a given domain
func (db *DB) GetSnapshotsByDomain(domain string, limit int) ([]*Snapshot, error) {
	query := `
		SELECT id, url, domain, content_hash, title, clean_text, raw_html, summary,
		       scraped_at, last_checked_at, status_code
		FROM snapshots
		WHERE domain = ?
		ORDER BY scraped_at DESC
		LIMIT ?
	`

	rows, err := db.conn.Query(query, domain, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query snapshots by domain: %w", err)
	}
	defer rows.Close()

	var snapshots []*Snapshot
	for rows.Next() {
		var snapshot Snapshot
		var summary sql.NullString
		err := rows.Scan(
			&snapshot.ID,
			&snapshot.URL,
			&snapshot.Domain,
			&snapshot.ContentHash,
			&snapshot.Title,
			&snapshot.CleanText,
			&snapshot.RawHTML,
			&summary,
			&snapshot.ScrapedAt,
			&snapshot.LastCheckedAt,
			&snapshot.StatusCode,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan snapshot: %w", err)
		}
		if summary.Valid {
			snapshot.Summary = summary.String
		}
		snapshots = append(snapshots, &snapshot)
	}

	return snapshots, nil
}

// UpdateSnapshotSummary updates the summary field for a given snapshot
func (db *DB) UpdateSnapshotSummary(snapshotID string, summary string) error {
	query := `UPDATE snapshots SET summary = ? WHERE id = ?`
	
	result, err := db.conn.Exec(query, summary, snapshotID)
	if err != nil {
		return fmt.Errorf("failed to update snapshot summary: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("snapshot not found: %s", snapshotID)
	}

	db.logger.Info("Updated summary for snapshot %s", snapshotID)
	return nil
}

// Close closes the database connection
func (db *DB) Close() error {
	if db.conn != nil {
		return db.conn.Close()
	}
	return nil
}

// computeContentHash computes SHA256 hash of content
func computeContentHash(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}

// extractDomain extracts the domain from a URL
func extractDomain(urlStr string) (string, error) {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}

	domain := parsed.Host

	// Remove www. prefix if present
	if strings.HasPrefix(domain, "www.") {
		domain = domain[4:]
	}

	return domain, nil
}

// GetRecentSnapshots retrieves recent snapshots with pagination
// Returns snapshots ordered by scraped_at DESC, the total count, and any error
func (db *DB) GetRecentSnapshots(limit, offset int) ([]*Snapshot, int, error) {
	// Set default and max limits
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}

	// Get total count
	var total int
	err := db.conn.QueryRow("SELECT COUNT(*) FROM snapshots").Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get total count: %w", err)
	}

	// Get paginated snapshots
	query := `
		SELECT id, url, domain, content_hash, title, clean_text, raw_html, summary,
		       scraped_at, last_checked_at, status_code
		FROM snapshots
		ORDER BY scraped_at DESC
		LIMIT ? OFFSET ?
	`

	rows, err := db.conn.Query(query, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query snapshots: %w", err)
	}
	defer rows.Close()

	var snapshots []*Snapshot
	for rows.Next() {
		var snapshot Snapshot
		var summary sql.NullString
		err := rows.Scan(
			&snapshot.ID,
			&snapshot.URL,
			&snapshot.Domain,
			&snapshot.ContentHash,
			&snapshot.Title,
			&snapshot.CleanText,
			&snapshot.RawHTML,
			&summary,
			&snapshot.ScrapedAt,
			&snapshot.LastCheckedAt,
			&snapshot.StatusCode,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan snapshot: %w", err)
		}
		if summary.Valid {
			snapshot.Summary = summary.String
		}
		snapshots = append(snapshots, &snapshot)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("error iterating snapshots: %w", err)
	}

	return snapshots, total, nil
}

// GetStats returns database statistics
func (db *DB) GetStats() (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	// Total snapshots
	var totalSnapshots int
	err := db.conn.QueryRow("SELECT COUNT(*) FROM snapshots").Scan(&totalSnapshots)
	if err != nil {
		return nil, fmt.Errorf("failed to get total snapshots: %w", err)
	}
	stats["total_snapshots"] = totalSnapshots

	// Unique URLs
	var uniqueURLs int
	err = db.conn.QueryRow("SELECT COUNT(DISTINCT url) FROM snapshots").Scan(&uniqueURLs)
	if err != nil {
		return nil, fmt.Errorf("failed to get unique URLs: %w", err)
	}
	stats["unique_urls"] = uniqueURLs

	// Unique domains
	var uniqueDomains int
	err = db.conn.QueryRow("SELECT COUNT(DISTINCT domain) FROM snapshots").Scan(&uniqueDomains)
	if err != nil {
		return nil, fmt.Errorf("failed to get unique domains: %w", err)
	}
	stats["unique_domains"] = uniqueDomains

	// Database file size
	var pageCount, pageSize int
	err = db.conn.QueryRow("PRAGMA page_count").Scan(&pageCount)
	if err == nil {
		err = db.conn.QueryRow("PRAGMA page_size").Scan(&pageSize)
		if err == nil {
			stats["db_size_bytes"] = pageCount * pageSize
		}
	}

	return stats, nil
}

