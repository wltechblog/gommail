package imap

import (
	"bufio"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/wltechblog/gommail/internal/email"
)

// serveIdleConn implements a minimal fake IMAP server for the IDLE connection.
// On the first IDLE it pushes "* 2 EXISTS" after pushDelay, simulating a new
// message arriving while the client is IDLEing. Subsequent IDLE rounds get no
// pushes.
func serveIdleConn(conn net.Conn, pushDelay time.Duration) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	writeLine(conn, "* OK [CAPABILITY IMAP4rev1 IDLE] fake ready")

	idleRounds := 0
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		parts := strings.Fields(strings.TrimRight(line, "\r\n"))
		if len(parts) < 2 {
			continue
		}
		tag, verb := parts[0], strings.ToUpper(parts[1])
		switch verb {
		case "LOGIN":
			writeLine(conn, tag+" OK LOGIN completed")
		case "SELECT":
			writeLine(conn, "* 1 EXISTS")
			writeLine(conn, "* OK [UIDNEXT 2]")
			writeLine(conn, tag+" OK [READ-WRITE] SELECT completed")
		case "IDLE":
			writeLine(conn, "+ idling")
			idleRounds++
			if idleRounds == 1 {
				time.Sleep(pushDelay)
				writeLine(conn, "* 2 EXISTS")
			}
			// Wait for DONE, then terminate the IDLE command
			for {
				dl, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(dl)), "DONE") {
					writeLine(conn, tag+" OK IDLE terminated")
					break
				}
			}
		default:
			writeLine(conn, tag+" OK "+verb+" completed")
		}
	}
}

// commandServerConfig controls the behavior of the fake command-connection
// server. emptyFetchAttempts is the number of UID FETCH commands that complete
// with no data, simulating servers (e.g. Gmail) that push EXISTS before the
// message is actually fetchable.
type commandServerConfig struct {
	emptyFetchAttempts int32
}

// serveCommandConn implements a minimal fake IMAP server for the command
// connection. The INBOX has 2 messages with UIDNEXT 3.
func serveCommandConn(conn net.Conn, cfg commandServerConfig) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	writeLine(conn, "* OK [CAPABILITY IMAP4rev1 IMAP4rev2] fake ready")

	var fetchAttempts int32
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		parts := strings.Fields(strings.TrimRight(line, "\r\n"))
		if len(parts) < 2 {
			continue
		}
		tag, verb := parts[0], strings.ToUpper(parts[1])
		if verb == "UID" && len(parts) > 2 {
			verb = "FETCH" // UID FETCH responses are FETCH responses
		}
		switch verb {
		case "LOGIN":
			writeLine(conn, tag+" OK LOGIN completed")
		case "STATUS":
			writeLine(conn, "* STATUS INBOX (MESSAGES 2 UIDNEXT 3)")
			writeLine(conn, tag+" OK STATUS completed")
		case "SELECT":
			writeLine(conn, "* 2 EXISTS")
			writeLine(conn, "* OK [UIDNEXT 3]")
			writeLine(conn, tag+" OK [READ-WRITE] SELECT completed")
		case "FETCH":
			attempt := atomic.AddInt32(&fetchAttempts, 1)
			if attempt <= atomic.LoadInt32(&cfg.emptyFetchAttempts) {
				// Message signalled but not yet visible: no FETCH data
				writeLine(conn, tag+" OK FETCH completed")
				continue
			}
			writeLine(conn, "* 2 FETCH (UID 2 FLAGS () RFC822.SIZE 120 INTERNALDATE \"01-Jan-2024 00:00:00 +0000\" ENVELOPE (\"Mon, 01 Jan 2024 00:00:00 +0000\" \"Test\" ((\"Alice\" NIL \"alice\" \"example.com\")) ((\"Alice\" NIL \"alice\" \"example.com\")) NIL ((\"Bob\" NIL \"bob\" \"example.com\")) NIL NIL NIL \"<m2@example.com>\"))")
			writeLine(conn, tag+" OK FETCH completed")
		default:
			writeLine(conn, tag+" OK "+verb+" completed")
		}
	}
}

func writeLine(conn net.Conn, line string) {
	conn.Write([]byte(line + "\r\n"))
}

// newIDLETestWorker creates a started worker wired to fake IMAP servers and
// returns it together with a callback channel. The INBOX starts with one
// tracked message (highest UID 1) and initial sync marked complete.
func newIDLETestWorker(t *testing.T, cfg commandServerConfig) (*IMAPWorker, chan NewMessageEvent, func()) {
	t.Helper()

	w := NewIMAPWorker(&email.ServerConfig{
		Host: "fake", Port: 993, Username: "user", Password: "pass", TLS: true,
	}, "test-account", "test-key", nil)

	if err := w.Start(); err != nil {
		t.Fatalf("failed to start worker: %v", err)
	}

	cmdConn, cmdServer := net.Pipe()
	go serveCommandConn(cmdServer, cfg)
	w.commandClient = imapclient.New(cmdConn, &imapclient.Options{})

	idleConn, idleServer := net.Pipe()
	w.idleClient = imapclient.New(idleConn, w.idleClientOptions(nil))
	go serveIdleConn(idleServer, 200*time.Millisecond)

	w.setState(ConnectionStateConnected, nil)

	w.mu.Lock()
	w.lastMessageCount["INBOX"] = 1
	w.lastHighestUID["INBOX"] = 1
	w.initialSyncDone["INBOX"] = true
	w.mu.Unlock()

	notified := make(chan NewMessageEvent, 1)
	w.SetNewMessageCallback(func(ev NewMessageEvent) {
		select {
		case notified <- ev:
		default:
		}
	})

	cleanup := func() {
		w.Stop()
		cmdConn.Close()
		idleConn.Close()
	}
	return w, notified, cleanup
}

// TestIDLEImmediateNewMessageDetection verifies that a server push ("* N
// EXISTS") received while IDLEing triggers the new-message callback promptly,
// without waiting for the IDLE timeout.
func TestIDLEImmediateNewMessageDetection(t *testing.T) {
	w, notified, cleanup := newIDLETestWorker(t, commandServerConfig{})
	defer cleanup()

	resp := w.handleStartIDLE(NewStartIDLECommand("INBOX"))
	if !resp.Success {
		t.Fatalf("failed to start IDLE: %v", resp.Error)
	}

	select {
	case ev := <-notified:
		if ev.Folder != "INBOX" {
			t.Errorf("expected folder INBOX, got %s", ev.Folder)
		}
		if ev.Count != 1 {
			t.Errorf("expected 1 new message, got %d", ev.Count)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("new message was not detected promptly after the server push; " +
			"detection appears to wait for the IDLE timeout")
	}
}

// TestIDLEDetectionRetriesAfterTransientFetchFailure reproduces the symptom
// where a new message is detected only at the IDLE timeout: the server pushes
// EXISTS before the message is fetchable, the immediate check fetches nothing,
// and nothing re-triggers a check until the 29-minute timeout. The worker must
// instead retry after a short delay.
func TestIDLEDetectionRetriesAfterTransientFetchFailure(t *testing.T) {
	// First two UID FETCH attempts return no data; the third succeeds.
	w, notified, cleanup := newIDLETestWorker(t, commandServerConfig{emptyFetchAttempts: 2})
	defer cleanup()

	resp := w.handleStartIDLE(NewStartIDLECommand("INBOX"))
	if !resp.Success {
		t.Fatalf("failed to start IDLE: %v", resp.Error)
	}

	// Retries are scheduled at 1s, 2s, ... With two failed attempts, the
	// notification must arrive after roughly 1s + 2s = 3s. Allow 15s headroom,
	// which is still far below the 29-minute IDLE timeout.
	select {
	case ev := <-notified:
		if ev.Count != 1 {
			t.Errorf("expected 1 new message, got %d", ev.Count)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("new message was not detected via short-delay retry after a transient " +
			"fetch failure; detection appears to wait for the IDLE timeout")
	}
}
