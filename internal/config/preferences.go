package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"fyne.io/fyne/v2"

	"github.com/wltechblog/gommail/internal/logging"
)

// PreferencesConfig implements the same interface as Config but uses Fyne's Preferences API
type PreferencesConfig struct {
	app     fyne.App
	prefs   fyne.Preferences
	logger  *logging.Logger
	profile string

	// Cached data to avoid repeated JSON deserialization
	accounts       []Account
	accountsCached bool
}

// NewPreferencesConfig creates a new preferences-based configuration manager
func NewPreferencesConfig(app fyne.App, profile string) *PreferencesConfig {
	return &PreferencesConfig{
		app:     app,
		prefs:   app.Preferences(),
		logger:  logging.NewComponent("config"),
		profile: profile,
	}
}

// GetProfile returns the profile name for this configuration instance
func (pc *PreferencesConfig) GetProfile() string {
	return pc.profile
}

// GetPreferences returns the underlying Fyne preferences for direct access
// This is used by components that need to store data not covered by the standard config
func (pc *PreferencesConfig) GetPreferences() fyne.Preferences {
	return pc.prefs
}

// Load loads configuration from Fyne preferences
func (pc *PreferencesConfig) Load() error {
	pc.logger.Debug("Loading configuration from Fyne preferences")

	// Check if preferences have been initialized
	if !pc.prefs.BoolWithFallback("config.initialized", false) {
		pc.logger.Info("Preferences not initialized - using defaults")
		return nil
	}

	// Load accounts from JSON
	if err := pc.loadAccounts(); err != nil {
		pc.logger.Error("Failed to load accounts from preferences: %v", err)
		return err
	}

	pc.logger.Info("Configuration loaded successfully from preferences: %d accounts configured", len(pc.accounts))
	// Note: Debug() will check the log level internally - no need to check here
	for i, account := range pc.accounts {
		pc.logger.Debug("Account %d: %s (%s)", i+1, account.Name, account.Email)
	}

	return nil
}

// Save saves configuration to Fyne preferences
func (pc *PreferencesConfig) Save() error {
	pc.logger.Debug("Saving configuration to Fyne preferences")

	// Save accounts as JSON
	if err := pc.saveAccounts(); err != nil {
		pc.logger.Error("Failed to save accounts to preferences: %v", err)
		return err
	}

	// Save UI configuration
	pc.saveUIConfig()

	// Save cache configuration
	pc.saveCacheConfig()

	// Save logging configuration
	pc.saveLoggingConfig()

	// Save tracing configuration
	pc.saveTracingConfig()

	// Save addressbook configuration
	pc.saveAddressbookConfig()

	// Mark preferences as initialized and set version
	pc.prefs.SetBool("config.initialized", true)
	pc.prefs.SetString("config.version", "1.0")
	pc.prefs.SetBool("config.migrated_from_yaml", true)

	pc.logger.Info("Configuration saved successfully to preferences: %d accounts", len(pc.accounts))
	return nil
}

// GetAccounts returns the accounts from cache or loads them from preferences
func (pc *PreferencesConfig) GetAccounts() []Account {
	if !pc.accountsCached {
		if err := pc.loadAccounts(); err != nil {
			pc.logger.Error("Failed to load accounts: %v", err)
			return []Account{}
		}
	}
	return pc.accounts
}

// SetAccounts updates the accounts and marks cache as dirty
func (pc *PreferencesConfig) SetAccounts(accounts []Account) {
	pc.accounts = accounts
	pc.accountsCached = true
}

// GetUI returns UI configuration from preferences
func (pc *PreferencesConfig) GetUI() UIConfig {
	return UIConfig{
		Theme:                    pc.prefs.StringWithFallback("ui.theme", "auto"),
		DefaultMessageView:       pc.prefs.StringWithFallback("ui.default_message_view", "html"),
		UnifiedInboxMessageLimit: pc.prefs.IntWithFallback("ui.unified_inbox_message_limit", 1000),
		Notifications: NotificationConfig{
			Enabled:        pc.prefs.BoolWithFallback("ui.notifications.enabled", true),
			TimeoutSeconds: pc.prefs.IntWithFallback("ui.notifications.timeout_seconds", 5),
		},
		WindowSize: struct {
			Width  int `yaml:"width"`
			Height int `yaml:"height"`
		}{
			Width:  pc.prefs.IntWithFallback("ui.window_size.width", 1200),
			Height: pc.prefs.IntWithFallback("ui.window_size.height", 800),
		},
	}
}

// SetUI saves UI configuration to preferences
func (pc *PreferencesConfig) SetUI(ui UIConfig) {
	pc.prefs.SetString("ui.theme", ui.Theme)
	pc.prefs.SetString("ui.default_message_view", ui.DefaultMessageView)
	pc.prefs.SetInt("ui.unified_inbox_message_limit", ui.UnifiedInboxMessageLimit)
	pc.prefs.SetBool("ui.notifications.enabled", ui.Notifications.Enabled)
	pc.prefs.SetInt("ui.notifications.timeout_seconds", ui.Notifications.TimeoutSeconds)
	pc.prefs.SetInt("ui.window_size.width", ui.WindowSize.Width)
	pc.prefs.SetInt("ui.window_size.height", ui.WindowSize.Height)
}

// GetCache returns cache configuration from preferences
func (pc *PreferencesConfig) GetCache() CacheConfig {
	homeDir, _ := os.UserHomeDir()
	// Include profile name in cache directory for isolation
	var defaultCacheDir string
	if pc.profile == "default" {
		defaultCacheDir = filepath.Join(homeDir, ".cache", "mail")
	} else {
		defaultCacheDir = filepath.Join(homeDir, ".cache", "mail", pc.profile)
	}

	return CacheConfig{
		Directory:   pc.prefs.StringWithFallback("cache.directory", defaultCacheDir),
		MaxSizeMB:   pc.prefs.IntWithFallback("cache.max_size_mb", 500),
		Compression: pc.prefs.BoolWithFallback("cache.compression", true),
	}
}

// SetCache saves cache configuration to preferences
func (pc *PreferencesConfig) SetCache(cache CacheConfig) {
	pc.prefs.SetString("cache.directory", cache.Directory)
	pc.prefs.SetInt("cache.max_size_mb", cache.MaxSizeMB)
	pc.prefs.SetBool("cache.compression", cache.Compression)
}

// GetLogging returns logging configuration from preferences
func (pc *PreferencesConfig) GetLogging() LoggingConfig {
	return LoggingConfig{
		Level:      pc.prefs.StringWithFallback("logging.level", "info"),
		Format:     pc.prefs.StringWithFallback("logging.format", "text"),
		File:       pc.prefs.StringWithFallback("logging.file", ""),
		MaxSizeMB:  pc.prefs.IntWithFallback("logging.max_size_mb", 10),
		MaxBackups: pc.prefs.IntWithFallback("logging.max_backups", 5),
		MaxAgeDays: pc.prefs.IntWithFallback("logging.max_age_days", 30),
	}
}

// SetLogging saves logging configuration to preferences
func (pc *PreferencesConfig) SetLogging(logging LoggingConfig) {
	pc.prefs.SetString("logging.level", logging.Level)
	pc.prefs.SetString("logging.format", logging.Format)
	pc.prefs.SetString("logging.file", logging.File)
	pc.prefs.SetInt("logging.max_size_mb", logging.MaxSizeMB)
	pc.prefs.SetInt("logging.max_backups", logging.MaxBackups)
	pc.prefs.SetInt("logging.max_age_days", logging.MaxAgeDays)
}

// GetTracing returns tracing configuration from preferences
func (pc *PreferencesConfig) GetTracing() TracingConfig {
	return TracingConfig{
		IMAP: IMAPTracingConfig{
			Enabled:   pc.prefs.BoolWithFallback("tracing.imap.enabled", false),
			Directory: pc.prefs.StringWithFallback("tracing.imap.directory", ""),
		},
	}
}

// SetTracing saves tracing configuration to preferences
func (pc *PreferencesConfig) SetTracing(tracing TracingConfig) {
	pc.prefs.SetBool("tracing.imap.enabled", tracing.IMAP.Enabled)
	pc.prefs.SetString("tracing.imap.directory", tracing.IMAP.Directory)
}

// GetAddressbook returns addressbook configuration from preferences
func (pc *PreferencesConfig) GetAddressbook() AddressbookConfig {
	return AddressbookConfig{
		AutoCollectEnabled: pc.prefs.BoolWithFallback("addressbook.auto_collect_enabled", true),
	}
}

// SetAddressbook saves addressbook configuration to preferences
func (pc *PreferencesConfig) SetAddressbook(addressbook AddressbookConfig) {
	pc.prefs.SetBool("addressbook.auto_collect_enabled", addressbook.AutoCollectEnabled)
}

// loadAccounts loads accounts from JSON stored in preferences
func (pc *PreferencesConfig) loadAccounts() error {
	accountsJSON := pc.prefs.StringWithFallback("accounts.data", "[]")

	if accountsJSON == "[]" {
		pc.accounts = []Account{}
		pc.accountsCached = true
		return nil
	}

	var accounts []Account
	if err := json.Unmarshal([]byte(accountsJSON), &accounts); err != nil {
		return fmt.Errorf("failed to unmarshal accounts JSON: %w", err)
	}

	pc.accounts = accounts
	pc.accountsCached = true
	return nil
}

// saveAccounts saves accounts as JSON to preferences
func (pc *PreferencesConfig) saveAccounts() error {
	accountsJSON, err := json.Marshal(pc.accounts)
	if err != nil {
		return fmt.Errorf("failed to marshal accounts to JSON: %w", err)
	}

	pc.prefs.SetString("accounts.data", string(accountsJSON))
	pc.prefs.SetInt("accounts.count", len(pc.accounts))
	return nil
}

// saveUIConfig saves UI configuration to preferences
func (pc *PreferencesConfig) saveUIConfig() {
	ui := pc.GetUI()
	pc.SetUI(ui)
}

// saveCacheConfig saves cache configuration to preferences
func (pc *PreferencesConfig) saveCacheConfig() {
	cache := pc.GetCache()
	pc.SetCache(cache)
}

// saveLoggingConfig saves logging configuration to preferences
func (pc *PreferencesConfig) saveLoggingConfig() {
	logging := pc.GetLogging()
	pc.SetLogging(logging)
}

// saveTracingConfig saves tracing configuration to preferences
func (pc *PreferencesConfig) saveTracingConfig() {
	tracing := pc.GetTracing()
	pc.SetTracing(tracing)
}

// saveAddressbookConfig saves addressbook configuration to preferences
func (pc *PreferencesConfig) saveAddressbookConfig() {
	addressbook := pc.GetAddressbook()
	pc.SetAddressbook(addressbook)
}

// UpdateServerValidation updates server validation information
func (pc *PreferencesConfig) UpdateServerValidation(accountName, serverType string, warnings []string, certificateHost string) {
	accounts := pc.GetAccounts()

	for i := range accounts {
		if accounts[i].Name == accountName {
			var serverConfig *ServerConfig
			if serverType == "imap" {
				serverConfig = &accounts[i].IMAP
			} else if serverType == "smtp" {
				serverConfig = &accounts[i].SMTP
			}

			if serverConfig != nil {
				serverConfig.ValidationWarnings = warnings
				serverConfig.AcceptedCertificateHost = certificateHost
				serverConfig.LastValidated = time.Now().Format("2006-01-02 15:04:05")
			}
			break
		}
	}

	pc.SetAccounts(accounts)
}

// AcceptCertificateWarnings marks certificate warnings as accepted for a server
func (pc *PreferencesConfig) AcceptCertificateWarnings(accountName, serverType string) {
	accounts := pc.GetAccounts()

	for i := range accounts {
		if accounts[i].Name == accountName {
			var serverConfig *ServerConfig
			if serverType == "imap" {
				serverConfig = &accounts[i].IMAP
			} else if serverType == "smtp" {
				serverConfig = &accounts[i].SMTP
			}

			if serverConfig != nil {
				serverConfig.IgnoreCertificateErrors = true
			}
			break
		}
	}

	pc.SetAccounts(accounts)
}

// ToConfig converts PreferencesConfig to the standard Config struct for compatibility
func (pc *PreferencesConfig) ToConfig() *Config {
	return &Config{
		Accounts:    pc.GetAccounts(),
		UI:          pc.GetUI(),
		Cache:       pc.GetCache(),
		Logging:     pc.GetLogging(),
		Tracing:     pc.GetTracing(),
		Addressbook: pc.GetAddressbook(),
	}
}

// FromConfig updates PreferencesConfig from a standard Config struct
func (pc *PreferencesConfig) FromConfig(cfg *Config) {
	pc.SetAccounts(cfg.Accounts)
	pc.SetUI(cfg.UI)
	pc.SetCache(cfg.Cache)
	pc.SetLogging(cfg.Logging)
	pc.SetTracing(cfg.Tracing)
	pc.SetAddressbook(cfg.Addressbook)
}

// IsInitialized checks if preferences have been initialized
func (pc *PreferencesConfig) IsInitialized() bool {
	return pc.prefs.BoolWithFallback("config.initialized", false)
}

// GetVersion returns the configuration version
func (pc *PreferencesConfig) GetVersion() string {
	return pc.prefs.StringWithFallback("config.version", "")
}

// IsMigratedFromYAML checks if this configuration was migrated from YAML
func (pc *PreferencesConfig) IsMigratedFromYAML() bool {
	return pc.prefs.BoolWithFallback("config.migrated_from_yaml", false)
}
