package imap

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
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

// TestNewMessageCheckWatchdogUnblocksStalledServer verifies that a server
// which accepts the connection but never answers SELECT cannot freeze the
// new-message check forever: the watchdog closes the connection and the
// check returns.
func TestNewMessageCheckWatchdogUnblocksStalledServer(t *testing.T) {
	w := NewIMAPWorker(&email.ServerConfig{
		Host: "fake", Port: 993, Username: "user", Password: "pass", TLS: true,
	}, "stalled-account", "stalled-key", nil)
	if err := w.Start(); err != nil {
		t.Fatalf("failed to start worker: %v", err)
	}
	defer w.Stop()
	w.newMessageCheckTimeout = 300 * time.Millisecond

	conn, srv := net.Pipe()
	go func() {
		writeLine(srv, "* OK [CAPABILITY IMAP4rev1] stalled ready")
		io.Copy(io.Discard, srv) // read and discard; never answer commands
	}()
	defer conn.Close()
	w.commandClient = imapclient.New(conn, &imapclient.Options{})
	w.setState(ConnectionStateConnected, nil)

	done := make(chan struct{})
	go func() {
		w.checkForNewMessages("INBOX")
		close(done)
	}()

	select {
	case <-done:
		// The check returned: the watchdog closed the stalled connection.
	case <-time.After(5 * time.Second):
		t.Fatal("checkForNewMessages did not return; stalled server froze it")
	}
}
