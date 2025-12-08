package database

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

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

// Snapshot represents a scrape history entry in the database.
// We keep the name for backward compatibility with the API layer.
type Snapshot struct {
	ID            string    `json:"id"`
	URL           string    `json:"url"`
	Domain        string    `json:"domain"`
	ContentHash   string    `json:"content_hash"`
	Title         string    `json:"title"`
	CleanText     string    `json:"clean_text"`
	RawContent    string    `json:"raw_content,omitempty"`
	Summary       string    `json:"summary,omitempty"`
	ScrapedAt     time.Time `json:"scraped_at"`
	LastCheckedAt time.Time `json:"last_checked_at"`
	StatusCode    int       `json:"status_code"`
	PreviousHash  string    `json:"previous_hash,omitempty"`
	HasChanges    bool      `json:"has_changes"`
	ChangeSummary string    `json:"change_summary,omitempty"`
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

// migrate creates or updates the schema to the unified scrape_history table.
func (db *DB) migrate() error {
	if err := db.createHistoryTable(); err != nil {
		return err
	}

	if err := db.migrateSnapshotsToHistory(); err != nil {
		return err
	}

	return nil
}

func (db *DB) createHistoryTable() error {
	schema := `
	CREATE TABLE IF NOT EXISTS scrape_history (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		url TEXT NOT NULL,
		domain TEXT,
		title TEXT,
		clean_text TEXT,
		raw_content TEXT NOT NULL,
		summary TEXT,
		content_hash TEXT NOT NULL,
		previous_hash TEXT,
		has_changes INTEGER DEFAULT 0,
		change_summary TEXT,
		status_code INTEGER,
		scraped_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		last_checked_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_history_url_time ON scrape_history(url, scraped_at DESC);
	CREATE INDEX IF NOT EXISTS idx_history_hash ON scrape_history(content_hash);
	CREATE INDEX IF NOT EXISTS idx_history_url_hash ON scrape_history(url, content_hash);
	`

	if _, err := db.conn.Exec(schema); err != nil {
		return fmt.Errorf("failed to create scrape_history schema: %w", err)
	}

	return nil
}

// migrateSnapshotsToHistory copies data from the legacy snapshots table (if present)
// into scrape_history and then drops the legacy table.
func (db *DB) migrateSnapshotsToHistory() (retErr error) {
	var oldTableCount int
	if err := db.conn.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='snapshots'",
	).Scan(&oldTableCount); err != nil {
		return fmt.Errorf("failed to check legacy snapshots table: %w", err)
	}

	if oldTableCount == 0 {
		return nil
	}

	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("failed to start migration transaction: %w", err)
	}
	defer func() {
		if retErr != nil {
			_ = tx.Rollback()
		}
	}()

	var historyCount int
	if err := tx.QueryRow("SELECT COUNT(*) FROM scrape_history").Scan(&historyCount); err != nil {
		retErr = fmt.Errorf("failed to count scrape_history rows: %w", err)
		return retErr
	}

	if historyCount == 0 {
		if _, err := tx.Exec(`
			INSERT INTO scrape_history (
				url, domain, title, clean_text, raw_content, summary, content_hash,
				status_code, scraped_at, last_checked_at
			)
			SELECT
				url, domain, title, clean_text,
				COALESCE(raw_html, clean_text, ''),
				summary, content_hash, status_code, scraped_at, last_checked_at
			FROM snapshots
		`); err != nil {
			retErr = fmt.Errorf("failed to migrate data from snapshots: %w", err)
			return retErr
		}
	}

	if _, err := tx.Exec("DROP TABLE snapshots"); err != nil {
		retErr = fmt.Errorf("failed to drop legacy snapshots table: %w", err)
		return retErr
	}

	if err := tx.Commit(); err != nil {
		retErr = fmt.Errorf("failed to commit migration: %w", err)
		return retErr
	}

	db.logger.Info("Migrated legacy snapshots table to scrape_history")
	return nil
}

// SaveSnapshot saves or updates a snapshot in the database.
// If the URL exists with the same content_hash, only last_checked_at is updated.
// If the content_hash differs, a new history row is created with change metadata.
func (db *DB) SaveSnapshot(snapshot *Snapshot) error {
	// Compute content hash if not provided
	if snapshot.ContentHash == "" {
		source := snapshot.CleanText
		if source == "" {
			source = snapshot.RawContent
		}
		snapshot.ContentHash = computeContentHash(source)
	}

	// Extract domain if not provided
	if snapshot.Domain == "" {
		var err error
		snapshot.Domain, err = extractDomain(snapshot.URL)
		if err != nil {
			return fmt.Errorf("failed to extract domain: %w", err)
		}
	}

	// Ensure we have something to persist into raw_content (NOT NULL)
	rawContent := snapshot.RawContent
	if rawContent == "" {
		rawContent = snapshot.CleanText
	}

	// Check if we already have this exact content
	existing, err := db.GetLatestSnapshot(snapshot.URL)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("failed to check existing snapshot: %w", err)
	}

	// If we have the same content, just update last_checked_at
	if existing != nil && existing.ContentHash == snapshot.ContentHash {
		query := `UPDATE scrape_history SET last_checked_at = ? WHERE id = ?`
		now := time.Now()
		if _, err := db.conn.Exec(query, now, existing.ID); err != nil {
			return fmt.Errorf("failed to update last_checked_at: %w", err)
		}
		snapshot.ID = existing.ID
		snapshot.ScrapedAt = existing.ScrapedAt
		snapshot.LastCheckedAt = now
		snapshot.PreviousHash = existing.PreviousHash
		snapshot.HasChanges = existing.HasChanges
		snapshot.ChangeSummary = existing.ChangeSummary
		db.logger.Info("Updated last_checked_at for URL: %s (content unchanged)", snapshot.URL)
		return nil
	}

	now := time.Now()
	snapshot.ScrapedAt = now
	snapshot.LastCheckedAt = now

	previousHash := ""
	if existing != nil {
		previousHash = existing.ContentHash
	}
	hasChanges := previousHash != "" && previousHash != snapshot.ContentHash

	query := `
		INSERT INTO scrape_history (
			url, domain, title, clean_text, raw_content, summary, content_hash,
			previous_hash, has_changes, change_summary, status_code, scraped_at, last_checked_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	result, err := db.conn.Exec(
		query,
		snapshot.URL,
		snapshot.Domain,
		snapshot.Title,
		snapshot.CleanText,
		rawContent,
		snapshot.Summary,
		snapshot.ContentHash,
		previousHash,
		boolToInt(hasChanges),
		snapshot.ChangeSummary,
		snapshot.StatusCode,
		snapshot.ScrapedAt,
		snapshot.LastCheckedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to insert snapshot: %w", err)
	}

	if id, err := result.LastInsertId(); err == nil {
		snapshot.ID = strconv.FormatInt(id, 10)
	}
	snapshot.PreviousHash = previousHash
	snapshot.HasChanges = hasChanges

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
		SELECT id, url, domain, title, clean_text, raw_content, summary,
		       content_hash, previous_hash, has_changes, change_summary,
		       status_code, scraped_at, last_checked_at
		FROM scrape_history
		WHERE url = ?
		ORDER BY scraped_at DESC
		LIMIT 1
	`

	var snapshot Snapshot
	var summary sql.NullString
	var previousHash sql.NullString
	var changeSummary sql.NullString
	var hasChangesInt int
	var id int64
	err := db.conn.QueryRow(query, urlStr).Scan(
		&id,
		&snapshot.URL,
		&snapshot.Domain,
		&snapshot.Title,
		&snapshot.CleanText,
		&snapshot.RawContent,
		&summary,
		&snapshot.ContentHash,
		&previousHash,
		&hasChangesInt,
		&changeSummary,
		&snapshot.StatusCode,
		&snapshot.ScrapedAt,
		&snapshot.LastCheckedAt,
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
	if previousHash.Valid {
		snapshot.PreviousHash = previousHash.String
	}
	if changeSummary.Valid {
		snapshot.ChangeSummary = changeSummary.String
	}
	snapshot.HasChanges = hasChangesInt != 0
	snapshot.ID = strconv.FormatInt(id, 10)

	return &snapshot, nil
}

// GetSnapshotByID retrieves a snapshot by its ID
func (db *DB) GetSnapshotByID(id string) (*Snapshot, error) {
	query := `
		SELECT id, url, domain, title, clean_text, raw_content, summary,
		       content_hash, previous_hash, has_changes, change_summary,
		       status_code, scraped_at, last_checked_at
		FROM scrape_history
		WHERE id = ?
	`

	var snapshot Snapshot
	var summary sql.NullString
	var previousHash sql.NullString
	var changeSummary sql.NullString
	var hasChangesInt int
	var numericID int64
	err := db.conn.QueryRow(query, id).Scan(
		&numericID,
		&snapshot.URL,
		&snapshot.Domain,
		&snapshot.Title,
		&snapshot.CleanText,
		&snapshot.RawContent,
		&summary,
		&snapshot.ContentHash,
		&previousHash,
		&hasChangesInt,
		&changeSummary,
		&snapshot.StatusCode,
		&snapshot.ScrapedAt,
		&snapshot.LastCheckedAt,
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
	if previousHash.Valid {
		snapshot.PreviousHash = previousHash.String
	}
	if changeSummary.Valid {
		snapshot.ChangeSummary = changeSummary.String
	}
	snapshot.HasChanges = hasChangesInt != 0
	snapshot.ID = strconv.FormatInt(numericID, 10)

	return &snapshot, nil
}

// GetHistoryByURL returns all snapshots for a URL ordered by scraped_at DESC.
func (db *DB) GetHistoryByURL(urlStr string) ([]*Snapshot, error) {
	query := `
		SELECT id, url, domain, title, clean_text, raw_content, summary,
		       content_hash, previous_hash, has_changes, change_summary,
		       status_code, scraped_at, last_checked_at
		FROM scrape_history
		WHERE url = ?
		ORDER BY scraped_at DESC
	`

	rows, err := db.conn.Query(query, urlStr)
	if err != nil {
		return nil, fmt.Errorf("failed to query history: %w", err)
	}
	defer rows.Close()

	var snapshots []*Snapshot
	for rows.Next() {
		var snapshot Snapshot
		var summary sql.NullString
		var previousHash sql.NullString
		var changeSummary sql.NullString
		var hasChangesInt int
		var id int64
		if err := rows.Scan(
			&id,
			&snapshot.URL,
			&snapshot.Domain,
			&snapshot.Title,
			&snapshot.CleanText,
			&snapshot.RawContent,
			&summary,
			&snapshot.ContentHash,
			&previousHash,
			&hasChangesInt,
			&changeSummary,
			&snapshot.StatusCode,
			&snapshot.ScrapedAt,
			&snapshot.LastCheckedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan history row: %w", err)
		}
		if summary.Valid {
			snapshot.Summary = summary.String
		}
		if previousHash.Valid {
			snapshot.PreviousHash = previousHash.String
		}
		if changeSummary.Valid {
			snapshot.ChangeSummary = changeSummary.String
		}
		snapshot.HasChanges = hasChangesInt != 0
		snapshot.ID = strconv.FormatInt(id, 10)
		snapshots = append(snapshots, &snapshot)
	}

	return snapshots, nil
}

// GetSnapshotByTimestamp returns the snapshot for a URL at an exact scraped_at timestamp.
func (db *DB) GetSnapshotByTimestamp(urlStr string, ts time.Time) (*Snapshot, error) {
	query := `
		SELECT id, url, domain, title, clean_text, raw_content, summary,
		       content_hash, previous_hash, has_changes, change_summary,
		       status_code, scraped_at, last_checked_at
		FROM scrape_history
		WHERE url = ? AND scraped_at = ?
		LIMIT 1
	`

	var snapshot Snapshot
	var summary sql.NullString
	var previousHash sql.NullString
	var changeSummary sql.NullString
	var hasChangesInt int
	var id int64
	err := db.conn.QueryRow(query, urlStr, ts).Scan(
		&id,
		&snapshot.URL,
		&snapshot.Domain,
		&snapshot.Title,
		&snapshot.CleanText,
		&snapshot.RawContent,
		&summary,
		&snapshot.ContentHash,
		&previousHash,
		&hasChangesInt,
		&changeSummary,
		&snapshot.StatusCode,
		&snapshot.ScrapedAt,
		&snapshot.LastCheckedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query snapshot by timestamp: %w", err)
	}

	if summary.Valid {
		snapshot.Summary = summary.String
	}
	if previousHash.Valid {
		snapshot.PreviousHash = previousHash.String
	}
	if changeSummary.Valid {
		snapshot.ChangeSummary = changeSummary.String
	}
	snapshot.HasChanges = hasChangesInt != 0
	snapshot.ID = strconv.FormatInt(id, 10)

	return &snapshot, nil
}

// GetSnapshotsByDomain retrieves all snapshots for a given domain
func (db *DB) GetSnapshotsByDomain(domain string, limit int) ([]*Snapshot, error) {
	query := `
		SELECT id, url, domain, title, clean_text, raw_content, summary,
		       content_hash, previous_hash, has_changes, change_summary,
		       status_code, scraped_at, last_checked_at
		FROM scrape_history
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
		var previousHash sql.NullString
		var changeSummary sql.NullString
		var hasChangesInt int
		var id int64
		err := rows.Scan(
			&id,
			&snapshot.URL,
			&snapshot.Domain,
			&snapshot.Title,
			&snapshot.CleanText,
			&snapshot.RawContent,
			&summary,
			&snapshot.ContentHash,
			&previousHash,
			&hasChangesInt,
			&changeSummary,
			&snapshot.StatusCode,
			&snapshot.ScrapedAt,
			&snapshot.LastCheckedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan snapshot: %w", err)
		}
		if summary.Valid {
			snapshot.Summary = summary.String
		}
		if previousHash.Valid {
			snapshot.PreviousHash = previousHash.String
		}
		if changeSummary.Valid {
			snapshot.ChangeSummary = changeSummary.String
		}
		snapshot.HasChanges = hasChangesInt != 0
		snapshot.ID = strconv.FormatInt(id, 10)
		snapshots = append(snapshots, &snapshot)
	}

	return snapshots, nil
}

// UpdateSnapshotSummary updates the summary field for a given snapshot
func (db *DB) UpdateSnapshotSummary(snapshotID string, summary string) error {
	query := `UPDATE scrape_history SET summary = ? WHERE id = ?`

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

// UpdateChangeSummary updates the change_summary field for a given snapshot
func (db *DB) UpdateChangeSummary(snapshotID string, summary string) error {
	query := `UPDATE scrape_history SET change_summary = ?, has_changes = 1 WHERE id = ?`

	result, err := db.conn.Exec(query, summary, snapshotID)
	if err != nil {
		return fmt.Errorf("failed to update change summary: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("snapshot not found: %s", snapshotID)
	}

	db.logger.Info("Updated change summary for snapshot %s", snapshotID)
	return nil
}

// DeleteSnapshot deletes a snapshot/version by ID
func (db *DB) DeleteSnapshot(snapshotID string) error {
	result, err := db.conn.Exec(`DELETE FROM scrape_history WHERE id = ?`, snapshotID)
	if err != nil {
		return fmt.Errorf("failed to delete snapshot: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("snapshot not found: %s", snapshotID)
	}

	db.logger.Info("Deleted snapshot %s", snapshotID)
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
	err := db.conn.QueryRow("SELECT COUNT(*) FROM scrape_history").Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get total count: %w", err)
	}

	// Get paginated snapshots
	query := `
		SELECT id, url, domain, title, clean_text, raw_content, summary,
		       content_hash, previous_hash, has_changes, change_summary,
		       status_code, scraped_at, last_checked_at
		FROM scrape_history
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
		var previousHash sql.NullString
		var changeSummary sql.NullString
		var hasChangesInt int
		var id int64
		err := rows.Scan(
			&id,
			&snapshot.URL,
			&snapshot.Domain,
			&snapshot.Title,
			&snapshot.CleanText,
			&snapshot.RawContent,
			&summary,
			&snapshot.ContentHash,
			&previousHash,
			&hasChangesInt,
			&changeSummary,
			&snapshot.StatusCode,
			&snapshot.ScrapedAt,
			&snapshot.LastCheckedAt,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan snapshot: %w", err)
		}
		if summary.Valid {
			snapshot.Summary = summary.String
		}
		if previousHash.Valid {
			snapshot.PreviousHash = previousHash.String
		}
		if changeSummary.Valid {
			snapshot.ChangeSummary = changeSummary.String
		}
		snapshot.HasChanges = hasChangesInt != 0
		snapshot.ID = strconv.FormatInt(id, 10)
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
	err := db.conn.QueryRow("SELECT COUNT(*) FROM scrape_history").Scan(&totalSnapshots)
	if err != nil {
		return nil, fmt.Errorf("failed to get total snapshots: %w", err)
	}
	stats["total_snapshots"] = totalSnapshots

	// Unique URLs
	var uniqueURLs int
	err = db.conn.QueryRow("SELECT COUNT(DISTINCT url) FROM scrape_history").Scan(&uniqueURLs)
	if err != nil {
		return nil, fmt.Errorf("failed to get unique URLs: %w", err)
	}
	stats["unique_urls"] = uniqueURLs

	// Unique domains
	var uniqueDomains int
	err = db.conn.QueryRow("SELECT COUNT(DISTINCT domain) FROM scrape_history").Scan(&uniqueDomains)
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

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
