package database

import (
	"os"
	"testing"
	"time"
)

// testLogger implements the Logger interface for testing
type testLogger struct{}

func (l *testLogger) Info(format string, v ...interface{})  {}
func (l *testLogger) Debug(format string, v ...interface{}) {}
func (l *testLogger) Warn(format string, v ...interface{})  {}
func (l *testLogger) Error(format string, v ...interface{}) {}

func setupTestDB(t *testing.T) (*DB, func()) {
	// Create temporary database file
	tmpFile, err := os.CreateTemp("", "test_snapshots_*.db")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	tmpFile.Close()

	dbPath := tmpFile.Name()

	// Initialize database
	log := &testLogger{} // Quiet logs during tests
	db, err := Initialize(dbPath, log)
	if err != nil {
		os.Remove(dbPath)
		t.Fatalf("Failed to initialize database: %v", err)
	}

	// Return cleanup function
	cleanup := func() {
		db.Close()
		os.Remove(dbPath)
	}

	return db, cleanup
}

func TestInitialize(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	if db == nil {
		t.Fatal("Expected database to be initialized")
	}

	// Verify tables exist
	var tableName string
	err := db.conn.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='snapshots'").Scan(&tableName)
	if err != nil {
		t.Fatalf("Failed to find snapshots table: %v", err)
	}

	if tableName != "snapshots" {
		t.Errorf("Expected table name 'snapshots', got '%s'", tableName)
	}
}

func TestSaveSnapshot(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	snapshot := &Snapshot{
		URL:        "https://example.com/test",
		Title:      "Test Page",
		CleanText:  "This is test content",
		StatusCode: 200,
	}

	err := db.SaveSnapshot(snapshot)
	if err != nil {
		t.Fatalf("Failed to save snapshot: %v", err)
	}

	// Verify snapshot was saved
	if snapshot.ID == "" {
		t.Error("Expected ID to be generated")
	}

	if snapshot.Domain != "example.com" {
		t.Errorf("Expected domain 'example.com', got '%s'", snapshot.Domain)
	}

	if snapshot.ContentHash == "" {
		t.Error("Expected content hash to be computed")
	}
}

func TestGetLatestSnapshot(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Save a snapshot
	snapshot := &Snapshot{
		URL:        "https://example.com/page",
		Title:      "Test Page",
		CleanText:  "Original content",
		StatusCode: 200,
	}

	err := db.SaveSnapshot(snapshot)
	if err != nil {
		t.Fatalf("Failed to save snapshot: %v", err)
	}

	// Retrieve it
	retrieved, err := db.GetLatestSnapshot(snapshot.URL)
	if err != nil {
		t.Fatalf("Failed to get snapshot: %v", err)
	}

	if retrieved == nil {
		t.Fatal("Expected to retrieve snapshot")
	}

	if retrieved.URL != snapshot.URL {
		t.Errorf("Expected URL '%s', got '%s'", snapshot.URL, retrieved.URL)
	}

	if retrieved.Title != snapshot.Title {
		t.Errorf("Expected title '%s', got '%s'", snapshot.Title, retrieved.Title)
	}

	if retrieved.ContentHash != snapshot.ContentHash {
		t.Errorf("Expected content hash '%s', got '%s'", snapshot.ContentHash, retrieved.ContentHash)
	}
}

func TestDeduplication(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Save initial snapshot
	snapshot := &Snapshot{
		URL:        "https://example.com/dedup",
		Title:      "Test Page",
		CleanText:  "Same content",
		StatusCode: 200,
	}

	err := db.SaveSnapshot(snapshot)
	if err != nil {
		t.Fatalf("Failed to save first snapshot: %v", err)
	}

	firstID := snapshot.ID
	firstScrapedAt := snapshot.ScrapedAt

	// Wait a bit to ensure timestamps differ
	time.Sleep(10 * time.Millisecond)

	// Save again with same content
	snapshot2 := &Snapshot{
		URL:        "https://example.com/dedup",
		Title:      "Test Page",
		CleanText:  "Same content", // Same content
		StatusCode: 200,
	}

	err = db.SaveSnapshot(snapshot2)
	if err != nil {
		t.Fatalf("Failed to save second snapshot: %v", err)
	}

	// Retrieve and verify only last_checked_at was updated
	retrieved, err := db.GetLatestSnapshot(snapshot.URL)
	if err != nil {
		t.Fatalf("Failed to get snapshot: %v", err)
	}

	if retrieved.ID != firstID {
		t.Error("Expected same ID (no new snapshot created)")
	}

	if !retrieved.ScrapedAt.Equal(firstScrapedAt) {
		t.Error("Expected scraped_at to remain unchanged")
	}

	if retrieved.LastCheckedAt.Before(firstScrapedAt) {
		t.Error("Expected last_checked_at to be updated")
	}

	// Now save with different content
	snapshot3 := &Snapshot{
		URL:        "https://example.com/dedup",
		Title:      "Test Page",
		CleanText:  "Different content", // Changed content
		StatusCode: 200,
	}

	err = db.SaveSnapshot(snapshot3)
	if err != nil {
		t.Fatalf("Failed to save third snapshot: %v", err)
	}

	// Retrieve and verify new snapshot was created
	retrieved2, err := db.GetLatestSnapshot(snapshot.URL)
	if err != nil {
		t.Fatalf("Failed to get snapshot: %v", err)
	}

	if retrieved2.ID == firstID {
		t.Error("Expected new ID (new snapshot should be created)")
	}

	if retrieved2.ContentHash == snapshot.ContentHash {
		t.Error("Expected different content hash")
	}
}

func TestExtractDomain(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"https://example.com/path", "example.com"},
		{"https://www.example.com/path", "example.com"},
		{"https://subdomain.example.com/path", "subdomain.example.com"},
		{"https://news.ycombinator.com/item?id=123", "news.ycombinator.com"},
	}

	for _, tt := range tests {
		domain, err := extractDomain(tt.url)
		if err != nil {
			t.Errorf("Failed to extract domain from '%s': %v", tt.url, err)
			continue
		}

		if domain != tt.expected {
			t.Errorf("For URL '%s', expected domain '%s', got '%s'", tt.url, tt.expected, domain)
		}
	}
}

func TestComputeContentHash(t *testing.T) {
	content1 := "Hello, World!"
	content2 := "Hello, World!"
	content3 := "Different content"

	hash1 := computeContentHash(content1)
	hash2 := computeContentHash(content2)
	hash3 := computeContentHash(content3)

	if hash1 != hash2 {
		t.Error("Expected same content to produce same hash")
	}

	if hash1 == hash3 {
		t.Error("Expected different content to produce different hash")
	}

	// Verify it's a valid hex string
	if len(hash1) != 64 { // SHA256 produces 64 hex characters
		t.Errorf("Expected hash length 64, got %d", len(hash1))
	}
}

func TestGetSnapshotsByDomain(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Save multiple snapshots for same domain
	for i := 0; i < 3; i++ {
		snapshot := &Snapshot{
			URL:        "https://example.com/page" + string(rune('1'+i)),
			Title:      "Test Page",
			CleanText:  "Content " + string(rune('1'+i)),
			StatusCode: 200,
		}
		err := db.SaveSnapshot(snapshot)
		if err != nil {
			t.Fatalf("Failed to save snapshot %d: %v", i, err)
		}
	}

	// Save snapshot for different domain
	snapshot := &Snapshot{
		URL:        "https://other.com/page",
		Title:      "Other Page",
		CleanText:  "Other content",
		StatusCode: 200,
	}
	err := db.SaveSnapshot(snapshot)
	if err != nil {
		t.Fatalf("Failed to save other domain snapshot: %v", err)
	}

	// Retrieve snapshots for example.com
	snapshots, err := db.GetSnapshotsByDomain("example.com", 10)
	if err != nil {
		t.Fatalf("Failed to get snapshots by domain: %v", err)
	}

	if len(snapshots) != 3 {
		t.Errorf("Expected 3 snapshots for example.com, got %d", len(snapshots))
	}

	// Verify all are from correct domain
	for _, s := range snapshots {
		if s.Domain != "example.com" {
			t.Errorf("Expected domain 'example.com', got '%s'", s.Domain)
		}
	}
}

func TestGetStats(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Save some snapshots
	for i := 0; i < 5; i++ {
		snapshot := &Snapshot{
			URL:        "https://example.com/page" + string(rune('1'+i)),
			Title:      "Test Page",
			CleanText:  "Content " + string(rune('1'+i)),
			StatusCode: 200,
		}
		err := db.SaveSnapshot(snapshot)
		if err != nil {
			t.Fatalf("Failed to save snapshot %d: %v", i, err)
		}
	}

	stats, err := db.GetStats()
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}

	totalSnapshots, ok := stats["total_snapshots"].(int)
	if !ok || totalSnapshots != 5 {
		t.Errorf("Expected 5 total snapshots, got %v", stats["total_snapshots"])
	}

	uniqueURLs, ok := stats["unique_urls"].(int)
	if !ok || uniqueURLs != 5 {
		t.Errorf("Expected 5 unique URLs, got %v", stats["unique_urls"])
	}

	uniqueDomains, ok := stats["unique_domains"].(int)
	if !ok || uniqueDomains != 1 {
		t.Errorf("Expected 1 unique domain, got %v", stats["unique_domains"])
	}
}

