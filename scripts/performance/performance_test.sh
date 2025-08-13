#!/bin/bash

# Performance Testing Script for Arachne Web Scraper
# This script tests various performance aspects of the scraper

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
API_BASE="http://localhost:8080"
TEST_RESULTS_DIR="performance_results"
TIMESTAMP=$(date +"%Y%m%d_%H%M%S")

# Test URLs for different scenarios
FAST_URLS=(
    "https://httpbin.org/get"
    "https://httpbin.org/status/200"
    "https://httpbin.org/json"
    "https://httpbin.org/xml"
    "https://httpbin.org/html"
)

SLOW_URLS=(
    "https://httpbin.org/delay/1"
    "https://httpbin.org/delay/2"
    "https://httpbin.org/delay/3"
)

ERROR_URLS=(
    "https://httpbin.org/status/404"
    "https://httpbin.org/status/500"
    "https://httpbin.org/status/503"
    "https://nonexistent-domain-12345.com"
)

REAL_WORLD_URLS=(
    "https://golang.org"
    "https://github.com"
    "https://stackoverflow.com"
    "https://reddit.com"
    "https://news.ycombinator.com"
)

# Helper functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Ensure required tools
command -v curl >/dev/null 2>&1 || { log_error "curl not found"; exit 1; }
command -v jq >/dev/null 2>&1 || { log_error "jq not found"; exit 1; }

# Check if API is running
check_api_health() {
    log_info "Checking API health..."
    if curl -s "$API_BASE/health" > /dev/null; then
        log_success "API is healthy"
        return 0
    else
        log_error "API is not responding"
        return 1
    fi
}

# Test basic functionality
test_basic_functionality() {
    log_info "Testing basic functionality..."
    
    # Test single URL scraping
    log_info "Testing single URL scraping..."
    response=$(curl -s -X POST "$API_BASE/scrape" \
        -H "Content-Type: application/json" \
        -d '{"urls": ["https://httpbin.org/get"]}')
    
    job_id=$(echo "$response" | jq -r '.job_id')
    log_info "Job submitted with ID: $job_id"
    
    # Wait for completion
    for i in {1..30}; do
        status_response=$(curl -s "$API_BASE/scrape/status?id=$job_id")
        status=$(echo "$status_response" | jq -r '.status')
        
        if [ "$status" = "completed" ]; then
            log_success "Job completed successfully"
            results=$(echo "$status_response" | jq -r '.results | length')
            log_info "Scraped $results URLs"
            break
        elif [ "$status" = "failed" ]; then
            log_error "Job failed"
            return 1
        fi
        
        sleep 2
    done
}

# Test concurrent load
test_concurrent_load() {
    log_info "Testing concurrent load..."
    
    local num_jobs=5
    local job_ids=()
    
    # Submit multiple jobs simultaneously
    for i in $(seq 1 $num_jobs); do
        response=$(curl -s -X POST "$API_BASE/scrape" \
            -H "Content-Type: application/json" \
            -d "{\"urls\": [\"https://httpbin.org/delay/1\", \"https://httpbin.org/get\"]}")
        
        job_id=$(echo "$response" | jq -r '.job_id')
        job_ids+=("$job_id")
        log_info "Submitted job $i with ID: $job_id"
    done
    
    # Monitor all jobs
    local completed=0
    local failed=0
    
    for job_id in "${job_ids[@]}"; do
        for i in {1..60}; do
            status_response=$(curl -s "$API_BASE/scrape/status?id=$job_id")
            status=$(echo "$status_response" | jq -r '.status')
            
            if [ "$status" = "completed" ]; then
                ((completed++))
                log_success "Job $job_id completed"
                break
            elif [ "$status" = "failed" ]; then
                ((failed++))
                log_error "Job $job_id failed"
                break
            fi
            
            sleep 1
        done
    done
    
    log_info "Concurrent load test results: $completed completed, $failed failed out of $num_jobs jobs"
}

# Test different URL types
test_url_types() {
    log_info "Testing different URL types..."
    
    # Test fast URLs
    log_info "Testing fast URLs..."
    response=$(curl -s -X POST "$API_BASE/scrape" \
        -H "Content-Type: application/json" \
        -d "{\"urls\": $(printf '%s\n' "${FAST_URLS[@]}" | jq -R . | jq -s .)}")
    
    job_id=$(echo "$response" | jq -r '.job_id')
    wait_for_job_completion "$job_id" "fast URLs"
    
    # Test slow URLs
    log_info "Testing slow URLs..."
    response=$(curl -s -X POST "$API_BASE/scrape" \
        -H "Content-Type: application/json" \
        -d "{\"urls\": $(printf '%s\n' "${SLOW_URLS[@]}" | jq -R . | jq -s .)}")
    
    job_id=$(echo "$response" | jq -r '.job_id')
    wait_for_job_completion "$job_id" "slow URLs"
    
    # Test error URLs
    log_info "Testing error URLs..."
    response=$(curl -s -X POST "$API_BASE/scrape" \
        -H "Content-Type: application/json" \
        -d "{\"urls\": $(printf '%s\n' "${ERROR_URLS[@]}" | jq -R . | jq -s .)}")
    
    job_id=$(echo "$response" | jq -r '.job_id')
    wait_for_job_completion "$job_id" "error URLs"
}

# Test site scraping
test_site_scraping() {
    log_info "Testing site scraping..."
    
    response=$(curl -s -X POST "$API_BASE/scrape" \
        -H "Content-Type: application/json" \
        -d '{"site_url": "https://httpbin.org"}')
    
    job_id=$(echo "$response" | jq -r '.job_id')
    wait_for_job_completion "$job_id" "site scraping"
}

# Test real-world URLs
test_real_world_urls() {
    log_info "Testing real-world URLs..."
    
    response=$(curl -s -X POST "$API_BASE/scrape" \
        -H "Content-Type: application/json" \
        -d "{\"urls\": $(printf '%s\n' "${REAL_WORLD_URLS[@]}" | jq -R . | jq -s .)}")
    
    job_id=$(echo "$response" | jq -r '.job_id')
    wait_for_job_completion "$job_id" "real-world URLs"
}

# Wait for job completion
wait_for_job_completion() {
    local job_id=$1
    local test_name=$2
    local max_wait=120
    
    log_info "Waiting for $test_name job completion..."
    
    for i in $(seq 1 $max_wait); do
        status_response=$(curl -s "$API_BASE/scrape/status?id=$job_id")
        status=$(echo "$status_response" | jq -r '.status')
        
        if [ "$status" = "completed" ]; then
            results=$(echo "$status_response" | jq -r '.results | length')
            log_success "$test_name completed: $results URLs scraped"
            return 0
        elif [ "$status" = "failed" ]; then
            log_error "$test_name failed"
            return 1
        fi
        
        sleep 1
    done
    
    log_warning "$test_name timed out after ${max_wait}s"
    return 1
}

# Test metrics endpoint
test_metrics() {
    log_info "Testing metrics endpoint..."
    
    metrics=$(curl -s "$API_BASE/metrics")
    if [ $? -eq 0 ]; then
        log_success "Metrics endpoint is working"
        echo "$metrics" | jq . > "$TEST_RESULTS_DIR/metrics_$TIMESTAMP.json"
    else
        log_warning "Metrics endpoint not available"
    fi
}

# Performance benchmark
run_benchmark() {
    log_info "Running performance benchmark..."
    
    local start_time=$(date +%s)
    
    # Submit a job with multiple URLs
    response=$(curl -s -X POST "$API_BASE/scrape" \
        -H "Content-Type: application/json" \
        -d "{\"urls\": $(printf '%s\n' "${FAST_URLS[@]}" | jq -R . | jq -s .)}")
    
    job_id=$(echo "$response" | jq -r '.job_id')
    
    # Wait for completion and measure time
    for i in {1..60}; do
        status_response=$(curl -s "$API_BASE/scrape/status?id=$job_id")
        status=$(echo "$status_response" | jq -r '.status')
        
        if [ "$status" = "completed" ]; then
            local end_time=$(date +%s)
            local duration=$((end_time - start_time))
            local results=$(echo "$status_response" | jq -r '.results | length')
            
            log_success "Benchmark completed in ${duration}s for $results URLs"
            echo "Benchmark: ${duration}s for $results URLs" >> "$TEST_RESULTS_DIR/benchmark_$TIMESTAMP.txt"
            break
        fi
        
        sleep 1
    done
}

# Stress test
run_stress_test() {
    log_info "Running stress test..."
    
    local num_requests=10
    local concurrent_jobs=()
    
    # Submit many jobs quickly
    for i in $(seq 1 $num_requests); do
        response=$(curl -s -X POST "$API_BASE/scrape" \
            -H "Content-Type: application/json" \
            -d '{"urls": ["https://httpbin.org/get", "https://httpbin.org/delay/1"]}')
        
        job_id=$(echo "$response" | jq -r '.job_id')
        concurrent_jobs+=("$job_id")
        
        if [ $((i % 5)) -eq 0 ]; then
            log_info "Submitted $i jobs..."
        fi
    done
    
    # Monitor completion
    local completed=0
    local failed=0
    
    for job_id in "${concurrent_jobs[@]}"; do
        for i in {1..120}; do
            status_response=$(curl -s "$API_BASE/scrape/status?id=$job_id")
            status=$(echo "$status_response" | jq -r '.status')
            
            if [ "$status" = "completed" ]; then
                ((completed++))
                break
            elif [ "$status" = "failed" ]; then
                ((failed++))
                break
            fi
            
            sleep 1
        done
    done
    
    log_info "Stress test results: $completed completed, $failed failed out of $num_requests jobs"
    echo "Stress test: $completed/$num_requests completed" >> "$TEST_RESULTS_DIR/stress_test_$TIMESTAMP.txt"
}

# Main execution
main() {
    log_info "Starting performance testing for Arachne Web Scraper"
    
    # Create results directory
    mkdir -p "$TEST_RESULTS_DIR"
    
    # Check API health
    if ! check_api_health; then
        log_error "Cannot proceed with tests - API is not healthy"
        exit 1
    fi
    
    # Run tests
    test_basic_functionality
    test_concurrent_load
    test_url_types
    test_site_scraping
    test_real_world_urls
    test_metrics
    run_benchmark
    run_stress_test
    
    log_success "Performance testing completed!"
    log_info "Results saved in $TEST_RESULTS_DIR/"
}

# Run main function
main "$@" 