package config

import (
	"testing"

	"fyne.io/fyne/v2/app"
)

func TestPreferencesConfig_NewPreferencesConfig(t *testing.T) {
	testApp := app.NewWithID("com.wltechblog.mail.test")
	defer testApp.Quit()

	pc := NewPreferencesConfig(testApp, "test")
	if pc == nil {
		t.Fatal("NewPreferencesConfig returned nil")
	}

	if pc.app != testApp {
		t.Error("App not set correctly")
	}

	if pc.prefs == nil {
		t.Error("Preferences not initialized")
	}

	if pc.logger == nil {
		t.Error("Logger not initialized")
	}
}

func TestPreferencesConfig_AccountsManagement(t *testing.T) {
	testApp := app.NewWithID("com.wltechblog.mail.test.accounts")
	defer testApp.Quit()

	pc := NewPreferencesConfig(testApp, "test")

	// Test empty accounts initially
	accounts := pc.GetAccounts()
	if len(accounts) != 0 {
		t.Errorf("Expected 0 accounts, got %d", len(accounts))
	}

	// Test setting accounts
	testAccounts := []Account{
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

	pc.SetAccounts(testAccounts)
	retrievedAccounts := pc.GetAccounts()

	if len(retrievedAccounts) != 1 {
		t.Errorf("Expected 1 account, got %d", len(retrievedAccounts))
	}

	if retrievedAccounts[0].Name != "Test Account" {
		t.Errorf("Expected account name 'Test Account', got '%s'", retrievedAccounts[0].Name)
	}

	if retrievedAccounts[0].Email != "test@example.com" {
		t.Errorf("Expected email 'test@example.com', got '%s'", retrievedAccounts[0].Email)
	}
}

func TestPreferencesConfig_SaveAndLoad(t *testing.T) {
	testApp := app.NewWithID("com.wltechblog.mail.test.saveload")
	defer testApp.Quit()

	pc := NewPreferencesConfig(testApp, "test")

	// Set up test data
	testAccounts := []Account{
		{
			Name:        "Gmail",
			Email:       "user@gmail.com",
			DisplayName: "Gmail User",
			IMAP: ServerConfig{
				Host:     "imap.gmail.com",
				Port:     993,
				Username: "user@gmail.com",
				Password: "app-password",
				TLS:      true,
			},
			SMTP: ServerConfig{
				Host:     "smtp.gmail.com",
				Port:     587,
				Username: "user@gmail.com",
				Password: "app-password",
				TLS:      false,
			},
		},
	}

	testUI := UIConfig{
		Theme:              "dark",
		DefaultMessageView: "text",
		WindowSize: struct {
			Width  int `yaml:"width"`
			Height int `yaml:"height"`
		}{
			Width:  1400,
			Height: 900,
		},
	}

	testCache := CacheConfig{
		Directory:   "/tmp/test-cache",
		MaxSizeMB:   100,
		Compression: false,
	}

	testLogging := LoggingConfig{
		Level:      "debug",
		Format:     "json",
		File:       "/tmp/test.log",
		MaxSizeMB:  5,
		MaxBackups: 3,
		MaxAgeDays: 7,
	}

	// Set all configuration
	pc.SetAccounts(testAccounts)
	pc.SetUI(testUI)
	pc.SetCache(testCache)
	pc.SetLogging(testLogging)

	// Save configuration
	if err := pc.Save(); err != nil {
		t.Fatalf("Failed to save configuration: %v", err)
	}

	// Create new instance and load
	pc2 := NewPreferencesConfig(testApp, "test")
	if err := pc2.Load(); err != nil {
		t.Fatalf("Failed to load configuration: %v", err)
	}

	// Verify accounts
	loadedAccounts := pc2.GetAccounts()
	if len(loadedAccounts) != 1 {
		t.Errorf("Expected 1 account after load, got %d", len(loadedAccounts))
	}

	if loadedAccounts[0].Name != "Gmail" {
		t.Errorf("Expected account name 'Gmail', got '%s'", loadedAccounts[0].Name)
	}

	// Verify UI config
	loadedUI := pc2.GetUI()
	if loadedUI.Theme != "dark" {
		t.Errorf("Expected theme 'dark', got '%s'", loadedUI.Theme)
	}

	if loadedUI.DefaultMessageView != "text" {
		t.Errorf("Expected default message view 'text', got '%s'", loadedUI.DefaultMessageView)
	}

	if loadedUI.WindowSize.Width != 1400 {
		t.Errorf("Expected window width 1400, got %d", loadedUI.WindowSize.Width)
	}

	// Verify cache config
	loadedCache := pc2.GetCache()
	if loadedCache.Directory != "/tmp/test-cache" {
		t.Errorf("Expected cache directory '/tmp/test-cache', got '%s'", loadedCache.Directory)
	}

	if loadedCache.MaxSizeMB != 100 {
		t.Errorf("Expected cache max size 100, got %d", loadedCache.MaxSizeMB)
	}

	if loadedCache.Compression != false {
		t.Errorf("Expected cache compression false, got %v", loadedCache.Compression)
	}

	// Verify logging config
	loadedLogging := pc2.GetLogging()
	if loadedLogging.Level != "debug" {
		t.Errorf("Expected logging level 'debug', got '%s'", loadedLogging.Level)
	}

	if loadedLogging.Format != "json" {
		t.Errorf("Expected logging format 'json', got '%s'", loadedLogging.Format)
	}
}

func TestPreferencesConfig_JSONSerialization(t *testing.T) {
	testApp := app.NewWithID("com.wltechblog.mail.test.json")
	defer testApp.Quit()

	pc := NewPreferencesConfig(testApp, "test")

	// Test complex account with personalities
	testAccounts := []Account{
		{
			Name:        "Work Email",
			Email:       "work@company.com",
			DisplayName: "Work User",
			SentFolder:  "Sent Items",
			TrashFolder: "Deleted Items",
			IMAP: ServerConfig{
				Host:                    "mail.company.com",
				Port:                    993,
				Username:                "work@company.com",
				Password:                "work-password",
				TLS:                     true,
				IgnoreCertificateErrors: false,
				AcceptedCertificateHost: "mail.company.com",
				LastValidated:           "2024-01-01 12:00:00",
				ValidationWarnings:      []string{"Certificate expires soon"},
			},
			SMTP: ServerConfig{
				Host:     "mail.company.com",
				Port:     587,
				Username: "work@company.com",
				Password: "work-password",
				TLS:      false,
			},
			Personalities: []Personality{
				{
					Name:        "Professional",
					Email:       "work@company.com",
					DisplayName: "Professional Name",
					Signature:   "Best regards,\nProfessional Name",
					IsDefault:   true,
				},
				{
					Name:        "Support",
					Email:       "support@company.com",
					DisplayName: "Support Team",
					Signature:   "Thank you,\nSupport Team",
					IsDefault:   false,
				},
			},
		},
	}

	pc.SetAccounts(testAccounts)

	// Save and reload
	if err := pc.Save(); err != nil {
		t.Fatalf("Failed to save configuration: %v", err)
	}

	pc2 := NewPreferencesConfig(testApp, "test")
	if err := pc2.Load(); err != nil {
		t.Fatalf("Failed to load configuration: %v", err)
	}

	loadedAccounts := pc2.GetAccounts()
	if len(loadedAccounts) != 1 {
		t.Fatalf("Expected 1 account, got %d", len(loadedAccounts))
	}

	account := loadedAccounts[0]

	// Verify complex fields
	if account.SentFolder != "Sent Items" {
		t.Errorf("Expected sent folder 'Sent Items', got '%s'", account.SentFolder)
	}

	if account.TrashFolder != "Deleted Items" {
		t.Errorf("Expected trash folder 'Deleted Items', got '%s'", account.TrashFolder)
	}

	// Verify server validation fields
	if account.IMAP.AcceptedCertificateHost != "mail.company.com" {
		t.Errorf("Expected certificate host 'mail.company.com', got '%s'", account.IMAP.AcceptedCertificateHost)
	}

	if len(account.IMAP.ValidationWarnings) != 1 {
		t.Errorf("Expected 1 validation warning, got %d", len(account.IMAP.ValidationWarnings))
	}

	// Verify personalities
	if len(account.Personalities) != 2 {
		t.Errorf("Expected 2 personalities, got %d", len(account.Personalities))
	}

	if account.Personalities[0].Name != "Professional" {
		t.Errorf("Expected personality name 'Professional', got '%s'", account.Personalities[0].Name)
	}

	if account.Personalities[1].Email != "support@company.com" {
		t.Errorf("Expected personality email 'support@company.com', got '%s'", account.Personalities[1].Email)
	}
}

func TestPreferencesConfig_DefaultValues(t *testing.T) {
	testApp := app.NewWithID("com.wltechblog.mail.test.defaults")
	defer testApp.Quit()

	pc := NewPreferencesConfig(testApp, "test")

	// Test default UI values
	ui := pc.GetUI()
	if ui.Theme != "auto" {
		t.Errorf("Expected default theme 'auto', got '%s'", ui.Theme)
	}

	if ui.DefaultMessageView != "html" {
		t.Errorf("Expected default message view 'html', got '%s'", ui.DefaultMessageView)
	}

	if ui.WindowSize.Width != 1200 {
		t.Errorf("Expected default window width 1200, got %d", ui.WindowSize.Width)
	}

	if ui.WindowSize.Height != 800 {
		t.Errorf("Expected default window height 800, got %d", ui.WindowSize.Height)
	}

	// Test default cache values
	cache := pc.GetCache()
	if cache.MaxSizeMB != 500 {
		t.Errorf("Expected default cache size 500, got %d", cache.MaxSizeMB)
	}

	if cache.Compression != true {
		t.Errorf("Expected default compression true, got %v", cache.Compression)
	}

	// Test default logging values
	logging := pc.GetLogging()
	if logging.Level != "info" {
		t.Errorf("Expected default log level 'info', got '%s'", logging.Level)
	}

	if logging.Format != "text" {
		t.Errorf("Expected default log format 'text', got '%s'", logging.Format)
	}

	if logging.MaxSizeMB != 10 {
		t.Errorf("Expected default log max size 10, got %d", logging.MaxSizeMB)
	}
}
