package strategy

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"github.com/kareemsasa3/arachne/internal/config"
	"github.com/kareemsasa3/arachne/internal/errors"
	"github.com/kareemsasa3/arachne/internal/types"
)

// debugLog logs a message with timestamp and URL context for headless debugging
func debugLog(urlStr string, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	log.Printf("[HEADLESS DEBUG] [%s] %s", urlStr, msg)
}

// HeadlessStrategy implements scraping using headless Chrome browser
type HeadlessStrategy struct{}

// NewHeadlessStrategy creates a new headless browser strategy
func NewHeadlessStrategy() *HeadlessStrategy {
	return &HeadlessStrategy{}
}

// Execute performs headless browser-based scraping
func (s *HeadlessStrategy) Execute(ctx context.Context, urlStr string, cfg *config.Config) (*ScrapedResult, error) {
	startTime := time.Now()
	debugLog(urlStr, "▶ Execute() started | RequestTimeout=%v | ParentCtxErr=%v", cfg.RequestTimeout, ctx.Err())

	// Check if parent context is already canceled
	if ctx.Err() != nil {
		debugLog(urlStr, "✗ Parent context already canceled before starting: %v", ctx.Err())
		return nil, errors.NewScraperError(urlStr, "Context canceled before execution", ctx.Err())
	}

	// Create a new chromedp context from the parent context with timeout
	taskCtx, taskCancel := context.WithTimeout(ctx, cfg.RequestTimeout)
	defer taskCancel()
	debugLog(urlStr, "  Created task context with timeout=%v | elapsed=%v", cfg.RequestTimeout, time.Since(startTime))

	// Create a unique temporary profile directory for this Chrome instance
	// This prevents "Failed to create SingletonLock" errors when running concurrent scrapes
	profileDir, err := os.MkdirTemp("", "chrome-profile-*")
	if err != nil {
		debugLog(urlStr, "✗ Failed to create temp profile: %v | elapsed=%v", err, time.Since(startTime))
		return nil, errors.NewScraperError(urlStr, "Failed to create temp profile directory", err)
	}
	// Clean up the temporary profile directory after scraping completes
	defer os.RemoveAll(profileDir)
	debugLog(urlStr, "  Created temp profile dir: %s | elapsed=%v", profileDir, time.Since(startTime))

	// Create chromedp context with options; security-related flags are configurable
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		// Use the unique temporary profile directory for this scrape
		chromedp.Flag("user-data-dir", profileDir),
		// --- Best Practice Flags for a Clean Run ---
		chromedp.Flag("headless", "new"),                                // Use new headless mode (Chrome 112+)
		chromedp.Flag("disable-blink-features", "AutomationControlled"), // Minimal stealth
		chromedp.Flag("window-size", "1920,1080"),                       // Realistic desktop viewport
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
	)

	// Check for Chrome binary in common locations (especially for Alpine/container environments)
	chromePaths := []string{
		"/usr/bin/chromium-browser", // Alpine Linux
		"/usr/bin/chromium",         // Some Linux distros
		"/usr/bin/google-chrome",    // Google Chrome
		"/usr/bin/google-chrome-stable",
	}
	foundContainerChrome := false
	for _, p := range chromePaths {
		if _, err := os.Stat(p); err == nil {
			opts = append(opts, chromedp.ExecPath(p))
			debugLog(urlStr, "  Found Chrome at: %s", p)
			// If Chrome is found in container-typical paths, add container-friendly flags
			if strings.Contains(p, "chromium") {
				foundContainerChrome = true
			}
			break
		}
	}

	// Always add container-friendly flags when using Chromium (common in Docker)
	if foundContainerChrome {
		opts = append(opts,
			chromedp.Flag("disable-dev-shm-usage", true),              // Use /tmp instead of /dev/shm
			chromedp.Flag("disable-software-rasterizer", true),        // Disable software GPU
			chromedp.Flag("disable-gpu-sandbox", true),                // Disable GPU sandbox
			chromedp.Flag("disable-gl-drawing-for-tests", true),       // Disable GL drawing
			chromedp.Flag("use-gl", "angle"),                          // Use ANGLE for GL
			chromedp.Flag("use-angle", "swiftshader"),                 // Use SwiftShader (software)
			chromedp.Flag("disable-features", "VizDisplayCompositor"), // Disable Viz
		)
		debugLog(urlStr, "  Added container-friendly flags (GPU disabled)")
	}

	// Only enable no-sandbox when explicitly configured (not recommended in prod)
	if cfg.HeadlessNoSandbox {
		opts = append(opts, chromedp.Flag("no-sandbox", true))
		opts = append(opts, chromedp.Flag("disable-setuid-sandbox", true))
		// Additional stability flags commonly used in containerized Chrome
		opts = append(opts,
			chromedp.Flag("disable-dev-shm-usage", true),
			chromedp.Flag("no-zygote", true),
			chromedp.Flag("single-process", true),
		)
		debugLog(urlStr, "  Sandbox disabled (container mode)")
	}

	// Only ignore SSL errors when explicitly configured
	if cfg.HeadlessIgnoreCertErrors {
		opts = append(opts,
			chromedp.Flag("ignore-certificate-errors", true),
			chromedp.Flag("ignore-ssl-errors", true),
		)
		debugLog(urlStr, "  SSL certificate errors ignored")
	}

	// Check context before creating allocator
	if taskCtx.Err() != nil {
		debugLog(urlStr, "✗ Context canceled before allocator creation: %v | elapsed=%v", taskCtx.Err(), time.Since(startTime))
		return nil, errors.NewScraperError(urlStr, "Context canceled before Chrome allocator", taskCtx.Err())
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(taskCtx, opts...)
	defer allocCancel()
	debugLog(urlStr, "  Created Chrome allocator context | elapsed=%v", time.Since(startTime))

	// Check context before creating browser context
	if allocCtx.Err() != nil {
		debugLog(urlStr, "✗ Context canceled before browser creation: %v | elapsed=%v", allocCtx.Err(), time.Since(startTime))
		return nil, errors.NewScraperError(urlStr, "Context canceled before Chrome browser", allocCtx.Err())
	}

	// Set explicit dial timeout to match our RequestTimeout
	// Also add logging to capture Chrome startup errors
	chromeCtx, chromeCancel := chromedp.NewContext(allocCtx,
		chromedp.WithBrowserOption(chromedp.WithDialTimeout(cfg.RequestTimeout)),
		chromedp.WithLogf(func(format string, args ...interface{}) {
			debugLog(urlStr, "  [chromedp LOG] "+format, args...)
		}),
		chromedp.WithErrorf(func(format string, args ...interface{}) {
			debugLog(urlStr, "  [chromedp ERR] "+format, args...)
		}),
	)
	defer chromeCancel()
	debugLog(urlStr, "  Created Chrome browser context (dialTimeout=%v) | elapsed=%v", cfg.RequestTimeout, time.Since(startTime))

	// Start a context watcher to detect exactly when/why cancellation happens
	go func() {
		select {
		case <-chromeCtx.Done():
			debugLog(urlStr, "  ⚠ CONTEXT WATCHER: chromeCtx canceled! Err=%v | elapsed=%v", chromeCtx.Err(), time.Since(startTime))
		case <-taskCtx.Done():
			debugLog(urlStr, "  ⚠ CONTEXT WATCHER: taskCtx canceled! Err=%v | elapsed=%v", taskCtx.Err(), time.Since(startTime))
		case <-ctx.Done():
			debugLog(urlStr, "  ⚠ CONTEXT WATCHER: parent ctx canceled! Err=%v | elapsed=%v", ctx.Err(), time.Since(startTime))
		}
	}()

	// Step 0: Force Chrome to start by running a no-op action
	// This separates Chrome startup time from navigation time in the logs
	debugLog(urlStr, "  → Step 0: Starting Chrome (first Run triggers browser start)... | elapsed=%v", time.Since(startTime))
	if err := chromedp.Run(chromeCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		debugLog(urlStr, "    Chrome started, inside first ActionFunc | elapsed=%v", time.Since(startTime))
		return nil
	})); err != nil {
		debugLog(urlStr, "✗ Chrome startup FAILED: %v | ctxErr=%v | elapsed=%v", err, chromeCtx.Err(), time.Since(startTime))
		// Attempt a quick screenshot for debugging what Chrome is showing
		screenshotPath := fmt.Sprintf("/tmp/chrome-debug-%d.png", time.Now().Unix())
		var buf []byte
		if shotErr := chromedp.Run(chromeCtx, chromedp.CaptureScreenshot(&buf)); shotErr == nil {
			if saveErr := os.WriteFile(screenshotPath, buf, 0644); saveErr == nil {
				debugLog(urlStr, "📸 Saved debug screenshot to: %s", screenshotPath)
			}
		} else {
			debugLog(urlStr, "⚠ Could not capture screenshot: %v", shotErr)
		}
		return nil, errors.NewScraperError(urlStr, "Chrome startup failed", err)
	}
	debugLog(urlStr, "  ✓ Step 0: Chrome started and ready | elapsed=%v", time.Since(startTime))

	var title string
	var body string
	var nextURL string

	// Calculate reasonable wait times based on the configured timeout
	// Use 5% of total timeout for initial JS wait, capped at 2s
	initialWait := cfg.RequestTimeout / 20
	if initialWait > 2*time.Second {
		initialWait = 2 * time.Second
	} else if initialWait < 500*time.Millisecond {
		initialWait = 500 * time.Millisecond
	}

	// Use 3% of total timeout for scroll delay, capped at 1s
	finalWait := cfg.RequestTimeout / 33
	if finalWait > 1*time.Second {
		finalWait = 1 * time.Second
	} else if finalWait < 300*time.Millisecond {
		finalWait = 300 * time.Millisecond
	}

	// Scroll delay per iteration: use 1% of timeout, capped at 500ms
	scrollDelay := cfg.RequestTimeout / 100
	if scrollDelay > 500*time.Millisecond {
		scrollDelay = 500 * time.Millisecond
	} else if scrollDelay < 100*time.Millisecond {
		scrollDelay = 100 * time.Millisecond
	}

	debugLog(urlStr, "  Adaptive waits: initialWait=%v, scrollDelay=%v, finalWait=%v | elapsed=%v",
		initialWait, scrollDelay, finalWait, time.Since(startTime))

	// Step 1: Navigate to URL
	// NOTE: We use raw CDP page.Navigate instead of chromedp.Navigate because
	// chromedp.Navigate has an internal page load timeout (~5s) that's separate
	// from our context timeout. By using the raw CDP command, we respect only
	// our RequestTimeout from the context.
	debugLog(urlStr, "  → Step 1: Navigate (raw CDP) starting... | ctxErr=%v | elapsed=%v", chromeCtx.Err(), time.Since(startTime))
	if err := chromedp.Run(chromeCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		debugLog(urlStr, "    Inside Navigate ActionFunc, calling page.Navigate... | ctxErr=%v | elapsed=%v", ctx.Err(), time.Since(startTime))
		_, _, errorText, err := page.Navigate(urlStr).Do(ctx)
		if err != nil {
			debugLog(urlStr, "    page.Navigate returned error: %v | ctxErr=%v | elapsed=%v", err, ctx.Err(), time.Since(startTime))
			return err
		}
		if errorText != "" {
			debugLog(urlStr, "    page.Navigate errorText: %s | elapsed=%v", errorText, time.Since(startTime))
			return fmt.Errorf("navigation error: %s", errorText)
		}
		debugLog(urlStr, "    page.Navigate succeeded | elapsed=%v", time.Since(startTime))
		return nil
	})); err != nil {
		debugLog(urlStr, "✗ Navigate FAILED: %v | ctxErr=%v | taskCtxErr=%v | parentCtxErr=%v | elapsed=%v",
			err, chromeCtx.Err(), taskCtx.Err(), ctx.Err(), time.Since(startTime))
		// Attempt a quick screenshot for debugging what Chrome is showing
		screenshotPath := fmt.Sprintf("/tmp/chrome-debug-%d.png", time.Now().Unix())
		var buf []byte
		if shotErr := chromedp.Run(chromeCtx, chromedp.CaptureScreenshot(&buf)); shotErr == nil {
			if saveErr := os.WriteFile(screenshotPath, buf, 0644); saveErr == nil {
				debugLog(urlStr, "📸 Saved debug screenshot to: %s", screenshotPath)
			}
		} else {
			debugLog(urlStr, "⚠ Could not capture screenshot: %v", shotErr)
		}
		return nil, errors.NewScraperError(urlStr, "Headless navigation failed", err)
	}
	debugLog(urlStr, "  ✓ Step 1: Navigate completed | elapsed=%v", time.Since(startTime))

	// Step 2: Wait for body to be ready
	debugLog(urlStr, "  → Step 2: WaitReady(body) starting... | elapsed=%v", time.Since(startTime))
	if err := chromedp.Run(chromeCtx, chromedp.WaitReady("body", chromedp.ByQuery)); err != nil {
		debugLog(urlStr, "✗ WaitReady FAILED: %v | elapsed=%v", err, time.Since(startTime))
		return nil, errors.NewScraperError(urlStr, "Headless wait for body failed", err)
	}
	debugLog(urlStr, "  ✓ Step 2: WaitReady(body) completed | elapsed=%v", time.Since(startTime))

	// Step 3: Initial JS wait
	debugLog(urlStr, "  → Step 3: Initial JS wait (%v) starting... | elapsed=%v", initialWait, time.Since(startTime))
	if err := chromedp.Run(chromeCtx, chromedp.Sleep(initialWait)); err != nil {
		debugLog(urlStr, "✗ Initial wait FAILED: %v | elapsed=%v", err, time.Since(startTime))
		return nil, errors.NewScraperError(urlStr, "Headless initial wait failed", err)
	}
	debugLog(urlStr, "  ✓ Step 3: Initial JS wait completed | elapsed=%v", time.Since(startTime))

	// Step 4: Scroll to trigger lazy loading
	debugLog(urlStr, "  → Step 4: Scroll loop (4 iterations × %v) starting... | elapsed=%v", scrollDelay, time.Since(startTime))
	err = chromedp.Run(chromeCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		for i := 0; i < 4; i++ {
			if ctx.Err() != nil {
				debugLog(urlStr, "  ✗ Scroll iteration %d: context canceled: %v", i+1, ctx.Err())
				return ctx.Err()
			}
			_ = chromedp.Evaluate(`window.scrollTo(0, document.body.scrollHeight);`, nil).Do(ctx)
			debugLog(urlStr, "    Scroll iteration %d completed, sleeping %v... | elapsed=%v", i+1, scrollDelay, time.Since(startTime))
			time.Sleep(scrollDelay)
		}
		return nil
	}))
	if err != nil {
		debugLog(urlStr, "✗ Scroll loop FAILED: %v | elapsed=%v", err, time.Since(startTime))
		return nil, errors.NewScraperError(urlStr, "Headless scroll loop failed", err)
	}
	debugLog(urlStr, "  ✓ Step 4: Scroll loop completed | elapsed=%v", time.Since(startTime))

	// Step 5: Final wait
	debugLog(urlStr, "  → Step 5: Final wait (%v) starting... | elapsed=%v", finalWait, time.Since(startTime))
	if err := chromedp.Run(chromeCtx, chromedp.Sleep(finalWait)); err != nil {
		debugLog(urlStr, "✗ Final wait FAILED: %v | elapsed=%v", err, time.Since(startTime))
		return nil, errors.NewScraperError(urlStr, "Headless final wait failed", err)
	}
	debugLog(urlStr, "  ✓ Step 5: Final wait completed | elapsed=%v", time.Since(startTime))

	// Step 6: Extract title
	debugLog(urlStr, "  → Step 6: Extract title starting... | elapsed=%v", time.Since(startTime))
	if err := chromedp.Run(chromeCtx, chromedp.Title(&title)); err != nil {
		debugLog(urlStr, "✗ Title extraction FAILED: %v | elapsed=%v", err, time.Since(startTime))
		return nil, errors.NewScraperError(urlStr, "Headless title extraction failed", err)
	}
	debugLog(urlStr, "  ✓ Step 6: Title extracted: %q | elapsed=%v", title, time.Since(startTime))

	// Step 7: Extract HTML body
	debugLog(urlStr, "  → Step 7: Extract HTML body starting... | elapsed=%v", time.Since(startTime))
	if err := chromedp.Run(chromeCtx, chromedp.OuterHTML("html", &body)); err != nil {
		debugLog(urlStr, "✗ HTML extraction FAILED: %v | elapsed=%v", err, time.Since(startTime))
		return nil, errors.NewScraperError(urlStr, "Headless HTML extraction failed", err)
	}
	debugLog(urlStr, "  ✓ Step 7: HTML extracted (%d bytes) | elapsed=%v", len(body), time.Since(startTime))

	// Step 8: Parse HTML with goquery
	debugLog(urlStr, "  → Step 8: Parse HTML with goquery starting... | elapsed=%v", time.Since(startTime))
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		debugLog(urlStr, "✗ HTML parsing FAILED: %v | elapsed=%v", err, time.Since(startTime))
		return nil, errors.NewScraperError(urlStr, "Failed to parse HTML", err)
	}
	debugLog(urlStr, "  ✓ Step 8: HTML parsed | elapsed=%v", time.Since(startTime))

	// Try to enrich title from Open Graph tags (company/site_name + title)
	if ogTitle, exists := doc.Find("meta[property='og:title']").Attr("content"); exists {
		ogTitle = strings.TrimSpace(ogTitle)
		if len(ogTitle) > 0 {
			if siteName, ok := doc.Find("meta[property='og:site_name']").Attr("content"); ok {
				siteName = strings.TrimSpace(siteName)
				if len(siteName) > 0 {
					title = fmt.Sprintf("%s - %s", siteName, ogTitle)
				} else {
					title = ogTitle
				}
			} else {
				title = ogTitle
			}
		}
	}

	// Extract next URL using flexible pagination config
	nextURL = s.extractNextURL(ctx, doc, urlStr)
	debugLog(urlStr, "  Extracted nextURL: %q | elapsed=%v", nextURL, time.Since(startTime))

	// Extract a meaningful title from the content if the page title is generic
	if title == "" || strings.Contains(strings.ToLower(title), "quotes") {
		title = s.extractTitleFromContent(doc)
	}

	debugLog(urlStr, "◼ Execute() completed successfully | title=%q | bodySize=%d | nextURL=%q | totalElapsed=%v",
		title, len(body), nextURL, time.Since(startTime))

	return &ScrapedResult{
		Title:      title,
		Body:       body,
		StatusCode: 200, // Chromedp doesn't easily expose status, 200 is safe on success
		NextURL:    nextURL,
	}, nil
}

// extractNextURL extracts the next page URL using flexible pagination configuration
func (s *HeadlessStrategy) extractNextURL(ctx context.Context, doc *goquery.Document, baseURLStr string) string {
	// Get pagination config from context, or use defaults
	var paginationCfg *types.PaginationConfig
	if cfgValue := ctx.Value(types.PaginationConfigKey); cfgValue != nil {
		paginationCfg = cfgValue.(*types.PaginationConfig)
	}

	// If no config provided, use defaults
	if paginationCfg == nil {
		paginationCfg = types.GetDefaultPaginationConfig()
	}

	// Determine which attribute to extract
	attribute := paginationCfg.Attribute
	if attribute == "" {
		attribute = "href" // Default to href
	}

	// Try each selector in order until one matches
	var nextURL string
	for _, selector := range paginationCfg.Selectors {
		if nextElement := doc.Find(selector); nextElement.Length() > 0 {
			if attrValue, exists := nextElement.First().Attr(attribute); exists {
				nextURL = strings.TrimSpace(attrValue)
				if nextURL != "" {
					// Validate against regex pattern if provided
					if paginationCfg.ValidationPattern != "" {
						matched, err := regexp.MatchString(paginationCfg.ValidationPattern, nextURL)
						if err == nil && !matched {
							// Pattern didn't match, try next selector
							nextURL = ""
							continue
						}
					}
					// Found a valid next URL
					break
				}
			}
		}
	}

	// If we found a next URL, make it absolute
	if nextURL != "" {
		baseURL, _ := url.Parse(baseURLStr)
		if nextURLRef, err := url.Parse(nextURL); err == nil {
			nextURL = baseURL.ResolveReference(nextURLRef).String()
		}
	}

	return nextURL
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
