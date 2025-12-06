package config

import (
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2/app"
)

// TestEndToEndMigration tests the complete migration flow from YAML to Preferences
func TestEndToEndMigration(t *testing.T) {
	testApp := app.NewWithID("com.wltechblog.mail.test.integration")
	defer testApp.Quit()

	// Create a temporary directory for this test
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	// Set environment variable to use our temp config
	originalConfigPath := os.Getenv("GOMMAIL_CONFIG_PATH")
	os.Setenv("GOMMAIL_CONFIG_PATH", configPath)
	defer func() {
		if originalConfigPath != "" {
			os.Setenv("GOMMAIL_CONFIG_PATH", originalConfigPath)
		} else {
			os.Unsetenv("GOMMAIL_CONFIG_PATH")
		}
	}()

	// Step 1: Create a comprehensive YAML configuration
	yamlContent := `accounts:
  - name: "Primary Account"
    email: "primary@example.com"
    display_name: "Primary User"
    sent_folder: "Sent Messages"
    trash_folder: "Deleted Items"
    imap:
      host: "imap.primary.com"
      port: 993
      username: "primary@example.com"
      password: "primary-password"
      tls: true
      ignore_certificate_errors: false
      accepted_certificate_host: "imap.primary.com"
      last_validated: "2024-01-15 10:30:00"
      validation_warnings:
        - "Certificate expires in 30 days"
    smtp:
      host: "smtp.primary.com"
      port: 587
      username: "primary@example.com"
      password: "primary-password"
      tls: false
    personalities:
      - name: "Professional"
        email: "primary@example.com"
        display_name: "Primary Professional"
        signature: "Best regards,\nPrimary Professional\nCompany Name"
      - name: "Support"
        email: "support@example.com"
        display_name: "Support Team"
        signature: "Thank you for contacting us.\n\nSupport Team"

  - name: "Secondary Account"
    email: "secondary@example.com"
    display_name: "Secondary User"
    sent_folder: "Sent"
    trash_folder: "Trash"
    imap:
      host: "imap.secondary.com"
      port: 993
      username: "secondary@example.com"
      password: "secondary-password"
      tls: true
    smtp:
      host: "smtp.secondary.com"
      port: 587
      username: "secondary@example.com"
      password: "secondary-password"
      tls: false

ui:
  theme: "dark"
  default_message_view: "text"
  window_size:
    width: 1600
    height: 1000

cache:
  directory: "/tmp/integration-cache"
  max_size_mb: 1000
  compression: true

logging:
  level: "debug"
  format: "json"
  file: "/tmp/integration.log"
  max_size_mb: 50
  max_backups: 10
  max_age_days: 90
`

	if err := os.WriteFile(configPath, []byte(yamlContent), 0600); err != nil {
		t.Fatalf("Failed to write test YAML config: %v", err)
	}

	// Step 2: Use LoadConfig to trigger migration
	configManager, err := LoadConfig(testApp, "test")
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	// Verify we got a PreferencesConfig
	prefsConfig, ok := configManager.(*PreferencesConfig)
	if !ok {
		t.Fatal("Expected PreferencesConfig after migration")
	}

	// Verify migration flags
	if !prefsConfig.IsInitialized() {
		t.Error("Preferences should be initialized after migration")
	}

	if !prefsConfig.IsMigratedFromYAML() {
		t.Error("Should be marked as migrated from YAML")
	}

	if prefsConfig.GetVersion() != "1.0" {
		t.Errorf("Expected version '1.0', got '%s'", prefsConfig.GetVersion())
	}

	// Step 3: Load and verify all migrated data
	if err := prefsConfig.Load(); err != nil {
		t.Fatalf("Failed to load migrated preferences: %v", err)
	}

	// Verify accounts
	accounts := prefsConfig.GetAccounts()
	if len(accounts) != 2 {
		t.Errorf("Expected 2 accounts, got %d", len(accounts))
	}

	// Verify primary account
	primary := accounts[0]
	if primary.Name != "Primary Account" {
		t.Errorf("Expected primary account name 'Primary Account', got '%s'", primary.Name)
	}

	if primary.Email != "primary@example.com" {
		t.Errorf("Expected primary email 'primary@example.com', got '%s'", primary.Email)
	}

	if primary.SentFolder != "Sent Messages" {
		t.Errorf("Expected sent folder 'Sent Messages', got '%s'", primary.SentFolder)
	}

	if primary.TrashFolder != "Deleted Items" {
		t.Errorf("Expected trash folder 'Deleted Items', got '%s'", primary.TrashFolder)
	}

	// Verify IMAP settings
	if primary.IMAP.Host != "imap.primary.com" {
		t.Errorf("Expected IMAP host 'imap.primary.com', got '%s'", primary.IMAP.Host)
	}

	if primary.IMAP.Port != 993 {
		t.Errorf("Expected IMAP port 993, got %d", primary.IMAP.Port)
	}

	if primary.IMAP.AcceptedCertificateHost != "imap.primary.com" {
		t.Errorf("Expected certificate host 'imap.primary.com', got '%s'", primary.IMAP.AcceptedCertificateHost)
	}

	if primary.IMAP.LastValidated != "2024-01-15 10:30:00" {
		t.Errorf("Expected last validated '2024-01-15 10:30:00', got '%s'", primary.IMAP.LastValidated)
	}

	if len(primary.IMAP.ValidationWarnings) != 1 {
		t.Errorf("Expected 1 validation warning, got %d", len(primary.IMAP.ValidationWarnings))
	}

	// Verify personalities
	if len(primary.Personalities) != 2 {
		t.Errorf("Expected 2 personalities, got %d", len(primary.Personalities))
	}

	prof := primary.Personalities[0]
	if prof.Name != "Professional" {
		t.Errorf("Expected personality name 'Professional', got '%s'", prof.Name)
	}

	if prof.Signature != "Best regards,\nPrimary Professional\nCompany Name" {
		t.Errorf("Expected professional signature, got '%s'", prof.Signature)
	}

	// Verify secondary account
	secondary := accounts[1]
	if secondary.Name != "Secondary Account" {
		t.Errorf("Expected secondary account name 'Secondary Account', got '%s'", secondary.Name)
	}

	if secondary.Email != "secondary@example.com" {
		t.Errorf("Expected secondary email 'secondary@example.com', got '%s'", secondary.Email)
	}

	// Verify UI configuration
	ui := prefsConfig.GetUI()
	if ui.Theme != "dark" {
		t.Errorf("Expected theme 'dark', got '%s'", ui.Theme)
	}

	if ui.DefaultMessageView != "text" {
		t.Errorf("Expected default message view 'text', got '%s'", ui.DefaultMessageView)
	}

	if ui.WindowSize.Width != 1600 {
		t.Errorf("Expected window width 1600, got %d", ui.WindowSize.Width)
	}

	if ui.WindowSize.Height != 1000 {
		t.Errorf("Expected window height 1000, got %d", ui.WindowSize.Height)
	}

	// Verify cache configuration
	cache := prefsConfig.GetCache()
	if cache.Directory != "/tmp/integration-cache" {
		t.Errorf("Expected cache directory '/tmp/integration-cache', got '%s'", cache.Directory)
	}

	if cache.MaxSizeMB != 1000 {
		t.Errorf("Expected cache max size 1000, got %d", cache.MaxSizeMB)
	}

	if cache.Compression != true {
		t.Errorf("Expected cache compression true, got %v", cache.Compression)
	}

	// Verify logging configuration
	logging := prefsConfig.GetLogging()
	if logging.Level != "debug" {
		t.Errorf("Expected log level 'debug', got '%s'", logging.Level)
	}

	if logging.Format != "json" {
		t.Errorf("Expected log format 'json', got '%s'", logging.Format)
	}

	if logging.File != "/tmp/integration.log" {
		t.Errorf("Expected log file '/tmp/integration.log', got '%s'", logging.File)
	}

	if logging.MaxSizeMB != 50 {
		t.Errorf("Expected log max size 50, got %d", logging.MaxSizeMB)
	}

	if logging.MaxBackups != 10 {
		t.Errorf("Expected log max backups 10, got %d", logging.MaxBackups)
	}

	if logging.MaxAgeDays != 90 {
		t.Errorf("Expected log max age 90, got %d", logging.MaxAgeDays)
	}

	// Step 4: Test that subsequent loads use preferences (not YAML)
	configManager2, err := LoadConfig(testApp, "test")
	if err != nil {
		t.Fatalf("Second LoadConfig failed: %v", err)
	}

	prefsConfig2, ok := configManager2.(*PreferencesConfig)
	if !ok {
		t.Fatal("Expected PreferencesConfig on second load")
	}

	if err := prefsConfig2.Load(); err != nil {
		t.Fatalf("Failed to load preferences on second load: %v", err)
	}

	// Verify data is still there
	accounts2 := prefsConfig2.GetAccounts()
	if len(accounts2) != 2 {
		t.Errorf("Expected 2 accounts on second load, got %d", len(accounts2))
	}

	if accounts2[0].Name != "Primary Account" {
		t.Errorf("Expected primary account name preserved on second load, got '%s'", accounts2[0].Name)
	}

	// Step 5: Test configuration updates persist
	testUI := UIConfig{
		Theme:              "light",
		DefaultMessageView: "html",
		WindowSize: struct {
			Width  int `yaml:"width"`
			Height int `yaml:"height"`
		}{
			Width:  1800,
			Height: 1200,
		},
	}

	prefsConfig2.SetUI(testUI)
	if err := prefsConfig2.Save(); err != nil {
		t.Fatalf("Failed to save updated configuration: %v", err)
	}

	// Load again and verify changes
	configManager3, err := LoadConfig(testApp, "test")
	if err != nil {
		t.Fatalf("Third LoadConfig failed: %v", err)
	}

	prefsConfig3 := configManager3.(*PreferencesConfig)
	if err := prefsConfig3.Load(); err != nil {
		t.Fatalf("Failed to load preferences on third load: %v", err)
	}

	ui3 := prefsConfig3.GetUI()
	if ui3.Theme != "light" {
		t.Errorf("Expected updated theme 'light', got '%s'", ui3.Theme)
	}

	if ui3.WindowSize.Width != 1800 {
		t.Errorf("Expected updated window width 1800, got %d", ui3.WindowSize.Width)
	}

	t.Log("End-to-end migration test completed successfully")
}
