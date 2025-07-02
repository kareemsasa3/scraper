# Scripts Directory

This directory contains various utility scripts for the Arachne web scraper project.

## Directory Structure

```
scripts/
├── performance/          # Performance testing scripts
│   ├── robust_performance_test.sh
│   ├── performance_test.sh
│   └── simple_performance_test.sh
├── workflows/            # GitHub Actions workflow testing
│   └── test-workflows.sh
├── interesting_sites_test.sh  # Site-specific testing
├── run.sh               # Main application runner
└── README.md           # This file
```

## Script Categories

### Performance Testing (`performance/`)
Scripts for testing the scraper's performance under various conditions:
- **`simple_performance_test.sh`** - Basic performance testing
- **`performance_test.sh`** - Comprehensive performance testing
- **`robust_performance_test.sh`** - Advanced performance testing with error handling

### Workflow Testing (`workflows/`)
Scripts for validating GitHub Actions workflows:
- **`test-workflows.sh`** - Validates workflow YAML files and tests Docker builds

### General Scripts
- **`interesting_sites_test.sh`** - Tests scraping on specific interesting websites
- **`run.sh`** - Main application launcher with configuration

## Usage

### Running Performance Tests
```bash
# Run all performance tests
./scripts/performance/robust_performance_test.sh

# Run basic performance test
./scripts/performance/simple_performance_test.sh

# Run comprehensive performance test
./scripts/performance/performance_test.sh
```

### Testing Workflows
```bash
# Test GitHub Actions workflows locally
./scripts/workflows/test-workflows.sh
```

### Running the Application
```bash
# Start the scraper application
./scripts/run.sh
```

### Testing Specific Sites
```bash
# Test scraping on interesting sites
./scripts/interesting_sites_test.sh
```

## Best Practices

1. **Always run scripts from the project root directory**
2. **Check script permissions**: `chmod +x scripts/*/*.sh`
3. **Review script output** for any errors or warnings
4. **Update scripts** when adding new features or changing configurations

## Contributing

When adding new scripts:
1. Place them in the appropriate subdirectory
2. Update this README with usage instructions
3. Ensure scripts are executable (`chmod +x`)
4. Test scripts thoroughly before committing 