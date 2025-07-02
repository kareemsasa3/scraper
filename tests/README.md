# Tests Directory

This directory contains comprehensive test files for the Arachne web scraper project, organized by test type and purpose.

## 📁 Directory Structure

```
tests/
├── integration/           # Integration tests
│   └── api_integration_test.go
├── e2e/                   # End-to-end tests
│   └── (future e2e tests)
├── fixtures/              # Test data and fixtures
│   └── test_urls.json
├── helpers/               # Test helper functions
│   └── test_helpers.go
└── README.md             # This file
```

## 🧪 Test Categories

### Unit Tests (`*_test.go` in source directories)
- **Location**: Same directory as source files (e.g., `api_test.go` next to `api.go`)
- **Purpose**: Test individual functions and methods
- **Scope**: Single package/component
- **Run with**: `go test ./...`

### Integration Tests (`tests/integration/`)
- **Purpose**: Test multiple components working together
- **Scope**: API endpoints, database interactions, external services
- **Examples**: Full HTTP request/response cycles, Redis operations
- **Run with**: `go test ./tests/integration/...`

### End-to-End Tests (`tests/e2e/`)
- **Purpose**: Test the complete application flow
- **Scope**: Full application stack (API + workers + storage)
- **Examples**: Complete scraping job lifecycle
- **Run with**: `go test ./tests/e2e/...`

### Test Helpers (`tests/helpers/`)
- **Purpose**: Shared utility functions for tests
- **Scope**: Common test setup, assertions, mock objects
- **Usage**: Imported by other test packages

### Test Fixtures (`tests/fixtures/`)
- **Purpose**: Test data, configuration files, sample responses
- **Scope**: Static data used across multiple tests
- **Examples**: Sample URLs, expected JSON responses

## 🚀 Running Tests

### All Tests
```bash
go test ./...
```

### Unit Tests Only
```bash
go test ./... -short
```

### Integration Tests
```bash
go test ./tests/integration/...
```

### End-to-End Tests
```bash
go test ./tests/e2e/...
```

### With Coverage
```bash
go test -cover ./...
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### With Verbose Output
```bash
go test -v ./...
```

## 📋 Test Guidelines

### Writing Unit Tests
1. Place test files next to source files
2. Use descriptive test names
3. Test both success and failure cases
4. Use table-driven tests for multiple scenarios
5. Mock external dependencies

### Writing Integration Tests
1. Use the `tests/integration/` directory
2. Test real HTTP endpoints
3. Use test fixtures for consistent data
4. Clean up test data after each test
5. Use test helpers for common operations

### Test Data Management
1. Use fixtures for static test data
2. Generate dynamic data in tests when needed
3. Clean up any created data
4. Use environment variables for configuration

## 🔧 Test Configuration

### Environment Variables
```bash
# Test-specific environment variables
TEST_REDIS_ADDR=localhost:6379
TEST_API_PORT=8081
TEST_LOG_LEVEL=error
```

### Test Tags
```bash
# Run only unit tests
go test -tags=unit ./...

# Run only integration tests
go test -tags=integration ./...

# Skip integration tests
go test -tags=!integration ./...
```

## 📊 Test Coverage

Maintain high test coverage:
- **Unit tests**: Aim for 80%+ coverage
- **Integration tests**: Cover all major workflows
- **E2E tests**: Cover critical user paths

## 🤝 Contributing

When adding new tests:
1. Place them in the appropriate directory
2. Follow existing naming conventions
3. Add test data to fixtures if needed
4. Update this README if adding new test categories
5. Ensure tests are fast and reliable 