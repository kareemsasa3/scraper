# Flexible Pagination in Arachne

Arachne now supports flexible, configurable pagination that allows you to scrape multi-page websites with custom CSS selectors and validation rules.

## Overview

Previously, Arachne was hardcoded to look for pagination links using a single CSS selector (`li.next a`). The new pagination system allows you to:

- Specify multiple CSS selectors to try in order
- Extract different HTML attributes (not just `href`)
- Validate extracted URLs with regex patterns
- Use sensible defaults that cover common pagination patterns

## Backward Compatibility

**The changes are fully backward compatible.** Existing scraping requests will continue to work without any modifications. If you don't specify a `pagination_config`, Arachne will automatically use a comprehensive set of default selectors that cover most common pagination patterns.

## API Usage

### Basic Usage (Using Defaults)

The simplest way to use pagination is to just specify a `site_url` - Arachne will automatically try common pagination patterns:

```json
{
  "site_url": "https://example.com/page/1"
}
```

### Custom Pagination Configuration

For sites with unique pagination structures, you can provide a custom configuration:

```json
{
  "site_url": "https://example.com/articles",
  "pagination_config": {
    "selectors": [
      "a.custom-next-button",
      "div.pagination a.next",
      "a[data-page='next']"
    ],
    "attribute": "href",
    "validation_pattern": "^/articles/page/\\d+$"
  }
}
```

## Configuration Options

### `pagination_config` Object

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `selectors` | `[]string` | No | See defaults below | Ordered list of CSS selectors to try |
| `attribute` | `string` | No | `"href"` | HTML attribute to extract from matched element |
| `validation_pattern` | `string` | No | None | Optional regex pattern to validate extracted URLs |

### Default Selectors

If no `pagination_config` is provided, Arachne tries these selectors in order:

1. `li.next a` - Original Arachne selector
2. `a.next` - Common "next" class
3. `a.next_page` - Common "next_page" class
4. `a.morelink` - Reddit-style "more" links
5. `button.next` - Button-based navigation
6. `a[rel='next']` - Semantic HTML rel attribute
7. `.pagination .next a` - Bootstrap-style pagination
8. `a.nextpostslink` - WordPress default
9. `.nav-next a` - WordPress alternative
10. `a[aria-label='Next']` - Accessible pagination

## Examples

### Example 1: WordPress Site

WordPress sites typically use specific pagination classes:

```json
{
  "site_url": "https://blog.example.com",
  "pagination_config": {
    "selectors": [
      "a.nextpostslink",
      ".nav-next a"
    ]
  }
}
```

### Example 2: Custom Button with Data Attribute

For sites using data attributes on buttons:

```json
{
  "site_url": "https://shop.example.com/products",
  "pagination_config": {
    "selectors": [
      "button[data-next-page]"
    ],
    "attribute": "data-next-page"
  }
}
```

### Example 3: Pagination with URL Validation

To ensure you only follow valid pagination URLs:

```json
{
  "site_url": "https://news.example.com/category/tech",
  "pagination_config": {
    "selectors": [
      "a.next-page",
      "a.pagination-next"
    ],
    "attribute": "href",
    "validation_pattern": "^/category/tech/page/\\d+$"
  }
}
```

### Example 4: Reddit-Style "More" Links

```json
{
  "site_url": "https://www.reddit.com/r/programming",
  "pagination_config": {
    "selectors": [
      "a.morelink"
    ]
  }
}
```

## Complete API Request Example

```bash
curl -X POST http://localhost:8080/scrape \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer your-api-token" \
  -d '{
    "site_url": "https://quotes.toscrape.com/page/1/",
    "pagination_config": {
      "selectors": [
        "li.next a",
        "a.next-page"
      ],
      "attribute": "href"
    }
  }'
```

Response:
```json
{
  "job_id": "123e4567-e89b-12d3-a456-426614174000",
  "status": "accepted",
  "results": [],
  "error": ""
}
```

Then check the status:
```bash
curl -X GET "http://localhost:8080/scrape/status?id=123e4567-e89b-12d3-a456-426614174000" \
  -H "Authorization: Bearer your-api-token"
```

## How It Works

1. **Selector Matching**: When scraping a page, Arachne tries each CSS selector in the order provided
2. **First Match Wins**: As soon as a selector matches an element, extraction stops
3. **Attribute Extraction**: The specified attribute (default: `href`) is extracted from the matched element
4. **Validation** (optional): If a `validation_pattern` is provided, the extracted URL must match the regex
5. **URL Resolution**: Relative URLs are automatically converted to absolute URLs
6. **Pagination Loop**: The process repeats for each discovered page until no more "next" links are found or `max_pages` is reached

## Limitations

- **Max Pages**: Controlled by the `MAX_PAGES` configuration (default varies by setup)
- **Timeout**: Subject to `TOTAL_TIMEOUT` configuration
- **Rate Limiting**: Pagination follows the same rate limiting rules as regular scraping
- **JavaScript Required**: Complex pagination (infinite scroll, AJAX) may require headless mode

## Configuration Settings

Related configuration environment variables:

```bash
MAX_PAGES=50              # Maximum pages to scrape per site
TOTAL_TIMEOUT=10m         # Total timeout for entire scraping job
USE_HEADLESS=true         # Enable headless browser for JS-rendered pagination
```

## Troubleshooting

### Pagination Not Working

1. **Check the selector**: Use browser DevTools to verify your CSS selector matches the pagination element
2. **Enable headless mode**: If pagination is JavaScript-rendered, set `USE_HEADLESS=true`
3. **Check logs**: Look for "Found next page:" messages in the logs
4. **Test selectors**: Start with one selector and add more as fallbacks

### Wrong Pages Being Scraped

1. **Add validation pattern**: Use `validation_pattern` to ensure only valid URLs are followed
2. **Check attribute**: Verify you're extracting the correct HTML attribute
3. **Inspect HTML**: Some sites use `data-*` attributes instead of `href`

### Performance Issues

1. **Reduce max_pages**: Lower `MAX_PAGES` to scrape fewer pages
2. **Increase timeout**: Raise `TOTAL_TIMEOUT` if timing out
3. **Adjust rate limiting**: Configure `MAX_CONCURRENT` and `DOMAIN_RATE_LIMIT`

## Technical Details

### Implementation

The pagination system is implemented through:

1. **`PaginationConfig` struct** (`internal/types/types.go`): Defines the configuration structure
2. **Context passing**: Configuration is passed through Go context to avoid breaking existing interfaces
3. **Default config function**: `GetDefaultPaginationConfig()` provides sensible defaults
4. **Extraction logic** (`internal/strategy/headless_strategy.go`): `extractNextURL()` method implements the flexible selector matching

### Code Example

To use pagination in Go code directly:

```go
import (
    "github.com/kareemsasa3/arachne/internal/scraper"
    "github.com/kareemsasa3/arachne/internal/types"
    "github.com/kareemsasa3/arachne/internal/config"
)

// Create scraper
cfg := config.NewConfig()
s := scraper.NewScraper(cfg)

// Define custom pagination
paginationCfg := &types.PaginationConfig{
    Selectors: []string{"a.next", "button.next-page"},
    Attribute: "href",
    ValidationPattern: "^/page/\\d+$",
}

// Scrape with custom pagination
results := s.ScrapeSiteWithConfig("https://example.com", paginationCfg)
```

## Best Practices

1. **Start Simple**: Begin with default selectors and add custom ones only if needed
2. **Order Matters**: Put most specific selectors first, generic ones last
3. **Test Selectors**: Verify selectors in browser DevTools before using in production
4. **Use Validation**: Add `validation_pattern` to prevent following unwanted links
5. **Monitor Logs**: Check logs to see which selectors are matching
6. **Handle Edge Cases**: Some sites change pagination structure; provide multiple selector fallbacks

## Migration Guide

### From Hardcoded Pagination

If you were relying on the hardcoded `li.next a` selector, no changes are needed. This selector is still the first default.

### Adding Custom Selectors

To add custom selectors while keeping defaults as fallback:

```json
{
  "site_url": "https://example.com",
  "pagination_config": {
    "selectors": [
      "a.my-custom-next",           // Try custom first
      "li.next a",                   // Fall back to defaults
      "a.next",
      "a[rel='next']"
    ]
  }
}
```

## Future Enhancements

Potential future improvements:

- [ ] Support for multiple attribute extraction fallbacks
- [ ] JavaScript selector evaluation (XPath, custom scripts)
- [ ] Pagination depth limits per branch
- [ ] Callback functions for custom extraction logic
- [ ] Pagination pattern auto-detection

## Support

For issues or questions about pagination:

1. Check the logs for pagination-related messages
2. Test your CSS selectors in browser DevTools
3. Review examples in this documentation
4. Open an issue on GitHub with:
   - Site URL
   - Pagination config used
   - Expected vs actual behavior
   - Relevant log output

