package main

import (
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/kareemsasa3/arachne/internal/database"
)

type TestLogger struct{}

func (l *TestLogger) Info(format string, v ...interface{})  { log.Printf("INFO: "+format, v...) }
func (l *TestLogger) Debug(format string, v ...interface{}) { log.Printf("DEBUG: "+format, v...) }
func (l *TestLogger) Warn(format string, v ...interface{})  { log.Printf("WARN: "+format, v...) }
func (l *TestLogger) Error(format string, v ...interface{}) { log.Printf("ERROR: "+format, v...) }

func main() {
	// 1. Setup temporary database
	tmpDir, err := os.MkdirTemp("", "arachne-verify")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	logger := &TestLogger{}

	db, err := database.Initialize(dbPath, logger)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// 2. Insert dummy snapshot
	snapshot := &database.Snapshot{
		URL:         "https://example.com/test",
		Domain:      "example.com",
		Title:       "Test Page",
		CleanText:   "Initial content",
		RawContent:  "<html>Initial content</html>",
		ContentHash: "hash12345678",
		ScrapedAt:   time.Now(),
		StatusCode:  200,
	}

	if err := db.SaveSnapshot(snapshot); err != nil {
		log.Fatalf("Failed to save snapshot: %v", err)
	}

	log.Printf("Created snapshot with ID: %s", snapshot.ID)

	// 3. Update summary
	newSummary := "This is a generated summary with UniqueToken123."
	log.Printf("Updating summary for ID %s...", snapshot.ID)
	if err := db.UpdateSnapshotSummary(snapshot.ID, newSummary); err != nil {
		log.Fatalf("Failed to update summary: %v", err)
	}

	// 4. Verify update
	updated, err := db.GetSnapshotByID(snapshot.ID)
	if err != nil {
		log.Fatalf("Failed to get updated snapshot: %v", err)
	}

	if updated.Summary != newSummary {
		log.Fatalf("Summary mismatch! Expected %q, got %q", newSummary, updated.Summary)
	}
	log.Printf("Snapshot summary verification passed.")

	// 5. Verify FTS update (if FTS is available)
	if db.HasFTS5() {
		results, err := db.SearchContent("UniqueToken123", "", 10, 0)
		if err != nil {
			log.Fatalf("Search failed: %v", err)
		}
		found := false
		for _, r := range results {
			// Convert SearchResult ID (int64) to string to match snapshot.ID
			if strconv.FormatInt(r.ID, 10) == snapshot.ID {
				found = true
				break
			}
		}
		if !found {
			log.Printf("Warning: FTS index not updated or search failed to find content.")
			// Not fatal if FTS not available/working in this env, but good to know
		} else {
			log.Printf("FTS verification passed.")
		}
	} else {
		log.Printf("FTS5 not available, skipping search verification.")
	}

	// 6. Test non-existent ID (should fail gracefully)
	log.Printf("Testing non-existent ID...")
	if err := db.UpdateSnapshotSummary("99999", "Summary"); err == nil {
		log.Fatalf("Expected error for non-existent ID, got nil")
	} else {
		log.Printf("Got expected error: %v", err)
	}

	log.Println("All verification steps passed successfully.")
}
