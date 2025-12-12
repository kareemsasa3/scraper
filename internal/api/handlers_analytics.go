package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
)

// HandleGetAnalyticsSummary returns high-level analytics.
func (h *APIHandler) HandleGetAnalyticsSummary(w http.ResponseWriter, r *http.Request) {
	if h.database == nil {
		http.Error(w, "Analytics unavailable", http.StatusServiceUnavailable)
		return
	}

	summary, err := h.database.GetAnalyticsSummary()
	if err != nil {
		log.Printf("analytics summary query failed: %v", err)
		http.Error(w, "Failed to fetch analytics summary", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(summary)
}

// HandleGetTimeSeriesData returns time-series analytics.
func (h *APIHandler) HandleGetTimeSeriesData(w http.ResponseWriter, r *http.Request) {
	if h.database == nil {
		http.Error(w, "Analytics unavailable", http.StatusServiceUnavailable)
		return
	}

	days := 30
	if daysParam := r.URL.Query().Get("days"); daysParam != "" {
		if parsed, err := strconv.Atoi(daysParam); err == nil && parsed > 0 && parsed <= 365 {
			days = parsed
		}
	}

	data, err := h.database.GetTimeSeriesData(days)
	if err != nil {
		http.Error(w, "Failed to fetch time series data", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(data)
}

// HandleGetTopDomains returns most scraped domains.
func (h *APIHandler) HandleGetTopDomains(w http.ResponseWriter, r *http.Request) {
	if h.database == nil {
		http.Error(w, "Analytics unavailable", http.StatusServiceUnavailable)
		return
	}

	limit := 10
	if limitParam := r.URL.Query().Get("limit"); limitParam != "" {
		if parsed, err := strconv.Atoi(limitParam); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	domains, err := h.database.GetTopDomains(limit)
	if err != nil {
		http.Error(w, "Failed to fetch top domains", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(domains)
}

// HandleGetRecentScrapes returns recent scrape attempts.
func (h *APIHandler) HandleGetRecentScrapes(w http.ResponseWriter, r *http.Request) {
	if h.database == nil {
		http.Error(w, "Analytics unavailable", http.StatusServiceUnavailable)
		return
	}

	limit := 20
	if limitParam := r.URL.Query().Get("limit"); limitParam != "" {
		if parsed, err := strconv.Atoi(limitParam); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	scrapes, err := h.database.GetRecentScrapes(limit)
	if err != nil {
		http.Error(w, "Failed to fetch recent scrapes", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(scrapes)
}
