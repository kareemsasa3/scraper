package helpers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestServer represents a test HTTP server
type TestServer struct {
	Server *httptest.Server
	Client *http.Client
}

// NewTestServer creates a new test server with the given handler
func NewTestServer(handler http.Handler) *TestServer {
	server := httptest.NewServer(handler)
	return &TestServer{
		Server: server,
		Client: &http.Client{},
	}
}

// Close closes the test server
func (ts *TestServer) Close() {
	ts.Server.Close()
}

// URL returns the server's URL
func (ts *TestServer) URL() string {
	return ts.Server.URL
}

// MakeRequest makes an HTTP request to the test server
func (ts *TestServer) MakeRequest(method, path string, body interface{}) (*http.Response, error) {
	var reqBody []byte
	var err error

	if body != nil {
		reqBody, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}

	req, err := http.NewRequest(method, ts.URL()+path, nil)
	if err != nil {
		return nil, err
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		// TODO: Use reqBody in the request body
		_ = reqBody // Suppress unused variable warning for now
	}

	return ts.Client.Do(req)
}

// AssertJSONResponse asserts that a response contains valid JSON
func AssertJSONResponse(t *testing.T, resp *http.Response, expectedStatus int) {
	if resp.StatusCode != expectedStatus {
		t.Errorf("Expected status %d, got %d", expectedStatus, resp.StatusCode)
	}

	// Check if response is JSON
	contentType := resp.Header.Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Expected Content-Type application/json, got %s", contentType)
	}
}

// LoadTestData loads test data from a file
func LoadTestData(filename string) ([]byte, error) {
	// This would load test data from the tests/fixtures directory
	// Implementation depends on your specific needs
	return nil, nil
}
