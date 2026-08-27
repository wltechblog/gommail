package imap

import (
	"bufio"
	"context"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/wltechblog/gommail/internal/email"
)

// TestBulkFetchTimeout verifies bulk fetches get timeouts commensurate with
// transferring an entire mailbox, instead of the 30s default that caused
// spurious timeouts on large mailboxes (see log cascade).
func TestBulkFetchTimeout(t *testing.T) {
	w := NewClientWrapper(email.ServerConfig{Host: "h", Port: 993, Username: "u", Password: "p", TLS: true})
	t.Cleanup(w.Stop)

	if got := w.getBulkFetchTimeout(0); got != 5*time.Minute {
		t.Errorf("unlimited fetch: expected 5m timeout, got %v", got)
	}
	if got := w.getBulkFetchTimeout(500); got != 2*time.Minute {
		t.Errorf("limited fetch: expected 2m timeout, got %v", got)
	}
	if got := w.getBulkFetchTimeout(1); got != 2*time.Minute {
		t.Errorf("small fetch: expected 2m timeout, got %v", got)
	}
}

// serveStreamingFetchConn streams fetchEnvelopeLine for the first
// UID FETCH, pacing messages streamDelay apart so the test can cancel
// mid-stream.
func serveStreamingFetchConn(conn net.Conn, totalMessages int, streamDelay time.Duration) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	writeLine(conn, "* OK [CAPABILITY IMAP4rev1 IMAP4rev2] fake ready")

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
			verb = strings.ToUpper(parts[2]) // UID <CMD> responses use <CMD> data
		}
		switch verb {
		case "LOGIN":
			writeLine(conn, tag+" OK LOGIN completed")
		case "SELECT":
			writeLine(conn, "* 3 EXISTS")
			writeLine(conn, "* OK [UIDNEXT 4]")
			writeLine(conn, tag+" OK [READ-WRITE] SELECT completed")
		case "SEARCH":
			uids := make([]string, totalMessages)
			for i := 1; i <= totalMessages; i++ {
				uids[i-1] = itoa(i)
			}
			writeLine(conn, "* SEARCH "+strings.Join(uids, " "))
			writeLine(conn, tag+" OK SEARCH completed")
		case "FETCH":
			for i := 1; i <= totalMessages; i++ {
				writeLine(conn, fetchEnvelopeLine(i))
				time.Sleep(streamDelay)
			}
			writeLine(conn, tag+" OK FETCH completed")
		default:
			writeLine(conn, tag+" OK "+verb+" completed")
		}
	}
}

func fetchEnvelopeLine(uid int) string {
	return "* " + itoa(uid) + " FETCH (UID " + itoa(uid) + " FLAGS () RFC822.SIZE 120 INTERNALDATE \"01-Jan-2024 00:00:00 +0000\" ENVELOPE (\"Mon, 01 Jan 2024 00:00:00 +0000\" \"Test\" ((\"Alice\" NIL \"alice\" \"example.com\")) ((\"Alice\" NIL \"alice\" \"example.com\")) NIL ((\"Bob\" NIL \"bob\" \"example.com\")) NIL NIL NIL \"<m" + itoa(uid) + "@example.com>\"))"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestFetchMessagesAbortsWhenCallerCancels verifies that a bulk fetch whose
// caller has timed out stops processing immediately instead of continuing to
// convert/cache the whole mailbox while blocking the worker's event loop.
// It covers both transports: the command-client fallback (drain semantics)
// and the dedicated fetch client (instant abort via connection close).
func TestFetchMessagesAbortsWhenCallerCancels(t *testing.T) {
	for _, tc := range []struct {
		name            string
		useFetchClient  bool
		maxAbortLatency time.Duration
	}{
		// Fallback path: Close() drains remaining responses, so the handler
		// returns after the stream ends (~1s) but without caching partial
		// data as success.
		{name: "command client fallback", useFetchClient: false, maxAbortLatency: 1500 * time.Millisecond},
		// Dedicated fetch client: abort closes the connection, so the handler
		// must return promptly (~150ms cancel point plus slack), long before
		// the ~1s stream would have finished.
		{name: "dedicated fetch client", useFetchClient: true, maxAbortLatency: 500 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := NewIMAPWorker(&email.ServerConfig{
				Host: "fake", Port: 993, Username: "user", Password: "pass", TLS: true,
			}, "test-account", "test-key", nil)
			if err := w.Start(); err != nil {
				t.Fatalf("failed to start worker: %v", err)
			}
			defer w.Stop()

			// Stream 20 messages, one per 50ms — the whole transfer takes ~1s.
			cmdConn, cmdServer := net.Pipe()
			go serveStreamingFetchConn(cmdServer, 20, 50*time.Millisecond)
			w.commandClient = imapclient.New(cmdConn, &imapclient.Options{})

			if tc.useFetchClient {
				fetchConn, fetchServer := net.Pipe()
				go serveStreamingFetchConn(fetchServer, 20, 50*time.Millisecond)
				w.fetchClient = imapclient.New(fetchConn, &imapclient.Options{DebugWriter: os.Stderr})
				w.fetchSelectedFolder = ""
				defer fetchConn.Close()
			}

			w.setState(ConnectionStateConnected, nil)

			ctx, cancel := context.WithCancel(context.Background())
			cmd := NewCommandWithContext(ctx, CmdFetchMessages, map[string]interface{}{
				ParamFolderName: "INBOX",
			})

			// Cancel after the first couple of messages have streamed in.
			go func() {
				time.Sleep(150 * time.Millisecond)
				cancel()
			}()

			start := time.Now()
			resp := w.handleFetchMessages(cmd)
			elapsed := time.Since(start)

			if resp.Success {
				t.Error("expected fetch to fail after caller cancellation, got success")
			}
			if resp.Error == nil || !strings.Contains(resp.Error.Error(), "cancelled") {
				t.Errorf("expected cancellation error, got: %v", resp.Error)
			}
			if elapsed > tc.maxAbortLatency {
				t.Errorf("fetch handler took %v to abort after cancellation (max %v); abort is not prompt",
					elapsed, tc.maxAbortLatency)
			}

			if tc.useFetchClient {
				w.mu.RLock()
				fc := w.fetchClient
				w.mu.RUnlock()
				if fc != nil {
					t.Error("expected fetch client to be closed and cleared after abort")
				}
			}
		})
	}
}

// TestFetchMessagesSucceedsWithoutCancellation guards against the abort
// machinery breaking normal fetches: a completed transfer must return success.
func TestFetchMessagesSucceedsWithoutCancellation(t *testing.T) {
	w := NewIMAPWorker(&email.ServerConfig{
		Host: "fake", Port: 993, Username: "user", Password: "pass", TLS: true,
	}, "test-account", "test-key", nil)
	if err := w.Start(); err != nil {
		t.Fatalf("failed to start worker: %v", err)
	}
	defer w.Stop()

	fetchConn, fetchServer := net.Pipe()
	go serveStreamingFetchConn(fetchServer, 5, 5*time.Millisecond)
	w.fetchClient = imapclient.New(fetchConn, &imapclient.Options{})
	defer fetchConn.Close()

	cmdConn, cmdServer := net.Pipe()
	go serveStreamingFetchConn(cmdServer, 5, 5*time.Millisecond)
	w.commandClient = imapclient.New(cmdConn, &imapclient.Options{})
	defer cmdConn.Close()

	w.setState(ConnectionStateConnected, nil)

	cmd := NewCommand(CmdFetchMessages, map[string]interface{}{
		ParamFolderName: "INBOX",
	})

	resp := w.handleFetchMessages(cmd)
	if !resp.Success {
		t.Fatalf("expected fetch to succeed, got error: %v", resp.Error)
	}
	msgs, ok := resp.GetMessages(DataMessages)
	if !ok || len(msgs) != 5 {
		t.Errorf("expected 5 messages, got %d (ok=%v)", len(msgs), ok)
	}
}
