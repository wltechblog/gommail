package config

import (
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2/app"
)

func TestConfigFactory_CreateConfigManager_NewPreferences(t *testing.T) {
	testApp := app.NewWithID("com.wltechblog.mail.test.factory1")
	defer testApp.Quit()

	factory := NewConfigFactory(testApp, "test")
	if factory == nil {
		t.Fatal("NewConfigFactory returned nil")
	}

	// Ensure no existing configuration
	configPath := getConfigPath()
	os.Remove(configPath)

	configManager, err := factory.CreateConfigManager()
	if err != nil {
		t.Fatalf("CreateConfigManager failed: %v", err)
	}

	// Should return PreferencesConfig
	prefsConfig, ok := configManager.(*PreferencesConfig)
	if !ok {
		t.Error("Expected PreferencesConfig, got different type")
	}

	if prefsConfig.IsInitialized() {
		t.Error("New preferences should not be initialized")
	}
}

func TestConfigFactory_CreateConfigManager_ExistingPreferences(t *testing.T) {
	testApp := app.NewWithID("com.wltechblog.mail.test.factory2")
	defer testApp.Quit()

	// Set up existing preferences
	prefsConfig := NewPreferencesConfig(testApp, "test")
	testAccounts := []Account{
		{
			Name:        "Existing Account",
			Email:       "existing@example.com",
			DisplayName: "Existing User",
		},
	}
	prefsConfig.SetAccounts(testAccounts)
	if err := prefsConfig.Save(); err != nil {
		t.Fatalf("Failed to save test preferences: %v", err)
	}

	// Create factory and load
	factory := NewConfigFactory(testApp, "test")
	configManager, err := factory.CreateConfigManager()
	if err != nil {
		t.Fatalf("CreateConfigManager failed: %v", err)
	}

	// Should return PreferencesConfig with existing data
	prefsConfig2, ok := configManager.(*PreferencesConfig)
	if !ok {
		t.Error("Expected PreferencesConfig, got different type")
	}

	if !prefsConfig2.IsInitialized() {
		t.Error("Existing preferences should be initialized")
	}

	// Load and verify data
	if err := prefsConfig2.Load(); err != nil {
		t.Fatalf("Failed to load preferences: %v", err)
	}

	accounts := prefsConfig2.GetAccounts()
	if len(accounts) != 1 {
		t.Errorf("Expected 1 account, got %d", len(accounts))
	}

	if accounts[0].Name != "Existing Account" {
		t.Errorf("Expected account name 'Existing Account', got '%s'", accounts[0].Name)
	}
}

func TestConfigFactory_MigrateFromYAML(t *testing.T) {
	testApp := app.NewWithID("com.wltechblog.mail.test.migrate")
	defer testApp.Quit()

	// Create a temporary YAML config file
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

	// Create test YAML content
	yamlContent := `accounts:
  - name: "YAML Account"
    email: "yaml@example.com"
    display_name: "YAML User"
    sent_folder: "Sent"
    trash_folder: "Trash"
    imap:
      host: "imap.example.com"
      port: 993
      username: "yaml@example.com"
      password: "yaml-password"
      tls: true
    smtp:
      host: "smtp.example.com"
      port: 587
      username: "yaml@example.com"
      password: "yaml-password"
      tls: false
    personalities:
      - name: "Professional"
        email: "yaml@example.com"
        display_name: "YAML Professional"
        signature: "Best regards"

ui:
  theme: "light"
  default_message_view: "text"
  window_size:
    width: 1000
    height: 600

cache:
  directory: "/tmp/yaml-cache"
  max_size_mb: 200
  compression: false

logging:
  level: "warn"
  format: "json"
  file: "/tmp/yaml.log"
  max_size_mb: 20
  max_backups: 10
  max_age_days: 60
`

	if err := os.WriteFile(configPath, []byte(yamlContent), 0600); err != nil {
		t.Fatalf("Failed to write test YAML config: %v", err)
	}

	// Create factory and migrate
	factory := NewConfigFactory(testApp, "test")
	configManager, err := factory.CreateConfigManager()
	if err != nil {
		t.Fatalf("CreateConfigManager failed: %v", err)
	}

	// Should return PreferencesConfig with migrated data
	prefsConfig, ok := configManager.(*PreferencesConfig)
	if !ok {
		t.Error("Expected PreferencesConfig, got different type")
	}

	if !prefsConfig.IsInitialized() {
		t.Error("Migrated preferences should be initialized")
	}

	if !prefsConfig.IsMigratedFromYAML() {
		t.Error("Should be marked as migrated from YAML")
	}

	// Load and verify migrated data
	if err := prefsConfig.Load(); err != nil {
		t.Fatalf("Failed to load migrated preferences: %v", err)
	}

	// Verify accounts
	accounts := prefsConfig.GetAccounts()
	if len(accounts) != 1 {
		t.Errorf("Expected 1 migrated account, got %d", len(accounts))
	}

	account := accounts[0]
	if account.Name != "YAML Account" {
		t.Errorf("Expected account name 'YAML Account', got '%s'", account.Name)
	}

	if account.Email != "yaml@example.com" {
		t.Errorf("Expected email 'yaml@example.com', got '%s'", account.Email)
	}

	if account.SentFolder != "Sent" {
		t.Errorf("Expected sent folder 'Sent', got '%s'", account.SentFolder)
	}

	if account.TrashFolder != "Trash" {
		t.Errorf("Expected trash folder 'Trash', got '%s'", account.TrashFolder)
	}

	// Verify IMAP config
	if account.IMAP.Host != "imap.example.com" {
		t.Errorf("Expected IMAP host 'imap.example.com', got '%s'", account.IMAP.Host)
	}

	if account.IMAP.Port != 993 {
		t.Errorf("Expected IMAP port 993, got %d", account.IMAP.Port)
	}

	if account.IMAP.TLS != true {
		t.Errorf("Expected IMAP TLS true, got %v", account.IMAP.TLS)
	}

	// Verify personalities
	if len(account.Personalities) != 1 {
		t.Errorf("Expected 1 personality, got %d", len(account.Personalities))
	}

	personality := account.Personalities[0]
	if personality.Name != "Professional" {
		t.Errorf("Expected personality name 'Professional', got '%s'", personality.Name)
	}

	if personality.Signature != "Best regards" {
		t.Errorf("Expected signature 'Best regards', got '%s'", personality.Signature)
	}

	// Verify UI config
	ui := prefsConfig.GetUI()
	if ui.Theme != "light" {
		t.Errorf("Expected theme 'light', got '%s'", ui.Theme)
	}

	if ui.DefaultMessageView != "text" {
		t.Errorf("Expected default message view 'text', got '%s'", ui.DefaultMessageView)
	}

	if ui.WindowSize.Width != 1000 {
		t.Errorf("Expected window width 1000, got %d", ui.WindowSize.Width)
	}

	// Verify cache config
	cache := prefsConfig.GetCache()
	if cache.Directory != "/tmp/yaml-cache" {
		t.Errorf("Expected cache directory '/tmp/yaml-cache', got '%s'", cache.Directory)
	}

	if cache.MaxSizeMB != 200 {
		t.Errorf("Expected cache max size 200, got %d", cache.MaxSizeMB)
	}

	if cache.Compression != false {
		t.Errorf("Expected cache compression false, got %v", cache.Compression)
	}

	// Verify logging config
	logging := prefsConfig.GetLogging()
	if logging.Level != "warn" {
		t.Errorf("Expected log level 'warn', got '%s'", logging.Level)
	}

	if logging.Format != "json" {
		t.Errorf("Expected log format 'json', got '%s'", logging.Format)
	}

	if logging.File != "/tmp/yaml.log" {
		t.Errorf("Expected log file '/tmp/yaml.log', got '%s'", logging.File)
	}

	if logging.MaxSizeMB != 20 {
		t.Errorf("Expected log max size 20, got %d", logging.MaxSizeMB)
	}
}

func TestLoadConfig(t *testing.T) {
	testApp := app.NewWithID("com.wltechblog.mail.test.loadconfig")
	defer testApp.Quit()

	// Ensure clean state
	configPath := getConfigPath()
	os.Remove(configPath)

	configManager, err := LoadConfig(testApp, "test")
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if configManager == nil {
		t.Fatal("LoadConfig returned nil")
	}

	// Should be PreferencesConfig for new installation
	_, ok := configManager.(*PreferencesConfig)
	if !ok {
		t.Error("Expected PreferencesConfig from LoadConfig")
	}
}

func TestIsFirstRunWithApp(t *testing.T) {
	testApp := app.NewWithID("com.wltechblog.mail.test.firstrun")
	defer testApp.Quit()

	// Clean state - should be first run
	configPath := getConfigPath()
	os.Remove(configPath)

	if !IsFirstRunWithApp(testApp, "test") {
		t.Error("Should be first run with no configuration")
	}

	// Create preferences - should not be first run
	prefsConfig := NewPreferencesConfig(testApp, "test")
	if err := prefsConfig.Save(); err != nil {
		t.Fatalf("Failed to save preferences: %v", err)
	}

	if IsFirstRunWithApp(testApp, "test") {
		t.Error("Should not be first run with existing preferences")
	}
}
