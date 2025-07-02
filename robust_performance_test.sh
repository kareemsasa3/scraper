#!/bin/bash

# Robust Performance Test for Arachne Web Scraper
# Improved version with better error handling and polling

set -e

# Colors for output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

API_BASE="http://localhost:8080"

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

# Check API health
check_health() {
    log_info "Checking API health..."
    if curl -s "$API_BASE/health" > /dev/null; then
        log_success "API is healthy"
        return 0
    else
        log_error "API is not responding"
        return 1
    fi
}

# Submit a job and return job ID
submit_job() {
    local urls="$1"
    local response=$(curl -s -X POST "$API_BASE/scrape" \
        -H "Content-Type: application/json" \
        -d "{\"urls\": $urls}")
    
    local job_id=$(echo "$response" | jq -r '.job_id')
    if [ "$job_id" = "null" ] || [ -z "$job_id" ]; then
        log_error "Failed to get job ID from response: $response"
        return 1
    fi
    
    echo "$job_id"
}

# Wait for job completion with better error handling
wait_for_job() {
    local job_id="$1"
    local test_name="$2"
    local max_wait="${3:-60}"
    
    log_info "Waiting for $test_name completion (job: $job_id)..."
    
    local start_time=$(date +%s)
    
    for i in $(seq 1 $max_wait); do
        # Get job status
        local status_response
        if ! status_response=$(curl -s "$API_BASE/scrape/status?id=$job_id" 2>/dev/null); then
            log_warning "Failed to get status for job $job_id (attempt $i)"
            sleep 2
            continue
        fi
        
        # Extract status
        local status
        if ! status=$(echo "$status_response" | jq -r '.job.status' 2>/dev/null); then
            log_warning "Failed to parse status response (attempt $i)"
            sleep 2
            continue
        fi
        
        # Check status
        case "$status" in
            "completed")
                local end_time=$(date +%s)
                local duration=$((end_time - start_time))
                local results=$(echo "$status_response" | jq -r '.job.results | length')
                log_success "$test_name completed in ${duration}s - $results URLs scraped"
                return 0
                ;;
            "failed")
                log_error "$test_name failed"
                echo "$status_response" | jq -r '.job.error // "Unknown error"'
                return 1
                ;;
            "processing"|"pending")
                if [ $((i % 5)) -eq 0 ]; then
                    log_info "Still processing... (${i}s elapsed)"
                fi
                sleep 1
                ;;
            *)
                log_warning "Unknown status: $status"
                sleep 2
                ;;
        esac
    done
    
    log_warning "$test_name timed out after ${max_wait}s"
    return 1
}

# Test 1: Single URL
test_single_url() {
    log_info "Test 1: Single URL scraping"
    
    local job_id
    if ! job_id=$(submit_job '["https://httpbin.org/get"]'); then
        return 1
    fi
    
    wait_for_job "$job_id" "Single URL test" 30
}

# Test 2: Multiple URLs
test_multiple_urls() {
    log_info "Test 2: Multiple URLs scraping"
    
    local job_id
    if ! job_id=$(submit_job '["https://httpbin.org/get", "https://httpbin.org/json", "https://httpbin.org/xml"]'); then
        return 1
    fi
    
    wait_for_job "$job_id" "Multiple URLs test" 45
}

# Test 3: Error handling
test_error_handling() {
    log_info "Test 3: Error handling"
    
    local job_id
    if ! job_id=$(submit_job '["https://httpbin.org/status/404", "https://nonexistent-domain-12345.com"]'); then
        return 1
    fi
    
    wait_for_job "$job_id" "Error handling test" 30
}

# Test 4: Concurrent jobs
test_concurrent_jobs() {
    log_info "Test 4: Concurrent jobs (3 jobs)"
    
    local job_ids=()
    
    # Submit 3 jobs
    for i in {1..3}; do
        local job_id
        if job_id=$(submit_job '["https://httpbin.org/get"]'); then
            job_ids+=("$job_id")
            log_info "Submitted job $i: $job_id"
        else
            log_error "Failed to submit job $i"
            return 1
        fi
    done
    
    # Wait for all jobs
    local completed=0
    local failed=0
    
    for job_id in "${job_ids[@]}"; do
        if wait_for_job "$job_id" "Concurrent job" 60; then
            ((completed++))
        else
            ((failed++))
        fi
    done
    
    log_info "Concurrent test results: $completed completed, $failed failed out of 3 jobs"
}

# Test 5: Performance metrics
test_metrics() {
    log_info "Test 5: Performance metrics"
    
    local metrics
    if ! metrics=$(curl -s "$API_BASE/metrics" 2>/dev/null); then
        log_error "Failed to get metrics"
        return 1
    fi
    
    local total_requests=$(echo "$metrics" | jq -r '.total_requests')
    local successful_requests=$(echo "$metrics" | jq -r '.successful_requests')
    local failed_requests=$(echo "$metrics" | jq -r '.failed_requests')
    local success_rate=$(echo "$metrics" | jq -r '.success_rate')
    local avg_response_time=$(echo "$metrics" | jq -r '.response_times.avg')
    local requests_per_second=$(echo "$metrics" | jq -r '.requests_per_second')
    
    log_success "Total requests: $total_requests"
    log_success "Successful: $successful_requests"
    log_success "Failed: $failed_requests"
    log_success "Success rate: ${success_rate}%"
    log_success "Avg response time: $avg_response_time"
    log_success "Requests per second: $requests_per_second"
}

# Main execution
main() {
    log_info "Starting robust performance tests..."
    
    # Check health first
    if ! check_health; then
        exit 1
    fi
    
    # Run tests
    test_single_url
    echo "---"
    test_multiple_urls
    echo "---"
    test_error_handling
    echo "---"
    test_concurrent_jobs
    echo "---"
    test_metrics
    
    log_success "All tests completed!"
}

main "$@" 