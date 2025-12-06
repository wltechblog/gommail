package ui

import (
	"sync"
	"time"
)

// Debouncer provides a simple debouncing mechanism for function calls.
// It ensures that a function is only called once after a specified delay,
// even if it's triggered multiple times during that period.
//
// This is useful for rate-limiting expensive operations like UI refreshes,
// network requests, or file system operations.
//
// Example usage:
//
//	debouncer := NewDebouncer(500 * time.Millisecond)
//	defer debouncer.Stop()
//
//	// This will only execute once, 500ms after the last call
//	debouncer.Debounce(func() {
//	    fmt.Println("Expensive operation")
//	})
type Debouncer struct {
	delay time.Duration
	timer *time.Timer
	mu    sync.Mutex
}

// NewDebouncer creates a new Debouncer with the specified delay.
// The delay determines how long to wait after the last call before
// executing the debounced function.
func NewDebouncer(delay time.Duration) *Debouncer {
	return &Debouncer{
		delay: delay,
	}
}

// Debounce schedules the given function to be called after the delay period.
// If Debounce is called again before the delay expires, the previous call
// is cancelled and a new delay period begins.
//
// This method is safe to call from multiple goroutines.
func (d *Debouncer) Debounce(fn func()) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Cancel any existing timer
	if d.timer != nil {
		d.timer.Stop()
	}

	// Create a new timer that will call the function after the delay
	d.timer = time.AfterFunc(d.delay, fn)
}

// Stop cancels any pending debounced function call.
// It's safe to call Stop multiple times.
func (d *Debouncer) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
}

// CallbackDebouncer is a specialized debouncer that supports callbacks.
// It's useful when you need to pass different callbacks to the same debounced operation.
type CallbackDebouncer struct {
	delay     time.Duration
	timer     *time.Timer
	mu        sync.Mutex
	callbacks []func(error)
}

// NewCallbackDebouncer creates a new CallbackDebouncer with the specified delay.
func NewCallbackDebouncer(delay time.Duration) *CallbackDebouncer {
	return &CallbackDebouncer{
		delay:     delay,
		callbacks: make([]func(error), 0),
	}
}

// Debounce schedules the given function to be called after the delay period,
// and registers a callback to be invoked when the operation completes.
//
// All callbacks registered during the debounce period will be called when
// the operation finally executes.
func (d *CallbackDebouncer) Debounce(fn func() error, callback func(error)) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Add callback to the list
	if callback != nil {
		d.callbacks = append(d.callbacks, callback)
	}

	// Cancel any existing timer
	if d.timer != nil {
		d.timer.Stop()
	}

	// Create a new timer that will call the function after the delay
	d.timer = time.AfterFunc(d.delay, func() {
		// Execute the function
		err := fn()

		// Call all registered callbacks
		d.mu.Lock()
		callbacks := d.callbacks
		d.callbacks = make([]func(error), 0) // Clear callbacks
		d.mu.Unlock()

		for _, cb := range callbacks {
			cb(err)
		}
	})
}

// Stop cancels any pending debounced function call and clears all callbacks.
func (d *CallbackDebouncer) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
	d.callbacks = make([]func(error), 0)
}

