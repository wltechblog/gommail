package imap

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/wltechblog/gommail/internal/email"
)

// staleStatusCommandConn simulates a server that answers STATUS for the
// currently selected mailbox with STALE select-time data (discouraged but
// real-world behavior; RFC 9051 §6.3.11), while SELECT answers live data.
// This reproduces the brokedown.net symptom: EXISTS pushes arrive, IDLE
// cycles run, but STATUS-based checks never observe the new message.
func staleStatusCommandConn(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	writeLine(conn, "* OK [CAPABILITY IMAP4rev1 IMAP4rev2 IDLE] fake ready")

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
			verb = strings.ToUpper(parts[2])
		}
		switch verb {
		case "LOGIN":
			writeLine(conn, tag+" OK LOGIN completed")
		case "STATUS":
			// Stale: always reports the pre-delivery state.
			writeLine(conn, "* STATUS INBOX (MESSAGES 1 UIDNEXT 2)")
			writeLine(conn, tag+" OK STATUS completed")
		case "SELECT":
			// Live: the new message is visible.
			writeLine(conn, "* 2 EXISTS")
			writeLine(conn, "* OK [UIDNEXT 3]")
			writeLine(conn, tag+" OK [READ-WRITE] SELECT completed")
		case "FETCH":
			writeLine(conn, "* 2 FETCH (UID 2 FLAGS () RFC822.SIZE 120 INTERNALDATE \"01-Jan-2024 00:00:00 +0000\" ENVELOPE (\"Mon, 01 Jan 2024 00:00:00 +0000\" \"Fwd: Test\" ((\"Alice\" NIL \"alice\" \"example.com\")) ((\"Alice\" NIL \"alice\" \"example.com\")) NIL ((\"Bob\" NIL \"bob\" \"example.com\")) NIL NIL NIL \"<fwd2@example.com>\"))")
			writeLine(conn, tag+" OK FETCH completed")
		default:
			writeLine(conn, tag+" OK "+verb+" completed")
		}
	}
}

// TestIDLEDetectionWithStaleStatusServer verifies that push-triggered
// detection works even when the server answers STATUS on the selected
// mailbox with stale data: the check must observe the mailbox via a live
// SELECT instead.
func TestIDLEDetectionWithStaleStatusServer(t *testing.T) {
	w := NewIMAPWorker(&email.ServerConfig{
		Host: "fake", Port: 993, Username: "user", Password: "pass", TLS: true,
	}, "test-account", "test-key", nil)
	if err := w.Start(); err != nil {
		t.Fatalf("failed to start worker: %v", err)
	}
	defer w.Stop()

	cmdConn, cmdServer := net.Pipe()
	go staleStatusCommandConn(cmdServer)
	w.commandClient = imapclient.New(cmdConn, &imapclient.Options{})

	idleConn, idleServer := net.Pipe()
	w.idleClient = imapclient.New(idleConn, w.idleClientOptions(nil))
	go serveIdleConn(idleServer, 200*time.Millisecond)

	w.setState(ConnectionStateConnected, nil)

	// Tracked state: 1 message, highest UID 1, initial sync complete.
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

	resp := w.handleStartIDLE(NewStartIDLECommand("INBOX"))
	if !resp.Success {
		t.Fatalf("failed to start IDLE: %v", resp.Error)
	}

	select {
	case ev := <-notified:
		if ev.Count != 1 {
			t.Errorf("expected 1 new message, got %d", ev.Count)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("new message not detected: check observed stale data (STATUS on selected " +
			"mailbox) instead of live mailbox state (SELECT)")
	}
}
