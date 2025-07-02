package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// ScrapedData represents the data we extract from websites
type ScrapedData struct {
	URL     string    `json:"url"`
	Title   string    `json:"title"`
	Status  int       `json:"status"`
	Size    int       `json:"size"`
	Error   string    `json:"error,omitempty"`
	Scraped time.Time `json:"scraped"`
	NextURL string    `json:"next_url,omitempty"`
}

// Scraper handles concurrent web scraping with rate limiting
type Scraper struct {
	config          *Config
	logger          *Logger
	metrics         *Metrics
	strategy        ScrapingStrategy
	rateLimiter     chan struct{}
	domainLimiters  map[string]chan struct{}
	circuitBreakers map[string]*CircuitBreaker
	results         chan ScrapedData
	wg              sync.WaitGroup
	mu              sync.RWMutex
}

// NewScraper creates a new scraper with configurable concurrency
func NewScraper(config *Config) *Scraper {
	var strategy ScrapingStrategy
	if config.UseHeadless {
		strategy = NewHeadlessStrategy()
	} else {
		strategy = NewHTTPStrategy(config)
	}

	scraper := &Scraper{
		config:          config,
		logger:          NewLogger(config.LogLevel),
		metrics:         NewMetrics(),
		strategy:        strategy,
		rateLimiter:     make(chan struct{}, config.MaxConcurrent),
		domainLimiters:  make(map[string]chan struct{}),
		circuitBreakers: make(map[string]*CircuitBreaker),
		results:         make(chan ScrapedData, 100),
	}

	// Initialize domain-specific rate limiters
	for domain, limit := range config.DomainRateLimit {
		scraper.domainLimiters[domain] = make(chan struct{}, limit)
	}

	return scraper
}

// scrapeURL fetches a single URL and extracts basic information with retry logic
func (s *Scraper) scrapeURL(ctx context.Context, urlStr string, resultsChan chan<- ScrapedData) {
	defer s.wg.Done()

	// Acquire rate limiters
	s.acquireRateLimiters(urlStr)
	defer s.releaseRateLimiters(urlStr)

	// Perform the actual scraping
	data := s.doScrape(ctx, urlStr)

	// Send result to channel
	resultsChan <- data
}

// doScrape contains the core scraping logic shared between concurrent and sync operations
func (s *Scraper) doScrape(ctx context.Context, urlStr string) ScrapedData {
	// Validate URL
	if err := ValidateURL(urlStr); err != nil {
		s.logger.Error("Invalid URL: %s", urlStr)
		return ScrapedData{
			URL:     urlStr,
			Error:   err.Error(),
			Scraped: time.Now(),
		}
	}

	// Extract domain for rate limiting and circuit breaker
	parsedURL, _ := url.Parse(urlStr)
	domain := parsedURL.Host

	// Get or create circuit breaker for this domain
	s.mu.Lock()
	cb, exists := s.circuitBreakers[domain]
	if !exists {
		cb = NewCircuitBreaker(s.config.CircuitBreakerThreshold, s.config.CircuitBreakerTimeout)
		s.circuitBreakers[domain] = cb
	}
	s.mu.Unlock()

	data := ScrapedData{
		URL:     urlStr,
		Scraped: time.Now(),
	}

	// Record request in metrics
	s.metrics.RecordRequest()

	// Attempt scraping with retry logic and circuit breaker
	var lastErr error
	for attempt := 1; attempt <= s.config.RetryAttempts; attempt++ {
		start := time.Now()

		// Execute request with circuit breaker protection
		err := cb.Execute(func() error {
			// Delegate the actual scraping to the strategy
			result, err := s.strategy.Execute(ctx, urlStr, s.config)
			if err != nil {
				return err
			}

			// Record success in metrics
			duration := time.Since(start)
			s.metrics.RecordSuccess(domain, result.StatusCode, int64(len(result.Body)), duration)

			// Log success
			s.logger.LogSuccess(urlStr, result.StatusCode, len(result.Body), duration)

			// Set data
			data.Status = result.StatusCode
			data.Size = len(result.Body)
			data.Title = result.Title
			data.NextURL = result.NextURL

			return nil
		})

		if err != nil {
			lastErr = err

			// Check if it's a circuit breaker error
			if IsCircuitBreakerError(err) {
				s.logger.Warn("Circuit breaker open for %s: %v", domain, err)
				break
			}

			// Log retry attempt if retryable
			if scraperErr, ok := err.(*ScraperError); ok && scraperErr.IsRetryable() && attempt < s.config.RetryAttempts {
				s.metrics.RecordRetry()
				s.logger.LogRetry(urlStr, attempt, err)
				time.Sleep(s.config.RetryDelay * time.Duration(attempt)) // Exponential backoff
				continue
			}
			break
		}

		// Success - break out of retry loop
		lastErr = nil
		break
	}

	// Handle final error if all retries failed
	if lastErr != nil {
		data.Error = lastErr.Error()
		s.metrics.RecordFailure(domain, 0)
		s.logger.LogFailure(urlStr, lastErr)
	}

	return data
}

// acquireRateLimiters acquires both global and domain-specific rate limiters
func (s *Scraper) acquireRateLimiters(urlStr string) {
	// Acquire global rate limiter slot
	s.rateLimiter <- struct{}{}

	// Acquire domain-specific rate limiter if configured
	parsedURL, _ := url.Parse(urlStr)
	domain := parsedURL.Host

	s.mu.RLock()
	domainLimiter, hasDomainLimit := s.domainLimiters[domain]
	s.mu.RUnlock()

	if hasDomainLimit {
		domainLimiter <- struct{}{}
	}
}

// releaseRateLimiters releases both global and domain-specific rate limiters
func (s *Scraper) releaseRateLimiters(urlStr string) {
	// Release global rate limiter
	<-s.rateLimiter

	// Release domain-specific rate limiter if configured
	parsedURL, _ := url.Parse(urlStr)
	domain := parsedURL.Host

	s.mu.RLock()
	domainLimiter, hasDomainLimit := s.domainLimiters[domain]
	s.mu.RUnlock()

	if hasDomainLimit {
		<-domainLimiter
	}
}

// extractTitle extracts title from HTML or JSON responses
func extractTitle(content, contentType string) string {
	// Check if it's JSON based on content type or content
	if strings.Contains(contentType, "application/json") ||
		(strings.HasPrefix(content, "{") || strings.HasPrefix(content, "[")) {
		return extractJSONTitle(content)
	}

	// Otherwise treat as HTML
	return extractHTMLTitle(content)
}

// extractHTMLTitle extracts title from HTML
func extractHTMLTitle(html string) string {
	// Look for <title> tag
	titleStart := strings.Index(strings.ToLower(html), "<title>")
	if titleStart == -1 {
		return "No HTML title found"
	}

	titleStart += 7 // length of "<title>"
	titleEnd := strings.Index(html[titleStart:], "</title>")
	if titleEnd == -1 {
		return "Malformed HTML title"
	}

	title := html[titleStart : titleStart+titleEnd]
	title = strings.TrimSpace(title)

	if title == "" {
		return "Empty HTML title"
	}

	return title
}

// extractJSONTitle extracts meaningful title from JSON responses
func extractJSONTitle(jsonStr string) string {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return "Invalid JSON"
	}

	// Look for common title fields in JSON
	titleFields := []string{"title", "name", "login", "message", "description"}
	for _, field := range titleFields {
		if value, exists := data[field]; exists {
			if str, ok := value.(string); ok && str != "" {
				return str
			}
		}
	}

	// If no title field, return first meaningful string value in sorted order
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		value := data[key]
		if str, ok := value.(string); ok && len(str) < 100 && str != "" {
			return fmt.Sprintf("%s: %s", key, str)
		}
	}

	return "JSON response (no title field)"
}

// ScrapeURLs concurrently scrapes multiple URLs
func (s *Scraper) ScrapeURLs(urls []string) []ScrapedData {
	ctx, cancel := context.WithTimeout(context.Background(), s.config.TotalTimeout)
	defer cancel()

	s.logger.Info("Starting to scrape %d URLs with %d max concurrent requests", len(urls), s.config.MaxConcurrent)

	// Create a new results channel for this scraping session
	resultsChan := make(chan ScrapedData, len(urls))

	// Start scraping goroutines
	for _, url := range urls {
		s.wg.Add(1)
		go s.scrapeURL(ctx, url, resultsChan)
	}

	// Close results channel when all goroutines complete
	go func() {
		s.wg.Wait()
		close(resultsChan)
	}()

	// Collect results
	var results []ScrapedData
	for data := range resultsChan {
		results = append(results, data)
	}

	// Finish metrics collection
	s.metrics.Finish()

	return results
}

// ScrapeSite scrapes a site with pagination support
func (s *Scraper) ScrapeSite(startURL string) []ScrapedData {
	ctx, cancel := context.WithTimeout(context.Background(), s.config.TotalTimeout)
	defer cancel()

	s.logger.Info("Starting to scrape site %s with pagination support", startURL)

	// Create a new results channel for this scraping session
	resultsChan := make(chan ScrapedData, s.config.MaxPages)

	urlsToScrape := []string{startURL}
	scrapedURLs := make(map[string]bool)
	pageCount := 0

	for len(urlsToScrape) > 0 && pageCount < s.config.MaxPages {
		// Pop the next URL
		url := urlsToScrape[0]
		urlsToScrape = urlsToScrape[1:]

		if scrapedURLs[url] {
			continue
		}
		scrapedURLs[url] = true
		pageCount++

		s.logger.Info("Scraping page %d: %s", pageCount, url)

		// Scrape this URL and get the result
		result := s.scrapeURLSync(ctx, url)

		// Add the result to our channel
		resultsChan <- result

		// If we got a next URL and haven't reached max pages, add it to the queue
		if result.NextURL != "" && pageCount < s.config.MaxPages {
			urlsToScrape = append(urlsToScrape, result.NextURL)
			s.logger.Info("Found next page: %s", result.NextURL)
		}
	}

	// Close results channel
	close(resultsChan)

	// Collect results
	var results []ScrapedData
	for data := range resultsChan {
		results = append(results, data)
	}

	// Finish metrics collection
	s.metrics.Finish()

	return results
}

// scrapeURLSync scrapes a single URL synchronously and returns the result
func (s *Scraper) scrapeURLSync(ctx context.Context, urlStr string) ScrapedData {
	// Acquire rate limiters for synchronous operation
	s.acquireRateLimiters(urlStr)
	defer s.releaseRateLimiters(urlStr)

	// Use the shared scraping logic
	return s.doScrape(ctx, urlStr)
}

// ResultProcessor processes and formats results
type ResultProcessor struct{}

// ProcessResults formats and prints results
func (rp *ResultProcessor) ProcessResults(results []ScrapedData) {
	fmt.Printf("\n=== Scraping Results (%d URLs) ===\n", len(results))

	successCount := 0
	totalSize := 0

	for _, data := range results {
		if data.Error != "" {
			fmt.Printf("❌ %s: %s\n", data.URL, data.Error)
		} else {
			fmt.Printf("✅ %s (Status: %d, Size: %d bytes)\n", data.URL, data.Status, data.Size)
			fmt.Printf("   Title: %s\n", data.Title)
			successCount++
			totalSize += data.Size
		}
		fmt.Println()
	}

	fmt.Printf("Summary: %d/%d successful, %d total bytes\n",
		successCount, len(results), totalSize)
}

// JSONExporter exports results to JSON
func (rp *ResultProcessor) ExportToJSON(results []ScrapedData, filename string) error {
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %v", err)
	}

	// Actually write to file
	err = os.WriteFile(filename, data, 0644)
	if err != nil {
		return fmt.Errorf("failed to write file: %v", err)
	}

	fmt.Printf("✅ JSON saved to %s\n", filename)
	return nil
}

func main() {
	// Setup configuration
	config := setupConfig()

	// Create scraper
	scraper := NewScraper(config)

	// Check if we should run in API mode (containerized or explicit flag)
	apiPort := flag.Lookup("api-port").Value.String()
	isContainerized := os.Getenv("SCRAPER_REDIS_ADDR") != "" // Detect containerized environment

	if apiPort != "0" || isContainerized {
		// API mode - start the server
		port := 8080 // Default port for containerized environment
		if apiPort != "0" {
			if _, err := fmt.Sscanf(apiPort, "%d", &port); err != nil {
				fmt.Printf("Warning: invalid API port '%s', using default port 8080\n", apiPort)
				port = 8080
			}
		}

		fmt.Printf("🚀 Starting Scraper API Server on port %d...\n", port)
		fmt.Printf("Configuration: %s\n", config.String())

		if err := StartAPIServer(scraper, config, port); err != nil {
			fmt.Printf("❌ Failed to start API server: %v\n", err)
			os.Exit(1)
		}
	} else {
		// CLI mode - run the demo scraping
		fmt.Println("🚀 Starting Enhanced Concurrent Web Scraper in Go!")
		fmt.Printf("Configuration: %s\n", config.String())

		results := runScrapingLogic(scraper, config)
		processAndSaveResults(scraper, config, results)
		printCompletionSummary(config)
	}
}

// setupConfig parses command-line flags and loads configuration
func setupConfig() *Config {
	// Parse command-line flags
	var (
		maxConcurrent  = flag.Int("concurrent", 3, "Maximum concurrent requests")
		requestTimeout = flag.Duration("timeout", 10*time.Second, "Request timeout")
		totalTimeout   = flag.Duration("total-timeout", 30*time.Second, "Total timeout for all requests")
		outputFile     = flag.String("output", "scraping_results.json", "Output file for results")
		retryAttempts  = flag.Int("retries", 3, "Number of retry attempts")
		retryDelay     = flag.Duration("retry-delay", 1*time.Second, "Delay between retries")
		logLevel       = flag.String("log-level", "info", "Log level (debug, info, warn, error)")
		enableMetrics  = flag.Bool("metrics", true, "Enable metrics collection")
		enableLogging  = flag.Bool("logging", true, "Enable logging")
		userAgent      = flag.String("user-agent", "Go-Scraper/2.0", "User-Agent string")
		useHeadless    = flag.Bool("headless", false, "Use headless browser for JavaScript-rendered sites")
		maxPages       = flag.Int("max-pages", 10, "Maximum pages to scrape for pagination")
		_              = flag.String("site", "", "Single site URL to scrape with pagination")
		storageBackend = flag.String("storage", "json", "Storage backend (json, memory)")
		enablePlugins  = flag.Bool("plugins", true, "Enable data processing plugins")
		_              = flag.Int("api-port", 0, "Start API server on port (0 = disabled)")
	)
	flag.Parse()

	// Load configuration
	config := LoadConfig()

	// Override with command-line flags
	config.MaxConcurrent = *maxConcurrent
	config.RequestTimeout = *requestTimeout
	config.TotalTimeout = *totalTimeout
	config.OutputFile = *outputFile
	config.RetryAttempts = *retryAttempts
	config.RetryDelay = *retryDelay
	config.LogLevel = *logLevel
	config.EnableMetrics = *enableMetrics
	config.EnableLogging = *enableLogging
	config.UserAgent = *userAgent
	config.UseHeadless = *useHeadless
	config.MaxPages = *maxPages
	config.StorageBackend = *storageBackend
	config.EnablePlugins = *enablePlugins

	// Validate configuration
	if err := config.Validate(); err != nil {
		fmt.Printf("❌ Configuration error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("🚀 Starting Enhanced Concurrent Web Scraper in Go!")
	fmt.Printf("Configuration: %s\n", config.String())

	return config
}

// runScrapingLogic executes the main scraping operation
func runScrapingLogic(scraper *Scraper, _ *Config) []ScrapedData {
	start := time.Now()

	// Check if we're scraping a single site with pagination
	siteURL := flag.Lookup("site").Value.String()
	if siteURL != "" {
		fmt.Printf("🌐 Scraping site with pagination: %s\n", siteURL)
		results := scraper.ScrapeSite(siteURL)
		fmt.Printf("\n⏱️  Total time: %v\n", time.Since(start))
		return results
	}

	// URLs to scrape - mix of HTML and JSON APIs
	urls := []string{
		"https://golang.org",                           // HTML with title
		"https://httpbin.org/get",                      // JSON API
		"https://jsonplaceholder.typicode.com/posts/1", // JSON API
		"https://api.github.com/users/golang",          // JSON API
		"https://httpbin.org/status/404",               // Error response
		"https://httpbin.org/delay/2",                  // Delayed response
		"https://httpbin.org/status/500",               // Server error (retryable)
		"https://httpbin.org/status/429",               // Rate limit (retryable)
	}
	fmt.Printf("Scraping %d URLs with rate limiting...\n", len(urls))
	results := scraper.ScrapeURLs(urls)

	fmt.Printf("\n⏱️  Total time: %v\n", time.Since(start))
	return results
}

// processAndSaveResults handles result processing, display, and file export
func processAndSaveResults(scraper *Scraper, config *Config, results []ScrapedData) {
	// Process and display results
	processor := &ResultProcessor{}
	processor.ProcessResults(results)

	// Print metrics if enabled
	if config.EnableMetrics {
		scraper.metrics.PrintSummary()
		printCircuitBreakerStats(scraper)
	}

	// Export to JSON
	fmt.Println("\n📄 Exporting results to JSON...")
	if err := processor.ExportToJSON(results, config.OutputFile); err != nil {
		fmt.Printf("❌ Failed to export results: %v\n", err)
	}

	// Export metrics if enabled
	if config.EnableMetrics {
		exportMetrics(scraper)
	}
}

// printCircuitBreakerStats displays circuit breaker statistics
func printCircuitBreakerStats(scraper *Scraper) {
	fmt.Printf("\n🔌 Circuit Breaker Statistics:\n")
	fmt.Printf("===========================\n")
	for domain, cb := range scraper.circuitBreakers {
		stats := cb.GetStats()
		fmt.Printf("🌐 %s: %s (%.1f%% failure rate)\n",
			domain,
			stats["state"],
			stats["failure_rate"])
	}
}

// exportMetrics saves metrics to a JSON file
func exportMetrics(scraper *Scraper) {
	metricsFile := "scraping_metrics.json"
	metricsData, err := json.MarshalIndent(scraper.metrics.GetMetrics(), "", "  ")
	if err == nil {
		if err := os.WriteFile(metricsFile, metricsData, 0644); err == nil {
			fmt.Printf("✅ Metrics saved to %s\n", metricsFile)
		}
	}
}

// printCompletionSummary displays the completion summary
func printCompletionSummary(config *Config) {
	fmt.Println("\n✨ Enhanced scraping complete! This demonstrates:")
	fmt.Println("   • Advanced configuration management with environment variables")
	fmt.Println("   • Structured logging with multiple levels")
	fmt.Println("   • Comprehensive metrics collection and analysis")
	fmt.Println("   • Retry logic with exponential backoff")
	fmt.Println("   • Custom error types and error categorization")
	fmt.Println("   • Domain-specific rate limiting")
	fmt.Println("   • Command-line flag parsing")
	fmt.Println("   • Goroutines for concurrent execution")
	fmt.Println("   • Channels for communication and rate limiting")
	fmt.Println("   • Context for timeout and cancellation")
	fmt.Println("   • Error handling with custom error types")
	fmt.Println("   • Structs, methods, and interfaces")
	fmt.Println("   • JSON marshaling and parsing")
	fmt.Println("   • HTTP client usage with proper headers")
	fmt.Println("   • Content-type detection and parsing")
	if config.UseHeadless {
		fmt.Println("   • Headless browser support for JavaScript-rendered sites")
		fmt.Println("   • Pagination support for multi-page scraping")
	}
}

// GetMetrics returns the metrics from the scraper
func (s *Scraper) GetMetrics() interface{} {
	return s.metrics.GetMetrics()
}
