package ui

import (
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/wltechblog/gommail/internal/config"
	"github.com/wltechblog/gommail/internal/email"
)

func TestInlineAttachmentDisplay(t *testing.T) {
	// Create test app and config
	app := test.NewApp()
	cfg := newMockConfigManager([]config.Account{})

	// Create main window
	mw := NewMainWindow(app, cfg)

	// Create test message with attachment
	testMessage := &email.Message{
		ID:      "test-msg-1",
		Subject: "Test Message with Attachment",
		From:    []email.Address{{Name: "Test Sender", Email: "sender@test.com"}},
		To:      []email.Address{{Name: "Test Recipient", Email: "recipient@test.com"}},
		Body:    email.MessageBody{Text: "This is a test message with an attachment."},
		Attachments: []email.Attachment{
			{
				Filename:    "test.txt",
				ContentType: "text/plain",
				Size:        100,
				Data:        []byte("This is test attachment content."),
			},
			{
				Filename:    "image.jpg",
				ContentType: "image/jpeg",
				Size:        2048,
				Data:        []byte("fake image data"),
			},
		},
	}

	// Test caching attachment
	if mw.attachmentManager != nil {
		attachmentID := mw.cacheAttachmentIfNeeded(testMessage, testMessage.Attachments[0])
		if attachmentID == "" {
			t.Error("Expected attachment ID to be generated, got empty string")
		}

		// Test that attachment is cached
		if !mw.attachmentManager.IsAttachmentCached(attachmentID) {
			t.Error("Expected attachment to be cached")
		}
	}

	// Test inline attachment display creation
	attachmentContent := mw.createInlineAttachmentDisplay(
		testMessage.Attachments[0],
		"test-attachment-id",
		0,
	)

	if attachmentContent == "" {
		t.Error("Expected attachment content to be generated")
	}

	// Check that content contains expected elements
	expectedElements := []string{
		"test.txt",
		"text/plain",
		"100 B",
	}

	t.Logf("Generated attachment content:\n%s", attachmentContent)

	for _, element := range expectedElements {
		if !containsSubstring(attachmentContent, element) {
			t.Errorf("Expected attachment content to contain '%s', but it didn't", element)
		}
	}

	// Test attachment icon generation
	textIcon := mw.getAttachmentIcon("text/plain")
	if textIcon != "📄" {
		t.Errorf("Expected text icon to be '📄', got '%s'", textIcon)
	}

	imageIcon := mw.getAttachmentIcon("image/jpeg")
	if imageIcon != "🖼️" {
		t.Errorf("Expected image icon to be '🖼️', got '%s'", imageIcon)
	}

	// Test previewable type detection
	if !mw.isPreviewableType("text/plain") {
		t.Error("Expected text/plain to be previewable")
	}

	if !mw.isPreviewableType("image/jpeg") {
		t.Error("Expected image/jpeg to be previewable")
	}

	if mw.isPreviewableType("application/octet-stream") {
		t.Error("Expected application/octet-stream to not be previewable")
	}
}

func TestSaveAttachmentFunctionality(t *testing.T) {
	// Create test app and config
	app := test.NewApp()
	cfg := newMockConfigManager([]config.Account{})

	// Create main window
	mw := NewMainWindow(app, cfg)

	// Test with no selected message
	mw.selectedMessage = nil
	// This should not panic and should show appropriate dialog
	// In a real test, we'd mock the dialog system
	mw.saveAttachments()

	// Test with message but no attachments
	mw.selectedMessage = &email.MessageIndexItem{
		Message: email.Message{
			ID:          "test-msg-2",
			Subject:     "Test Message without Attachments",
			Attachments: []email.Attachment{},
		},
		AccountName: "Test Account",
	}
	// This should not panic and should show appropriate dialog
	mw.saveAttachments()

	// Test with single attachment
	mw.selectedMessage = &email.MessageIndexItem{
		Message: email.Message{
			ID:      "test-msg-3",
			Subject: "Test Message with Single Attachment",
			Attachments: []email.Attachment{
				{
					Filename:    "single.txt",
					ContentType: "text/plain",
					Size:        50,
					Data:        []byte("Single attachment content."),
				},
			},
		},
		AccountName: "Test Account",
	}
	// This should trigger single attachment save flow
	// In a real test, we'd mock the file dialog
	// mw.saveAttachments()
}

// Helper function to check if a string contains a substring
func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestOpenAttachmentFunctionality(t *testing.T) {
	// Create test app and config
	app := test.NewApp()
	cfg := newMockConfigManager([]config.Account{})

	// Create main window (for future use if needed)
	_ = NewMainWindow(app, cfg)

	// Create test attachment
	testAttachment := email.Attachment{
		Filename:    "test-open.txt",
		ContentType: "text/plain",
		Size:        25,
		Data:        []byte("Test content for opening"),
	}

	// Call openAttachment - this will save to temp directory
	// We can't actually test the system opening the file, but we can verify
	// that the file is created in the temp directory
	tempDir := filepath.Join(os.TempDir(), "gommail-attachments")

	// Clean up before test
	os.RemoveAll(tempDir)

	// Note: We can't actually call openAttachment in the test because it will
	// try to open the file with the system's default application, which may
	// not be available in a test environment. Instead, we'll test the file
	// creation logic separately.

	// Create temp directory
	err := os.MkdirAll(tempDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Sanitize filename and create path
	safeFilename := email.SanitizeFilename(testAttachment.Filename)
	tempFilePath := filepath.Join(tempDir, safeFilename)

	// Write attachment to temp file
	err = os.WriteFile(tempFilePath, testAttachment.Data, 0644)
	if err != nil {
		t.Fatalf("Failed to write attachment to temp file: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(tempFilePath); os.IsNotExist(err) {
		t.Error("Expected temp file to exist after writing")
	}

	// Verify file content
	content, err := os.ReadFile(tempFilePath)
	if err != nil {
		t.Fatalf("Failed to read temp file: %v", err)
	}

	if string(content) != string(testAttachment.Data) {
		t.Errorf("Expected file content to be '%s', got '%s'", string(testAttachment.Data), string(content))
	}

	// Test filename sanitization in the context of opening
	dangerousAttachment := email.Attachment{
		Filename:    "../../etc/passwd",
		ContentType: "text/plain",
		Size:        10,
		Data:        []byte("dangerous"),
	}

	safeName := email.SanitizeFilename(dangerousAttachment.Filename)
	if safeName == dangerousAttachment.Filename {
		t.Error("Expected dangerous filename to be sanitized")
	}

	// Verify sanitized name doesn't contain path separators
	if filepath.Base(safeName) != safeName {
		t.Errorf("Expected sanitized filename to not contain path separators, got '%s'", safeName)
	}
}

func TestOpenAttachmentSecurityValidation(t *testing.T) {
	// Test that dangerous file types are blocked from opening
	dangerousFiles := []struct {
		filename    string
		description string
	}{
		{"virus.exe", "Windows executable"},
		{"malware.bat", "Batch file"},
		{"script.sh", "Shell script"},
		{"evil.vbs", "VBScript"},
		{"trojan.js", "JavaScript"},
		{"backdoor.py", "Python script"},
		{"malicious.ps1", "PowerShell script"},
		{"app.apk", "Android package"},
	}

	for _, test := range dangerousFiles {
		err := email.ValidateAttachmentSafety(test.filename)
		if err == nil {
			t.Errorf("Expected %s (%s) to be blocked, but it wasn't", test.filename, test.description)
		}
	}

	// Test that safe file types are allowed
	safeFiles := []string{
		"document.pdf",
		"image.jpg",
		"photo.png",
		"data.csv",
		"report.docx",
		"archive.zip",
	}

	for _, filename := range safeFiles {
		err := email.ValidateAttachmentSafety(filename)
		if err != nil {
			t.Errorf("Expected %s to be allowed, but got error: %v", filename, err)
		}
	}
}
