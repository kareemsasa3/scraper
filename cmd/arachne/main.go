package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/kareemsasa3/arachne/internal/api"
	"github.com/kareemsasa3/arachne/internal/config"
	"github.com/kareemsasa3/arachne/internal/processor"
	"github.com/kareemsasa3/arachne/internal/scraper"
	"github.com/kareemsasa3/arachne/internal/types"
)

func main() {
	// Setup configuration
	cfg := setupConfig()

	// Create scraper
	s := scraper.NewScraper(cfg)

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
		fmt.Printf("Configuration: %s\n", cfg.String())

		if err := api.StartAPIServer(s, cfg, port); err != nil {
			log.Fatalf("Failed to start API server: %v", err)
		}
	} else {
		// CLI mode - run the demo scraping
		fmt.Println("🚀 Starting Enhanced Concurrent Web Scraper in Go!")
		fmt.Printf("Configuration: %s\n", cfg.String())

		results := runScrapingLogic(s, cfg)
		processAndSaveResults(s, cfg, results)
		printCompletionSummary(cfg)
	}
}

// setupConfig parses command-line flags and loads configuration
func setupConfig() *config.Config {
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
	cfg := config.LoadConfig()

	// Override with command-line flags (only if explicitly set)
	cfg.MaxConcurrent = *maxConcurrent
	cfg.RequestTimeout = *requestTimeout
	cfg.TotalTimeout = *totalTimeout
	cfg.OutputFile = *outputFile
	cfg.RetryAttempts = *retryAttempts
	cfg.RetryDelay = *retryDelay
	cfg.LogLevel = *logLevel
	cfg.EnableMetrics = *enableMetrics
	cfg.EnableLogging = *enableLogging
	cfg.UserAgent = *userAgent
	// Only override UseHeadless if the headless flag was explicitly provided
	if len(os.Args) > 1 {
		for _, arg := range os.Args[1:] {
			if strings.HasPrefix(arg, "-headless") {
				cfg.UseHeadless = *useHeadless
				break
			}
		}
	}
	cfg.MaxPages = *maxPages
	cfg.StorageBackend = *storageBackend
	cfg.EnablePlugins = *enablePlugins

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		log.Fatalf("Configuration error: %v", err)
	}

	return cfg
}

// runScrapingLogic executes the main scraping operation
func runScrapingLogic(s *scraper.Scraper, _ *config.Config) []types.ScrapedData {
	start := time.Now()

	// Check if we're scraping a single site with pagination
	siteURL := flag.Lookup("site").Value.String()
	if siteURL != "" {
		fmt.Printf("🌐 Scraping site with pagination: %s\n", siteURL)
		results := s.ScrapeSite(siteURL)
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
	results := s.ScrapeURLs(urls)

	fmt.Printf("\n⏱️  Total time: %v\n", time.Since(start))
	return results
}

// processAndSaveResults handles result processing, display, and file export
func processAndSaveResults(s *scraper.Scraper, cfg *config.Config, results []types.ScrapedData) {
	// Process and display results
	proc := &processor.ResultProcessor{}
	proc.ProcessResults(results)

	// Print metrics if enabled
	if cfg.EnableMetrics {
		// TODO: Add metrics printing functionality
		printCircuitBreakerStats(s)
	}

	// Export to JSON
	fmt.Println("\n📄 Exporting results to JSON...")
	if err := proc.ExportToJSON(results, cfg.OutputFile); err != nil {
		fmt.Printf("❌ Failed to export results: %v\n", err)
	}

	// Export metrics if enabled
	if cfg.EnableMetrics {
		exportMetrics(s)
	}
}

// printCircuitBreakerStats displays circuit breaker statistics
func printCircuitBreakerStats(s *scraper.Scraper) {
	fmt.Printf("\n🔌 Circuit Breaker Statistics:\n")
	fmt.Printf("===========================\n")
	stats := s.GetCircuitBreakerStats()
	for domain, cbStats := range stats {
		failureRate := cbStats["failure_rate"].(float64)
		state := cbStats["state"].(string)
		totalRequests := cbStats["total_requests"].(int64)
		totalFailures := cbStats["total_failures"].(int64)

		fmt.Printf("🌐 %s:\n", domain)
		fmt.Printf("   State: %s\n", state)
		fmt.Printf("   Total Requests: %d\n", totalRequests)
		fmt.Printf("   Total Failures: %d\n", totalFailures)
		fmt.Printf("   Failure Rate: %.1f%%\n", failureRate)
		fmt.Println()
	}
}

// exportMetrics saves metrics to a JSON file
func exportMetrics(s *scraper.Scraper) {
	metricsFile := "scraping_metrics.json"
	metricsData, err := json.MarshalIndent(s.GetMetrics(), "", "  ")
	if err != nil {
		fmt.Printf("❌ Failed to marshal metrics: %v\n", err)
		return
	}

	if err := os.WriteFile(metricsFile, metricsData, 0644); err != nil {
		fmt.Printf("❌ Failed to write metrics file: %v\n", err)
		return
	}

	fmt.Printf("✅ Metrics saved to %s\n", metricsFile)
}

// printCompletionSummary displays the completion summary
func printCompletionSummary(cfg *config.Config) {
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
	if cfg.UseHeadless {
		fmt.Println("   • Headless browser support for JavaScript-rendered sites")
		fmt.Println("   • Pagination support for multi-page scraping")
	}
}
