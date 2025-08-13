package strategy

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/chromedp"

	"github.com/kareemsasa3/arachne/internal/config"
	"github.com/kareemsasa3/arachne/internal/errors"
)

// HeadlessStrategy implements scraping using headless Chrome browser
type HeadlessStrategy struct{}

// NewHeadlessStrategy creates a new headless browser strategy
func NewHeadlessStrategy() *HeadlessStrategy {
	return &HeadlessStrategy{}
}

// Execute performs headless browser-based scraping
func (s *HeadlessStrategy) Execute(ctx context.Context, urlStr string, cfg *config.Config) (*ScrapedResult, error) {
	// Create a new chromedp context from the parent context with timeout
	taskCtx, cancel := context.WithTimeout(ctx, cfg.RequestTimeout)
	defer cancel()

	// Create chromedp context with options; security-related flags are configurable
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		// Create a temporary, disposable user profile for this scrape
		chromedp.Flag("user-data-dir", os.TempDir()+"/go-scraper-profile"),
		// --- Best Practice Flags for a Clean Run ---
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
	)

	// Only enable no-sandbox when explicitly configured (not recommended in prod)
	if cfg.HeadlessNoSandbox {
		opts = append(opts, chromedp.Flag("no-sandbox", true))
	}

	// Only ignore SSL errors when explicitly configured
	if cfg.HeadlessIgnoreCertErrors {
		opts = append(opts,
			chromedp.Flag("ignore-certificate-errors", true),
			chromedp.Flag("ignore-ssl-errors", true),
		)
	}

	allocCtx, cancel := chromedp.NewExecAllocator(taskCtx, opts...)
	defer cancel()

	chromeCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	var title string
	var body string
	var nextURL string

	// Define the sequence of actions the browser will perform
	err := chromedp.Run(chromeCtx,
		// Navigate to the URL
		chromedp.Navigate(urlStr),

		// Wait for the page to load (wait for body to be ready)
		chromedp.WaitReady("body", chromedp.ByQuery),

		// Wait a bit for JavaScript to execute
		chromedp.Sleep(3*time.Second),

		// Extract the page title
		chromedp.Title(&title),

		// Extract the full HTML body
		chromedp.OuterHTML("html", &body),
	)

	if err != nil {
		return nil, errors.NewScraperError(urlStr, "Headless execution failed", err)
	}

	// Use goquery to parse the HTML and extract content robustly
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		return nil, errors.NewScraperError(urlStr, "Failed to parse HTML", err)
	}

	// Extract next URL using CSS selector
	if nextElement := doc.Find("li.next a"); nextElement.Length() > 0 {
		if href, exists := nextElement.Attr("href"); exists {
			nextURL = href
		}
	}

	// If we found a next URL, make it absolute
	if nextURL != "" {
		baseURL, _ := url.Parse(urlStr)
		if nextURLRef, err := url.Parse(nextURL); err == nil {
			nextURL = baseURL.ResolveReference(nextURLRef).String()
		}
	}

	// Extract a meaningful title from the content if the page title is generic
	if title == "" || strings.Contains(strings.ToLower(title), "quotes") {
		title = s.extractTitleFromContent(doc)
	}

	return &ScrapedResult{
		Title:      title,
		Body:       body,
		StatusCode: 200, // Chromedp doesn't easily expose status, 200 is safe on success
		NextURL:    nextURL,
	}, nil
}

// extractTitleFromContent extracts a meaningful title from the HTML content using goquery
func (s *HeadlessStrategy) extractTitleFromContent(doc *goquery.Document) string {
	// For quotes.toscrape.com, try to extract the first quote as title
	// Use CSS selector to find quote text
	if quoteElement := doc.Find(".text"); quoteElement.Length() > 0 {
		quote := strings.TrimSpace(quoteElement.First().Text())
		if len(quote) > 0 {
			// Limit length for title
			if len(quote) > 100 {
				quote = quote[:97] + "..."
			}
			return fmt.Sprintf("Quotes - %s", quote)
		}
	}

	// Fallback: try to find any meaningful heading
	if h1 := doc.Find("h1").First(); h1.Length() > 0 {
		title := strings.TrimSpace(h1.Text())
		if len(title) > 0 {
			return title
		}
	}

	return "JavaScript-rendered page"
}
