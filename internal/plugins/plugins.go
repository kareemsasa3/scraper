package plugins

import (
	"context"
	"fmt"
	"strings"

	"github.com/kareemsasa3/arachne/internal/errors"
	"github.com/kareemsasa3/arachne/internal/types"
)

// DataProcessor defines the interface for processing scraped data
type DataProcessor interface {
	Process(ctx context.Context, data *types.ScrapedData) error
	Name() string
}

// PluginManager manages data processing plugins
type PluginManager struct {
	processors []DataProcessor
}

// NewPluginManager creates a new plugin manager
func NewPluginManager() *PluginManager {
	return &PluginManager{
		processors: make([]DataProcessor, 0),
	}
}

// RegisterPlugin registers a new data processor plugin
func (pm *PluginManager) RegisterPlugin(processor DataProcessor) {
	pm.processors = append(pm.processors, processor)
}

// ProcessData processes data through all registered plugins
func (pm *PluginManager) ProcessData(ctx context.Context, data *types.ScrapedData) error {
	for _, processor := range pm.processors {
		if err := processor.Process(ctx, data); err != nil {
			return fmt.Errorf("plugin %s failed: %v", processor.Name(), err)
		}
	}
	return nil
}

// GetPluginCount returns the number of registered plugins
func (pm *PluginManager) GetPluginCount() int {
	return len(pm.processors)
}

// Built-in plugins

// TitleCleanerPlugin cleans and normalizes titles
type TitleCleanerPlugin struct{}

// NewTitleCleanerPlugin creates a new title cleaner plugin
func NewTitleCleanerPlugin() *TitleCleanerPlugin {
	return &TitleCleanerPlugin{}
}

// Process cleans the title
func (t *TitleCleanerPlugin) Process(ctx context.Context, data *types.ScrapedData) error {
	if data.Title != "" {
		// Remove extra whitespace and normalize
		data.Title = strings.TrimSpace(data.Title)
		// Limit title length
		if len(data.Title) > 200 {
			data.Title = data.Title[:197] + "..."
		}
	}
	return nil
}

// Name returns the plugin name
func (t *TitleCleanerPlugin) Name() string {
	return "TitleCleaner"
}

// URLValidatorPlugin validates URLs in the scraped data
type URLValidatorPlugin struct{}

// NewURLValidatorPlugin creates a new URL validator plugin
func NewURLValidatorPlugin() *URLValidatorPlugin {
	return &URLValidatorPlugin{}
}

// Process validates the URL
func (u *URLValidatorPlugin) Process(ctx context.Context, data *types.ScrapedData) error {
	if data.URL != "" {
		if err := errors.ValidateURL(data.URL); err != nil {
			data.Error = fmt.Sprintf("Invalid URL: %v", err)
		}
	}
	return nil
}

// Name returns the plugin name
func (u *URLValidatorPlugin) Name() string {
	return "URLValidator"
}

// ContentTypePlugin adds content type information
type ContentTypePlugin struct{}

// NewContentTypePlugin creates a new content type plugin
func NewContentTypePlugin() *ContentTypePlugin {
	return &ContentTypePlugin{}
}

// Process adds content type information
func (c *ContentTypePlugin) Process(ctx context.Context, data *types.ScrapedData) error {
	// This plugin could be extended to add content type detection
	// For now, it's a placeholder for future functionality
	return nil
}

// Name returns the plugin name
func (c *ContentTypePlugin) Name() string {
	return "ContentType"
}
