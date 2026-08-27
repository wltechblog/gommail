package imap

import (
	"bytes"
	"testing"

	"github.com/wltechblog/gommail/internal/email"
)

// TestStoreSentMessageCommandParams verifies the parameter contract between
// ClientWrapper.StoreSentMessage and IMAPWorker.handleStoreSentMessage:
// message content must be passed as a string because the worker retrieves it
// via GetString.
func TestStoreSentMessageCommandParams(t *testing.T) {
	// Content includes non-UTF8 bytes to prove the string conversion
	// round-trips arbitrary binary message data.
	content := []byte("Date: Mon, 01 Jan 2024 00:00:00 +0000\r\nSubject: Test\r\n\r\n\x00\xff\xfe binary body")

	// Build the command exactly as ClientWrapper.StoreSentMessage does
	cmd := NewCommand(CmdStoreSentMessage, map[string]interface{}{
		ParamFolderName:     "Sent",
		ParamMessageContent: string(content),
	})

	// Read the parameters the way handleStoreSentMessage does
	folderName, ok := cmd.GetString(ParamFolderName)
	if !ok || folderName != "Sent" {
		t.Errorf("Expected folder name 'Sent', got '%s' (ok: %v)", folderName, ok)
	}

	messageData, ok := cmd.GetString(ParamMessageContent)
	if !ok {
		t.Fatal("Message content not readable as string; the sent copy would never be stored")
	}

	if !bytes.Equal([]byte(messageData), content) {
		t.Error("Message content did not round-trip byte-identical")
	}
}

// TestStoreSentMessageByteParamRejected guards against passing the message
// content as []byte: the worker's GetString lookup silently fails on it.
func TestStoreSentMessageByteParamRejected(t *testing.T) {
	cmd := NewCommand(CmdStoreSentMessage, map[string]interface{}{
		ParamFolderName:     "Sent",
		ParamMessageContent: []byte("message body"),
	})

	if _, ok := cmd.GetString(ParamMessageContent); ok {
		t.Error("Expected GetString to reject []byte message content; pass string(content) instead")
	}
}

// TestStoreSentMessageRequiresConnection verifies the worker rejects the
// command when no client connection exists.
func TestStoreSentMessageRequiresConnection(t *testing.T) {
	config := &email.ServerConfig{
		Host:     "imap.example.com",
		Port:     993,
		Username: "test@example.com",
		Password: "password",
		TLS:      true,
	}
	worker := NewIMAPWorker(config, "test-account", "test-cache-key", nil)

	cmd := NewCommand(CmdStoreSentMessage, map[string]interface{}{
		ParamFolderName:     "Sent",
		ParamMessageContent: "message body",
	})

	response := worker.handleStoreSentMessage(cmd)
	if response.Success {
		t.Error("Expected store sent message to fail without a connection")
	}
	if response.Error == nil {
		t.Error("Expected an error explaining the missing connection")
	}
}
