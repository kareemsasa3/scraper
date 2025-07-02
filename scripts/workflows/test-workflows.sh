#!/bin/bash

# Test script to validate GitHub Actions workflows locally
# This helps catch issues before pushing to GitHub

set -e

echo "🧪 Testing GitHub Actions Workflows Locally"
echo "=========================================="

# Test 1: Verify Go tests pass
echo "📋 Running Go tests..."
if go test -v ./...; then
    echo "✅ Go tests passed"
else
    echo "❌ Go tests failed"
    exit 1
fi

# Test 2: Verify Docker build works
echo "🐳 Testing Docker build..."
if docker build -t arachne-test .; then
    echo "✅ Docker build successful"
    # Clean up test image
    docker rmi arachne-test
else
    echo "❌ Docker build failed"
    exit 1
fi

# Test 3: Verify workflow files are valid YAML
echo "📄 Validating workflow YAML files..."
for workflow in .github/workflows/*.yml; do
    if [ -f "$workflow" ]; then
        echo "  Checking $workflow..."
        # Simple YAML validation using grep to check for basic syntax
        if grep -q "^name:" "$workflow" && grep -q "^on:" "$workflow" && grep -q "^jobs:" "$workflow"; then
            echo "    ✅ Valid YAML structure"
        else
            echo "    ❌ Invalid YAML structure"
            exit 1
        fi
    fi
done

echo ""
echo "🎉 All tests passed! Your workflows are ready for GitHub."
echo ""
echo "Next steps:"
echo "1. Commit and push these changes to GitHub"
echo "2. Set up Docker Hub secrets in GitHub repository settings"
echo "3. Create a test PR to see the workflows in action" 