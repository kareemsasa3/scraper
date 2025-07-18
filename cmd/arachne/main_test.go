package main

import (
	"testing"
	"time"

	"github.com/kareemsasa3/arachne/pkg/parser"

	"github.com/kareemsasa3/arachne/internal/config"
	"github.com/kareemsasa3/arachne/internal/errors"
	"github.com/kareemsasa3/arachne/internal/metrics"
)

func TestExtractHTMLTitle(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		expected string
	}{
		{
			name:     "Valid title",
			html:     `<html><head><title>Test Title</title></head><body>Content</body></html>`,
			expected: "Test Title",
		},
		{
			name:     "No title tag",
			html:     `<html><head></head><body>Content</body></html>`,
			expected: "No HTML title found",
		},
		{
			name:     "Empty title",
			html:     `<html><head><title></title></head><body>Content</body></html>`,
			expected: "Empty HTML title",
		},
		{
			name:     "Malformed title",
			html:     `<html><head><title>Test Title</head><body>Content</body></html>`,
			expected: "Malformed HTML title",
		},
		{
			name:     "Case insensitive title",
			html:     `<html><head><TITLE>Test Title</TITLE></head><body>Content</body></html>`,
			expected: "Malformed HTML title",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parser.ExtractHTMLTitle(tt.html)
			if result != tt.expected {
				t.Errorf("extractHTMLTitle() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestExtractJSONTitle(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		expected string
	}{
		{
			name:     "Title field",
			json:     `{"title": "Test Title", "content": "test"}`,
			expected: "Test Title",
		},
		{
			name:     "Name field",
			json:     `{"name": "Test Name", "content": "test"}`,
			expected: "Test Name",
		},
		{
			name:     "Login field",
			json:     `{"login": "testuser", "content": "test"}`,
			expected: "testuser",
		},
		{
			name:     "No title fields",
			json:     `{"content": "test", "data": "value"}`,
			expected: "content: test",
		},
		{
			name:     "Invalid JSON",
			json:     `{"title": "test"`,
			expected: "Invalid JSON",
		},
		{
			name:     "Empty JSON",
			json:     `{}`,
			expected: "JSON response (no title field)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parser.ExtractJSONTitle(tt.json)
			if result != tt.expected {
				t.Errorf("extractJSONTitle() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestExtractTitle(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		contentType string
		expected    string
	}{
		{
			name:        "JSON content type",
			content:     `{"title": "JSON Title"}`,
			contentType: "application/json",
			expected:    "JSON Title",
		},
		{
			name:        "JSON content without content type",
			content:     `{"title": "JSON Title"}`,
			contentType: "text/plain",
			expected:    "JSON Title",
		},
		{
			name:        "HTML content",
			content:     `<html><head><title>HTML Title</title></head><body>Content</body></html>`,
			contentType: "text/html",
			expected:    "HTML Title",
		},
		{
			name:        "Array JSON",
			content:     `[{"title": "Array Title"}]`,
			contentType: "application/json",
			expected:    "Invalid JSON",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parser.ExtractTitle(tt.content, tt.contentType)
			if result != tt.expected {
				t.Errorf("extractTitle() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestValidateURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{
			name:    "Valid HTTP URL",
			url:     "http://example.com",
			wantErr: false,
		},
		{
			name:    "Valid HTTPS URL",
			url:     "https://example.com",
			wantErr: false,
		},
		{
			name:    "Empty URL",
			url:     "",
			wantErr: true,
		},
		{
			name:    "Invalid URL",
			url:     "not-a-url",
			wantErr: true,
		},
		{
			name:    "Missing scheme",
			url:     "example.com",
			wantErr: true,
		},
		{
			name:    "Invalid scheme",
			url:     "ftp://example.com",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := errors.ValidateURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateURL() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		config  *config.Config
		wantErr bool
	}{
		{
			name:    "Valid config",
			config:  config.DefaultConfig(),
			wantErr: false,
		},
		{
			name: "Invalid max concurrent",
			config: &config.Config{
				MaxConcurrent: 0,
			},
			wantErr: true,
		},
		{
			name: "Invalid request timeout",
			config: &config.Config{
				MaxConcurrent:  1,
				RequestTimeout: 0,
			},
			wantErr: true,
		},
		{
			name: "Invalid total timeout",
			config: &config.Config{
				MaxConcurrent:  1,
				RequestTimeout: 1 * time.Second,
				TotalTimeout:   0,
			},
			wantErr: true,
		},
		{
			name: "Invalid retry attempts",
			config: &config.Config{
				MaxConcurrent:  1,
				RequestTimeout: 1 * time.Second,
				TotalTimeout:   1 * time.Second,
				RetryAttempts:  -1,
			},
			wantErr: true,
		},
		{
			name: "Invalid retry delay",
			config: &config.Config{
				MaxConcurrent:  1,
				RequestTimeout: 1 * time.Second,
				TotalTimeout:   1 * time.Second,
				RetryAttempts:  1,
				RetryDelay:     -1 * time.Second,
			},
			wantErr: true,
		},
		{
			name: "Invalid log level",
			config: &config.Config{
				MaxConcurrent:  1,
				RequestTimeout: 1 * time.Second,
				TotalTimeout:   1 * time.Second,
				RetryAttempts:  1,
				RetryDelay:     1 * time.Second,
				LogLevel:       "invalid",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Config.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestMetrics(t *testing.T) {
	metrics := metrics.NewMetrics()

	// Test initial state
	if metrics.TotalRequests != 0 {
		t.Errorf("Expected initial total requests to be 0, got %d", metrics.TotalRequests)
	}

	// Test recording request
	metrics.RecordRequest()
	if metrics.TotalRequests != 1 {
		t.Errorf("Expected total requests to be 1, got %d", metrics.TotalRequests)
	}

	// Test recording success
	metrics.RecordSuccess("example.com", 200, 1024, 100*time.Millisecond)
	if metrics.SuccessfulRequests != 1 {
		t.Errorf("Expected successful requests to be 1, got %d", metrics.SuccessfulRequests)
	}

	// Test recording failure
	metrics.RecordFailure("example.com", 404)
	if metrics.FailedRequests != 1 {
		t.Errorf("Expected failed requests to be 1, got %d", metrics.FailedRequests)
	}

	// Test recording retry
	metrics.RecordRetry()
	if metrics.RetryAttempts != 1 {
		t.Errorf("Expected retry attempts to be 1, got %d", metrics.RetryAttempts)
	}
}

func TestScraperError(t *testing.T) {
	// Test creating a scraper error
	err := errors.NewScraperError("https://example.com", "Test error", nil)
	if err == nil {
		t.Fatal("Expected error to be created")
	}

	// Test error message
	expectedMsg := "scraper error for https://example.com: Test error (status: 0, retryable: false, attempts: 0)"
	if err.Error() != expectedMsg {
		t.Errorf("Expected error message '%s', got '%s'", expectedMsg, err.Error())
	}

	// Test retryable error
	retryableErr := errors.NewScraperError("https://example.com", "Timeout error", nil)
	if retryableErr.IsRetryable() {
		t.Error("Expected non-retryable error for nil underlying error")
	}
}

func BenchmarkExtractHTMLTitle(b *testing.B) {
	html := `<html><head><title>Benchmark Test Title</title></head><body>Content</body></html>`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		parser.ExtractHTMLTitle(html)
	}
}

func BenchmarkExtractJSONTitle(b *testing.B) {
	json := `{"title": "Benchmark Test Title", "content": "test content", "data": "more data"}`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		parser.ExtractJSONTitle(json)
	}
}
