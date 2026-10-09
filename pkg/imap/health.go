package imap

// Code in this file was mechanically split from worker.go.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
)

// performComprehensiveHealthCheck performs a thorough health check of the IMAP connection
func (w *IMAPWorker) performComprehensiveHealthCheck() (bool, error) {
	// commandClient can be set to nil concurrently (e.g. force disconnect / reconnect).
	// Capture a stable reference for this health check to avoid nil dereference.
	w.mu.RLock()
	client := w.commandClient
	w.mu.RUnlock()
	if client == nil {
		return false, fmt.Errorf("command client not available")
	}

	// Test 1: Basic NOOP command with timeout
	w.logger.Debug("Performing NOOP command for health check")

	// Create a context with timeout for the health check
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Channel to receive the result
	done := make(chan error, 1)

	// Perform NOOP in a goroutine with timeout
	go func(c *imapclient.Client) {
		// c may be closed, but should never be nil here. If it is, fail gracefully.
		if c == nil {
			done <- fmt.Errorf("command client not available")
			return
		}
		noopCmd := c.Noop()
		if noopCmd == nil {
			done <- fmt.Errorf("NOOP command returned nil")
			return
		}
		done <- noopCmd.Wait()
	}(client)

	// Wait for completion or timeout
	select {
	case err := <-done:
		if err != nil {
			w.logger.Debug("NOOP command failed: %v", err)
			return false, wrapCommandError("NOOP", err)
		}
		w.logger.Debug("NOOP command succeeded")
		// NOOP succeeded, now check IDLE health
		return w.checkIDLEHealth()

	case <-ctx.Done():
		w.logger.Debug("NOOP command timed out")
		return false, fmt.Errorf("NOOP command timed out after 10 seconds")
	}
}

// checkIDLEHealth verifies that IDLE monitoring is working correctly
func (w *IMAPWorker) checkIDLEHealth() (bool, error) {
	w.mu.RLock()
	idleActive := w.idleActive
	idlePaused := w.idlePaused
	idleFolder := w.idleFolder
	lastActivity := w.lastIDLEActivity
	w.mu.RUnlock()

	// If IDLE is supposed to be active and not paused, verify it's actually running
	if idleActive && !idlePaused {
		// Check if IDLE activity timestamp is too old (more than 2x the IDLE timeout)
		// This indicates the IDLE loop may have died without restarting
		maxIdleAge := w.idleTimeout * 2
		idleAge := time.Since(lastActivity)

		if idleAge > maxIdleAge {
			w.logger.Warn("IDLE monitoring appears stuck for folder %s: last activity was %v ago (max expected: %v)",
				idleFolder, idleAge, maxIdleAge)
			return false, fmt.Errorf("IDLE monitoring stuck: last activity %v ago", idleAge)
		}

		w.logger.Debug("IDLE health check passed: folder=%s, last activity=%v ago", idleFolder, idleAge)
	}

	return true, nil
}

// handleHealthCheck handles the health check command
func (w *IMAPWorker) handleHealthCheck(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling health check command")

	if w.commandClient == nil {
		w.logger.Warn("Health check: command client is nil, triggering reconnection")
		w.bgGo(w.attemptAutomaticReconnection)

		return NewResponse(cmd.ID, false, map[string]interface{}{
			DataHealthStatus: w.GetHealthStatus(),
		}, fmt.Errorf("not connected"))
	}

	// Perform comprehensive health check
	healthy, err := w.performComprehensiveHealthCheck()

	if !healthy {
		w.logger.Warn("Connection health check failed: %v", err)
		w.incrementErrorCount()

		// Check if this is an IDLE monitoring issue
		if err != nil && strings.Contains(err.Error(), "IDLE monitoring stuck") {
			w.logger.Warn("IDLE monitoring stuck detected, attempting to restart IDLE")
			w.bgGo(w.recoverStuckIDLE)
		} else if w.isConnectionError(err) {
			// If health check fails with connection error, attempt automatic reconnection
			w.logger.Info("Connection error detected during health check, attempting automatic reconnection")
			w.bgGo(w.attemptAutomaticReconnection)
		}
	}

	// Get detailed health status
	healthStatus := w.GetHealthStatus()
	healthStatus["connection_healthy"] = healthy
	healthStatus["last_health_check"] = time.Now().Format(time.RFC3339)

	if !healthy {
		return NewResponse(cmd.ID, false, map[string]interface{}{
			DataHealthStatus: healthStatus,
		}, fmt.Errorf("connection health check failed: %w", err))
	}

	return NewResponse(cmd.ID, true, map[string]interface{}{
		DataHealthStatus: healthStatus,
	}, nil)
}

// handleGetStatus handles the get status command
func (w *IMAPWorker) handleGetStatus(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling get status command")

	status := w.GetStatus()
	return NewResponse(cmd.ID, true, map[string]interface{}{
		DataWorkerStatus: status,
	}, nil)
}

// getHealthTicker returns the health check ticker channel, or nil if not active
func (w *IMAPWorker) getHealthTicker() <-chan time.Time {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.healthTicker != nil {
		return w.healthTicker.C
	}
	return nil
}

// SetHealthCheckInterval configures the health check interval
func (w *IMAPWorker) SetHealthCheckInterval(interval time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.healthCheckInterval = interval

	// Restart the ticker if it's running
	if w.healthTicker != nil {
		w.healthTicker.Stop()
		w.healthTicker = time.NewTicker(interval)
	}
}

// SetReconnectConfig configures reconnection parameters
func (w *IMAPWorker) SetReconnectConfig(delay, maxDelay time.Duration, maxAttempts int) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.reconnectDelay = delay
	w.maxReconnectDelay = maxDelay
	w.maxReconnectAttempts = maxAttempts
}

// GetHealthStatus returns the current health status
func (w *IMAPWorker) GetHealthStatus() map[string]interface{} {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return map[string]interface{}{
		"state":                  w.state.String(),
		"last_health_check":      w.lastHealthCheck,
		"reconnect_attempt":      w.reconnectAttempt,
		"max_reconnect_attempts": w.maxReconnectAttempts,
		"health_check_interval":  w.healthCheckInterval,
		"error_count":            w.errorCount,
	}
}

// GetState returns the current connection state
func (w *IMAPWorker) GetState() ConnectionState {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.state
}

// incrementErrorCount increments the error counter
func (w *IMAPWorker) incrementErrorCount() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.errorCount++
}

// incrementReconnectCount increments the reconnect counter
func (w *IMAPWorker) incrementReconnectCount() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.reconnectCount++
}
