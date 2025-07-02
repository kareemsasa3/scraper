# GitHub Actions Setup Guide

## 🚀 Quick Setup Checklist

### 1. Repository Setup ✅
- [x] GitHub Actions workflows created
- [x] Test script created
- [x] Documentation updated

### 2. Docker Hub Setup (Required for releases)
- [ ] Create Docker Hub Access Token
- [ ] Add secrets to GitHub repository

### 3. GitHub Repository Settings
- [ ] Add Docker Hub secrets
- [ ] Enable branch protection (recommended)

## 🔧 Docker Hub Access Token Setup

1. **Go to Docker Hub**: https://hub.docker.com/
2. **Login** to your account
3. **Navigate to**: Account Settings → Security
4. **Click**: "New Access Token"
5. **Configure**:
   - Name: `GitHub Actions`
   - Permissions: `Read, Write, Delete`
6. **Copy** the generated token

## 🔐 GitHub Secrets Setup

1. **Go to your repository**: https://github.com/kareemsasa3/arachne
2. **Navigate to**: Settings → Secrets and variables → Actions
3. **Add these secrets**:
   - `DOCKERHUB_USERNAME`: `kareemsasa3`
   - `DOCKERHUB_TOKEN`: [Your Docker Hub Access Token]

## 🛡️ Branch Protection (Recommended)

1. **Go to**: Settings → Branches
2. **Add rule** for `main` branch
3. **Enable**:
   - ✅ "Require status checks to pass before merging"
   - ✅ "Require branches to be up to date before merging"
4. **Select**: "Run Go Tests" as required status check

## 🧪 Testing Your Setup

### Local Testing
```bash
# Run the test script
./scripts/test-workflows.sh
```

### GitHub Testing (Step by Step)

#### Phase 1: Test Without Docker Hub Secrets
1. **Start with the test-only workflow** (no secrets required):
   ```bash
   git checkout -b test/github-actions
   git add .
   git commit -m "Add GitHub Actions workflows"
   git push origin test/github-actions
   ```

2. **Create a Pull Request** on GitHub
3. **Watch the Actions tab** - you should see "Test Only (No Docker)" workflow run
4. **Verify tests pass** ✅

#### Phase 2: Add Docker Hub Secrets (Optional)
5. **Set up Docker Hub Access Token** (see Docker Hub Setup section above)
6. **Add secrets to GitHub repository** (see GitHub Secrets Setup section above)
7. **Merge the PR** - this will trigger the full release workflow
8. **Verify Docker image is pushed** to Docker Hub

### Understanding the Warnings
- **"Context access might be invalid"** warnings are normal when secrets aren't configured yet
- These warnings will disappear once you add the Docker Hub secrets
- The workflows include validation steps that will give clear error messages if secrets are missing

## 📋 Workflow Triggers

| Workflow | Trigger | Purpose |
|----------|---------|---------|
| `test.yml` | Pull Request to `main` | Run tests |
| `release.yml` | Push to `main` | Build & push Docker image |
| `release-versioned.yml` | Git tag (`v*`) | Create versioned release |

## 🚀 Creating a Release

```bash
# 1. Create a version tag
git tag v1.0.1

# 2. Push the tag
git push origin v1.0.1

# 3. GitHub Actions automatically:
#    - Builds Docker image with v1.0.1 tag
#    - Pushes to Docker Hub
#    - Creates GitHub Release
```

## 🔍 Monitoring Workflows

- **Actions Tab**: View all workflow runs
- **Pull Request**: See test results inline
- **Docker Hub**: Check for new images
- **Releases**: View created releases

## 🐛 Troubleshooting

### Common Issues

1. **Docker Hub Authentication Failed**
   - Check `DOCKERHUB_TOKEN` secret is correct
   - Verify token has "Read, Write, Delete" permissions

2. **Tests Failing**
   - Run `go test ./...` locally first
   - Check for missing dependencies

3. **Workflow Not Triggering**
   - Verify branch name is `main`
   - Check workflow file syntax

### Getting Help

- **GitHub Actions Logs**: Check the Actions tab for detailed error messages
- **Local Testing**: Use `./scripts/test-workflows.sh` to catch issues early
- **YAML Validation**: GitHub will show syntax errors in the Actions tab

## 📈 Next Steps

Once this is working, consider:

1. **Add more tests** to increase coverage
2. **Set up deployment** to staging/production
3. **Add security scanning** (e.g., CodeQL)
4. **Configure notifications** for failed builds
5. **Add performance testing** to the pipeline 