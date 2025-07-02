package main

import (
	"errors"
	"testing"
	"time"
)

func TestCircuitBreakerStates(t *testing.T) {
	cb := NewCircuitBreaker(2, 100*time.Millisecond)

	// Test initial state
	if cb.getState() != StateClosed {
		t.Errorf("Expected initial state to be CLOSED, got %s", cb.getState())
	}

	// Test transition to open after failures
	err := cb.Execute(func() error { return errors.New("test error") })
	if err == nil {
		t.Error("Expected error, got nil")
	}

	err = cb.Execute(func() error { return errors.New("test error") })
	if err == nil {
		t.Error("Expected error, got nil")
	}

	// Should be open now
	if cb.getState() != StateOpen {
		t.Errorf("Expected state to be OPEN after failures, got %s", cb.getState())
	}

	// Test that circuit breaker blocks requests when open
	err = cb.Execute(func() error { return nil })
	if err == nil {
		t.Error("Expected circuit breaker error, got nil")
	}
	if !IsCircuitBreakerError(err) {
		t.Error("Expected circuit breaker error type")
	}

	// Wait for reset timeout
	time.Sleep(200 * time.Millisecond)
	// Trigger state check/transition
	cb.canExecute()

	// Should be half-open now
	if cb.getState() != StateHalfOpen {
		t.Errorf("Expected state to be HALF_OPEN after timeout, got %s", cb.getState())
	}

	// Test successful request in half-open state
	err = cb.Execute(func() error { return nil })
	if err != nil {
		t.Errorf("Expected success, got error: %v", err)
	}

	// Should be closed now
	if cb.getState() != StateClosed {
		t.Errorf("Expected state to be CLOSED after success, got %s", cb.getState())
	}
}

func TestCircuitBreakerStats(t *testing.T) {
	cb := NewCircuitBreaker(3, 50*time.Millisecond) // Higher threshold to allow all requests

	// Execute some requests
	_ = cb.Execute(func() error { return errors.New("error1") })
	_ = cb.Execute(func() error { return nil })
	_ = cb.Execute(func() error { return errors.New("error2") })

	stats := cb.GetStats()

	// Check basic stats
	if stats["total_requests"] != int64(3) {
		t.Errorf("Expected 3 total requests, got %v", stats["total_requests"])
	}

	if stats["total_failures"] != int64(2) {
		t.Errorf("Expected 2 total failures, got %v", stats["total_failures"])
	}

	if stats["total_successes"] != int64(1) {
		t.Errorf("Expected 1 total success, got %v", stats["total_successes"])
	}

	// Check failure rate
	failureRate := stats["failure_rate"].(float64)
	expectedRate := 66.67 // 2/3 * 100
	if failureRate < expectedRate-1 || failureRate > expectedRate+1 {
		t.Errorf("Expected failure rate around %.2f%%, got %.2f%%", expectedRate, failureRate)
	}
}

func TestCircuitBreakerErrorType(t *testing.T) {
	cb := NewCircuitBreaker(1, 10*time.Millisecond)

	// Make it fail once to open the circuit
	_ = cb.Execute(func() error { return errors.New("test error") })

	// Try to execute when circuit is open
	err := cb.Execute(func() error { return nil })

	if !IsCircuitBreakerError(err) {
		t.Error("Expected circuit breaker error type")
	}

	cbErr, ok := err.(*CircuitBreakerError)
	if !ok {
		t.Error("Expected CircuitBreakerError type")
	}

	if cbErr.State != StateOpen {
		t.Errorf("Expected OPEN state, got %s", cbErr.State)
	}
}

func TestCircuitBreakerConcurrentAccess(t *testing.T) {
	cb := NewCircuitBreaker(5, 100*time.Millisecond)
	done := make(chan bool, 10)

	// Test concurrent access
	for i := 0; i < 10; i++ {
		go func() {
			_ = cb.Execute(func() error { return errors.New("concurrent error") })
			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}

	// Should be open after 5 failures
	if cb.getState() != StateOpen {
		t.Errorf("Expected state to be OPEN after concurrent failures, got %s", cb.getState())
	}
}

func BenchmarkCircuitBreakerExecute(b *testing.B) {
	cb := NewCircuitBreaker(10, 100*time.Millisecond)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cb.Execute(func() error { return nil })
	}
}
