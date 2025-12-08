package api

import (
	"encoding/json"
	"net/http"
)

// HandleRebuildFTS5 forces a rebuild of the FTS5 search index.
func (h *APIHandler) HandleRebuildFTS5(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.database == nil {
		http.Error(w, "Memory database not available", http.StatusServiceUnavailable)
		return
	}

	if err := h.database.RebuildFTS5(); err != nil {
		http.Error(w, "Failed to rebuild FTS5: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Backfill with NULL-safe values to avoid insertion failures
	if _, err := h.database.ExecRaw(`
		INSERT INTO scrape_search(rowid, url, domain, title, clean_text, summary)
		SELECT 
			id,
			url,
			COALESCE(domain, ''),
			COALESCE(title, ''),
			COALESCE(clean_text, ''),
			COALESCE(summary, '')
		FROM scrape_history
	`); err != nil {
		http.Error(w, "Failed to backfill FTS5: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "FTS5 index rebuilt successfully",
	})
}

// HandleTestFTS5 runs a direct FTS5 query without JOIN to inspect index rows.
func (h *APIHandler) HandleTestFTS5(w http.ResponseWriter, r *http.Request) {
	if h.database == nil {
		http.Error(w, "Memory database not available", http.StatusServiceUnavailable)
		return
	}

	query := r.URL.Query().Get("q")
	if query == "" {
		query = "Spotify"
	}

	rows, err := h.database.QueryRaw(`SELECT rowid, url, title FROM scrape_search WHERE scrape_search MATCH ? LIMIT 5`, query)
	if err != nil {
		http.Error(w, "FTS5 query failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type result struct {
		RowID int64  `json:"rowid"`
		URL   string `json:"url"`
		Title string `json:"title"`
	}

	var results []result
	for rows.Next() {
		var r result
		if err := rows.Scan(&r.RowID, &r.URL, &r.Title); err != nil {
			http.Error(w, "Scan failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		results = append(results, r)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"query":   query,
		"count":   len(results),
		"results": results,
	})
}
