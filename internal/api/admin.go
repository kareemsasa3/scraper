package api

import (
	"encoding/json"
	"net/http"

	"github.com/kareemsasa3/arachne/internal/database"
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

	if !h.database.HasFTS5() {
		http.Error(w, database.ErrFTS5NotAvailable.Error(), http.StatusNotImplemented)
		return
	}

	if err := h.database.RebuildFTS5(); err != nil {
		http.Error(w, "Failed to rebuild FTS5: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// RebuildFTS5 already performs complete DROP + CREATE + backfill (via createFTS5Index).
	// We MUST NOT perform a second backfill here, as FTS5 rowid segments are sensitive to
	// double-insertion even if they are logically "updates", leading to corruption.

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

	if !h.database.HasFTS5() {
		http.Error(w, database.ErrFTS5NotAvailable.Error(), http.StatusNotImplemented)
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
