package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/kareemsasa3/arachne/internal/types"
)

// StorageBackend defines the interface for different storage backends
type StorageBackend interface {
	Save(ctx context.Context, data []types.ScrapedData) error
	Load(ctx context.Context) ([]types.ScrapedData, error)
	Close() error
}

// JSONStorage implements StorageBackend for JSON file storage
type JSONStorage struct {
	filename string
}

// NewJSONStorage creates a new JSON storage backend
func NewJSONStorage(filename string) *JSONStorage {
	return &JSONStorage{filename: filename}
}

// Save saves scraped data to JSON file
func (j *JSONStorage) Save(ctx context.Context, data []types.ScrapedData) error {
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %v", err)
	}

	return os.WriteFile(j.filename, jsonData, 0644)
}

// Load loads scraped data from JSON file
func (j *JSONStorage) Load(ctx context.Context) ([]types.ScrapedData, error) {
	data, err := os.ReadFile(j.filename)
	if err != nil {
		if os.IsNotExist(err) {
			return []types.ScrapedData{}, nil
		}
		return nil, fmt.Errorf("failed to read file: %v", err)
	}

	var results []types.ScrapedData
	if err := json.Unmarshal(data, &results); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %v", err)
	}

	return results, nil
}

// Close implements StorageBackend interface
func (j *JSONStorage) Close() error {
	return nil
}

// MemoryStorage implements StorageBackend for in-memory storage (useful for testing)
type MemoryStorage struct {
	data []types.ScrapedData
}

// NewMemoryStorage creates a new in-memory storage backend
func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{data: make([]types.ScrapedData, 0)}
}

// Save saves scraped data to memory
func (m *MemoryStorage) Save(ctx context.Context, data []types.ScrapedData) error {
	m.data = append(m.data, data...)
	return nil
}

// Load loads scraped data from memory
func (m *MemoryStorage) Load(ctx context.Context) ([]types.ScrapedData, error) {
	return m.data, nil
}

// Close implements StorageBackend interface
func (m *MemoryStorage) Close() error {
	return nil
}

// StorageManager manages storage operations
type StorageManager struct {
	backend StorageBackend
}

// NewStorageManager creates a new storage manager
func NewStorageManager(backend StorageBackend) *StorageManager {
	return &StorageManager{backend: backend}
}

// SaveResults saves scraping results
func (sm *StorageManager) SaveResults(ctx context.Context, results []types.ScrapedData) error {
	return sm.backend.Save(ctx, results)
}

// LoadResults loads previously saved results
func (sm *StorageManager) LoadResults(ctx context.Context) ([]types.ScrapedData, error) {
	return sm.backend.Load(ctx)
}

// Close closes the storage backend
func (sm *StorageManager) Close() error {
	return sm.backend.Close()
}

// RedisStorage implements persistent job storage using Redis
type RedisStorage struct {
	client *redis.Client
}

// NewRedisStorage creates a new Redis storage instance
func NewRedisStorage(addr string, password string, db int) (*RedisStorage, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})

	// Test the connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	return &RedisStorage{client: client}, nil
}

// SaveJob persists a job to Redis
func (r *RedisStorage) SaveJob(ctx context.Context, job *ScrapingJob) error {
	jobData, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("failed to marshal job: %w", err)
	}

	key := fmt.Sprintf("job:%s", job.ID)
	err = r.client.Set(ctx, key, jobData, 24*time.Hour).Err() // Jobs expire after 24 hours
	if err != nil {
		return fmt.Errorf("failed to save job to Redis: %w", err)
	}

	// Also add to a set of all job IDs for easy listing
	err = r.client.SAdd(ctx, "jobs:all", job.ID).Err()
	if err != nil {
		return fmt.Errorf("failed to add job to jobs set: %w", err)
	}

	return nil
}

// GetJob retrieves a job from Redis
func (r *RedisStorage) GetJob(ctx context.Context, jobID string) (*ScrapingJob, error) {
	key := fmt.Sprintf("job:%s", jobID)
	jobData, err := r.client.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, fmt.Errorf("job not found: %s", jobID)
		}
		return nil, fmt.Errorf("failed to get job from Redis: %w", err)
	}

	var job ScrapingJob
	if err := json.Unmarshal([]byte(jobData), &job); err != nil {
		return nil, fmt.Errorf("failed to unmarshal job: %w", err)
	}

	return &job, nil
}

// UpdateJob updates an existing job in Redis
func (r *RedisStorage) UpdateJob(ctx context.Context, job *ScrapingJob) error {
	return r.SaveJob(ctx, job) // SaveJob handles both create and update
}

// ListJobs retrieves all job IDs from Redis
func (r *RedisStorage) ListJobs(ctx context.Context) ([]string, error) {
	jobIDs, err := r.client.SMembers(ctx, "jobs:all").Result()
	if err != nil {
		return nil, fmt.Errorf("failed to list jobs from Redis: %w", err)
	}
	return jobIDs, nil
}

// GetJobsByStatus retrieves jobs filtered by status
func (r *RedisStorage) GetJobsByStatus(ctx context.Context, status string) ([]*ScrapingJob, error) {
	allJobIDs, err := r.ListJobs(ctx)
	if err != nil {
		return nil, err
	}

	var jobs []*ScrapingJob
	for _, jobID := range allJobIDs {
		job, err := r.GetJob(ctx, jobID)
		if err != nil {
			// Skip jobs that can't be retrieved (might be expired)
			continue
		}
		if job.Status == status {
			jobs = append(jobs, job)
		}
	}

	return jobs, nil
}

// DeleteJob removes a job from Redis
func (r *RedisStorage) DeleteJob(ctx context.Context, jobID string) error {
	key := fmt.Sprintf("job:%s", jobID)

	// Remove from jobs set
	err := r.client.SRem(ctx, "jobs:all", jobID).Err()
	if err != nil {
		return fmt.Errorf("failed to remove job from jobs set: %w", err)
	}

	// Remove the job data
	err = r.client.Del(ctx, key).Err()
	if err != nil {
		return fmt.Errorf("failed to delete job from Redis: %w", err)
	}

	return nil
}

// Close closes the Redis connection
func (r *RedisStorage) Close() error {
	return r.client.Close()
}

// InMemoryStorage implements in-memory job storage (fallback)
type InMemoryStorage struct {
	jobs map[string]*ScrapingJob
}

// NewInMemoryStorage creates a new in-memory storage instance
func NewInMemoryStorage() *InMemoryStorage {
	return &InMemoryStorage{
		jobs: make(map[string]*ScrapingJob),
	}
}

// SaveJob stores a job in memory
func (m *InMemoryStorage) SaveJob(ctx context.Context, job *ScrapingJob) error {
	m.jobs[job.ID] = job
	return nil
}

// GetJob retrieves a job from memory
func (m *InMemoryStorage) GetJob(ctx context.Context, jobID string) (*ScrapingJob, error) {
	job, exists := m.jobs[jobID]
	if !exists {
		return nil, fmt.Errorf("job not found: %s", jobID)
	}
	return job, nil
}

// UpdateJob updates an existing job in memory
func (m *InMemoryStorage) UpdateJob(ctx context.Context, job *ScrapingJob) error {
	return m.SaveJob(ctx, job)
}

// ListJobs retrieves all job IDs from memory
func (m *InMemoryStorage) ListJobs(ctx context.Context) ([]string, error) {
	var jobIDs []string
	for jobID := range m.jobs {
		jobIDs = append(jobIDs, jobID)
	}
	return jobIDs, nil
}

// GetJobsByStatus retrieves jobs filtered by status
func (m *InMemoryStorage) GetJobsByStatus(ctx context.Context, status string) ([]*ScrapingJob, error) {
	var jobs []*ScrapingJob
	for _, job := range m.jobs {
		if job.Status == status {
			jobs = append(jobs, job)
		}
	}
	return jobs, nil
}

// DeleteJob removes a job from memory
func (m *InMemoryStorage) DeleteJob(ctx context.Context, jobID string) error {
	delete(m.jobs, jobID)
	return nil
}

// Close is a no-op for in-memory storage
func (m *InMemoryStorage) Close() error {
	return nil
}

// ScrapeRequest represents a scraping request
type ScrapeRequest struct {
	URLs              []string                 `json:"urls"`
	SiteURL           string                   `json:"site_url,omitempty"`
	PaginationConfig  *types.PaginationConfig  `json:"pagination_config,omitempty"`
}

// ScrapingJob represents an asynchronous scraping job
type ScrapingJob struct {
	ID          string              `json:"id"`
	Status      string              `json:"status"` // "pending", "running", "completed", "failed"
	Request     ScrapeRequest       `json:"request"`
	Results     []types.ScrapedData `json:"results,omitempty"`
	Error       string              `json:"error,omitempty"`
	CreatedAt   time.Time           `json:"created_at"`
	StartedAt   *time.Time          `json:"started_at,omitempty"`
	CompletedAt *time.Time          `json:"completed_at,omitempty"`
	Progress    int                 `json:"progress"` // 0-100
}
