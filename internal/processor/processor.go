package processor

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/kareemsasa3/arachne/internal/types"
)

// ResultProcessor processes and formats results
type ResultProcessor struct{}

// ProcessResults formats and prints results
func (rp *ResultProcessor) ProcessResults(results []types.ScrapedData) {
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

// ExportToJSON exports results to JSON
func (rp *ResultProcessor) ExportToJSON(results []types.ScrapedData, filename string) error {
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
