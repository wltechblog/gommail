package email

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewAttachmentManager(t *testing.T) {
	tempDir := t.TempDir()
	cacheDir := filepath.Join(tempDir, "cache")
	downloadDir := filepath.Join(tempDir, "downloads")

	am := NewAttachmentManager(cacheDir, downloadDir)

	if am == nil {
		t.Fatal("NewAttachmentManager returned nil")
	}

	if am.downloadDir != downloadDir {
		t.Errorf("Expected download dir %s, got %s", downloadDir, am.downloadDir)
	}

	if am.maxCacheSize != 50*1024*1024 {
		t.Errorf("Expected max cache size 50MB, got %d", am.maxCacheSize)
	}

	if !am.previewEnabled {
		t.Error("Expected preview to be enabled by default")
	}
}

func TestCacheAttachment(t *testing.T) {
	tempDir := t.TempDir()
	am := NewAttachmentManager(tempDir, tempDir)

	attachment := Attachment{
		Filename:    "test.txt",
		ContentType: "text/plain",
		Size:        11,
		Data:        []byte("Hello World"),
	}

	messageID := "test-message-123"

	info, err := am.CacheAttachment(messageID, attachment)
	if err != nil {
		t.Fatalf("CacheAttachment failed: %v", err)
	}

	if info.MessageID != messageID {
		t.Errorf("Expected message ID %s, got %s", messageID, info.MessageID)
	}

	if info.Filename != attachment.Filename {
		t.Errorf("Expected filename %s, got %s", attachment.Filename, info.Filename)
	}

	if info.Size != attachment.Size {
		t.Errorf("Expected size %d, got %d", attachment.Size, info.Size)
	}

	if !info.IsViewable {
		t.Error("Expected text/plain to be viewable")
	}
}

func TestGetCachedAttachment(t *testing.T) {
	tempDir := t.TempDir()
	am := NewAttachmentManager(tempDir, tempDir)

	attachment := Attachment{
		Filename:    "test.txt",
		ContentType: "text/plain",
		Size:        11,
		Data:        []byte("Hello World"),
	}

	messageID := "test-message-123"

	// Cache the attachment first
	info, err := am.CacheAttachment(messageID, attachment)
	if err != nil {
		t.Fatalf("CacheAttachment failed: %v", err)
	}

	// Retrieve the cached attachment
	viewable, err := am.GetCachedAttachment(info.ID)
	if err != nil {
		t.Fatalf("GetCachedAttachment failed: %v", err)
	}

	if viewable.Info.ID != info.ID {
		t.Errorf("Expected ID %s, got %s", info.ID, viewable.Info.ID)
	}

	if string(viewable.Data) != "Hello World" {
		t.Errorf("Expected data 'Hello World', got '%s'", string(viewable.Data))
	}
}

func TestExtractAttachment(t *testing.T) {
	tempDir := t.TempDir()
	am := NewAttachmentManager(tempDir, tempDir)

	attachment := Attachment{
		Filename:    "test.txt",
		ContentType: "text/plain",
		Size:        11,
		Data:        []byte("Hello World"),
	}

	messageID := "test-message-123"

	// Cache the attachment first
	info, err := am.CacheAttachment(messageID, attachment)
	if err != nil {
		t.Fatalf("CacheAttachment failed: %v", err)
	}

	// Extract to a target directory
	targetDir := filepath.Join(tempDir, "extracted")
	extractedPath, err := am.ExtractAttachment(info.ID, targetDir)
	if err != nil {
		t.Fatalf("ExtractAttachment failed: %v", err)
	}

	// Verify the file was created
	if _, err := os.Stat(extractedPath); os.IsNotExist(err) {
		t.Errorf("Extracted file does not exist: %s", extractedPath)
	}

	// Verify the content
	content, err := os.ReadFile(extractedPath)
	if err != nil {
		t.Fatalf("Failed to read extracted file: %v", err)
	}

	if string(content) != "Hello World" {
		t.Errorf("Expected content 'Hello World', got '%s'", string(content))
	}
}

func TestValidateAttachment(t *testing.T) {
	tempDir := t.TempDir()
	am := NewAttachmentManager(tempDir, tempDir)

	// Test valid attachment
	validAttachment := Attachment{
		Filename:    "document.pdf",
		ContentType: "application/pdf",
		Size:        1024,
		Data:        make([]byte, 1024),
	}

	err := am.ValidateAttachment(validAttachment)
	if err != nil {
		t.Errorf("Valid attachment failed validation: %v", err)
	}

	// Test dangerous Windows executable
	dangerousAttachment := Attachment{
		Filename:    "virus.exe",
		ContentType: "application/octet-stream",
		Size:        1024,
		Data:        make([]byte, 1024),
	}

	err = am.ValidateAttachment(dangerousAttachment)
	if err == nil {
		t.Error("Dangerous .exe attachment should have failed validation")
	}

	// Test dangerous shell script
	shellScriptAttachment := Attachment{
		Filename:    "malicious.sh",
		ContentType: "application/x-sh",
		Size:        512,
		Data:        make([]byte, 512),
	}

	err = am.ValidateAttachment(shellScriptAttachment)
	if err == nil {
		t.Error("Dangerous .sh attachment should have failed validation")
	}

	// Test dangerous batch file
	batchFileAttachment := Attachment{
		Filename:    "script.bat",
		ContentType: "application/x-bat",
		Size:        256,
		Data:        make([]byte, 256),
	}

	err = am.ValidateAttachment(batchFileAttachment)
	if err == nil {
		t.Error("Dangerous .bat attachment should have failed validation")
	}

	// Test oversized attachment
	oversizedAttachment := Attachment{
		Filename:    "huge.txt",
		ContentType: "text/plain",
		Size:        100 * 1024 * 1024, // 100MB
		Data:        make([]byte, 100*1024*1024),
	}

	err = am.ValidateAttachment(oversizedAttachment)
	if err == nil {
		t.Error("Oversized attachment should have failed validation")
	}
}

func TestValidateAttachmentSafety(t *testing.T) {
	tests := []struct {
		filename    string
		shouldError bool
		description string
	}{
		// Safe files
		{"document.pdf", false, "PDF document"},
		{"image.jpg", false, "JPEG image"},
		{"photo.png", false, "PNG image"},
		{"data.csv", false, "CSV file"},
		{"report.docx", false, "Word document"},
		{"presentation.pptx", false, "PowerPoint"},
		{"archive.zip", false, "ZIP archive"},
		{"text.txt", false, "Text file"},

		// Dangerous Windows executables
		{"virus.exe", true, "Windows executable"},
		{"script.bat", true, "Batch file"},
		{"command.cmd", true, "Command file"},
		{"program.com", true, "COM executable"},
		{"screensaver.scr", true, "Screensaver"},
		{"installer.msi", true, "MSI installer"},
		{"malware.vbs", true, "VBScript"},
		{"evil.js", true, "JavaScript"},

		// Dangerous Unix/Linux executables
		{"script.sh", true, "Shell script"},
		{"program.bash", true, "Bash script"},
		{"tool.zsh", true, "Zsh script"},
		{"app.run", true, "Run file"},
		{"binary.bin", true, "Binary file"},
		{"executable.out", true, "Out file"},

		// Dangerous script files
		{"script.py", true, "Python script"},
		{"program.pl", true, "Perl script"},
		{"app.rb", true, "Ruby script"},
		{"web.php", true, "PHP script"},
		{"powershell.ps1", true, "PowerShell script"},

		// Dangerous package files
		{"app.apk", true, "Android package"},
		{"installer.deb", true, "Debian package"},
		{"package.rpm", true, "RPM package"},
		{"disk.dmg", true, "macOS disk image"},
		{"installer.pkg", true, "macOS package"},

		// Case insensitivity test
		{"VIRUS.EXE", true, "Uppercase .EXE"},
		{"Script.SH", true, "Mixed case .SH"},
		{"Document.PDF", false, "Uppercase .PDF (safe)"},
	}

	for _, test := range tests {
		err := ValidateAttachmentSafety(test.filename)
		if test.shouldError && err == nil {
			t.Errorf("%s: Expected error for %s, but got none", test.description, test.filename)
		}
		if !test.shouldError && err != nil {
			t.Errorf("%s: Expected no error for %s, but got: %v", test.description, test.filename, err)
		}
	}

	// Test empty filename
	err := ValidateAttachmentSafety("")
	if err == nil {
		t.Error("Expected error for empty filename")
	}
}

func TestIsViewableType(t *testing.T) {
	tempDir := t.TempDir()
	am := NewAttachmentManager(tempDir, tempDir)

	viewableTypes := []string{
		"text/plain",
		"text/html",
		"image/jpeg",
		"image/png",
		"application/pdf",
	}

	for _, contentType := range viewableTypes {
		if !am.isViewableType(contentType) {
			t.Errorf("Content type %s should be viewable", contentType)
		}
	}

	nonViewableTypes := []string{
		"application/octet-stream",
		"video/mp4",
		"audio/mp3",
		"application/zip",
	}

	for _, contentType := range nonViewableTypes {
		if am.isViewableType(contentType) {
			t.Errorf("Content type %s should not be viewable", contentType)
		}
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"normal.txt", "normal.txt"},
		{"../../../etc/passwd", "______etc_passwd"},
		{"file:with*special?chars", "file_with_special_chars"},
		{"document<>|.pdf", "document___.pdf"},
		{"file/with/slashes.txt", "file_with_slashes.txt"},
		{"path\\with\\backslashes.doc", "path_with_backslashes.doc"},
	}

	for _, test := range tests {
		result := SanitizeFilename(test.input)
		if result != test.expected {
			t.Errorf("SanitizeFilename(%s) = %s, expected %s", test.input, result, test.expected)
		}
	}
}

func TestGetAttachmentPreview(t *testing.T) {
	tempDir := t.TempDir()
	am := NewAttachmentManager(tempDir, tempDir)

	attachment := Attachment{
		Filename:    "test.txt",
		ContentType: "text/plain",
		Size:        11,
		Data:        []byte("Hello World"),
	}

	messageID := "test-message-123"

	// Cache the attachment first
	info, err := am.CacheAttachment(messageID, attachment)
	if err != nil {
		t.Fatalf("CacheAttachment failed: %v", err)
	}

	// Get preview
	preview, err := am.GetAttachmentPreview(info.ID, 100)
	if err != nil {
		t.Fatalf("GetAttachmentPreview failed: %v", err)
	}

	if string(preview) != "Hello World" {
		t.Errorf("Expected preview 'Hello World', got '%s'", string(preview))
	}
}

func TestProcessMessageAttachments(t *testing.T) {
	tempDir := t.TempDir()
	am := NewAttachmentManager(tempDir, tempDir)

	msg := &Message{
		ID:      "test-message-123",
		Subject: "Test Message",
		Date:    time.Now(),
		Attachments: []Attachment{
			{
				Filename:    "doc1.txt",
				ContentType: "text/plain",
				Size:        5,
				Data:        []byte("Hello"),
			},
			{
				Filename:    "doc2.pdf",
				ContentType: "application/pdf",
				Size:        10,
				Data:        []byte("PDF content"),
			},
		},
	}

	infos, err := am.ProcessMessageAttachments(msg)
	if err != nil {
		t.Fatalf("ProcessMessageAttachments failed: %v", err)
	}

	if len(infos) != 2 {
		t.Errorf("Expected 2 attachment infos, got %d", len(infos))
	}

	// Verify both attachments were cached
	for _, info := range infos {
		if !am.IsAttachmentCached(info.ID) {
			t.Errorf("Attachment %s was not cached", info.ID)
		}
	}
}
