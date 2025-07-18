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
