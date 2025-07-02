#!/bin/bash

# Interesting Sites Test for Arachne Web Scraper
# Tests against challenging and diverse websites

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

# Wait for job completion
wait_for_job() {
    local job_id="$1"
    local test_name="$2"
    local max_wait="${3:-120}"
    
    log_info "Waiting for $test_name completion (job: $job_id)..."
    
    local start_time=$(date +%s)
    
    for i in $(seq 1 $max_wait); do
        local status_response
        if ! status_response=$(curl -s "$API_BASE/scrape/status?id=$job_id" 2>/dev/null); then
            log_warning "Failed to get status for job $job_id (attempt $i)"
            sleep 2
            continue
        fi
        
        local status
        if ! status=$(echo "$status_response" | jq -r '.job.status' 2>/dev/null); then
            log_warning "Failed to parse status response (attempt $i)"
            sleep 2
            continue
        fi
        
        case "$status" in
            "completed")
                local end_time=$(date +%s)
                local duration=$((end_time - start_time))
                local results=$(echo "$status_response" | jq -r '.job.results | length')
                log_success "$test_name completed in ${duration}s - $results URLs scraped"
                
                # Show some interesting results
                echo "$status_response" | jq -r '.job.results[] | "  - \(.url): \(.status) (\(.size) bytes)"'
                return 0
                ;;
            "failed")
                log_error "$test_name failed"
                echo "$status_response" | jq -r '.job.error // "Unknown error"'
                return 1
                ;;
            "processing"|"pending"|"running")
                if [ $((i % 10)) -eq 0 ]; then
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

# Test 1: Tech News & Developer Sites
test_tech_sites() {
    log_info "Test 1: Tech News & Developer Sites"
    
    local job_id
    if ! job_id=$(submit_job '[
        "https://news.ycombinator.com",
        "https://reddit.com/r/programming",
        "https://stackoverflow.com",
        "https://github.com/trending",
        "https://dev.to"
    ]'); then
        return 1
    fi
    
    wait_for_job "$job_id" "Tech sites test" 180
}

# Test 2: Dynamic Content & JavaScript Heavy
test_dynamic_sites() {
    log_info "Test 2: Dynamic Content & JavaScript Heavy Sites"
    
    local job_id
    if ! job_id=$(submit_job '[
        "https://reactjs.org",
        "https://vuejs.org",
        "https://angular.io",
        "https://svelte.dev",
        "https://nextjs.org"
    ]'); then
        return 1
    fi
    
    wait_for_job "$job_id" "Dynamic sites test" 180
}

# Test 3: E-commerce & High-Traffic Sites
test_ecommerce_sites() {
    log_info "Test 3: E-commerce & High-Traffic Sites"
    
    local job_id
    if ! job_id=$(submit_job '[
        "https://amazon.com",
        "https://ebay.com",
        "https://etsy.com",
        "https://shopify.com",
        "https://stripe.com"
    ]'); then
        return 1
    fi
    
    wait_for_job "$job_id" "E-commerce sites test" 180
}

# Test 4: API Documentation & Developer Tools
test_api_docs() {
    log_info "Test 4: API Documentation & Developer Tools"
    
    local job_id
    if ! job_id=$(submit_job '[
        "https://docs.github.com/en/rest",
        "https://developer.mozilla.org/en-US/docs/Web/API",
        "https://swagger.io",
        "https://postman.com",
        "https://insomnia.rest"
    ]'); then
        return 1
    fi
    
    wait_for_job "$job_id" "API docs test" 180
}

# Test 5: International & Multi-language Sites
test_international_sites() {
    log_info "Test 5: International & Multi-language Sites"
    
    local job_id
    if ! job_id=$(submit_job '[
        "https://wikipedia.org",
        "https://bbc.com",
        "https://lemonde.fr",
        "https://spiegel.de",
        "https://asahi.com"
    ]'); then
        return 1
    fi
    
    wait_for_job "$job_id" "International sites test" 180
}

# Test 6: Real-time Data & Streaming
test_realtime_sites() {
    log_info "Test 6: Real-time Data & Streaming Sites"
    
    local job_id
    if ! job_id=$(submit_job '[
        "https://twitch.tv",
        "https://youtube.com",
        "https://twitter.com",
        "https://discord.com",
        "https://slack.com"
    ]'); then
        return 1
    fi
    
    wait_for_job "$job_id" "Real-time sites test" 180
}

# Test 7: Government & Educational Sites
test_gov_edu_sites() {
    log_info "Test 7: Government & Educational Sites"
    
    local job_id
    if ! job_id=$(submit_job '[
        "https://whitehouse.gov",
        "https://nasa.gov",
        "https://mit.edu",
        "https://stanford.edu",
        "https://coursera.org"
    ]'); then
        return 1
    fi
    
    wait_for_job "$job_id" "Government/Education sites test" 180
}

# Test 8: Creative & Media Sites
test_creative_sites() {
    log_info "Test 8: Creative & Media Sites"
    
    local job_id
    if ! job_id=$(submit_job '[
        "https://behance.net",
        "https://dribbble.com",
        "https://flickr.com",
        "https://500px.com",
        "https://artstation.com"
    ]'); then
        return 1
    fi
    
    wait_for_job "$job_id" "Creative sites test" 180
}

# Main execution
main() {
    log_info "Starting interesting sites performance tests..."
    log_info "This will test your scraper against diverse, challenging websites!"
    
    # Check API health first
    if ! curl -s "$API_BASE/health" > /dev/null; then
        log_error "API is not responding"
        exit 1
    fi
    
    log_success "API is healthy - starting tests..."
    
    # Run tests
    test_tech_sites
    echo "---"
    test_dynamic_sites
    echo "---"
    test_ecommerce_sites
    echo "---"
    test_api_docs
    echo "---"
    test_international_sites
    echo "---"
    test_realtime_sites
    echo "---"
    test_gov_edu_sites
    echo "---"
    test_creative_sites
    
    log_success "All interesting sites tests completed!"
    log_info "Check the results above to see how your scraper handles diverse content!"
}

main "$@" 