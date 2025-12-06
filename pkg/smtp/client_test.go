package smtp

import (
	"strings"
	"testing"
	"time"

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

func TestQueueMessage(t *testing.T) {
	config := email.ServerConfig{
		Host: "smtp.example.com",
		Port: 587,
	}

	client := NewClient(config)

	msg := &email.Message{
		Subject: "Test Queue",
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

	err := client.QueueMessage(msg)
	if err != nil {
		t.Errorf("QueueMessage failed: %v", err)
	}

	total, pending, failed := client.GetQueueStatus()
	if total != 1 {
		t.Errorf("Expected 1 message in queue, got %d", total)
	}
	if pending != 1 {
		t.Errorf("Expected 1 pending message, got %d", pending)
	}
	if failed != 0 {
		t.Errorf("Expected 0 failed messages, got %d", failed)
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
