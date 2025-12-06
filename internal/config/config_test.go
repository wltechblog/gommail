package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConfigExists(t *testing.T) {
	// Test with non-existent config
	originalPath := os.Getenv("GOMMAIL_CONFIG_PATH")
	defer os.Setenv("GOMMAIL_CONFIG_PATH", originalPath)

	tempPath := filepath.Join(os.TempDir(), "test-config-nonexistent.yaml")
	os.Setenv("GOMMAIL_CONFIG_PATH", tempPath)

	if ConfigExists() {
		t.Error("ConfigExists should return false for non-existent config")
	}

	// Test with existing config
	tempDir := os.TempDir()
	tempFile := filepath.Join(tempDir, "test-config-exists.yaml")
	defer os.Remove(tempFile)

	// Create a temporary config file
	if err := os.WriteFile(tempFile, []byte("accounts: []"), 0600); err != nil {
		t.Fatalf("Failed to create temp config file: %v", err)
	}

	os.Setenv("GOMMAIL_CONFIG_PATH", tempFile)

	if !ConfigExists() {
		t.Error("ConfigExists should return true for existing config")
	}
}

func TestIsFirstRun(t *testing.T) {
	// Test first run (no config exists)
	originalPath := os.Getenv("GOMMAIL_CONFIG_PATH")
	defer os.Setenv("GOMMAIL_CONFIG_PATH", originalPath)

	tempPath := filepath.Join(os.TempDir(), "test-first-run.yaml")
	os.Setenv("GOMMAIL_CONFIG_PATH", tempPath)

	if !IsFirstRun() {
		t.Error("IsFirstRun should return true when no config exists")
	}

	// Create config file
	if err := os.WriteFile(tempPath, []byte("accounts: []"), 0600); err != nil {
		t.Fatalf("Failed to create temp config file: %v", err)
	}
	defer os.Remove(tempPath)

	if IsFirstRun() {
		t.Error("IsFirstRun should return false when config exists")
	}
}

func TestDefault(t *testing.T) {
	cfg := Default()

	if cfg == nil {
		t.Fatal("Default config is nil")
	}

	if len(cfg.Accounts) != 0 {
		t.Error("Default config should have no accounts")
	}

	if cfg.UI.Theme != "auto" {
		t.Errorf("Expected theme 'auto', got '%s'", cfg.UI.Theme)
	}

	if cfg.UI.DefaultMessageView != "html" {
		t.Errorf("Expected default message view 'html', got '%s'", cfg.UI.DefaultMessageView)
	}

	if cfg.UI.WindowSize.Width != 1200 {
		t.Errorf("Expected width 1200, got %d", cfg.UI.WindowSize.Width)
	}

	if cfg.UI.WindowSize.Height != 800 {
		t.Errorf("Expected height 800, got %d", cfg.UI.WindowSize.Height)
	}

	if cfg.Cache.MaxSizeMB != 500 {
		t.Errorf("Expected cache size 500MB, got %d", cfg.Cache.MaxSizeMB)
	}

	if !cfg.Cache.Compression {
		t.Error("Expected compression to be enabled")
	}
}

func TestDefaultMessageViewConfiguration(t *testing.T) {
	tests := []struct {
		name          string
		configYAML    string
		expectedView  string
		shouldDefault bool
	}{
		{
			name: "HTML view configured",
			configYAML: `
ui:
  theme: "auto"
  default_message_view: "html"
  window_size:
    width: 1200
    height: 800
accounts: []
cache:
  directory: "/tmp/test"
  max_size_mb: 100
  compression: true
`,
			expectedView:  "html",
			shouldDefault: false,
		},
		{
			name: "Text view configured",
			configYAML: `
ui:
  theme: "auto"
  default_message_view: "text"
  window_size:
    width: 1200
    height: 800
accounts: []
cache:
  directory: "/tmp/test"
  max_size_mb: 100
  compression: true
`,
			expectedView:  "text",
			shouldDefault: false,
		},
		{
			name: "No default_message_view specified",
			configYAML: `
ui:
  theme: "auto"
  window_size:
    width: 1200
    height: 800
accounts: []
cache:
  directory: "/tmp/test"
  max_size_mb: 100
  compression: true
`,
			expectedView:  "", // Should be empty when not specified
			shouldDefault: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary config file
			tempDir := os.TempDir()
			tempFile := filepath.Join(tempDir, "test-message-view.yaml")
			defer os.Remove(tempFile)

			// Write test config
			if err := os.WriteFile(tempFile, []byte(tt.configYAML), 0600); err != nil {
				t.Fatalf("Failed to write test config: %v", err)
			}

			// Set environment variable to use temp file
			originalPath := os.Getenv("GOMMAIL_CONFIG_PATH")
			os.Setenv("GOMMAIL_CONFIG_PATH", tempFile)
			defer func() {
				if originalPath != "" {
					os.Setenv("GOMMAIL_CONFIG_PATH", originalPath)
				} else {
					os.Unsetenv("GOMMAIL_CONFIG_PATH")
				}
			}()

			// Load config
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Failed to load config: %v", err)
			}

			if cfg.UI.DefaultMessageView != tt.expectedView {
				t.Errorf("Expected default message view '%s', got '%s'", tt.expectedView, cfg.UI.DefaultMessageView)
			}
		})
	}
}

func TestLoadSave(t *testing.T) {
	// Create a temporary config file
	tempDir := os.TempDir()
	tempFile := filepath.Join(tempDir, "test-load-save.yaml")
	defer os.Remove(tempFile)

	// Set environment variable to use temp file
	originalPath := os.Getenv("GOMMAIL_CONFIG_PATH")
	defer os.Setenv("GOMMAIL_CONFIG_PATH", originalPath)
	os.Setenv("GOMMAIL_CONFIG_PATH", tempFile)

	// Create test config
	cfg := Default()
	cfg.Accounts = []Account{
		{
			Name:        "Test Account",
			Email:       "test@example.com",
			DisplayName: "Test User",
			IMAP: ServerConfig{
				Host:     "imap.example.com",
				Port:     993,
				Username: "test@example.com",
				Password: "password",
				TLS:      true,
			},
			SMTP: ServerConfig{
				Host:     "smtp.example.com",
				Port:     587,
				Username: "test@example.com",
				Password: "password",
				TLS:      false,
			},
		},
	}

	// Save config
	if err := cfg.Save(); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	// Load config
	loadedCfg, err := Load()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify loaded config
	if len(loadedCfg.Accounts) != 1 {
		t.Errorf("Expected 1 account, got %d", len(loadedCfg.Accounts))
	}

	account := loadedCfg.Accounts[0]
	if account.Name != "Test Account" {
		t.Errorf("Expected account name 'Test Account', got '%s'", account.Name)
	}

	if account.Email != "test@example.com" {
		t.Errorf("Expected email 'test@example.com', got '%s'", account.Email)
	}

	if account.IMAP.Host != "imap.example.com" {
		t.Errorf("Expected IMAP host 'imap.example.com', got '%s'", account.IMAP.Host)
	}

	if account.SMTP.Host != "smtp.example.com" {
		t.Errorf("Expected SMTP host 'smtp.example.com', got '%s'", account.SMTP.Host)
	}
}

func TestUpdateServerValidation(t *testing.T) {
	cfg := Default()
	cfg.Accounts = []Account{
		{
			Name:  "Test Account",
			Email: "test@example.com",
			IMAP: ServerConfig{
				Host: "imap.example.com",
				Port: 993,
			},
			SMTP: ServerConfig{
				Host: "smtp.example.com",
				Port: 587,
			},
		},
	}

	warnings := []string{"Certificate expired", "Self-signed certificate"}
	certificateHost := "mail.example.com"

	// Update IMAP validation
	cfg.UpdateServerValidation("Test Account", "imap", warnings, certificateHost)

	account := cfg.Accounts[0]
	if len(account.IMAP.ValidationWarnings) != 2 {
		t.Errorf("Expected 2 IMAP warnings, got %d", len(account.IMAP.ValidationWarnings))
	}

	if account.IMAP.AcceptedCertificateHost != certificateHost {
		t.Errorf("Expected certificate host '%s', got '%s'", certificateHost, account.IMAP.AcceptedCertificateHost)
	}

	if account.IMAP.LastValidated == "" {
		t.Error("LastValidated should be set")
	}

	// Verify timestamp format
	if _, err := time.Parse("2006-01-02 15:04:05", account.IMAP.LastValidated); err != nil {
		t.Errorf("Invalid timestamp format: %v", err)
	}

	// Update SMTP validation
	cfg.UpdateServerValidation("Test Account", "smtp", warnings, certificateHost)

	// Get updated account reference
	account = cfg.Accounts[0]
	if len(account.SMTP.ValidationWarnings) != 2 {
		t.Errorf("Expected 2 SMTP warnings, got %d", len(account.SMTP.ValidationWarnings))
	}
}

func TestAcceptCertificateWarnings(t *testing.T) {
	cfg := Default()
	cfg.Accounts = []Account{
		{
			Name:  "Test Account",
			Email: "test@example.com",
			IMAP: ServerConfig{
				Host: "imap.example.com",
				Port: 993,
			},
			SMTP: ServerConfig{
				Host: "smtp.example.com",
				Port: 587,
			},
		},
	}

	// Accept IMAP certificate warnings
	cfg.AcceptCertificateWarnings("Test Account", "imap")

	account := cfg.Accounts[0]
	if !account.IMAP.IgnoreCertificateErrors {
		t.Error("IMAP IgnoreCertificateErrors should be true")
	}

	// Accept SMTP certificate warnings
	cfg.AcceptCertificateWarnings("Test Account", "smtp")

	// Get updated account reference
	account = cfg.Accounts[0]
	if !account.SMTP.IgnoreCertificateErrors {
		t.Error("SMTP IgnoreCertificateErrors should be true")
	}
}

func TestUpdateServerValidation_NonExistentAccount(t *testing.T) {
	cfg := Default()
	cfg.Accounts = []Account{
		{
			Name:  "Test Account",
			Email: "test@example.com",
		},
	}

	// Try to update non-existent account
	cfg.UpdateServerValidation("Non-existent Account", "imap", []string{"warning"}, "host")

	// Should not panic and should not affect existing account
	account := cfg.Accounts[0]
	if len(account.IMAP.ValidationWarnings) != 0 {
		t.Error("Non-existent account update should not affect existing accounts")
	}
}

func TestGetConfigDir(t *testing.T) {
	originalPath := os.Getenv("GOMMAIL_CONFIG_PATH")
	defer os.Setenv("GOMMAIL_CONFIG_PATH", originalPath)

	testPath := "/tmp/test/config.yaml"
	os.Setenv("GOMMAIL_CONFIG_PATH", testPath)

	expectedDir := "/tmp/test"
	actualDir := GetConfigDir()

	if actualDir != expectedDir {
		t.Errorf("Expected config dir '%s', got '%s'", expectedDir, actualDir)
	}
}

func TestSpecialFolderConfiguration(t *testing.T) {
	// Create a temporary config file with sent and trash folder configuration
	configContent := `
accounts:
  - name: "Gmail Test"
    email: "test@gmail.com"
    display_name: "Test User"
    sent_folder: "[Gmail]/Sent Mail"
    trash_folder: "[Gmail]/Trash"
    imap:
      host: "imap.gmail.com"
      port: 993
      username: "test@gmail.com"
      password: "password"
      tls: true
    smtp:
      host: "smtp.gmail.com"
      port: 587
      username: "test@gmail.com"
      password: "password"
      tls: false
  - name: "Outlook Test"
    email: "test@outlook.com"
    display_name: "Test User"
    sent_folder: "Sent Items"
    trash_folder: "Deleted Items"
    imap:
      host: "outlook.office365.com"
      port: 993
      username: "test@outlook.com"
      password: "password"
      tls: true
    smtp:
      host: "smtp-mail.outlook.com"
      port: 587
      username: "test@outlook.com"
      password: "password"
      tls: false
  - name: "Generic Test"
    email: "test@example.com"
    display_name: "Test User"
    # No sent_folder or trash_folder specified - should use auto-detection
    imap:
      host: "imap.example.com"
      port: 993
      username: "test@example.com"
      password: "password"
      tls: true
    smtp:
      host: "smtp.example.com"
      port: 587
      username: "test@example.com"
      password: "password"
      tls: false

ui:
  theme: "auto"
  window_size:
    width: 1200
    height: 800

cache:
  directory: "/tmp/test-cache"
  max_size_mb: 100
  compression: true
`

	tempDir := os.TempDir()
	tempFile := filepath.Join(tempDir, "test-trash-config.yaml")
	defer os.Remove(tempFile)

	// Write the config file
	if err := os.WriteFile(tempFile, []byte(configContent), 0600); err != nil {
		t.Fatalf("Failed to create temp config file: %v", err)
	}

	// Set environment variable to use our test config
	originalPath := os.Getenv("GOMMAIL_CONFIG_PATH")
	defer os.Setenv("GOMMAIL_CONFIG_PATH", originalPath)
	os.Setenv("GOMMAIL_CONFIG_PATH", tempFile)

	// Load the configuration
	config, err := Load()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Test that we have the expected number of accounts
	if len(config.Accounts) != 3 {
		t.Fatalf("Expected 3 accounts, got %d", len(config.Accounts))
	}

	// Test Gmail account special folders
	gmailAccount := config.Accounts[0]
	if gmailAccount.Name != "Gmail Test" {
		t.Errorf("Expected first account name 'Gmail Test', got '%s'", gmailAccount.Name)
	}
	if gmailAccount.SentFolder != "[Gmail]/Sent Mail" {
		t.Errorf("Expected Gmail sent folder '[Gmail]/Sent Mail', got '%s'", gmailAccount.SentFolder)
	}
	if gmailAccount.TrashFolder != "[Gmail]/Trash" {
		t.Errorf("Expected Gmail trash folder '[Gmail]/Trash', got '%s'", gmailAccount.TrashFolder)
	}

	// Test Outlook account special folders
	outlookAccount := config.Accounts[1]
	if outlookAccount.Name != "Outlook Test" {
		t.Errorf("Expected second account name 'Outlook Test', got '%s'", outlookAccount.Name)
	}
	if outlookAccount.SentFolder != "Sent Items" {
		t.Errorf("Expected Outlook sent folder 'Sent Items', got '%s'", outlookAccount.SentFolder)
	}
	if outlookAccount.TrashFolder != "Deleted Items" {
		t.Errorf("Expected Outlook trash folder 'Deleted Items', got '%s'", outlookAccount.TrashFolder)
	}

	// Test generic account (no special folders specified)
	genericAccount := config.Accounts[2]
	if genericAccount.Name != "Generic Test" {
		t.Errorf("Expected third account name 'Generic Test', got '%s'", genericAccount.Name)
	}
	if genericAccount.SentFolder != "" {
		t.Errorf("Expected generic account sent folder to be empty, got '%s'", genericAccount.SentFolder)
	}
	if genericAccount.TrashFolder != "" {
		t.Errorf("Expected generic account trash folder to be empty, got '%s'", genericAccount.TrashFolder)
	}
}
