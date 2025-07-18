package strategy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/kareemsasa3/arachne/pkg/parser"

	"github.com/kareemsasa3/arachne/internal/config"
	"github.com/kareemsasa3/arachne/internal/errors"
)

// ScrapedResult is a unified struct returned by any strategy.
// This decouples the strategy from the main ScrapedData struct.
type ScrapedResult struct {
	Title      string
	Body       string // The full HTML/JSON content
	StatusCode int
	NextURL    string // For pagination support
}

// ScrapingStrategy defines the contract for different scraping methods.
type ScrapingStrategy interface {
	Execute(ctx context.Context, urlStr string, config *config.Config) (*ScrapedResult, error)
}

// HTTPStrategy implements scraping using standard HTTP requests
type HTTPStrategy struct {
	client *http.Client
}

// NewHTTPStrategy creates a new HTTP strategy with the given configuration
func NewHTTPStrategy(cfg *config.Config) *HTTPStrategy {
	// Create transport with connection pooling and HTTP/2 support
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		DisableCompression:  false, // Enable compression
		ForceAttemptHTTP2:   true,  // Force HTTP/2 when possible
	}

	return &HTTPStrategy{
		client: &http.Client{
			Timeout:   cfg.RequestTimeout,
			Transport: transport,
		},
	}
}

// Execute performs HTTP-based scraping
func (s *HTTPStrategy) Execute(ctx context.Context, urlStr string, cfg *config.Config) (*ScrapedResult, error) {
	// Create request with context for cancellation
	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return nil, errors.NewScraperError(urlStr, "Failed to create request", err)
	}

	// Set user agent to be respectful
	req.Header.Set("User-Agent", cfg.UserAgent)

	// Make the request
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, errors.NewScraperError(urlStr, "Request failed", err)
	}
	defer resp.Body.Close()

	// Check for HTTP errors
	if resp.StatusCode >= 400 {
		return nil, errors.NewHTTPError(urlStr, resp.StatusCode, fmt.Sprintf("HTTP %d", resp.StatusCode))
	}

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.NewScraperError(urlStr, "Failed to read body", err)
	}

	// Extract title from response
	title := parser.ExtractTitle(string(body), resp.Header.Get("Content-Type"))

	// For HTTP strategy, we don't extract next URL (no JavaScript execution)
	return &ScrapedResult{
		Title:      title,
		Body:       string(body),
		StatusCode: resp.StatusCode,
		NextURL:    "", // HTTP strategy doesn't handle pagination
	}, nil
}
