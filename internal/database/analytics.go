package database

import (
	"database/sql"
	"time"
)

// AnalyticsSummary captures high-level scrape metrics.
type AnalyticsSummary struct {
	TotalScrapes      int     `json:"total_scrapes"`
	SuccessfulScrapes int     `json:"successful_scrapes"`
	FailedScrapes     int     `json:"failed_scrapes"`
	SuccessRate       float64 `json:"success_rate"`
	AverageDuration   float64 `json:"average_duration_seconds"`
	FastestScrape     float64 `json:"fastest_scrape_seconds"`
	SlowestScrape     float64 `json:"slowest_scrape_seconds"`
	LargestScrapeSize int64   `json:"largest_scrape_bytes"`
	TotalDataScraped  int64   `json:"total_data_bytes"`
	AverageScrapeSize int64   `json:"average_scrape_bytes"`
	UniqueURLs        int     `json:"unique_urls"`
	TotalVersions     int     `json:"total_versions"`
	URLsWithChanges   int     `json:"urls_with_changes"`
}

// TimeSeriesDataPoint represents aggregated metrics per day.
type TimeSeriesDataPoint struct {
	Date          string  `json:"date"`
	ScrapesCount  int     `json:"scrapes_count"`
	SuccessRate   float64 `json:"success_rate"`
	AvgDuration   float64 `json:"avg_duration_seconds"`
	TotalDataSize int64   `json:"total_data_bytes"`
}

// DomainStats contains aggregated metrics per domain.
type DomainStats struct {
	Domain       string  `json:"domain"`
	ScrapesCount int     `json:"scrapes_count"`
	SuccessRate  float64 `json:"success_rate"`
	AvgDuration  float64 `json:"avg_duration_seconds"`
	TotalSize    int64   `json:"total_size_bytes"`
}

// RecentScrape represents a recent scrape attempt.
type RecentScrape struct {
	URL         string    `json:"url"`
	Status      string    `json:"status"`
	Duration    float64   `json:"duration_seconds"`
	Size        int64     `json:"size_bytes"`
	CompletedAt time.Time `json:"completed_at"`
	Error       string    `json:"error,omitempty"`
}

// GetAnalyticsSummary returns high-level metrics computed from scrape_history.
func (db *DB) GetAnalyticsSummary() (*AnalyticsSummary, error) {
	query := `
		WITH stats AS (
			SELECT
				COUNT(*) AS total_scrapes,
				SUM(CASE WHEN scrape_status = 'success' THEN 1 ELSE 0 END) AS successful_scrapes,
				SUM(CASE WHEN scrape_status != 'success' THEN 1 ELSE 0 END) AS failed_scrapes,
				MAX(LENGTH(COALESCE(raw_content, clean_text, ''))) AS largest_size,
				SUM(LENGTH(COALESCE(raw_content, clean_text, ''))) AS total_size,
				CAST(AVG(LENGTH(COALESCE(raw_content, clean_text, ''))) AS INTEGER) AS average_size,
				COUNT(DISTINCT url) AS unique_urls,
				COUNT(*) AS total_versions,
				COUNT(DISTINCT CASE WHEN has_changes = 1 THEN url END) AS urls_with_changes
			FROM scrape_history
		)
		SELECT
			COALESCE(total_scrapes, 0),
			COALESCE(successful_scrapes, 0),
			COALESCE(failed_scrapes, 0),
			0 AS average_duration,
			0 AS fastest_scrape,
			0 AS slowest_scrape,
			COALESCE(largest_size, 0),
			COALESCE(total_size, 0),
			COALESCE(average_size, 0),
			COALESCE(unique_urls, 0),
			COALESCE(total_versions, 0),
			COALESCE(urls_with_changes, 0)
		FROM stats;
	`

	var summary AnalyticsSummary
	if err := db.conn.QueryRow(query).Scan(
		&summary.TotalScrapes,
		&summary.SuccessfulScrapes,
		&summary.FailedScrapes,
		&summary.AverageDuration,
		&summary.FastestScrape,
		&summary.SlowestScrape,
		&summary.LargestScrapeSize,
		&summary.TotalDataScraped,
		&summary.AverageScrapeSize,
		&summary.UniqueURLs,
		&summary.TotalVersions,
		&summary.URLsWithChanges,
	); err != nil {
		return nil, err
	}

	if summary.TotalScrapes > 0 {
		summary.SuccessRate = float64(summary.SuccessfulScrapes) / float64(summary.TotalScrapes) * 100
	}

	return &summary, nil
}

// GetTimeSeriesData returns daily aggregated metrics for the last N days.
func (db *DB) GetTimeSeriesData(days int) ([]TimeSeriesDataPoint, error) {
	query := `
		WITH RECURSIVE dates(date) AS (
			SELECT DATE('now', 'localtime')
			UNION ALL
			SELECT DATE(date, '-1 day')
			FROM dates
			LIMIT ?
		),
		daily AS (
			SELECT
				DATE(scraped_at) AS date,
				COUNT(*) AS total,
				SUM(CASE WHEN scrape_status = 'success' THEN 1 ELSE 0 END) AS successes,
				SUM(LENGTH(COALESCE(raw_content, clean_text, ''))) AS total_size
			FROM scrape_history
			WHERE DATE(scraped_at) >= DATE('now', 'localtime', '-' || ? || ' days')
			GROUP BY DATE(scraped_at)
		)
		SELECT
			d.date,
			COALESCE(a.total, 0) AS scrapes_count,
			CASE
				WHEN COALESCE(a.total, 0) > 0 THEN (a.successes * 100.0) / a.total
				ELSE 0
			END AS success_rate,
			0 AS avg_duration_seconds,
			COALESCE(a.total_size, 0) AS total_size
		FROM dates d
		LEFT JOIN daily a ON d.date = a.date
		ORDER BY d.date ASC;
	`

	rows, err := db.conn.Query(query, days, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []TimeSeriesDataPoint
	for rows.Next() {
		var point TimeSeriesDataPoint
		if err := rows.Scan(
			&point.Date,
			&point.ScrapesCount,
			&point.SuccessRate,
			&point.AvgDuration,
			&point.TotalDataSize,
		); err != nil {
			return nil, err
		}
		results = append(results, point)
	}

	return results, nil
}

// GetTopDomains returns most scraped domains with stats.
func (db *DB) GetTopDomains(limit int) ([]DomainStats, error) {
	query := `
		SELECT
			domain,
			COUNT(*) AS scrapes_count,
			SUM(CASE WHEN scrape_status = 'success' THEN 1 ELSE 0 END) * 100.0 / COUNT(*) AS success_rate,
			0 AS avg_duration_seconds,
			SUM(LENGTH(COALESCE(raw_content, clean_text, ''))) AS total_size
		FROM scrape_history
		WHERE domain IS NOT NULL AND domain != ''
		GROUP BY domain
		ORDER BY scrapes_count DESC
		LIMIT ?
	`

	rows, err := db.conn.Query(query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []DomainStats
	for rows.Next() {
		var stats DomainStats
		var avgDuration sql.NullFloat64
		if err := rows.Scan(
			&stats.Domain,
			&stats.ScrapesCount,
			&stats.SuccessRate,
			&avgDuration,
			&stats.TotalSize,
		); err != nil {
			return nil, err
		}
		if avgDuration.Valid {
			stats.AvgDuration = avgDuration.Float64
		}
		results = append(results, stats)
	}

	return results, nil
}

// GetRecentScrapes returns the most recent scrape attempts.
func (db *DB) GetRecentScrapes(limit int) ([]RecentScrape, error) {
	query := `
		SELECT
			url,
			scrape_status,
			0 AS duration_seconds,
			LENGTH(COALESCE(raw_content, clean_text, '')) AS size_bytes,
			scraped_at AS completed_at,
			error_message
		FROM scrape_history
		ORDER BY scraped_at DESC
		LIMIT ?
	`

	rows, err := db.conn.Query(query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []RecentScrape
	for rows.Next() {
		var scrape RecentScrape
		var errorMsg sql.NullString
		if err := rows.Scan(
			&scrape.URL,
			&scrape.Status,
			&scrape.Duration,
			&scrape.Size,
			&scrape.CompletedAt,
			&errorMsg,
		); err != nil {
			return nil, err
		}
		if errorMsg.Valid {
			scrape.Error = errorMsg.String
		}
		results = append(results, scrape)
	}

	return results, nil
}
