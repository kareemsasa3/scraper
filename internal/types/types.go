package types

import "time"

// ScrapedData represents the data we extract from websites
type ScrapedData struct {
	URL     string    `json:"url"`
	Title   string    `json:"title"`
	Status  int       `json:"status"`
	Size    int       `json:"size"`
	Error   string    `json:"error,omitempty"`
	Scraped time.Time `json:"scraped"`
	NextURL string    `json:"next_url,omitempty"`
	Content string    `json:"content,omitempty"` // Full HTML/JSON content
}

// ProgressStats captures scrape progress as discovered vs. completed counts.
// Discovered should reflect the total known pages/URLs (queued + completed).
// Completed is the number of pages that finished scraping (success or error).
type ProgressStats struct {
	Completed  int `json:"completed"`
	Discovered int `json:"discovered"`
}

// PaginationConfig defines flexible pagination extraction rules
type PaginationConfig struct {
	// Selectors is an ordered list of CSS selectors to try for finding the next page link
	// The first selector that matches will be used
	Selectors []string `json:"selectors,omitempty"`

	// Attribute is the HTML attribute to extract from the matched element (default: "href")
	Attribute string `json:"attribute,omitempty"`

	// ValidationPattern is an optional regex pattern to validate the extracted URL
	// If provided, only URLs matching this pattern will be considered valid
	ValidationPattern string `json:"validation_pattern,omitempty"`
}

// GetDefaultPaginationConfig returns a configuration with common pagination selectors
func GetDefaultPaginationConfig() *PaginationConfig {
	return &PaginationConfig{
		Selectors: []string{
			"li.next a",            // Original Arachne selector
			"a.next",               // Common "next" class
			"a.next_page",          // Common "next_page" class
			"a.morelink",           // Reddit-style "more" links
			"button.next",          // Button-based navigation
			"a[rel='next']",        // Semantic HTML rel attribute
			".pagination .next a",  // Bootstrap-style pagination
			"a.nextpostslink",      // WordPress default
			".nav-next a",          // WordPress alternative
			"a[aria-label='Next']", // Accessible pagination
		},
		Attribute: "href",
	}
}

// PaginationConfigKey is the context key for pagination configuration
type contextKey string

const PaginationConfigKey contextKey = "pagination_config"
