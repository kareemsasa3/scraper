#!/bin/bash

# Simple Performance Test for Arachne Web Scraper
# Quick tests to evaluate performance without hanging

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

# Test 1: Quick single URL test
test_single_url() {
    log_info "Test 1: Single URL scraping"
    
    start_time=$(date +%s.%N)
    
    response=$(curl -s -X POST "$API_BASE/scrape" \
        -H "Content-Type: application/json" \
        -d '{"urls": ["https://httpbin.org/get"]}')
    
    job_id=$(echo "$response" | jq -r '.job_id')
    log_info "Job ID: $job_id"
    
    # Wait max 30 seconds
    for i in {1..30}; do
        status_response=$(curl -s "$API_BASE/scrape/status?id=$job_id")
        status=$(echo "$status_response" | jq -r '.status')
        
        if [ "$status" = "completed" ]; then
            end_time=$(date +%s.%N)
            duration=$(echo "$end_time - $start_time" | bc)
            results=$(echo "$status_response" | jq -r '.results | length')
            log_success "Completed in ${duration}s - $results URLs scraped"
            return 0
        elif [ "$status" = "failed" ]; then
            log_error "Job failed"
            return 1
        fi
        
        sleep 1
    done
    
    log_warning "Test timed out after 30s"
    return 1
}

# Test 2: Multiple fast URLs
test_multiple_urls() {
    log_info "Test 2: Multiple fast URLs"
    
    start_time=$(date +%s.%N)
    
    response=$(curl -s -X POST "$API_BASE/scrape" \
        -H "Content-Type: application/json" \
        -d '{"urls": ["https://httpbin.org/get", "https://httpbin.org/json", "https://httpbin.org/xml"]}')
    
    job_id=$(echo "$response" | jq -r '.job_id')
    log_info "Job ID: $job_id"
    
    # Wait max 45 seconds
    for i in {1..45}; do
        status_response=$(curl -s "$API_BASE/scrape/status?id=$job_id")
        status=$(echo "$status_response" | jq -r '.status')
        
        if [ "$status" = "completed" ]; then
            end_time=$(date +%s.%N)
            duration=$(echo "$end_time - $start_time" | bc)
            results=$(echo "$status_response" | jq -r '.results | length')
            log_success "Completed in ${duration}s - $results URLs scraped"
            return 0
        elif [ "$status" = "failed" ]; then
            log_error "Job failed"
            return 1
        fi
        
        sleep 1
    done
    
    log_warning "Test timed out after 45s"
    return 1
}

# Test 3: Error handling
test_error_handling() {
    log_info "Test 3: Error handling"
    
    response=$(curl -s -X POST "$API_BASE/scrape" \
        -H "Content-Type: application/json" \
        -d '{"urls": ["https://httpbin.org/status/404", "https://nonexistent-domain-12345.com"]}')
    
    job_id=$(echo "$response" | jq -r '.job_id')
    log_info "Job ID: $job_id"
    
    # Wait max 30 seconds
    for i in {1..30}; do
        status_response=$(curl -s "$API_BASE/scrape/status?id=$job_id")
        status=$(echo "$status_response" | jq -r '.status')
        
        if [ "$status" = "completed" ]; then
            results=$(echo "$status_response" | jq -r '.results | length')
            errors=$(echo "$status_response" | jq -r '.results[] | select(.error != null) | .error' | wc -l)
            log_success "Completed - $results URLs processed, $errors errors"
            return 0
        elif [ "$status" = "failed" ]; then
            log_error "Job failed"
            return 1
        fi
        
        sleep 1
    done
    
    log_warning "Test timed out after 30s"
    return 1
}

# Test 4: Concurrent jobs
test_concurrent_jobs() {
    log_info "Test 4: Concurrent jobs (3 jobs)"
    
    job_ids=()
    
    # Submit 3 jobs quickly
    for i in {1..3}; do
        response=$(curl -s -X POST "$API_BASE/scrape" \
            -H "Content-Type: application/json" \
            -d '{"urls": ["https://httpbin.org/get"]}')
        
        job_id=$(echo "$response" | jq -r '.job_id')
        job_ids+=("$job_id")
        log_info "Submitted job $i: $job_id"
    done
    
    # Monitor all jobs
    completed=0
    failed=0
    
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
    
    log_info "Concurrent test: $completed completed, $failed failed out of 3 jobs"
}

# Test 5: Performance metrics
test_metrics() {
    log_info "Test 5: Performance metrics"
    
    metrics=$(curl -s "$API_BASE/metrics")
    
    total_requests=$(echo "$metrics" | jq -r '.total_requests')
    successful_requests=$(echo "$metrics" | jq -r '.successful_requests')
    failed_requests=$(echo "$metrics" | jq -r '.failed_requests')
    success_rate=$(echo "$metrics" | jq -r '.success_rate')
    avg_response_time=$(echo "$metrics" | jq -r '.response_times.avg')
    requests_per_second=$(echo "$metrics" | jq -r '.requests_per_second')
    
    log_success "Total requests: $total_requests"
    log_success "Successful: $successful_requests"
    log_success "Failed: $failed_requests"
    log_success "Success rate: ${success_rate}%"
    log_success "Avg response time: $avg_response_time"
    log_success "Requests per second: $requests_per_second"
}

# Main execution
main() {
    log_info "Starting simple performance tests..."
    
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