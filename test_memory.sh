#!/bin/bash

# Test script for SQLite memory layer
# This script tests the memory/lookup endpoint and snapshot persistence

set -e

echo "🧪 Testing Arachne Memory Layer"
echo "================================"
echo ""

# Configuration
ARACHNE_URL="http://localhost:8080"
TEST_URL="https://example.com"

echo "1️⃣  Testing health endpoint..."
curl -s "${ARACHNE_URL}/health" | jq '.'
echo ""

echo "2️⃣  Checking memory for URL (should not exist initially)..."
ENCODED_URL=$(echo -n "$TEST_URL" | jq -sRr @uri)
curl -s "${ARACHNE_URL}/memory/lookup?url=${ENCODED_URL}" | jq '.'
echo ""

echo "3️⃣  Scraping test URL..."
JOB_RESPONSE=$(curl -s -X POST "${ARACHNE_URL}/scrape" \
  -H "Content-Type: application/json" \
  -d "{\"urls\": [\"${TEST_URL}\"]}")
JOB_ID=$(echo "$JOB_RESPONSE" | jq -r '.job_id')
echo "Job ID: $JOB_ID"
echo ""

echo "4️⃣  Waiting for job to complete..."
sleep 5

echo "5️⃣  Checking job status..."
curl -s "${ARACHNE_URL}/scrape/status?id=${JOB_ID}" | jq '.job | {id, status, progress}'
echo ""

echo "6️⃣  Checking memory again (should exist now)..."
curl -s "${ARACHNE_URL}/memory/lookup?url=${ENCODED_URL}" | jq '.'
echo ""

echo "7️⃣  Scraping same URL again (should update last_checked_at only)..."
JOB_RESPONSE2=$(curl -s -X POST "${ARACHNE_URL}/scrape" \
  -H "Content-Type: application/json" \
  -d "{\"urls\": [\"${TEST_URL}\"]}")
JOB_ID2=$(echo "$JOB_RESPONSE2" | jq -r '.job_id')
echo "Job ID: $JOB_ID2"
sleep 5
echo ""

echo "8️⃣  Checking memory final time (last_checked_at should be updated)..."
curl -s "${ARACHNE_URL}/memory/lookup?url=${ENCODED_URL}" | jq '.'
echo ""

echo "✅ Memory layer test complete!"
echo ""
echo "Expected behavior:"
echo "  - First memory lookup: found=false"
echo "  - After scrape: found=true with snapshot data"
echo "  - Second scrape: same snapshot ID, updated last_checked_at"

