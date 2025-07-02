package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// MockScraper is a mock implementation for testing
type MockScraper struct{}

func (m *MockScraper) ScrapeURLs(urls []string) []ScrapedData {
	results := make([]ScrapedData, len(urls))
	for i, url := range urls {
		results[i] = ScrapedData{
			URL:     url,
			Title:   "Mock Title for " + url,
			Status:  200,
			Size:    1024,
			Scraped: time.Now(),
		}
	}
	return results
}

func (m *MockScraper) ScrapeSite(siteURL string) []ScrapedData {
	return []ScrapedData{
		{
			URL:     siteURL,
			Title:   "Mock Site Title for " + siteURL,
			Status:  200,
			Size:    2048,
			Scraped: time.Now(),
		},
	}
}

func (m *MockScraper) GetMetrics() interface{} {
	return map[string]interface{}{
		"total_requests": 0,
		"successful":     0,
		"failed":         0,
		"retries":        0,
	}
}

func TestHandleHealth(t *testing.T) {
	// Create a mock request to pass to our handler
	req, err := http.NewRequest("GET", "/health", nil)
	if err != nil {
		t.Fatal(err)
	}

	// We use httptest.NewRecorder to record the response
	rr := httptest.NewRecorder()
	storage := NewInMemoryStorage()
	mockScraper := &MockScraper{}
	handler := http.HandlerFunc(NewAPIHandler(mockScraper, DefaultConfig(), storage).HandleHealth)

	// Our handlers satisfy http.Handler, so we can call their ServeHTTP method
	// directly and pass in our Request and ResponseRecorder
	handler.ServeHTTP(rr, req)

	// Check the status code is what we expect
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusOK)
	}

	// Check the response body is what we expect
	expected := `"status":"healthy"`
	if !strings.Contains(rr.Body.String(), expected) {
		t.Errorf("handler returned unexpected body: got %s want it to contain %s",
			rr.Body.String(), expected)
	}

	// Check Content-Type header
	if contentType := rr.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("handler returned wrong content type: got %v want %v",
			contentType, "application/json")
	}
}

func TestHandleScrape(t *testing.T) {
	storage := NewInMemoryStorage()
	mockScraper := &MockScraper{}
	handler := NewAPIHandler(mockScraper, DefaultConfig(), storage)

	tests := []struct {
		name           string
		method         string
		body           string
		expectedStatus int
		expectedFields []string
	}{
		{
			name:           "Valid scrape request with URLs",
			method:         "POST",
			body:           `{"urls": ["https://example.com", "https://test.com"]}`,
			expectedStatus: http.StatusAccepted,
			expectedFields: []string{"job_id", "status", "message"},
		},
		{
			name:           "Valid scrape request with site URL",
			method:         "POST",
			body:           `{"site_url": "https://example.com"}`,
			expectedStatus: http.StatusAccepted,
			expectedFields: []string{"job_id", "status", "message"},
		},
		{
			name:           "Invalid method",
			method:         "GET",
			body:           `{"urls": ["https://example.com"]}`,
			expectedStatus: http.StatusMethodNotAllowed,
			expectedFields: []string{},
		},
		{
			name:           "Invalid JSON",
			method:         "POST",
			body:           `{"urls": ["https://example.com"`,
			expectedStatus: http.StatusBadRequest,
			expectedFields: []string{},
		},
		{
			name:           "No URLs provided",
			method:         "POST",
			body:           `{}`,
			expectedStatus: http.StatusBadRequest,
			expectedFields: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(tt.method, "/scrape", bytes.NewBufferString(tt.body))
			if err != nil {
				t.Fatal(err)
			}

			rr := httptest.NewRecorder()
			handlerFunc := http.HandlerFunc(handler.HandleScrape)
			handlerFunc.ServeHTTP(rr, req)

			if status := rr.Code; status != tt.expectedStatus {
				t.Errorf("handler returned wrong status code: got %v want %v",
					status, tt.expectedStatus)
			}

			if tt.expectedStatus == http.StatusAccepted {
				// Check that response contains expected fields
				responseBody := rr.Body.String()
				for _, field := range tt.expectedFields {
					if !strings.Contains(responseBody, field) {
						t.Errorf("response body does not contain expected field %s: %s",
							field, responseBody)
					}
				}

				// Verify job was created in storage
				var response ScrapeResponse
				if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
					t.Fatalf("failed to unmarshal response: %v", err)
				}

				// Check that job exists in storage
				job, err := storage.GetJob(context.Background(), response.JobID)
				if err != nil {
					t.Errorf("job not found in storage: %v", err)
				}

				// The job should be in a valid state after creation
				// Note: Due to the asynchronous nature, the job could be in any of these states:
				// - "pending": Job created but not yet started
				// - "running": Job started but not yet completed
				// - "completed": Job completed (mock scraper is very fast)
				validStates := []string{"pending", "running", "completed"}
				isValidState := false
				for _, state := range validStates {
					if job.Status == state {
						isValidState = true
						break
					}
				}
				if !isValidState {
					t.Errorf("expected job status to be one of %v, got %s", validStates, job.Status)
				}
			}
		})
	}
}

func TestHandleJobStatus(t *testing.T) {
	storage := NewInMemoryStorage()
	mockScraper := &MockScraper{}
	handler := NewAPIHandler(mockScraper, DefaultConfig(), storage)

	// Create a test job
	testJob := &ScrapingJob{
		ID:        "test-job-123",
		Status:    "completed",
		CreatedAt: time.Now(),
		Progress:  100,
	}
	if err := storage.SaveJob(context.Background(), testJob); err != nil {
		t.Fatalf("failed to save test job: %v", err)
	}

	tests := []struct {
		name           string
		method         string
		jobID          string
		expectedStatus int
		expectedFields []string
	}{
		{
			name:           "Valid job status request",
			method:         "GET",
			jobID:          "test-job-123",
			expectedStatus: http.StatusOK,
			expectedFields: []string{"job", "id", "status", "progress"},
		},
		{
			name:           "Invalid method",
			method:         "POST",
			jobID:          "test-job-123",
			expectedStatus: http.StatusMethodNotAllowed,
			expectedFields: []string{},
		},
		{
			name:           "Missing job ID",
			method:         "GET",
			jobID:          "",
			expectedStatus: http.StatusBadRequest,
			expectedFields: []string{},
		},
		{
			name:           "Non-existent job",
			method:         "GET",
			jobID:          "non-existent-job",
			expectedStatus: http.StatusNotFound,
			expectedFields: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "/job/status"
			if tt.jobID != "" {
				url += "?id=" + tt.jobID
			}

			req, err := http.NewRequest(tt.method, url, nil)
			if err != nil {
				t.Fatal(err)
			}

			rr := httptest.NewRecorder()
			handlerFunc := http.HandlerFunc(handler.HandleJobStatus)
			handlerFunc.ServeHTTP(rr, req)

			if status := rr.Code; status != tt.expectedStatus {
				t.Errorf("handler returned wrong status code: got %v want %v",
					status, tt.expectedStatus)
			}

			if tt.expectedStatus == http.StatusOK {
				// Check that response contains expected fields
				responseBody := rr.Body.String()
				for _, field := range tt.expectedFields {
					if !strings.Contains(responseBody, field) {
						t.Errorf("response body does not contain expected field %s: %s",
							field, responseBody)
					}
				}

				// Verify job data in response
				var response JobStatusResponse
				if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
					t.Fatalf("failed to unmarshal response: %v", err)
				}

				if response.Job.ID != tt.jobID {
					t.Errorf("expected job ID %s, got %s", tt.jobID, response.Job.ID)
				}
			}
		})
	}
}

func TestHandleMetrics(t *testing.T) {
	config := DefaultConfig()
	config.EnableMetrics = true
	storage := NewInMemoryStorage()
	mockScraper := &MockScraper{}
	handler := NewAPIHandler(mockScraper, config, storage)

	// Test with metrics enabled
	t.Run("Metrics enabled", func(t *testing.T) {
		req, err := http.NewRequest("GET", "/metrics", nil)
		if err != nil {
			t.Fatal(err)
		}

		rr := httptest.NewRecorder()
		handlerFunc := http.HandlerFunc(handler.HandleMetrics)
		handlerFunc.ServeHTTP(rr, req)

		if status := rr.Code; status != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v",
				status, http.StatusOK)
		}

		if contentType := rr.Header().Get("Content-Type"); contentType != "application/json" {
			t.Errorf("handler returned wrong content type: got %v want %v",
				contentType, "application/json")
		}
	})

	// Test with metrics disabled
	t.Run("Metrics disabled", func(t *testing.T) {
		config.EnableMetrics = false
		mockScraper := &MockScraper{}
		handler := NewAPIHandler(mockScraper, config, storage)

		req, err := http.NewRequest("GET", "/metrics", nil)
		if err != nil {
			t.Fatal(err)
		}

		rr := httptest.NewRecorder()
		handlerFunc := http.HandlerFunc(handler.HandleMetrics)
		handlerFunc.ServeHTTP(rr, req)

		if status := rr.Code; status != http.StatusServiceUnavailable {
			t.Errorf("handler returned wrong status code: got %v want %v",
				status, http.StatusServiceUnavailable)
		}
	})
}

func TestStorageInterface(t *testing.T) {
	storage := NewInMemoryStorage()
	ctx := context.Background()

	// Test job creation and retrieval
	t.Run("Save and Get Job", func(t *testing.T) {
		job := &ScrapingJob{
			ID:        "test-job",
			Status:    "pending",
			CreatedAt: time.Now(),
		}

		// Save job
		err := storage.SaveJob(ctx, job)
		if err != nil {
			t.Fatalf("failed to save job: %v", err)
		}

		// Retrieve job
		retrievedJob, err := storage.GetJob(ctx, job.ID)
		if err != nil {
			t.Fatalf("failed to get job: %v", err)
		}

		if retrievedJob.ID != job.ID {
			t.Errorf("expected job ID %s, got %s", job.ID, retrievedJob.ID)
		}

		if retrievedJob.Status != job.Status {
			t.Errorf("expected job status %s, got %s", job.Status, retrievedJob.Status)
		}
	})

	// Test job update
	t.Run("Update Job", func(t *testing.T) {
		job := &ScrapingJob{
			ID:        "update-test-job",
			Status:    "pending",
			CreatedAt: time.Now(),
		}

		// Save initial job
		err := storage.SaveJob(ctx, job)
		if err != nil {
			t.Fatalf("failed to save job: %v", err)
		}

		// Update job
		job.Status = "completed"
		err = storage.UpdateJob(ctx, job)
		if err != nil {
			t.Fatalf("failed to update job: %v", err)
		}

		// Verify update
		retrievedJob, err := storage.GetJob(ctx, job.ID)
		if err != nil {
			t.Fatalf("failed to get updated job: %v", err)
		}

		if retrievedJob.Status != "completed" {
			t.Errorf("expected updated job status 'completed', got %s", retrievedJob.Status)
		}
	})

	// Test job listing
	t.Run("List Jobs", func(t *testing.T) {
		jobIDs, err := storage.ListJobs(ctx)
		if err != nil {
			t.Fatalf("failed to list jobs: %v", err)
		}

		// Should have at least the jobs we created in previous tests
		if len(jobIDs) < 2 {
			t.Errorf("expected at least 2 jobs, got %d", len(jobIDs))
		}
	})

	// Test job deletion
	t.Run("Delete Job", func(t *testing.T) {
		job := &ScrapingJob{
			ID:        "delete-test-job",
			Status:    "pending",
			CreatedAt: time.Now(),
		}

		// Save job
		err := storage.SaveJob(ctx, job)
		if err != nil {
			t.Fatalf("failed to save job: %v", err)
		}

		// Delete job
		err = storage.DeleteJob(ctx, job.ID)
		if err != nil {
			t.Fatalf("failed to delete job: %v", err)
		}

		// Verify deletion
		_, err = storage.GetJob(ctx, job.ID)
		if err == nil {
			t.Error("expected error when getting deleted job, got nil")
		}
	})
}

func TestScrapeJobLifecycle(t *testing.T) {
	storage := NewInMemoryStorage()
	mockScraper := &MockScraper{}
	handler := NewAPIHandler(mockScraper, DefaultConfig(), storage)

	// Step 1: Submit a job
	requestBody := `{"urls": ["https://example.com", "https://test.com"]}`
	req, err := http.NewRequest("POST", "/scrape", bytes.NewBufferString(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	handlerFunc := http.HandlerFunc(handler.HandleScrape)
	handlerFunc.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusAccepted {
		t.Fatalf("handler returned wrong status code: got %v want %v", status, http.StatusAccepted)
	}

	var response ScrapeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	jobID := response.JobID

	// Step 2: Poll for job status until completed (with timeout)
	var job *ScrapingJob
	var pollErr error
	maxWait := 2 * time.Second
	interval := 20 * time.Millisecond
	start := time.Now()
	for {
		job, pollErr = storage.GetJob(context.Background(), jobID)
		if pollErr != nil {
			t.Fatalf("job not found in storage: %v", pollErr)
		}
		if job.Status == "completed" {
			break
		}
		if time.Since(start) > maxWait {
			t.Fatalf("job did not complete within %v (last status: %s)", maxWait, job.Status)
		}
		time.Sleep(interval)
	}

	// Step 3: Assert final job state
	if job.Status != "completed" {
		t.Errorf("expected job status 'completed', got %s", job.Status)
	}
	if len(job.Results) != 2 {
		t.Errorf("expected 2 results, got %d", len(job.Results))
	}
	for _, result := range job.Results {
		if result.Status != 200 {
			t.Errorf("expected result status 200, got %d", result.Status)
		}
		if result.Title == "" {
			t.Errorf("expected non-empty title for result")
		}
	}
}
