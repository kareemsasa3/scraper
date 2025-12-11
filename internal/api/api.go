package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/kareemsasa3/arachne/internal/config"
	"github.com/kareemsasa3/arachne/internal/database"
	"github.com/kareemsasa3/arachne/internal/storage"
	"github.com/kareemsasa3/arachne/internal/types"
)

// ScraperInterface defines the interface for scrapers
type ScraperInterface interface {
	ScrapeURLs(urls []string) []types.ScrapedData
	ScrapeSite(siteURL string) []types.ScrapedData
	ScrapeSiteWithConfig(siteURL string, paginationConfig *types.PaginationConfig) []types.ScrapedData
	ScrapeURLsStreaming(urls []string, callback func(*types.ScrapedData, *types.ProgressStats)) []types.ScrapedData
	ScrapeSiteWithConfigStreaming(siteURL string, paginationConfig *types.PaginationConfig, callback func(*types.ScrapedData, *types.ProgressStats)) []types.ScrapedData
	GetMetrics() interface{}
}

// Storage interface for job persistence
type Storage interface {
	SaveJob(ctx context.Context, job *storage.ScrapingJob) error
	GetJob(ctx context.Context, jobID string) (*storage.ScrapingJob, error)
	UpdateJob(ctx context.Context, job *storage.ScrapingJob) error
	ListJobs(ctx context.Context) ([]string, error)
	GetJobsByStatus(ctx context.Context, status string) ([]*storage.ScrapingJob, error)
	DeleteJob(ctx context.Context, jobID string) error
	Close() error
}

// APIHandler handles HTTP API requests
type APIHandler struct {
	scraper  ScraperInterface
	config   *config.Config
	storage  Storage
	database *database.DB
}

// NewAPIHandler creates a new API handler
func NewAPIHandler(scraper ScraperInterface, cfg *config.Config, storage Storage, db *database.DB) *APIHandler {
	return &APIHandler{
		scraper:  scraper,
		config:   cfg,
		storage:  storage,
		database: db,
	}
}

// ScrapeRequest represents a scraping request
type ScrapeRequest struct {
	URLs             []string                `json:"urls"`
	SiteURL          string                  `json:"site_url,omitempty"`
	PaginationConfig *types.PaginationConfig `json:"pagination_config,omitempty"`
}

// ScrapeResponse represents a scraping response
type ScrapeResponse struct {
	JobID   string              `json:"job_id"`
	Status  string              `json:"status"`
	Results []types.ScrapedData `json:"results,omitempty"`
	Error   string              `json:"error,omitempty"`
}

// JobStatusResponse represents a job status response
type JobStatusResponse struct {
	Job     *storage.ScrapingJob `json:"job"`
	Metrics interface{}          `json:"metrics,omitempty"`
}

// HandleScrape handles scraping requests asynchronously
func (h *APIHandler) HandleScrape(w http.ResponseWriter, r *http.Request) {
	// Simple bearer token auth if configured
	if h.config.APIToken != "" {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(auth) <= len(prefix) || auth[:len(prefix)] != prefix || auth[len(prefix):] != h.config.APIToken {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ScrapeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate request
	if req.SiteURL == "" && len(req.URLs) == 0 {
		http.Error(w, "No URLs provided", http.StatusBadRequest)
		return
	}

	// Create job
	jobID := uuid.New().String()
	job := &storage.ScrapingJob{
		ID:        jobID,
		Status:    "pending",
		Request:   storage.ScrapeRequest{URLs: req.URLs, SiteURL: req.SiteURL, PaginationConfig: req.PaginationConfig},
		CreatedAt: time.Now(),
		Progress:  0,
	}

	// Store job in persistent storage
	ctx := r.Context()
	if err := h.storage.SaveJob(ctx, job); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save job: %v", err), http.StatusInternalServerError)
		return
	}

	// Start scraping in background
	go h.executeScrapingJob(job)

	// Return job ID immediately
	response := ScrapeResponse{
		JobID:   jobID,
		Status:  "accepted",
		Results: []types.ScrapedData{},
		Error:   "",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}

// HandleJobStatus handles job status requests
func (h *APIHandler) HandleJobStatus(w http.ResponseWriter, r *http.Request) {
	// Simple bearer token auth if configured
	if h.config.APIToken != "" {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(auth) <= len(prefix) || auth[:len(prefix)] != prefix || auth[len(prefix):] != h.config.APIToken {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract job ID from URL path
	jobID := r.URL.Query().Get("id")
	if jobID == "" {
		http.Error(w, "Job ID required", http.StatusBadRequest)
		return
	}

	// Get job from persistent storage
	ctx := r.Context()
	job, err := h.storage.GetJob(ctx, jobID)
	if err != nil {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}

	response := JobStatusResponse{
		Job: job,
	}

	if h.config.EnableMetrics {
		response.Metrics = h.scraper.GetMetrics()
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}

// executeScrapingJob executes a scraping job in the background
func (h *APIHandler) executeScrapingJob(job *storage.ScrapingJob) {
	ctx := context.Background()

	// Update job status to running
	job.Status = "running"
	now := time.Now()
	job.StartedAt = &now
	job.Results = []types.ScrapedData{} // Initialize empty results array
	if err := h.storage.UpdateJob(ctx, job); err != nil {
		// Log error but continue execution
		fmt.Printf("Failed to update job status to running: %v\n", err)
	}

	// Track totals for progress; adjust dynamically for site crawls.
	totalKnown := len(job.Request.URLs)
	if job.Request.SiteURL != "" {
		totalKnown = 1 // seed; will grow as we discover more pages
	}

	var mu sync.Mutex
	completedCount := 0

	callback := func(data *types.ScrapedData, stats *types.ProgressStats) {
		mu.Lock()
		defer mu.Unlock()

		// Update discovered/total if provided
		if stats != nil && stats.Discovered > totalKnown {
			totalKnown = stats.Discovered
		}

		// Increment completed when we receive a result
		if data != nil {
			completedCount++
		}

		// Allow scraper to override completed count if it provides it
		if stats != nil && stats.Completed > completedCount {
			completedCount = stats.Completed
		}

		// Append new result when provided
		if data != nil {
			job.Results = append(job.Results, *data)

			// Save snapshot to database if scrape was successful
			if h.database != nil && data.Error == "" && data.Status >= 200 && data.Status < 400 {
				snapshot := &database.Snapshot{
					URL:        data.URL,
					Title:      data.Title,
					CleanText:  data.Content, // Using Content field as clean text
					StatusCode: data.Status,
				}
				if err := h.database.SaveSnapshot(snapshot); err != nil {
					fmt.Printf("Warning: Failed to save snapshot for %s: %v\n", data.URL, err)
				}
			} else if h.database != nil && data != nil && (data.Error != "" || data.Status >= 400 || data.Status == 0) {
				// Capture failures to avoid losing recent error states
				scrapeStatus := classifyFailureStatus(data.Status, data.Error)
				domain := ""
				if parsed, err := url.Parse(data.URL); err == nil {
					domain = parsed.Host
				}
				if err := h.database.SaveFailedScrape(data.URL, domain, data.Status, data.Error, scrapeStatus); err != nil {
					fmt.Printf("Warning: Failed to record failed scrape for %s: %v\n", data.URL, err)
				}
			}
		}

		// Compute progress using current totals; reserve 100 for finalization
		total := totalKnown
		if total == 0 {
			total = 1 // safety to avoid div by zero
		}
		job.Progress = (completedCount * 100) / total
		if job.Progress > 99 && job.Status != "completed" {
			job.Progress = 99
		}

		// Persist incremental update
		if err := h.storage.UpdateJob(ctx, job); err != nil {
			fmt.Printf("Failed to update job with incremental result: %v\n", err)
		} else {
			fmt.Printf("Job %s: Progress %d/%d (%d%%)\n",
				job.ID, completedCount, total, job.Progress)
		}
	}

	var results []types.ScrapedData

	// Execute scraping based on request type with streaming
	if job.Request.SiteURL != "" {
		// Use custom pagination config if provided, otherwise use defaults
		results = h.scraper.ScrapeSiteWithConfigStreaming(
			job.Request.SiteURL,
			job.Request.PaginationConfig,
			callback,
		)
	} else {
		results = h.scraper.ScrapeURLsStreaming(job.Request.URLs, callback)
	}

	// Final update: mark as completed
	mu.Lock()
	defer mu.Unlock()

	job.Status = "completed"
	job.Results = results
	completedAt := time.Now()
	job.CompletedAt = &completedAt
	job.Progress = 100

	if err := h.storage.UpdateJob(ctx, job); err != nil {
		fmt.Printf("Failed to update job with final status: %v\n", err)
	} else {
		fmt.Printf("Job %s completed with %d results\n", job.ID, len(results))
	}
}

// HandleHealth handles health check requests
func (h *APIHandler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	response := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().UTC(),
		"version":   "2.0.0",
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}

// HandleMetrics handles metrics requests
func (h *APIHandler) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	if !h.config.EnableMetrics {
		http.Error(w, "Metrics disabled", http.StatusServiceUnavailable)
		return
	}

	metrics := h.scraper.GetMetrics()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(metrics); err != nil {
		http.Error(w, "Failed to encode metrics", http.StatusInternalServerError)
		return
	}
}

// MemoryLookupResponse represents the response for memory lookup
type MemoryLookupResponse struct {
	Found    bool              `json:"found"`
	Snapshot *SnapshotResponse `json:"snapshot,omitempty"`
}

// SnapshotResponse represents a snapshot in the API response
type SnapshotResponse struct {
	ID            string    `json:"id"`
	URL           string    `json:"url"`
	Domain        string    `json:"domain"`
	Title         string    `json:"title"`
	ContentHash   string    `json:"content_hash"`
	Summary       string    `json:"summary,omitempty"`
	ScrapedAt     time.Time `json:"scraped_at"`
	LastCheckedAt time.Time `json:"last_checked_at"`
	AgeHours      float64   `json:"age_hours"`
	StatusCode    int       `json:"status_code"`
	ScrapeStatus  string    `json:"scrape_status"`
	ErrorMessage  string    `json:"error_message,omitempty"`
	RetryCount    int       `json:"retry_count,omitempty"`
}

// HistoryEntryResponse represents a single history entry for a URL.
type HistoryEntryResponse struct {
	ID            string    `json:"id"`
	URL           string    `json:"url"`
	Domain        string    `json:"domain"`
	Title         string    `json:"title"`
	ContentHash   string    `json:"content_hash"`
	PreviousHash  string    `json:"previous_hash,omitempty"`
	HasChanges    bool      `json:"has_changes"`
	ChangeSummary string    `json:"change_summary,omitempty"`
	Summary       string    `json:"summary,omitempty"`
	ScrapedAt     time.Time `json:"scraped_at"`
	LastCheckedAt time.Time `json:"last_checked_at"`
	StatusCode    int       `json:"status_code"`
	ScrapeStatus  string    `json:"scrape_status"`
	ErrorMessage  string    `json:"error_message,omitempty"`
	RetryCount    int       `json:"retry_count,omitempty"`
}

// VersionResponse represents a full snapshot version.
type VersionResponse struct {
	ID            string    `json:"id"`
	URL           string    `json:"url"`
	Domain        string    `json:"domain"`
	Title         string    `json:"title"`
	ContentHash   string    `json:"content_hash"`
	PreviousHash  string    `json:"previous_hash,omitempty"`
	HasChanges    bool      `json:"has_changes"`
	Summary       string    `json:"summary,omitempty"`
	ChangeSummary string    `json:"change_summary,omitempty"`
	CleanText     string    `json:"clean_text,omitempty"`
	RawContent    string    `json:"raw_content,omitempty"`
	ScrapedAt     time.Time `json:"scraped_at"`
	LastCheckedAt time.Time `json:"last_checked_at"`
	StatusCode    int       `json:"status_code"`
	ScrapeStatus  string    `json:"scrape_status"`
	ErrorMessage  string    `json:"error_message,omitempty"`
	RetryCount    int       `json:"retry_count,omitempty"`
}

// DiffResponse represents a diff between two versions.
type DiffResponse struct {
	URL           string    `json:"url"`
	FromID        string    `json:"from_id"`
	ToID          string    `json:"to_id"`
	FromTimestamp time.Time `json:"from_timestamp"`
	ToTimestamp   time.Time `json:"to_timestamp"`
	FromHash      string    `json:"from_hash"`
	ToHash        string    `json:"to_hash"`
	Diff          string    `json:"diff"`
	LinesAdded    int       `json:"lines_added"`
	LinesRemoved  int       `json:"lines_removed"`
}

// HandleMemoryLookup handles memory lookup requests
func (h *APIHandler) HandleMemoryLookup(w http.ResponseWriter, r *http.Request) {
	// Simple bearer token auth if configured
	if h.config.APIToken != "" {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(auth) <= len(prefix) || auth[:len(prefix)] != prefix || auth[len(prefix):] != h.config.APIToken {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check if database is available
	if h.database == nil {
		http.Error(w, "Memory database not available", http.StatusServiceUnavailable)
		return
	}

	// Get URL parameter
	urlParam := r.URL.Query().Get("url")
	if urlParam == "" {
		http.Error(w, "URL parameter required", http.StatusBadRequest)
		return
	}

	// Decode URL parameter
	decodedURL, err := url.QueryUnescape(urlParam)
	if err != nil {
		http.Error(w, "Invalid URL parameter", http.StatusBadRequest)
		return
	}

	// Look up snapshot in database
	snapshot, err := h.database.GetLatestSnapshot(decodedURL)
	if err != nil {
		http.Error(w, fmt.Sprintf("Database error: %v", err), http.StatusInternalServerError)
		return
	}

	// Prepare response
	response := MemoryLookupResponse{
		Found: snapshot != nil,
	}

	if snapshot != nil {
		ageHours := time.Since(snapshot.LastCheckedAt).Hours()
		response.Snapshot = &SnapshotResponse{
			ID:            snapshot.ID,
			URL:           snapshot.URL,
			Domain:        snapshot.Domain,
			Title:         snapshot.Title,
			ContentHash:   snapshot.ContentHash,
			Summary:       snapshot.Summary,
			ScrapedAt:     snapshot.ScrapedAt,
			LastCheckedAt: snapshot.LastCheckedAt,
			AgeHours:      ageHours,
			StatusCode:    snapshot.StatusCode,
			ScrapeStatus:  snapshot.ScrapeStatus,
			ErrorMessage:  snapshot.ErrorMessage,
			RetryCount:    snapshot.RetryCount,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}

// MemoryRecentResponse represents the response for memory recent snapshots
type MemoryRecentResponse struct {
	Snapshots []SnapshotResponse `json:"snapshots"`
	Total     int                `json:"total"`
	Limit     int                `json:"limit"`
	Offset    int                `json:"offset"`
}

// MemorySearchResponse represents the response for full-text search.
type MemorySearchResponse struct {
	Query   string                  `json:"query"`
	Domain  string                  `json:"domain,omitempty"`
	Limit   int                     `json:"limit"`
	Offset  int                     `json:"offset"`
	Count   int                     `json:"count"`
	Results []database.SearchResult `json:"results"`
}

// HandleMemoryRecent handles requests for recent snapshots with pagination
func (h *APIHandler) HandleMemoryRecent(w http.ResponseWriter, r *http.Request) {
	// Simple bearer token auth if configured
	if h.config.APIToken != "" {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(auth) <= len(prefix) || auth[:len(prefix)] != prefix || auth[len(prefix):] != h.config.APIToken {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check if database is available
	if h.database == nil {
		http.Error(w, "Memory database not available", http.StatusServiceUnavailable)
		return
	}

	// Parse query parameters
	query := r.URL.Query()
	limit := 50 // default
	offset := 0

	if limitStr := query.Get("limit"); limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil {
			limit = parsedLimit
		}
	}

	if offsetStr := query.Get("offset"); offsetStr != "" {
		if parsedOffset, err := strconv.Atoi(offsetStr); err == nil {
			offset = parsedOffset
		}
	}

	// Get recent snapshots from database
	snapshots, total, err := h.database.GetRecentSnapshots(limit, offset)
	if err != nil {
		http.Error(w, fmt.Sprintf("Database error: %v", err), http.StatusInternalServerError)
		return
	}

	// Convert to response format
	snapshotResponses := make([]SnapshotResponse, 0, len(snapshots))
	for _, snapshot := range snapshots {
		ageHours := time.Since(snapshot.LastCheckedAt).Hours()
		snapshotResponses = append(snapshotResponses, SnapshotResponse{
			ID:            snapshot.ID,
			URL:           snapshot.URL,
			Domain:        snapshot.Domain,
			Title:         snapshot.Title,
			ContentHash:   snapshot.ContentHash,
			Summary:       snapshot.Summary,
			ScrapedAt:     snapshot.ScrapedAt,
			LastCheckedAt: snapshot.LastCheckedAt,
			AgeHours:      ageHours,
			StatusCode:    snapshot.StatusCode,
			ScrapeStatus:  snapshot.ScrapeStatus,
			ErrorMessage:  snapshot.ErrorMessage,
			RetryCount:    snapshot.RetryCount,
		})
	}

	// Prepare response
	response := MemoryRecentResponse{
		Snapshots: snapshotResponses,
		Total:     total,
		Limit:     limit,
		Offset:    offset,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}

// HandleFailedScrapes returns recent failed scrape attempts.
func (h *APIHandler) HandleFailedScrapes(w http.ResponseWriter, r *http.Request) {
	if h.config.APIToken != "" {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(auth) <= len(prefix) || auth[:len(prefix)] != prefix || auth[len(prefix):] != h.config.APIToken {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.database == nil {
		http.Error(w, "Memory database not available", http.StatusServiceUnavailable)
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}

	rows, err := h.database.QueryRaw(`
		SELECT url, domain, status_code, scrape_status, error_message, retry_count, scraped_at
		FROM scrape_history
		WHERE scrape_status != 'success'
		ORDER BY scraped_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		http.Error(w, fmt.Sprintf("Database error: %v", err), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var failures []map[string]interface{}
	for rows.Next() {
		var (
			urlStr     string
			domain     string
			statusCode int
			status     string
			errMsg     sql.NullString
			retryCount sql.NullInt64
			scrapedAt  time.Time
		)
		if err := rows.Scan(&urlStr, &domain, &statusCode, &status, &errMsg, &retryCount, &scrapedAt); err != nil {
			continue
		}
		failures = append(failures, map[string]interface{}{
			"url":           urlStr,
			"domain":        domain,
			"status_code":   statusCode,
			"scrape_status": status,
			"error":         errMsg.String,
			"retry_count":   int(retryCount.Int64),
			"failed_at":     scrapedAt,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"failures": failures,
		"count":    len(failures),
	})
}

// HandleMemorySearch performs full-text search across stored snapshots.
func (h *APIHandler) HandleMemorySearch(w http.ResponseWriter, r *http.Request) {
	if h.config.APIToken != "" {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(auth) <= len(prefix) || auth[:len(prefix)] != prefix || auth[len(prefix):] != h.config.APIToken {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.database == nil {
		http.Error(w, "Memory database not available", http.StatusServiceUnavailable)
		return
	}

	if !h.database.HasFTS5() {
		http.Error(w, database.ErrFTS5NotAvailable.Error(), http.StatusNotImplemented)
		return
	}

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		http.Error(w, "q query parameter is required", http.StatusBadRequest)
		return
	}

	domain := strings.TrimSpace(r.URL.Query().Get("domain"))

	limit := 20
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil {
			limit = parsed
		}
	}

	offset := 0
	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if parsed, err := strconv.Atoi(offsetStr); err == nil {
			offset = parsed
		}
	}

	results, err := h.database.SearchContent(q, domain, limit, offset)
	if err != nil {
		http.Error(w, fmt.Sprintf("Database error: %v", err), http.StatusInternalServerError)
		return
	}

	resp := MemorySearchResponse{
		Query:   q,
		Domain:  domain,
		Limit:   limit,
		Offset:  offset,
		Count:   len(results),
		Results: results,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}

// HandleScrapeHistory returns all versions for a given URL.
func (h *APIHandler) HandleScrapeHistory(w http.ResponseWriter, r *http.Request) {
	if h.config.APIToken != "" {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(auth) <= len(prefix) || auth[:len(prefix)] != prefix || auth[len(prefix):] != h.config.APIToken {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.database == nil {
		http.Error(w, "Memory database not available", http.StatusServiceUnavailable)
		return
	}

	rawURL := r.URL.Query().Get("url")
	if rawURL == "" {
		http.Error(w, "url query parameter is required", http.StatusBadRequest)
		return
	}

	decodedURL, err := url.QueryUnescape(rawURL)
	if err != nil {
		http.Error(w, "invalid url encoding", http.StatusBadRequest)
		return
	}

	history, err := h.database.GetHistoryByURL(decodedURL)
	if err != nil {
		http.Error(w, fmt.Sprintf("Database error: %v", err), http.StatusInternalServerError)
		return
	}

	entries := make([]HistoryEntryResponse, 0, len(history))
	for _, snap := range history {
		entries = append(entries, mapSnapshotToHistoryResponse(snap))
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(entries); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}

// HandleScrapeVersion returns a specific version by ID.
func (h *APIHandler) HandleScrapeVersion(w http.ResponseWriter, r *http.Request) {
	if h.config.APIToken != "" {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(auth) <= len(prefix) || auth[:len(prefix)] != prefix || auth[len(prefix):] != h.config.APIToken {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	if h.database == nil {
		http.Error(w, "Memory database not available", http.StatusServiceUnavailable)
		return
	}

	path := strings.TrimSuffix(r.URL.Path, "/")

	var id string
	switch {
	case strings.HasPrefix(path, "/memory/version/"):
		id = strings.TrimPrefix(path, "/memory/version/")
	case strings.HasPrefix(path, "/api/scrapes/version/"):
		// Temporary compatibility for old route; prefer /memory/version/:id
		id = strings.TrimPrefix(path, "/api/scrapes/version/")
	default:
		http.Error(w, "Invalid URL path", http.StatusBadRequest)
		return
	}

	if id == "" || strings.ContainsRune(id, '/') {
		http.Error(w, "Missing version ID", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		snap, err := h.database.GetSnapshotByID(id)
		if err != nil {
			http.Error(w, fmt.Sprintf("Database error: %v", err), http.StatusInternalServerError)
			return
		}
		if snap == nil {
			http.Error(w, "Version not found", http.StatusNotFound)
			return
		}

		resp := mapSnapshotToVersionResponse(snap)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			http.Error(w, "Failed to encode response", http.StatusInternalServerError)
			return
		}
	case http.MethodDelete:
		if err := h.database.DeleteSnapshot(id); err != nil {
			if strings.Contains(err.Error(), "not found") {
				http.Error(w, fmt.Sprintf("Snapshot not found: %s", id), http.StatusNotFound)
			} else {
				http.Error(w, fmt.Sprintf("Failed to delete snapshot: %v", err), http.StatusInternalServerError)
			}
			return
		}
		resp := DeleteResponse{
			Success: true,
			Message: "Deleted",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// HandleScrapeDiff returns a unified diff between two versions.
func (h *APIHandler) HandleScrapeDiff(w http.ResponseWriter, r *http.Request) {
	if h.config.APIToken != "" {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(auth) <= len(prefix) || auth[:len(prefix)] != prefix || auth[len(prefix):] != h.config.APIToken {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.database == nil {
		http.Error(w, "Memory database not available", http.StatusServiceUnavailable)
		return
	}

	query := r.URL.Query()
	rawURL := query.Get("url")
	fromTs := query.Get("from")
	toTs := query.Get("to")

	if rawURL == "" || fromTs == "" || toTs == "" {
		http.Error(w, "url, from, and to query parameters are required", http.StatusBadRequest)
		return
	}

	decodedURL, err := url.QueryUnescape(rawURL)
	if err != nil {
		http.Error(w, "invalid url encoding", http.StatusBadRequest)
		return
	}

	fromTime, err := time.Parse(time.RFC3339, fromTs)
	if err != nil {
		http.Error(w, "invalid from timestamp, use RFC3339", http.StatusBadRequest)
		return
	}

	toTime, err := time.Parse(time.RFC3339, toTs)
	if err != nil {
		http.Error(w, "invalid to timestamp, use RFC3339", http.StatusBadRequest)
		return
	}

	fromSnap, err := h.database.GetSnapshotByTimestamp(decodedURL, fromTime)
	if err != nil {
		http.Error(w, fmt.Sprintf("Database error: %v", err), http.StatusInternalServerError)
		return
	}
	if fromSnap == nil {
		http.Error(w, "from version not found", http.StatusNotFound)
		return
	}

	toSnap, err := h.database.GetSnapshotByTimestamp(decodedURL, toTime)
	if err != nil {
		http.Error(w, fmt.Sprintf("Database error: %v", err), http.StatusInternalServerError)
		return
	}
	if toSnap == nil {
		http.Error(w, "to version not found", http.StatusNotFound)
		return
	}

	diffText, added, removed := buildUnifiedDiff(contentForDiff(fromSnap), contentForDiff(toSnap))

	resp := DiffResponse{
		URL:           decodedURL,
		FromID:        fromSnap.ID,
		ToID:          toSnap.ID,
		FromTimestamp: fromSnap.ScrapedAt,
		ToTimestamp:   toSnap.ScrapedAt,
		FromHash:      fromSnap.ContentHash,
		ToHash:        toSnap.ContentHash,
		Diff:          diffText,
		LinesAdded:    added,
		LinesRemoved:  removed,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}

// UpdateSummaryRequest represents the request to update a snapshot's summary
type UpdateSummaryRequest struct {
	Summary string `json:"summary"`
}

// UpdateSummaryResponse represents the response after updating a summary
type UpdateSummaryResponse struct {
	Success    bool      `json:"success"`
	SnapshotID string    `json:"snapshot_id"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// AnalyzeChangesRequest represents the request to analyze changes via AI
type AnalyzeChangesRequest struct {
	URL           string `json:"url"`
	FromTimestamp string `json:"from_timestamp"`
	ToTimestamp   string `json:"to_timestamp"`
}

// DeleteResponse represents a generic delete result
type DeleteResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

// HandleUpdateSnapshotSummary handles requests to update a snapshot's AI-generated summary
func (h *APIHandler) HandleUpdateSnapshotSummary(w http.ResponseWriter, r *http.Request) {
	// Simple bearer token auth if configured
	if h.config.APIToken != "" {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(auth) <= len(prefix) || auth[:len(prefix)] != prefix || auth[len(prefix):] != h.config.APIToken {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	if r.Method != http.MethodPatch {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check if database is available
	if h.database == nil {
		http.Error(w, "Memory database not available", http.StatusServiceUnavailable)
		return
	}

	// Extract snapshot ID from URL path (e.g., /memory/snapshot/uuid/summary)
	path := r.URL.Path
	parts := strings.Split(path, "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid URL path", http.StatusBadRequest)
		return
	}
	snapshotID := parts[3] // Gets the UUID from /memory/snapshot/{id}/summary

	// Parse request body
	var req UpdateSummaryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Summary == "" {
		http.Error(w, "Summary cannot be empty", http.StatusBadRequest)
		return
	}

	// Update the summary in the database
	err := h.database.UpdateSnapshotSummary(snapshotID, req.Summary)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, fmt.Sprintf("Snapshot not found: %s", snapshotID), http.StatusNotFound)
		} else {
			http.Error(w, fmt.Sprintf("Failed to update summary: %v", err), http.StatusInternalServerError)
		}
		return
	}

	// Prepare response
	response := UpdateSummaryResponse{
		Success:    true,
		SnapshotID: snapshotID,
		UpdatedAt:  time.Now(),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}

// HandleAnalyzeChanges proxies AI change analysis and persists the result.
func (h *APIHandler) HandleAnalyzeChanges(w http.ResponseWriter, r *http.Request) {
	if h.config.APIToken != "" {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(auth) <= len(prefix) || auth[:len(prefix)] != prefix || auth[len(prefix):] != h.config.APIToken {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.database == nil {
		http.Error(w, "Memory database not available", http.StatusServiceUnavailable)
		return
	}

	var reqBody AnalyzeChangesRequest
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if reqBody.URL == "" || reqBody.FromTimestamp == "" || reqBody.ToTimestamp == "" {
		http.Error(w, "url, from_timestamp, and to_timestamp are required", http.StatusBadRequest)
		return
	}

	fromTime, err := time.Parse(time.RFC3339, reqBody.FromTimestamp)
	if err != nil {
		http.Error(w, "invalid from_timestamp, must be RFC3339", http.StatusBadRequest)
		return
	}
	toTime, err := time.Parse(time.RFC3339, reqBody.ToTimestamp)
	if err != nil {
		http.Error(w, "invalid to_timestamp, must be RFC3339", http.StatusBadRequest)
		return
	}

	oldSnap, err := h.database.GetSnapshotByTimestamp(reqBody.URL, fromTime)
	if err != nil {
		http.Error(w, fmt.Sprintf("Database error: %v", err), http.StatusInternalServerError)
		return
	}
	if oldSnap == nil {
		http.Error(w, "from version not found", http.StatusNotFound)
		return
	}

	newSnap, err := h.database.GetSnapshotByTimestamp(reqBody.URL, toTime)
	if err != nil {
		http.Error(w, fmt.Sprintf("Database error: %v", err), http.StatusInternalServerError)
		return
	}
	if newSnap == nil {
		http.Error(w, "to version not found", http.StatusNotFound)
		return
	}

	diffText, _, _ := buildUnifiedDiff(contentForDiff(oldSnap), contentForDiff(newSnap))

	backendURL := os.Getenv("AI_BACKEND_URL")
	if backendURL == "" {
		backendURL = "http://ai:3001/analyze-changes"
	}
	payload := map[string]string{
		"diff":          diffText,
		"old_url":       oldSnap.URL,
		"new_url":       newSnap.URL,
		"old_timestamp": oldSnap.ScrapedAt.Format(time.RFC3339),
		"new_timestamp": newSnap.ScrapedAt.Format(time.RFC3339),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, "Failed to encode request", http.StatusInternalServerError)
		return
	}

	req, err := http.NewRequest(http.MethodPost, backendURL, bytes.NewReader(body))
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create backend request: %v", err), http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{
		Timeout: 0, // streaming
	}

	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf("AI backend error: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(resp.Body)
		http.Error(w, fmt.Sprintf("AI backend returned %d: %s", resp.StatusCode, string(msg)), http.StatusBadGateway)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Transfer-Encoding", "chunked")

	var buf bytes.Buffer
	tmp := make([]byte, 2048)
	for {
		n, readErr := resp.Body.Read(tmp)
		if n > 0 {
			chunk := tmp[:n]
			buf.Write(chunk)
			if _, err := w.Write(chunk); err != nil {
				return
			}
			flusher.Flush()
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			http.Error(w, fmt.Sprintf("Stream error: %v", readErr), http.StatusBadGateway)
			return
		}
	}

	// Persist analysis as change_summary on the newer snapshot
	summary := strings.TrimSpace(buf.String())
	if summary != "" {
		if err := h.database.UpdateChangeSummary(newSnap.ID, summary); err != nil {
			// log but don't fail the response
			fmt.Printf("Warning: failed to persist change summary: %v\n", err)
		}
	}
}

// HandleDeleteSnapshot deletes a specific version by ID.
func (h *APIHandler) HandleDeleteSnapshot(w http.ResponseWriter, r *http.Request) {
	if h.config.APIToken != "" {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(auth) <= len(prefix) || auth[:len(prefix)] != prefix || auth[len(prefix):] != h.config.APIToken {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.database == nil {
		http.Error(w, "Memory database not available", http.StatusServiceUnavailable)
		return
	}

	parts := strings.Split(strings.TrimSuffix(r.URL.Path, "/"), "/")
	if len(parts) < 5 {
		http.Error(w, "Invalid URL path", http.StatusBadRequest)
		return
	}
	id := parts[len(parts)-1]
	if id == "" {
		http.Error(w, "Missing version ID", http.StatusBadRequest)
		return
	}

	if err := h.database.DeleteSnapshot(id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, fmt.Sprintf("Snapshot not found: %s", id), http.StatusNotFound)
		} else {
			http.Error(w, fmt.Sprintf("Failed to delete snapshot: %v", err), http.StatusInternalServerError)
		}
		return
	}

	resp := DeleteResponse{
		Success: true,
		Message: "Deleted",
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func mapSnapshotToHistoryResponse(snapshot *database.Snapshot) HistoryEntryResponse {
	return HistoryEntryResponse{
		ID:            snapshot.ID,
		URL:           snapshot.URL,
		Domain:        snapshot.Domain,
		Title:         snapshot.Title,
		ContentHash:   snapshot.ContentHash,
		PreviousHash:  snapshot.PreviousHash,
		HasChanges:    snapshot.HasChanges,
		ChangeSummary: snapshot.ChangeSummary,
		Summary:       snapshot.Summary,
		ScrapedAt:     snapshot.ScrapedAt,
		LastCheckedAt: snapshot.LastCheckedAt,
		StatusCode:    snapshot.StatusCode,
		ScrapeStatus:  snapshot.ScrapeStatus,
		ErrorMessage:  snapshot.ErrorMessage,
		RetryCount:    snapshot.RetryCount,
	}
}

func mapSnapshotToVersionResponse(snapshot *database.Snapshot) VersionResponse {
	return VersionResponse{
		ID:            snapshot.ID,
		URL:           snapshot.URL,
		Domain:        snapshot.Domain,
		Title:         snapshot.Title,
		ContentHash:   snapshot.ContentHash,
		PreviousHash:  snapshot.PreviousHash,
		HasChanges:    snapshot.HasChanges,
		Summary:       snapshot.Summary,
		ChangeSummary: snapshot.ChangeSummary,
		CleanText:     snapshot.CleanText,
		RawContent:    snapshot.RawContent,
		ScrapedAt:     snapshot.ScrapedAt,
		LastCheckedAt: snapshot.LastCheckedAt,
		StatusCode:    snapshot.StatusCode,
		ScrapeStatus:  snapshot.ScrapeStatus,
		ErrorMessage:  snapshot.ErrorMessage,
		RetryCount:    snapshot.RetryCount,
	}
}

func contentForDiff(snapshot *database.Snapshot) string {
	if snapshot == nil {
		return ""
	}
	if snapshot.RawContent != "" {
		return snapshot.RawContent
	}
	return snapshot.CleanText
}

func classifyFailureStatus(status int, msg string) string {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return "failed_blocked"
	}
	if status >= 400 && status < 600 {
		return "failed_http"
	}

	lower := strings.ToLower(msg)
	if strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline") || strings.Contains(lower, "connection") {
		return "failed_network"
	}
	if strings.Contains(lower, "captcha") || strings.Contains(lower, "blocked") {
		return "failed_blocked"
	}

	return "failed_network"
}

// buildUnifiedDiff returns a unified diff string plus added/removed line counts.
func buildUnifiedDiff(oldText, newText string) (string, int, int) {
	dmp := diffmatchpatch.New()
	diffs := dmp.DiffMain(oldText, newText, false)

	added := 0
	removed := 0
	for _, d := range diffs {
		switch d.Type {
		case diffmatchpatch.DiffInsert:
			added += strings.Count(d.Text, "\n")
		case diffmatchpatch.DiffDelete:
			removed += strings.Count(d.Text, "\n")
		}
	}

	diffText := dmp.DiffPrettyText(diffs)

	return diffText, added, removed
}

// corsMiddleware wraps an HTTP handler with CORS headers
func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Set CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		// Handle preflight OPTIONS request
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Call the next handler
		next.ServeHTTP(w, r)
	}
}

// StartAPIServer starts the HTTP API server
func StartAPIServer(scraper ScraperInterface, cfg *config.Config, port int, dbPath string) error {
	// Initialize storage based on configuration
	var storageBackend Storage
	var err error

	if cfg.RedisAddr != "" {
		// Use Redis for persistent storage
		storageBackend, err = storage.NewRedisStorage(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
		if err != nil {
			return fmt.Errorf("failed to initialize Redis storage: %w", err)
		}
		fmt.Printf("Using Redis storage at %s\n", cfg.RedisAddr)
	} else {
		// Fall back to in-memory storage
		storageBackend = storage.NewInMemoryStorage()
		fmt.Println("Using in-memory storage (not persistent)")
	}

	// Initialize SQLite database for memory/snapshots
	var db *database.DB
	if dbPath != "" {
		logLevel := cfg.LogLevel
		if logLevel == "" {
			logLevel = "info"
		}
		// Create a logger for the database using the existing logger package
		dbLogger := &simpleLoggerWrapper{level: logLevel}
		db, err = database.Initialize(dbPath, dbLogger)
		if err != nil {
			fmt.Printf("Warning: Failed to initialize memory database: %v\n", err)
			fmt.Println("Continuing without memory layer...")
		} else {
			fmt.Printf("✅ Memory database initialized at %s\n", dbPath)
		}
	}

	handler := NewAPIHandler(scraper, cfg, storageBackend, db)

	// Set up routes with CORS middleware
	http.HandleFunc("/scrape", corsMiddleware(handler.HandleScrape))
	http.HandleFunc("/scrape/status", corsMiddleware(handler.HandleJobStatus))
	http.HandleFunc("/health", corsMiddleware(handler.HandleHealth))
	http.HandleFunc("/metrics", corsMiddleware(handler.HandleMetrics))
	http.HandleFunc("/memory/lookup", corsMiddleware(handler.HandleMemoryLookup))
	http.HandleFunc("/memory/recent", corsMiddleware(handler.HandleMemoryRecent))
	http.HandleFunc("/memory/search", corsMiddleware(handler.HandleMemorySearch))
	http.HandleFunc("/memory/failures", corsMiddleware(handler.HandleFailedScrapes))
	http.HandleFunc("/memory/snapshot/", corsMiddleware(handler.HandleUpdateSnapshotSummary)) // Handles /memory/snapshot/{id}/summary
	http.HandleFunc("/memory/history", corsMiddleware(handler.HandleScrapeHistory))
	http.HandleFunc("/memory/version/", corsMiddleware(handler.HandleScrapeVersion))
	http.HandleFunc("/memory/diff", corsMiddleware(handler.HandleScrapeDiff))
	http.HandleFunc("/memory/analyze-changes", corsMiddleware(handler.HandleAnalyzeChanges))
	http.HandleFunc("/admin/rebuild-fts5", corsMiddleware(handler.HandleRebuildFTS5))
	http.HandleFunc("/admin/test-fts5", corsMiddleware(handler.HandleTestFTS5))

	// Temporary aliases for legacy /api/scrapes/* routes (to be removed once clients migrate)
	http.HandleFunc("/api/scrapes/history", corsMiddleware(handler.HandleScrapeHistory))
	http.HandleFunc("/api/scrapes/version/", corsMiddleware(handler.HandleScrapeVersion))
	http.HandleFunc("/api/scrapes/diff", corsMiddleware(handler.HandleScrapeDiff))
	http.HandleFunc("/api/scrapes/analyze-changes", corsMiddleware(handler.HandleAnalyzeChanges))

	// Prometheus metrics endpoint (wrapped with CORS handler)
	http.Handle("/prometheus", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		promhttp.Handler().ServeHTTP(w, r)
	}))

	// Start server
	addr := fmt.Sprintf(":%d", port)
	fmt.Printf("🚀 Starting API server on port %d\n", port)
	fmt.Printf("📡 Endpoints:\n")
	fmt.Printf("   POST  /scrape - Create scraping job\n")
	fmt.Printf("   GET   /scrape/status?id=<job_id> - Get job status\n")
	fmt.Printf("   GET   /health - Health check\n")
	fmt.Printf("   GET   /metrics - Get metrics\n")
	fmt.Printf("   GET   /memory/lookup?url=<url> - Check memory for URL\n")
	fmt.Printf("   GET   /memory/recent?limit=N&offset=M - Get recent snapshots\n")
	fmt.Printf("   GET   /memory/search?q=<query>&domain=<domain> - Search scraped content\n")
	fmt.Printf("   GET   /memory/failures?limit=N - Get recent failed scrapes\n")
	fmt.Printf("   PATCH /memory/snapshot/:id/summary - Update snapshot summary\n")
	fmt.Printf("   GET   /memory/history?url=<url> - Get version history for URL\n")
	fmt.Printf("   GET   /memory/version/:id - Get a specific version by ID\n")
	fmt.Printf("   DELETE /memory/version/:id - Delete a specific version\n")
	fmt.Printf("   GET   /memory/diff?url=<url>&from=<ts>&to=<ts> - Diff two versions\n")
	fmt.Printf("   POST  /memory/analyze-changes - AI change analysis\n")
	fmt.Printf("   POST  /admin/rebuild-fts5 - Rebuild FTS5 index\n")
	fmt.Printf("   GET   /admin/test-fts5?q=<query> - Direct FTS5 probe (debug)\n")
	fmt.Printf("   GET   /prometheus - Prometheus metrics\n")

	return http.ListenAndServe(addr, nil)
}

// simpleLoggerWrapper wraps the logger interface for database use
type simpleLoggerWrapper struct {
	level string
}

func (l *simpleLoggerWrapper) Info(format string, args ...interface{}) {
	if l.level == "info" || l.level == "debug" {
		fmt.Printf("[INFO] "+format+"\n", args...)
	}
}

func (l *simpleLoggerWrapper) Debug(format string, args ...interface{}) {
	if l.level == "debug" {
		fmt.Printf("[DEBUG] "+format+"\n", args...)
	}
}

func (l *simpleLoggerWrapper) Warn(format string, args ...interface{}) {
	fmt.Printf("[WARN] "+format+"\n", args...)
}

func (l *simpleLoggerWrapper) Error(format string, args ...interface{}) {
	fmt.Printf("[ERROR] "+format+"\n", args...)
}
