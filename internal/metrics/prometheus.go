package metrics

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// PrometheusMetrics provides Prometheus-compatible metrics for the scraper
type PrometheusMetrics struct {
	mu sync.RWMutex

	// HTTP request metrics
	httpRequestsTotal   *prometheus.CounterVec
	httpRequestDuration *prometheus.HistogramVec

	// Scraping metrics
	scrapingRequestsTotal *prometheus.CounterVec
	scrapingSuccessTotal  *prometheus.CounterVec
	scrapingFailureTotal  *prometheus.CounterVec
	scrapingRetryTotal    *prometheus.CounterVec
	scrapingDuration      *prometheus.HistogramVec
	scrapingBytesTotal    *prometheus.CounterVec

	// Domain-specific metrics
	domainRequestsTotal *prometheus.CounterVec
	domainSuccessTotal  *prometheus.CounterVec
	domainFailureTotal  *prometheus.CounterVec
	domainResponseTime  *prometheus.HistogramVec

	// Status code metrics
	statusCodeTotal *prometheus.CounterVec

	// Circuit breaker metrics
	circuitBreakerState     *prometheus.GaugeVec
	circuitBreakerFailures  *prometheus.CounterVec
	circuitBreakerSuccesses *prometheus.CounterVec

	// Registry for all metrics
	registry *prometheus.Registry
}

// NewPrometheusMetrics creates a new Prometheus metrics instance
func NewPrometheusMetrics() *PrometheusMetrics {
	registry := prometheus.NewRegistry()

	metrics := &PrometheusMetrics{
		registry: registry,

		// HTTP request metrics
		httpRequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "arachne_http_requests_total",
				Help: "Total number of HTTP requests",
			},
			[]string{"method", "endpoint", "status_code"},
		),

		httpRequestDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "arachne_http_request_duration_seconds",
				Help:    "Duration of HTTP requests in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"method", "endpoint"},
		),

		// Scraping metrics
		scrapingRequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "arachne_scraping_requests_total",
				Help: "Total number of scraping requests",
			},
			[]string{"domain"},
		),

		scrapingSuccessTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "arachne_scraping_success_total",
				Help: "Total number of successful scraping requests",
			},
			[]string{"domain"},
		),

		scrapingFailureTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "arachne_scraping_failure_total",
				Help: "Total number of failed scraping requests",
			},
			[]string{"domain", "error_type"},
		),

		scrapingRetryTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "arachne_scraping_retry_total",
				Help: "Total number of retry attempts",
			},
			[]string{"domain"},
		),

		scrapingDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "arachne_scraping_duration_seconds",
				Help:    "Duration of scraping requests in seconds",
				Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60},
			},
			[]string{"domain"},
		),

		scrapingBytesTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "arachne_scraping_bytes_total",
				Help: "Total bytes scraped",
			},
			[]string{"domain"},
		),

		// Domain-specific metrics
		domainRequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "arachne_domain_requests_total",
				Help: "Total requests per domain",
			},
			[]string{"domain"},
		),

		domainSuccessTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "arachne_domain_success_total",
				Help: "Total successful requests per domain",
			},
			[]string{"domain"},
		),

		domainFailureTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "arachne_domain_failure_total",
				Help: "Total failed requests per domain",
			},
			[]string{"domain"},
		),

		domainResponseTime: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "arachne_domain_response_time_seconds",
				Help:    "Response time per domain in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"domain"},
		),

		// Status code metrics
		statusCodeTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "arachne_status_code_total",
				Help: "Total responses by status code",
			},
			[]string{"status_code"},
		),

		// Circuit breaker metrics
		circuitBreakerState: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "arachne_circuit_breaker_state",
				Help: "Current state of circuit breaker (0=closed, 1=open, 2=half_open)",
			},
			[]string{"domain"},
		),

		circuitBreakerFailures: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "arachne_circuit_breaker_failures_total",
				Help: "Total failures tracked by circuit breaker",
			},
			[]string{"domain"},
		),

		circuitBreakerSuccesses: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "arachne_circuit_breaker_successes_total",
				Help: "Total successes tracked by circuit breaker",
			},
			[]string{"domain"},
		),
	}

	// Register all metrics with the registry
	registry.MustRegister(
		metrics.httpRequestsTotal,
		metrics.httpRequestDuration,
		metrics.scrapingRequestsTotal,
		metrics.scrapingSuccessTotal,
		metrics.scrapingFailureTotal,
		metrics.scrapingRetryTotal,
		metrics.scrapingDuration,
		metrics.scrapingBytesTotal,
		metrics.domainRequestsTotal,
		metrics.domainSuccessTotal,
		metrics.domainFailureTotal,
		metrics.domainResponseTime,
		metrics.statusCodeTotal,
		metrics.circuitBreakerState,
		metrics.circuitBreakerFailures,
		metrics.circuitBreakerSuccesses,
	)

	return metrics
}

// RecordHTTPRequest records an HTTP request
func (pm *PrometheusMetrics) RecordHTTPRequest(method, endpoint string, statusCode int, duration time.Duration) {
	pm.httpRequestsTotal.WithLabelValues(method, endpoint, string(rune(statusCode))).Inc()
	pm.httpRequestDuration.WithLabelValues(method, endpoint).Observe(duration.Seconds())
}

// RecordScrapingRequest records a scraping request
func (pm *PrometheusMetrics) RecordScrapingRequest(domain string) {
	pm.scrapingRequestsTotal.WithLabelValues(domain).Inc()
	pm.domainRequestsTotal.WithLabelValues(domain).Inc()
}

// RecordScrapingSuccess records a successful scraping request
func (pm *PrometheusMetrics) RecordScrapingSuccess(domain string, statusCode int, bytes int64, duration time.Duration) {
	pm.scrapingSuccessTotal.WithLabelValues(domain).Inc()
	pm.domainSuccessTotal.WithLabelValues(domain).Inc()
	pm.scrapingDuration.WithLabelValues(domain).Observe(duration.Seconds())
	pm.scrapingBytesTotal.WithLabelValues(domain).Add(float64(bytes))
	pm.domainResponseTime.WithLabelValues(domain).Observe(duration.Seconds())
	pm.statusCodeTotal.WithLabelValues(string(rune(statusCode))).Inc()
}

// RecordScrapingFailure records a failed scraping request
func (pm *PrometheusMetrics) RecordScrapingFailure(domain, errorType string, statusCode int) {
	pm.scrapingFailureTotal.WithLabelValues(domain, errorType).Inc()
	pm.domainFailureTotal.WithLabelValues(domain).Inc()
	if statusCode > 0 {
		pm.statusCodeTotal.WithLabelValues(string(rune(statusCode))).Inc()
	}
}

// RecordScrapingRetry records a retry attempt
func (pm *PrometheusMetrics) RecordScrapingRetry(domain string) {
	pm.scrapingRetryTotal.WithLabelValues(domain).Inc()
}

// UpdateCircuitBreakerState updates the circuit breaker state
func (pm *PrometheusMetrics) UpdateCircuitBreakerState(domain string, state int) {
	pm.circuitBreakerState.WithLabelValues(domain).Set(float64(state))
}

// RecordCircuitBreakerFailure records a circuit breaker failure
func (pm *PrometheusMetrics) RecordCircuitBreakerFailure(domain string) {
	pm.circuitBreakerFailures.WithLabelValues(domain).Inc()
}

// RecordCircuitBreakerSuccess records a circuit breaker success
func (pm *PrometheusMetrics) RecordCircuitBreakerSuccess(domain string) {
	pm.circuitBreakerSuccesses.WithLabelValues(domain).Inc()
}

// GetRegistry returns the Prometheus registry
func (pm *PrometheusMetrics) GetRegistry() *prometheus.Registry {
	return pm.registry
}
