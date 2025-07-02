# Contributing to Arachne

We're excited that you're interested in contributing to Arachne! This document provides guidelines for development, testing, and understanding our CI/CD pipeline.

## 🚀 Development Workflow

We follow a standard Git workflow using feature branches and Pull Requests.

1. **Create a feature branch:** `git checkout -b feature/my-new-feature`
2. **Make changes and commit:** Use [Conventional Commits](https://www.conventionalcommits.org/) for your messages (e.g., `feat: add new feature`).
3. **Push and create a Pull Request:** `git push origin feature/my-new-feature`.
4. Wait for automated tests to pass. All checks must be green before merging.

## 🧪 Testing

Run all local tests before pushing your changes:

```bash
go test -v -cover ./...
```

## 🤖 CI/CD with GitHub Actions

Our CI/CD pipeline ensures code quality and automates the release process.

### Automated Workflows

#### 1. **Test Workflow** (`.github/workflows/test.yml`)
- **Trigger**: Runs on every Pull Request to `main`
- **Purpose**: Acts as a quality gate to ensure no broken code is merged. It runs all Go unit tests.

#### 2. **Development Build Workflow** (`.github/workflows/build-on-main.yml`)
- **Trigger**: Runs when code is pushed to the `main` branch
- **Purpose**: Creates a development build for every commit on main
- **Actions**: Builds a Docker image and pushes it to Docker Hub with a tag corresponding to the Git commit SHA (e.g., `:1868284c...`)

#### 3. **Production Release Workflow** (`.github/workflows/release-versioned.yml`)
- **Trigger**: Runs when you create and push a Git tag formatted like `v*.*.*` (e.g., `v1.0.1`)
- **Purpose**: Creates an official, versioned production release
- **Actions**:
  - Builds and pushes a versioned Docker image (e.g., `:v1.0.1`)
  - Updates the `:latest` tag to point to this new version
  - Creates an official Release page on GitHub

## 🛠️ Setup for Forked Repositories

If you fork this repository and want to enable the release workflows, you will need to configure the following secrets in your forked repo's settings (Settings → Secrets and variables → Actions):

- `DOCKERHUB_USERNAME`: Your Docker Hub username
- `DOCKERHUB_TOKEN`: A Docker Hub Access Token with "Read & Write" permissions

### Creating a Docker Hub Access Token

1. Go to [Docker Hub](https://hub.docker.com/) → Account Settings → Security
2. Click "New Access Token"
3. Give it a name (e.g., "GitHub Actions")
4. Set permissions to "Read, Write, Delete"
5. Copy the token and add it as `DOCKERHUB_TOKEN` secret

## 🚀 Workflow Usage

### Daily Development
1. Create feature branch: `git checkout -b feature/new-feature`
2. Make changes and commit: `git commit -m "feat: add new feature"`
3. Push and create PR: `git push origin feature/new-feature`
4. GitHub Actions automatically runs tests
5. Merge when tests pass ✅

### Creating a Release
```bash
# Create and push a version tag
git tag v1.0.1
git push origin v1.0.1

# GitHub Actions automatically:
# - Builds Docker image with v1.0.1 tag
# - Pushes to Docker Hub
# - Creates GitHub Release
```

## 📊 Benefits

- **🛡️ Quality Gate**: Tests run automatically on every PR
- **🚀 Zero-Downtime Deployments**: Automated Docker builds
- **📦 Consistent Releases**: Standardized release process
- **🔍 Transparency**: All builds and tests are visible in GitHub
- **⚡ Speed**: No manual testing or deployment steps

## 🚀 Extending for Distributed Job Queue

- **Message Queue**: Swap the in-process goroutine for a message queue (RabbitMQ, Kafka, or Redis Streams/Lists)
- **Worker Scaling**: Run multiple scraper workers, each pulling jobs from the queue and updating job state in Redis
- **Load Balancing**: Distribute scraping load across multiple instances
- **High Availability**: Enable true horizontal scaling and fault tolerance

## 📈 Performance Features

- **Circuit Breaker**: Automatic failure detection and recovery
- **Rate Limiting**: Domain-specific and global rate limiting
- **Retry Logic**: Exponential backoff for transient failures
- **Metrics Collection**: Real-time performance monitoring
- **Health Checks**: System health monitoring 