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
	ScrapeStatus  string    `json:"scrape_status,omitempty"`
	ErrorMessage  string    `json:"error_message,omitempty"`
	RetryCount    int       `json:"retry_count,omitempty"`
}

// SearchResult represents a row returned from full-text search.
type SearchResult struct {
	ID        int64     `json:"id"`
	URL       string    `json:"url"`
	Domain    string    `json:"domain"`
	Title     string    `json:"title"`
	Snippet   string    `json:"snippet"`
	ScrapedAt time.Time `json:"scraped_at"`
	Rank      float64   `json:"rank"`
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

	if err := db.createFTS5Index(); err != nil {
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
		raw_content TEXT,
		summary TEXT,
		content_hash TEXT,
		previous_hash TEXT,
		has_changes INTEGER DEFAULT 0,
		change_summary TEXT,
		status_code INTEGER,
		scrape_status TEXT DEFAULT 'success',
		error_message TEXT,
		retry_count INTEGER DEFAULT 0,
		scraped_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		last_checked_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_history_url_time ON scrape_history(url, scraped_at DESC);
	CREATE INDEX IF NOT EXISTS idx_history_hash ON scrape_history(content_hash);
	CREATE INDEX IF NOT EXISTS idx_history_url_hash ON scrape_history(url, content_hash);
	CREATE INDEX IF NOT EXISTS idx_history_status ON scrape_history(scrape_status);
	`

	if _, err := db.conn.Exec(schema); err != nil {
		return fmt.Errorf("failed to create scrape_history schema: %w", err)
	}

	// Ensure legacy schemas are migrated to the relaxed/nullable version
	if err := db.migrateHistorySchema(); err != nil {
		return err
	}

	return nil
}

type tableColumn struct {
	Name    string
	NotNull bool
}

// migrateHistorySchema backfills new columns and relaxes NOT NULL constraints introduced
// for failed scrape tracking.
func (db *DB) migrateHistorySchema() error {
	cols, err := db.getTableColumns("scrape_history")
	if err != nil {
		return err
	}
	if len(cols) == 0 {
		return nil
	}

	addColumn := func(name, ddl string) error {
		if _, exists := cols[name]; exists {
			return nil
		}
		if _, err := db.conn.Exec(ddl); err != nil {
			return fmt.Errorf("failed to add column %s: %w", name, err)
		}
		return nil
	}

	if err := addColumn("scrape_status", `ALTER TABLE scrape_history ADD COLUMN scrape_status TEXT DEFAULT 'success'`); err != nil {
		return err
	}
	if err := addColumn("error_message", `ALTER TABLE scrape_history ADD COLUMN error_message TEXT`); err != nil {
		return err
	}
	if err := addColumn("retry_count", `ALTER TABLE scrape_history ADD COLUMN retry_count INTEGER DEFAULT 0`); err != nil {
		return err
	}

	// Refresh column metadata after ALTERs
	cols, err = db.getTableColumns("scrape_history")
	if err != nil {
		return err
	}

	needRebuild := false
	if col, ok := cols["raw_content"]; ok && col.NotNull {
		needRebuild = true
	}
	if col, ok := cols["content_hash"]; ok && col.NotNull {
		needRebuild = true
	}

	if needRebuild {
		if err := db.rebuildScrapeHistoryTable(); err != nil {
			return err
		}
	} else {
		if _, err := db.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_history_status ON scrape_history(scrape_status);`); err != nil {
			return fmt.Errorf("failed to ensure status index: %w", err)
		}
	}

	return nil
}

func (db *DB) getTableColumns(table string) (map[string]tableColumn, error) {
	rows, err := db.conn.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return nil, fmt.Errorf("failed to inspect table %s: %w", table, err)
	}
	defer rows.Close()

	cols := make(map[string]tableColumn)
	for rows.Next() {
		var (
			cid       int
			name      string
			colType   string
			notNull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return nil, fmt.Errorf("failed to scan table info for %s: %w", table, err)
		}
		cols[name] = tableColumn{
			Name:    name,
			NotNull: notNull != 0,
		}
	}

	return cols, nil
}

// rebuildScrapeHistoryTable recreates the scrape_history table with the relaxed schema
// while preserving existing data.
func (db *DB) rebuildScrapeHistoryTable() (retErr error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("failed to start scrape_history rebuild: %w", err)
	}
	defer func() {
		if retErr != nil {
			_ = tx.Rollback()
		}
	}()

	create := `
	CREATE TABLE IF NOT EXISTS scrape_history_new (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		url TEXT NOT NULL,
		domain TEXT,
		title TEXT,
		clean_text TEXT,
		raw_content TEXT,
		summary TEXT,
		content_hash TEXT,
		previous_hash TEXT,
		has_changes INTEGER DEFAULT 0,
		change_summary TEXT,
		status_code INTEGER,
		scrape_status TEXT DEFAULT 'success',
		error_message TEXT,
		retry_count INTEGER DEFAULT 0,
		scraped_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		last_checked_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	`

	if _, err := tx.Exec(create); err != nil {
		retErr = fmt.Errorf("failed to create scrape_history_new: %w", err)
		return retErr
	}

	copyStmt := `
	INSERT INTO scrape_history_new (
		id, url, domain, title, clean_text, raw_content, summary,
		content_hash, previous_hash, has_changes, change_summary,
		status_code, scrape_status, error_message, retry_count,
		scraped_at, last_checked_at
	)
	SELECT
		id, url, domain, title, clean_text, raw_content, summary,
		content_hash, previous_hash, has_changes, change_summary,
		status_code,
		COALESCE(scrape_status, 'success'),
		error_message,
		COALESCE(retry_count, 0),
		scraped_at, last_checked_at
	FROM scrape_history;
	`

	if _, err := tx.Exec(copyStmt); err != nil {
		retErr = fmt.Errorf("failed to copy data into scrape_history_new: %w", err)
		return retErr
	}

	if _, err := tx.Exec(`DROP TABLE scrape_history`); err != nil {
		retErr = fmt.Errorf("failed to drop old scrape_history: %w", err)
		return retErr
	}

	if _, err := tx.Exec(`ALTER TABLE scrape_history_new RENAME TO scrape_history`); err != nil {
		retErr = fmt.Errorf("failed to rename scrape_history_new: %w", err)
		return retErr
	}

	indexes := `
	CREATE INDEX IF NOT EXISTS idx_history_url_time ON scrape_history(url, scraped_at DESC);
	CREATE INDEX IF NOT EXISTS idx_history_hash ON scrape_history(content_hash);
	CREATE INDEX IF NOT EXISTS idx_history_url_hash ON scrape_history(url, content_hash);
	CREATE INDEX IF NOT EXISTS idx_history_status ON scrape_history(scrape_status);
	`

	if _, err := tx.Exec(indexes); err != nil {
		retErr = fmt.Errorf("failed to recreate indexes for scrape_history: %w", err)
		return retErr
	}

	if err := tx.Commit(); err != nil {
		retErr = fmt.Errorf("failed to commit scrape_history rebuild: %w", err)
		return retErr
	}

	db.logger.Info("Rebuilt scrape_history with relaxed constraints and failure tracking")
	return nil
}

// createFTS5Index sets up the FTS5 virtual table, backfills existing data,
// and installs triggers to keep the index in sync with scrape_history.
func (db *DB) createFTS5Index() error {
	createTable := `
	CREATE VIRTUAL TABLE IF NOT EXISTS scrape_search USING fts5(
		url,
		domain,
		title,
		clean_text,
		summary,
		content='scrape_history',
		content_rowid='id'
	);
	`

	if _, err := db.conn.Exec(createTable); err != nil {
		if strings.Contains(err.Error(), "no such module: fts5") {
			return fmt.Errorf("fts5 extension not available; rebuild with `-tags sqlite_fts5`: %w", err)
		}
		return fmt.Errorf("failed to create FTS5 virtual table: %w", err)
	}

	if _, err := db.conn.Exec(`
		INSERT INTO scrape_search(rowid, url, domain, title, clean_text, summary)
		SELECT id, url, domain, title, clean_text, summary
		FROM scrape_history
		WHERE id NOT IN (SELECT rowid FROM scrape_search)
	`); err != nil {
		return fmt.Errorf("failed to backfill FTS5 index: %w", err)
	}

	triggers := `
	CREATE TRIGGER IF NOT EXISTS scrape_history_ai AFTER INSERT ON scrape_history BEGIN
		INSERT INTO scrape_search(rowid, url, domain, title, clean_text, summary)
		VALUES (new.id, new.url, new.domain, new.title, new.clean_text, new.summary);
	END;

	CREATE TRIGGER IF NOT EXISTS scrape_history_au AFTER UPDATE ON scrape_history BEGIN
		UPDATE scrape_search SET
			url = new.url,
			domain = new.domain,
			title = new.title,
			clean_text = new.clean_text,
			summary = new.summary
		WHERE rowid = new.id;
	END;

	CREATE TRIGGER IF NOT EXISTS scrape_history_ad AFTER DELETE ON scrape_history BEGIN
		DELETE FROM scrape_search WHERE rowid = old.id;
	END;
	`

	if _, err := db.conn.Exec(triggers); err != nil {
		return fmt.Errorf("failed to create FTS5 triggers: %w", err)
	}

	return nil
}

// RebuildFTS5 drops and recreates the FTS5 index from scratch.
func (db *DB) RebuildFTS5() error {
	if _, err := db.conn.Exec(`DROP TABLE IF EXISTS scrape_search;`); err != nil {
		return fmt.Errorf("failed to drop FTS5 table: %w", err)
	}
	return db.createFTS5Index()
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
			previous_hash, has_changes, change_summary, status_code, scrape_status,
			error_message, retry_count, scraped_at, last_checked_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'success', NULL, 0, ?, ?)
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
		// scrape_status fixed as success
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

// SaveFailedScrape records a failed scrape attempt, allowing nullable content/hash.
func (db *DB) SaveFailedScrape(url, domain string, statusCode int, errorMsg, scrapeStatus string) error {
	stmt := `
		INSERT INTO scrape_history (
			url, domain, status_code, scrape_status, error_message,
			retry_count, scraped_at, last_checked_at
		) VALUES (?, ?, ?, ?, ?, 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`
	if _, err := db.conn.Exec(stmt, url, domain, statusCode, scrapeStatus, errorMsg); err != nil {
		return fmt.Errorf("failed to save failed scrape: %w", err)
	}
	return nil
}

// UpdateRetryCount increments the retry counter for the most recent attempt of a URL.
func (db *DB) UpdateRetryCount(url string) error {
	_, err := db.conn.Exec(`
		UPDATE scrape_history
		SET retry_count = retry_count + 1,
			last_checked_at = CURRENT_TIMESTAMP
		WHERE url = ?
		ORDER BY scraped_at DESC
		LIMIT 1
	`, url)
	return err
}

// GetLatestSnapshot retrieves the most recent snapshot for a given URL
func (db *DB) GetLatestSnapshot(urlStr string) (*Snapshot, error) {
	query := `
		SELECT id, url, domain, title, clean_text, raw_content, summary,
		       content_hash, previous_hash, has_changes, change_summary,
		       status_code, scraped_at, last_checked_at, scrape_status, error_message, retry_count
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
	var scrapeStatus sql.NullString
	var errorMessage sql.NullString
	var retryCount sql.NullInt64
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
		&scrapeStatus,
		&errorMessage,
		&retryCount,
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
	if scrapeStatus.Valid {
		snapshot.ScrapeStatus = scrapeStatus.String
	}
	if errorMessage.Valid {
		snapshot.ErrorMessage = errorMessage.String
	}
	if retryCount.Valid {
		snapshot.RetryCount = int(retryCount.Int64)
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
		       status_code, scraped_at, last_checked_at, scrape_status, error_message, retry_count
		FROM scrape_history
		WHERE id = ?
	`

	var snapshot Snapshot
	var summary sql.NullString
	var previousHash sql.NullString
	var changeSummary sql.NullString
	var hasChangesInt int
	var numericID int64
	var scrapeStatus sql.NullString
	var errorMessage sql.NullString
	var retryCount sql.NullInt64
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
		&scrapeStatus,
		&errorMessage,
		&retryCount,
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
	if scrapeStatus.Valid {
		snapshot.ScrapeStatus = scrapeStatus.String
	}
	if errorMessage.Valid {
		snapshot.ErrorMessage = errorMessage.String
	}
	if retryCount.Valid {
		snapshot.RetryCount = int(retryCount.Int64)
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
		       status_code, scraped_at, last_checked_at, scrape_status, error_message, retry_count
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
		var scrapeStatus sql.NullString
		var errorMessage sql.NullString
		var retryCount sql.NullInt64
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
			&scrapeStatus,
			&errorMessage,
			&retryCount,
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
		if scrapeStatus.Valid {
			snapshot.ScrapeStatus = scrapeStatus.String
		}
		if errorMessage.Valid {
			snapshot.ErrorMessage = errorMessage.String
		}
		if retryCount.Valid {
			snapshot.RetryCount = int(retryCount.Int64)
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
		       status_code, scraped_at, last_checked_at, scrape_status, error_message, retry_count
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
	var scrapeStatus sql.NullString
	var errorMessage sql.NullString
	var retryCount sql.NullInt64
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
		&scrapeStatus,
		&errorMessage,
		&retryCount,
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
	if scrapeStatus.Valid {
		snapshot.ScrapeStatus = scrapeStatus.String
	}
	if errorMessage.Valid {
		snapshot.ErrorMessage = errorMessage.String
	}
	if retryCount.Valid {
		snapshot.RetryCount = int(retryCount.Int64)
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
		       status_code, scraped_at, last_checked_at, scrape_status, error_message, retry_count
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
		var scrapeStatus sql.NullString
		var errorMessage sql.NullString
		var retryCount sql.NullInt64
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
			&scrapeStatus,
			&errorMessage,
			&retryCount,
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
		if scrapeStatus.Valid {
			snapshot.ScrapeStatus = scrapeStatus.String
		}
		if errorMessage.Valid {
			snapshot.ErrorMessage = errorMessage.String
		}
		if retryCount.Valid {
			snapshot.RetryCount = int(retryCount.Int64)
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
		       status_code, scraped_at, last_checked_at, scrape_status, error_message, retry_count
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
		var scrapeStatus sql.NullString
		var errorMessage sql.NullString
		var retryCount sql.NullInt64
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
			&scrapeStatus,
			&errorMessage,
			&retryCount,
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
		if scrapeStatus.Valid {
			snapshot.ScrapeStatus = scrapeStatus.String
		}
		if errorMessage.Valid {
			snapshot.ErrorMessage = errorMessage.String
		}
		if retryCount.Valid {
			snapshot.RetryCount = int(retryCount.Int64)
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

// SearchContent performs full-text search across stored snapshots.
func (db *DB) SearchContent(query string, domainFilter string, limit int, offset int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	sqlQuery := `
		SELECT
			s.id,
			s.url,
			s.domain,
			s.title,
			snippet(scrape_search, 3, '<mark>', '</mark>', '...', 32) as snippet,
			s.scraped_at,
			bm25(scrape_search) as rank
		FROM scrape_search
		JOIN scrape_history s ON s.id = scrape_search.rowid
		WHERE scrape_search MATCH ?
		AND s.scrape_status = 'success'
	`

	args := []interface{}{query}

	if domainFilter != "" {
		sqlQuery += " AND s.domain = ?"
		args = append(args, domainFilter)
	}

	sqlQuery += " ORDER BY rank, s.scraped_at DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := db.conn.Query(sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("search query failed: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var (
			r       SearchResult
			title   sql.NullString
			snippet sql.NullString
		)

		if err := rows.Scan(&r.ID, &r.URL, &r.Domain, &title, &snippet, &r.ScrapedAt, &r.Rank); err != nil {
			return nil, err
		}

		if title.Valid {
			r.Title = title.String
		}
		if snippet.Valid {
			r.Snippet = snippet.String
		}

		results = append(results, r)
	}

	return results, nil
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

// ExecRaw exposes a thin wrapper for executing arbitrary SQL (used by admin/debug tooling).
func (db *DB) ExecRaw(query string, args ...interface{}) (sql.Result, error) {
	return db.conn.Exec(query, args...)
}

// QueryRaw exposes a thin wrapper for querying arbitrary SQL (used by admin/debug tooling).
func (db *DB) QueryRaw(query string, args ...interface{}) (*sql.Rows, error) {
	return db.conn.Query(query, args...)
}
