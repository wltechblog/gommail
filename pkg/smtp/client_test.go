package smtp

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-message"
	"github.com/wltechblog/gommail/internal/email"
)

func TestNewClient(t *testing.T) {
	config := email.ServerConfig{
		Host:     "smtp.example.com",
		Port:     587,
		Username: "test@example.com",
		Password: "password",
		TLS:      false,
	}

	client := NewClient(config)
	if client == nil {
		t.Fatal("NewClient returned nil")
	}

	if client.config.Host != config.Host {
		t.Errorf("Expected host %s, got %s", config.Host, client.config.Host)
	}
}

func TestConnect(t *testing.T) {
	config := email.ServerConfig{
		Host:     "smtp.example.com",
		Port:     587,
		Username: "test@example.com",
		Password: "password",
		TLS:      false,
	}

	client := NewClient(config)

	// Connect should always succeed for SMTP (no persistent connection)
	err := client.Connect()
	if err != nil {
		t.Errorf("Connect should not fail: %v", err)
	}
}

func TestDisconnect(t *testing.T) {
	config := email.ServerConfig{
		Host:     "smtp.example.com",
		Port:     587,
		Username: "test@example.com",
		Password: "password",
		TLS:      false,
	}

	client := NewClient(config)

	// Disconnect should always succeed for SMTP (no persistent connection)
	err := client.Disconnect()
	if err != nil {
		t.Errorf("Disconnect should not fail: %v", err)
	}
}

func TestBuildMessage_TextOnly(t *testing.T) {
	config := email.ServerConfig{
		Host:     "smtp.example.com",
		Port:     587,
		Username: "test@example.com",
		Password: "password",
		TLS:      false,
	}

	client := NewClient(config)

	msg := &email.Message{
		Subject: "Test Subject",
		From: []email.Address{
			{Name: "Test Sender", Email: "sender@example.com"},
		},
		To: []email.Address{
			{Name: "Test Recipient", Email: "recipient@example.com"},
		},
		Date: time.Now(),
		Body: email.MessageBody{
			Text: "This is a test message",
		},
	}

	content, err := client.buildMessage(msg)
	if err != nil {
		t.Fatalf("buildMessage failed: %v", err)
	}

	contentStr := string(content)

	// Check that basic headers are present
	if !strings.Contains(contentStr, "Subject: Test Subject") {
		t.Error("Subject header not found")
	}
	if !strings.Contains(contentStr, "From: Test Sender <sender@example.com>") {
		t.Error("From header not found or incorrectly formatted")
	}
	if !strings.Contains(contentStr, "To: Test Recipient <recipient@example.com>") {
		t.Error("To header not found or incorrectly formatted")
	}
	if !strings.Contains(contentStr, "This is a test message") {
		t.Error("Message body not found")
	}
}

func TestBuildMessage_WithAttachment(t *testing.T) {
	config := email.ServerConfig{
		Host:     "smtp.example.com",
		Port:     587,
		Username: "test@example.com",
		Password: "password",
		TLS:      false,
	}

	client := NewClient(config)

	msg := &email.Message{
		Subject: "Test with Attachment",
		From: []email.Address{
			{Name: "Test Sender", Email: "sender@example.com"},
		},
		To: []email.Address{
			{Name: "Test Recipient", Email: "recipient@example.com"},
		},
		Date: time.Now(),
		Body: email.MessageBody{
			Text: "This message has an attachment",
		},
		Attachments: []email.Attachment{
			{
				Filename:    "test.txt",
				ContentType: "text/plain",
				Data:        []byte("Test attachment content"),
			},
		},
	}

	content, err := client.buildMessage(msg)
	if err != nil {
		t.Fatalf("buildMessage with attachment failed: %v", err)
	}

	contentStr := string(content)

	// Check for multipart content
	if !strings.Contains(contentStr, "multipart/mixed") {
		t.Error("Expected multipart/mixed content type for message with attachment")
	}
	if !strings.Contains(contentStr, "filename=\"test.txt\"") {
		t.Error("Attachment filename not found")
	}
}

func TestSendMessage_InvalidHost(t *testing.T) {
	config := email.ServerConfig{
		Host:     "invalid.smtp.server.com",
		Port:     587,
		Username: "test@example.com",
		Password: "password",
		TLS:      false,
	}

	client := NewClient(config)

	msg := &email.Message{
		Subject: "Test Subject",
		From: []email.Address{
			{Email: "sender@example.com"},
		},
		To: []email.Address{
			{Email: "recipient@example.com"},
		},
		Date: time.Now(),
		Body: email.MessageBody{
			Text: "Test message",
		},
	}

	// This should fail due to invalid host
	err := client.SendMessage(msg)
	if err == nil {
		t.Skip("Skipping SendMessage test - would require real SMTP server")
	}

	// Check that we get a reasonable error message
	if !strings.Contains(err.Error(), "failed to connect") && !strings.Contains(err.Error(), "no such host") {
		t.Errorf("Expected connection error, got: %v", err)
	}
}

func TestFormatAddresses(t *testing.T) {
	config := email.ServerConfig{
		Host: "smtp.example.com",
		Port: 587,
	}

	client := NewClient(config)

	addresses := []email.Address{
		{Name: "John Doe", Email: "john@example.com"},
		{Email: "jane@example.com"},
		{Name: "Bob Smith", Email: "bob@example.com"},
	}

	formatted := client.formatAddresses(addresses)
	expected := "John Doe <john@example.com>, jane@example.com, Bob Smith <bob@example.com>"

	if formatted != expected {
		t.Errorf("Expected %q, got %q", expected, formatted)
	}
}

func TestNewClientWithAuth(t *testing.T) {
	config := email.ServerConfig{
		Host: "smtp.example.com",
		Port: 587,
	}

	client := NewClientWithAuth(config, AuthLogin)
	if client.authMethod != AuthLogin {
		t.Errorf("Expected auth method %v, got %v", AuthLogin, client.authMethod)
	}
}

func TestValidateMessage(t *testing.T) {
	config := email.ServerConfig{
		Host: "smtp.example.com",
		Port: 587,
	}

	client := NewClient(config)

	// Test valid message
	validMsg := &email.Message{
		Subject: "Test Subject",
		From: []email.Address{
			{Email: "sender@example.com"},
		},
		To: []email.Address{
			{Email: "recipient@example.com"},
		},
		Body: email.MessageBody{
			Text: "Test message",
		},
	}

	err := client.ValidateMessage(validMsg)
	if err != nil {
		t.Errorf("Valid message failed validation: %v", err)
	}

	// Test invalid message - no subject
	invalidMsg := &email.Message{
		From: []email.Address{
			{Email: "sender@example.com"},
		},
		To: []email.Address{
			{Email: "recipient@example.com"},
		},
		Body: email.MessageBody{
			Text: "Test message",
		},
	}

	err = client.ValidateMessage(invalidMsg)
	if err == nil {
		t.Error("Invalid message (no subject) should have failed validation")
	}

	// Test invalid message - no recipients
	invalidMsg2 := &email.Message{
		Subject: "Test Subject",
		From: []email.Address{
			{Email: "sender@example.com"},
		},
		Body: email.MessageBody{
			Text: "Test message",
		},
	}

	err = client.ValidateMessage(invalidMsg2)
	if err == nil {
		t.Error("Invalid message (no recipients) should have failed validation")
	}
}

func TestIsValidEmail(t *testing.T) {
	config := email.ServerConfig{
		Host: "smtp.example.com",
		Port: 587,
	}

	client := NewClient(config)

	validEmails := []string{
		"test@example.com",
		"user.name@domain.co.uk",
		"user+tag@example.org",
	}

	for _, email := range validEmails {
		if !client.isValidEmail(email) {
			t.Errorf("Valid email %s was marked as invalid", email)
		}
	}

	invalidEmails := []string{
		"invalid",
		"@example.com",
		"test@",
		"test@domain",
		"",
	}

	for _, email := range invalidEmails {
		if client.isValidEmail(email) {
			t.Errorf("Invalid email %s was marked as valid", email)
		}
	}
}

// TestBuildMessageMIMEStructureWithAttachments verifies the two structural
// fixes for attachment messages: the body must be a nested
// multipart/alternative inside multipart/mixed (not siblings), and
// attachments must be base64-encoded exactly once by go-message (the
// previous manual encoder on top of go-message's CTE handling corrupted
// every attachment).
func TestBuildMessageMIMEStructureWithAttachments(t *testing.T) {
	c := NewClient(email.ServerConfig{Host: "smtp.example.com", Port: 465, Username: "u@example.com"})
	attachmentData := []byte("attachment content that must survive exactly")
	msg := &email.Message{
		Subject:     "structure probe",
		From:        []email.Address{{Email: "from@example.com"}},
		To:          []email.Address{{Email: "to@example.com"}},
		Body:        email.MessageBody{Text: "plain body", HTML: "<p>html body</p>"},
		Attachments: []email.Attachment{{Filename: "file.bin", ContentType: "application/octet-stream", Data: attachmentData}},
	}

	raw, err := c.BuildMessage(msg)
	if err != nil {
		t.Fatalf("BuildMessage failed: %v", err)
	}
	str := string(raw)

	if !strings.Contains(str, "multipart/mixed") {
		t.Error("outer structure should be multipart/mixed")
	}
	if !strings.Contains(str, "multipart/alternative") {
		t.Error("text+html body should be nested in multipart/alternative")
	}
	if !strings.Contains(str, "plain body") || !strings.Contains(str, "<p>html body</p>") {
		t.Error("body parts missing from output")
	}

	// Round-trip through go-message's reader: the attachment must decode
	// back to the exact original bytes.
	parsed, err := message.Read(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("failed to parse built message: %v", err)
	}

	foundAttachment := false
	err = parsed.Walk(func(path []int, p *message.Entity, err error) error {
		if strings.HasPrefix(p.Header.Get("Content-Disposition"), "attachment") {
			foundAttachment = true
			data, err := io.ReadAll(p.Body)
			if err != nil {
				t.Fatalf("failed to read attachment part: %v", err)
			}
			if !bytes.Equal(data, attachmentData) {
				t.Errorf("attachment decoded to %q, want %q (double encoding?)", data, attachmentData)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk failed: %v", err)
	}
	if !foundAttachment {
		t.Error("no attachment part found in built message")
	}
}

func TestBuildMessageHeadersDateAndMessageID(t *testing.T) {
	c := NewClient(email.ServerConfig{Host: "smtp.example.com", Port: 465})

	// Zero date must not serialize as year 0001.
	raw, err := c.BuildMessage(&email.Message{
		Subject: "header probe",
		From:    []email.Address{{Email: "from@example.com"}},
		To:      []email.Address{{Email: "to@example.com"}},
		Body:    email.MessageBody{Text: "hi"},
	})
	if err != nil {
		t.Fatalf("BuildMessage failed: %v", err)
	}
	str := string(raw)
	if strings.Contains(str, "Jan 0001") {
		t.Error("zero Date serialized as year 0001")
	}
	if !strings.Contains(strings.ToLower(str), "message-id: <") {
		t.Error("no Message-ID generated")
	}

	// An explicit Message-ID header must be preserved.
	explicit := "<custom@example.com>"
	raw, err = c.BuildMessage(&email.Message{
		Subject: "header probe 2",
		From:    []email.Address{{Email: "from@example.com"}},
		To:      []email.Address{{Email: "to@example.com"}},
		Body:    email.MessageBody{Text: "hi"},
		Headers: map[string]string{"Message-ID": explicit},
	})
	if err != nil {
		t.Fatalf("BuildMessage failed: %v", err)
	}
	if !strings.Contains(string(raw), explicit) {
		t.Error("explicit Message-ID header not preserved")
	}

}
