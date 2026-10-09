package imap

import (
	"sync"
	"testing"

	"github.com/wltechblog/gommail/internal/email"
)

// TestStopRacesWithCommandSenders exercises Stop while multiple goroutines
// hammer SendCommand. Stop previously closed commandCh, so a sender that
// observed the closed channel panicked with "send on closed channel"; the
// channels are no longer closed and senders unwind via the worker context.
func TestStopRacesWithCommandSenders(t *testing.T) {
	for i := 0; i < 10; i++ {
		w := NewIMAPWorker(&email.ServerConfig{
			Host: "fake", Port: 993, Username: "user", Password: "pass", TLS: true,
		}, "shutdown-account", "shutdown-key", nil)
		if err := w.Start(); err != nil {
			t.Fatalf("failed to start worker: %v", err)
		}

		var wg sync.WaitGroup
		for j := 0; j < 4; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for k := 0; k < 50; k++ {
					// The worker is never connected, so the event loop
					// rejects these quickly; errors here are expected and
					// irrelevant - the test asserts shutdown safety.
					_, _ = w.SendCommand(NewCommand(CmdGetStatus, nil))
				}
			}()
		}
		// Stop mid-stream while senders are still in flight.
		w.Stop()
		wg.Wait()

		if w.IsRunning() {
			t.Fatal("worker should not be running after Stop")
		}
	}
}

// TestStopIsIdempotent verifies repeated Stop/StopFast calls are safe.
func TestStopIsIdempotent(t *testing.T) {
	w := NewIMAPWorker(&email.ServerConfig{
		Host: "fake", Port: 993, Username: "user", Password: "pass", TLS: true,
	}, "idempotent-account", "idempotent-key", nil)
	if err := w.Start(); err != nil {
		t.Fatalf("failed to start worker: %v", err)
	}

	if err := w.Stop(); err != nil {
		t.Fatalf("first Stop failed: %v", err)
	}
	if err := w.Stop(); err != nil {
		t.Fatalf("second Stop failed: %v", err)
	}
	if err := w.StopFast(); err != nil {
		t.Fatalf("StopFast after Stop failed: %v", err)
	}
}
