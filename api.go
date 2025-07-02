package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// ScraperInterface defines the interface for scrapers
type ScraperInterface interface {
	ScrapeURLs(urls []string) []ScrapedData
	ScrapeSite(siteURL string) []ScrapedData
	GetMetrics() interface{}
}

// Storage interface for job persistence
type Storage interface {
	SaveJob(ctx context.Context, job *ScrapingJob) error
	GetJob(ctx context.Context, jobID string) (*ScrapingJob, error)
	UpdateJob(ctx context.Context, job *ScrapingJob) error
	ListJobs(ctx context.Context) ([]string, error)
	GetJobsByStatus(ctx context.Context, status string) ([]*ScrapingJob, error)
	DeleteJob(ctx context.Context, jobID string) error
	Close() error
}

// APIHandler handles HTTP API requests
type APIHandler struct {
	scraper ScraperInterface
	config  *Config
	storage Storage
}

// NewAPIHandler creates a new API handler
func NewAPIHandler(scraper ScraperInterface, config *Config, storage Storage) *APIHandler {
	return &APIHandler{
		scraper: scraper,
		config:  config,
		storage: storage,
	}
}

// ScrapingJob represents an asynchronous scraping job
type ScrapingJob struct {
	ID          string        `json:"id"`
	Status      string        `json:"status"` // "pending", "running", "completed", "failed"
	Request     ScrapeRequest `json:"request"`
	Results     []ScrapedData `json:"results,omitempty"`
	Error       string        `json:"error,omitempty"`
	CreatedAt   time.Time     `json:"created_at"`
	StartedAt   *time.Time    `json:"started_at,omitempty"`
	CompletedAt *time.Time    `json:"completed_at,omitempty"`
	Progress    int           `json:"progress"` // 0-100
}

// ScrapeRequest represents a scraping request
type ScrapeRequest struct {
	URLs    []string `json:"urls"`
	SiteURL string   `json:"site_url,omitempty"`
}

// ScrapeResponse represents a scraping response
type ScrapeResponse struct {
	JobID   string `json:"job_id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// JobStatusResponse represents a job status response
type JobStatusResponse struct {
	Job     *ScrapingJob `json:"job"`
	Metrics interface{}  `json:"metrics,omitempty"`
}

// HandleScrape handles scraping requests asynchronously
func (h *APIHandler) HandleScrape(w http.ResponseWriter, r *http.Request) {
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
	job := &ScrapingJob{
		ID:        jobID,
		Status:    "pending",
		Request:   req,
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
		Message: "Scraping job created successfully",
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
func (h *APIHandler) executeScrapingJob(job *ScrapingJob) {
	ctx := context.Background()

	// Update job status to running
	job.Status = "running"
	now := time.Now()
	job.StartedAt = &now
	if err := h.storage.UpdateJob(ctx, job); err != nil {
		// Log error but continue execution
		fmt.Printf("Failed to update job status to running: %v\n", err)
	}

	var results []ScrapedData

	// Execute scraping based on request type
	if job.Request.SiteURL != "" {
		results = h.scraper.ScrapeSite(job.Request.SiteURL)
	} else {
		results = h.scraper.ScrapeURLs(job.Request.URLs)
	}

	// Update job with results
	job.Results = results
	job.Progress = 100
	now = time.Now()
	job.CompletedAt = &now
	job.Status = "completed"

	if err := h.storage.UpdateJob(ctx, job); err != nil {
		fmt.Printf("Failed to update job with results: %v\n", err)
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

// StartAPIServer starts the HTTP API server
func StartAPIServer(scraper ScraperInterface, config *Config, port int) error {
	// Initialize storage based on configuration
	var storage Storage
	var err error

	if config.RedisAddr != "" {
		// Use Redis for persistent storage
		storage, err = NewRedisStorage(config.RedisAddr, config.RedisPassword, config.RedisDB)
		if err != nil {
			return fmt.Errorf("failed to initialize Redis storage: %w", err)
		}
		fmt.Printf("Using Redis storage at %s\n", config.RedisAddr)
	} else {
		// Fall back to in-memory storage
		storage = NewInMemoryStorage()
		fmt.Println("Using in-memory storage (not persistent)")
	}

	handler := NewAPIHandler(scraper, config, storage)

	// Set up routes
	http.HandleFunc("/scrape", handler.HandleScrape)
	http.HandleFunc("/scrape/status", handler.HandleJobStatus)
	http.HandleFunc("/health", handler.HandleHealth)
	http.HandleFunc("/metrics", handler.HandleMetrics)

	// Start server
	addr := fmt.Sprintf(":%d", port)
	fmt.Printf("🚀 Starting API server on port %d\n", port)
	fmt.Printf("📡 Endpoints:\n")
	fmt.Printf("   POST /scrape - Create scraping job\n")
	fmt.Printf("   GET  /scrape/status?id=<job_id> - Get job status\n")
	fmt.Printf("   GET  /health - Health check\n")
	fmt.Printf("   GET  /metrics - Get metrics\n")

	return http.ListenAndServe(addr, nil)
}
