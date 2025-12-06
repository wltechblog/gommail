package ui

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestDebouncer_SingleCall tests that a single debounced call executes after the delay
func TestDebouncer_SingleCall(t *testing.T) {
	var callCount int32
	debouncer := NewDebouncer(100 * time.Millisecond)
	defer debouncer.Stop()

	debouncer.Debounce(func() {
		atomic.AddInt32(&callCount, 1)
	})

	// Wait for the debounce delay plus a buffer
	time.Sleep(150 * time.Millisecond)

	if count := atomic.LoadInt32(&callCount); count != 1 {
		t.Errorf("Expected function to be called once, got %d calls", count)
	}
}

// TestDebouncer_MultipleCalls tests that multiple rapid calls result in only one execution
func TestDebouncer_MultipleCalls(t *testing.T) {
	var callCount int32
	debouncer := NewDebouncer(100 * time.Millisecond)
	defer debouncer.Stop()

	// Call debounce multiple times rapidly
	for i := 0; i < 10; i++ {
		debouncer.Debounce(func() {
			atomic.AddInt32(&callCount, 1)
		})
		time.Sleep(10 * time.Millisecond) // Small delay between calls
	}

	// Wait for the debounce delay plus a buffer
	time.Sleep(150 * time.Millisecond)

	if count := atomic.LoadInt32(&callCount); count != 1 {
		t.Errorf("Expected function to be called once despite multiple triggers, got %d calls", count)
	}
}

// TestDebouncer_Stop tests that stopping the debouncer cancels pending calls
func TestDebouncer_Stop(t *testing.T) {
	var callCount int32
	debouncer := NewDebouncer(100 * time.Millisecond)

	debouncer.Debounce(func() {
		atomic.AddInt32(&callCount, 1)
	})

	// Stop immediately
	debouncer.Stop()

	// Wait to ensure the function would have been called
	time.Sleep(150 * time.Millisecond)

	if count := atomic.LoadInt32(&callCount); count != 0 {
		t.Errorf("Expected function not to be called after Stop(), got %d calls", count)
	}
}

// TestDebouncer_ConcurrentCalls tests thread safety with concurrent calls
func TestDebouncer_ConcurrentCalls(t *testing.T) {
	var callCount int32
	debouncer := NewDebouncer(100 * time.Millisecond)
	defer debouncer.Stop()

	// Launch multiple goroutines calling debounce concurrently
	for i := 0; i < 100; i++ {
		go func() {
			debouncer.Debounce(func() {
				atomic.AddInt32(&callCount, 1)
			})
		}()
	}

	// Wait for the debounce delay plus a buffer
	time.Sleep(200 * time.Millisecond)

	if count := atomic.LoadInt32(&callCount); count != 1 {
		t.Errorf("Expected function to be called once despite concurrent triggers, got %d calls", count)
	}
}

// TestCallbackDebouncer_SingleCallback tests that a single callback is invoked
func TestCallbackDebouncer_SingleCallback(t *testing.T) {
	var callCount int32
	var callbackCount int32
	debouncer := NewCallbackDebouncer(100 * time.Millisecond)
	defer debouncer.Stop()

	debouncer.Debounce(func() error {
		atomic.AddInt32(&callCount, 1)
		return nil
	}, func(err error) {
		atomic.AddInt32(&callbackCount, 1)
		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
	})

	// Wait for the debounce delay plus a buffer
	time.Sleep(150 * time.Millisecond)

	if count := atomic.LoadInt32(&callCount); count != 1 {
		t.Errorf("Expected function to be called once, got %d calls", count)
	}
	if count := atomic.LoadInt32(&callbackCount); count != 1 {
		t.Errorf("Expected callback to be called once, got %d calls", count)
	}
}

// TestCallbackDebouncer_MultipleCallbacks tests that all callbacks are invoked
func TestCallbackDebouncer_MultipleCallbacks(t *testing.T) {
	var callCount int32
	var callbackCount int32
	debouncer := NewCallbackDebouncer(100 * time.Millisecond)
	defer debouncer.Stop()

	// Register multiple callbacks
	for i := 0; i < 5; i++ {
		debouncer.Debounce(func() error {
			atomic.AddInt32(&callCount, 1)
			return nil
		}, func(err error) {
			atomic.AddInt32(&callbackCount, 1)
		})
		time.Sleep(10 * time.Millisecond)
	}

	// Wait for the debounce delay plus a buffer
	time.Sleep(150 * time.Millisecond)

	if count := atomic.LoadInt32(&callCount); count != 1 {
		t.Errorf("Expected function to be called once, got %d calls", count)
	}
	if count := atomic.LoadInt32(&callbackCount); count != 5 {
		t.Errorf("Expected all 5 callbacks to be called, got %d calls", count)
	}
}

// TestCallbackDebouncer_NilCallback tests that nil callbacks are handled gracefully
func TestCallbackDebouncer_NilCallback(t *testing.T) {
	var callCount int32
	debouncer := NewCallbackDebouncer(100 * time.Millisecond)
	defer debouncer.Stop()

	debouncer.Debounce(func() error {
		atomic.AddInt32(&callCount, 1)
		return nil
	}, nil) // nil callback

	// Wait for the debounce delay plus a buffer
	time.Sleep(150 * time.Millisecond)

	if count := atomic.LoadInt32(&callCount); count != 1 {
		t.Errorf("Expected function to be called once, got %d calls", count)
	}
}

// TestCallbackDebouncer_Stop tests that stopping clears callbacks
func TestCallbackDebouncer_Stop(t *testing.T) {
	var callCount int32
	var callbackCount int32
	debouncer := NewCallbackDebouncer(100 * time.Millisecond)

	debouncer.Debounce(func() error {
		atomic.AddInt32(&callCount, 1)
		return nil
	}, func(err error) {
		atomic.AddInt32(&callbackCount, 1)
	})

	// Stop immediately
	debouncer.Stop()

	// Wait to ensure the function would have been called
	time.Sleep(150 * time.Millisecond)

	if count := atomic.LoadInt32(&callCount); count != 0 {
		t.Errorf("Expected function not to be called after Stop(), got %d calls", count)
	}
	if count := atomic.LoadInt32(&callbackCount); count != 0 {
		t.Errorf("Expected callback not to be called after Stop(), got %d calls", count)
	}
}

// TestDebouncer_TimingAccuracy tests that the debounce delay is reasonably accurate
func TestDebouncer_TimingAccuracy(t *testing.T) {
	delay := 100 * time.Millisecond
	debouncer := NewDebouncer(delay)
	defer debouncer.Stop()

	start := time.Now()
	done := make(chan struct{})

	debouncer.Debounce(func() {
		close(done)
	})

	<-done
	elapsed := time.Since(start)

	// Allow 50ms tolerance for timing
	if elapsed < delay || elapsed > delay+50*time.Millisecond {
		t.Errorf("Expected delay around %v, got %v", delay, elapsed)
	}
}

// TestCallbackDebouncer_ConcurrentCallbacks tests thread safety with concurrent callbacks
func TestCallbackDebouncer_ConcurrentCallbacks(t *testing.T) {
	var callCount int32
	var callbackCount int32
	debouncer := NewCallbackDebouncer(100 * time.Millisecond)
	defer debouncer.Stop()

	// Launch multiple goroutines registering callbacks concurrently
	for i := 0; i < 50; i++ {
		go func() {
			debouncer.Debounce(func() error {
				atomic.AddInt32(&callCount, 1)
				return nil
			}, func(err error) {
				atomic.AddInt32(&callbackCount, 1)
			})
		}()
	}

	// Wait for the debounce delay plus a buffer
	time.Sleep(200 * time.Millisecond)

	if count := atomic.LoadInt32(&callCount); count != 1 {
		t.Errorf("Expected function to be called once, got %d calls", count)
	}
	if count := atomic.LoadInt32(&callbackCount); count != 50 {
		t.Errorf("Expected all 50 callbacks to be called, got %d calls", count)
	}
}

