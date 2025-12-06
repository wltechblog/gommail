package config

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/wltechblog/gommail/internal/logging"
)

// Config represents the application configuration
type Config struct {
	Accounts    []Account         `yaml:"accounts"`
	UI          UIConfig          `yaml:"ui"`
	Cache       CacheConfig       `yaml:"cache"`
	Logging     LoggingConfig     `yaml:"logging,omitempty"`
	Tracing     TracingConfig     `yaml:"tracing,omitempty"`
	Addressbook AddressbookConfig `yaml:"addressbook,omitempty"`
}

// Account represents an email account configuration
type Account struct {
	Name          string        `yaml:"name"`
	Email         string        `yaml:"email"`
	DisplayName   string        `yaml:"display_name"`
	IMAP          ServerConfig  `yaml:"imap"`
	SMTP          ServerConfig  `yaml:"smtp"`
	SentFolder    string        `yaml:"sent_folder,omitempty"`
	TrashFolder   string        `yaml:"trash_folder,omitempty"`
	Personalities []Personality `yaml:"personalities,omitempty"`
}

// Personality represents different identities for an account
type Personality struct {
	Name        string `yaml:"name" json:"name"`
	Email       string `yaml:"email" json:"email"`
	DisplayName string `yaml:"display_name" json:"display_name"`
	Signature   string `yaml:"signature,omitempty" json:"signature,omitempty"`
	IsDefault   bool   `yaml:"is_default,omitempty" json:"is_default,omitempty"`
}

// GetDefaultPersonality returns the default personality for the account, or nil if none is set
func (a *Account) GetDefaultPersonality() *Personality {
	for i := range a.Personalities {
		if a.Personalities[i].IsDefault {
			return &a.Personalities[i]
		}
	}
	return nil
}

// SetDefaultPersonality sets the specified personality as default and clears default from others
func (a *Account) SetDefaultPersonality(targetEmail string) bool {
	found := false
	for i := range a.Personalities {
		if strings.EqualFold(a.Personalities[i].Email, targetEmail) {
			a.Personalities[i].IsDefault = true
			found = true
		} else {
			a.Personalities[i].IsDefault = false
		}
	}
	return found
}

// FindPersonalityByEmail finds a personality by email address (case-insensitive)
func (a *Account) FindPersonalityByEmail(email string) *Personality {
	for i := range a.Personalities {
		if strings.EqualFold(a.Personalities[i].Email, email) {
			return &a.Personalities[i]
		}
	}
	return nil
}

// ServerConfig represents IMAP/SMTP server configuration
type ServerConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	TLS      bool   `yaml:"tls"`

	// Validation and security settings
	IgnoreCertificateErrors bool     `yaml:"ignore_certificate_errors,omitempty"`
	AcceptedCertificateHost string   `yaml:"accepted_certificate_host,omitempty"`
	LastValidated           string   `yaml:"last_validated,omitempty"`
	ValidationWarnings      []string `yaml:"validation_warnings,omitempty"`
}

// UIConfig represents UI-specific configuration
type UIConfig struct {
	Theme                    string             `yaml:"theme"`
	DefaultMessageView       string             `yaml:"default_message_view,omitempty"`        // "html" or "text" - default message view preference
	UnifiedInboxMessageLimit int                `yaml:"unified_inbox_message_limit,omitempty"` // Maximum messages to load per account in unified inbox (0 = no limit)
	Notifications            NotificationConfig `yaml:"notifications,omitempty"`               // Desktop notification settings
	WindowSize               struct {
		Width  int `yaml:"width"`
		Height int `yaml:"height"`
	} `yaml:"window_size"`
}

// NotificationConfig represents desktop notification configuration
type NotificationConfig struct {
	Enabled        bool `yaml:"enabled"`                   // Enable/disable desktop notifications
	TimeoutSeconds int  `yaml:"timeout_seconds,omitempty"` // Notification timeout in seconds (0 = system default)
}

// CacheConfig represents caching configuration
type CacheConfig struct {
	Directory   string `yaml:"directory"`
	MaxSizeMB   int    `yaml:"max_size_mb"`
	Compression bool   `yaml:"compression"`
}

// LoggingConfig represents logging configuration
type LoggingConfig struct {
	Level      string `yaml:"level,omitempty"`        // debug, info, warn, error
	Format     string `yaml:"format,omitempty"`       // text, json
	File       string `yaml:"file,omitempty"`         // log file path
	MaxSizeMB  int    `yaml:"max_size_mb,omitempty"`  // max log file size before rotation
	MaxBackups int    `yaml:"max_backups,omitempty"`  // max number of backup files
	MaxAgeDays int    `yaml:"max_age_days,omitempty"` // max age of log files in days
}

// TracingConfig represents IMAP protocol tracing configuration
type TracingConfig struct {
	IMAP IMAPTracingConfig `yaml:"imap,omitempty"` // IMAP protocol tracing settings
}

// AddressbookConfig represents addressbook configuration
type AddressbookConfig struct {
	AutoCollectEnabled bool `yaml:"auto_collect_enabled,omitempty"` // Automatically collect contacts when sending emails
}

// IMAPTracingConfig represents IMAP-specific tracing configuration
type IMAPTracingConfig struct {
	Enabled   bool   `yaml:"enabled,omitempty"`   // Enable IMAP protocol tracing
	Directory string `yaml:"directory,omitempty"` // Directory to store trace files (default: ./traces)
}

// Load loads configuration from the default location
func Load() (*Config, error) {
	logger := logging.NewComponent("config")
	configPath := getConfigPath()

	logger.Debug("Loading configuration from: %s", configPath)

	data, err := os.ReadFile(configPath)
	if err != nil {
		logger.Error("Failed to read configuration file %s: %v", configPath, err)
		return nil, err
	}

	logger.Debug("Configuration file read successfully (%d bytes)", len(data))

	var config Config
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		logger.Error("Failed to parse configuration file %s: %v", configPath, err)
		return nil, err
	}

	logger.Info("Configuration loaded successfully: %d accounts configured", len(config.Accounts))
	// Note: Debug() will check the log level internally - no need to check here
	for i, account := range config.Accounts {
		logger.Debug("Account %d: %s (%s)", i+1, account.Name, account.Email)
	}

	return &config, nil
}

// Save saves the configuration to the default location
func (c *Config) Save() error {
	logger := logging.NewComponent("config")
	configPath := getConfigPath()

	logger.Debug("Saving configuration to: %s", configPath)

	// Ensure config directory exists
	configDir := filepath.Dir(configPath)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		logger.Error("Failed to create config directory %s: %v", configDir, err)
		return err
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		logger.Error("Failed to marshal configuration: %v", err)
		return err
	}

	logger.Debug("Configuration marshaled successfully (%d bytes)", len(data))

	err = os.WriteFile(configPath, data, 0600)
	if err != nil {
		logger.Error("Failed to write configuration file %s: %v", configPath, err)
		return err
	}

	logger.Info("Configuration saved successfully: %d accounts", len(c.Accounts))
	return nil
}

// Default returns a default configuration
func Default() *Config {
	homeDir, _ := os.UserHomeDir()
	cacheDir := filepath.Join(homeDir, ".cache", "mail")

	return &Config{
		Accounts: []Account{},
		UI: UIConfig{
			Theme:                    "auto",
			DefaultMessageView:       "html", // Default to HTML view for rich formatting
			UnifiedInboxMessageLimit: 1000,   // Default to 1000 messages per account for performance
			Notifications: NotificationConfig{
				Enabled:        true, // Enable notifications by default
				TimeoutSeconds: 5,    // 5 second timeout by default
			},
			WindowSize: struct {
				Width  int `yaml:"width"`
				Height int `yaml:"height"`
			}{
				Width:  1200,
				Height: 800,
			},
		},
		Cache: CacheConfig{
			Directory:   cacheDir,
			MaxSizeMB:   500,
			Compression: true,
		},
		Logging: LoggingConfig{
			Level:      "info",
			Format:     "text",
			MaxSizeMB:  10,
			MaxBackups: 5,
			MaxAgeDays: 30,
		},
		Tracing: TracingConfig{
			IMAP: IMAPTracingConfig{
				Enabled:   false, // Disabled by default
				Directory: "",    // Empty means use default (./traces)
			},
		},
		Addressbook: AddressbookConfig{
			AutoCollectEnabled: true, // Enable auto-collection by default
		},
	}
}

// getConfigPath returns the path to the configuration file
func getConfigPath() string {
	// Check for environment override first
	if configPath := os.Getenv("GOMMAIL_CONFIG_PATH"); configPath != "" {
		return configPath
	}

	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".config", "mail", "config.yaml")
}

// ConfigExists checks if a configuration file exists
func ConfigExists() bool {
	logger := logging.NewComponent("config")
	configPath := getConfigPath()

	_, err := os.Stat(configPath)
	exists := err == nil

	logger.Debug("Configuration file exists check: %s -> %v", configPath, exists)
	return exists
}

// IsFirstRun determines if this is the first time the application is being run
func IsFirstRun() bool {
	logger := logging.NewComponent("config")
	isFirst := !ConfigExists()

	if isFirst {
		logger.Info("First run detected - no configuration file found")
	} else {
		logger.Debug("Configuration file exists - not first run")
	}

	return isFirst
}

// GetConfigDir returns the configuration directory path
func GetConfigDir() string {
	return filepath.Dir(getConfigPath())
}

// UpdateServerValidation updates server validation information
func (c *Config) UpdateServerValidation(accountName, serverType string, warnings []string, certificateHost string) {
	for i := range c.Accounts {
		if c.Accounts[i].Name == accountName {
			var serverConfig *ServerConfig
			if serverType == "imap" {
				serverConfig = &c.Accounts[i].IMAP
			} else if serverType == "smtp" {
				serverConfig = &c.Accounts[i].SMTP
			}

			if serverConfig != nil {
				serverConfig.ValidationWarnings = warnings
				serverConfig.AcceptedCertificateHost = certificateHost
				serverConfig.LastValidated = time.Now().Format("2006-01-02 15:04:05")
			}
			break
		}
	}
}

// AcceptCertificateWarnings marks certificate warnings as accepted for a server
func (c *Config) AcceptCertificateWarnings(accountName, serverType string) {
	for i := range c.Accounts {
		if c.Accounts[i].Name == accountName {
			var serverConfig *ServerConfig
			if serverType == "imap" {
				serverConfig = &c.Accounts[i].IMAP
			} else if serverType == "smtp" {
				serverConfig = &c.Accounts[i].SMTP
			}

			if serverConfig != nil {
				serverConfig.IgnoreCertificateErrors = true
			}
			break
		}
	}
}

// GetAccounts returns the accounts (ConfigManager interface compatibility)
func (c *Config) GetAccounts() []Account {
	return c.Accounts
}

// SetAccounts updates the accounts (ConfigManager interface compatibility)
func (c *Config) SetAccounts(accounts []Account) {
	c.Accounts = accounts
}

// GetUI returns UI configuration (ConfigManager interface compatibility)
func (c *Config) GetUI() UIConfig {
	return c.UI
}

// SetUI updates UI configuration (ConfigManager interface compatibility)
func (c *Config) SetUI(ui UIConfig) {
	c.UI = ui
}

// GetCache returns cache configuration (ConfigManager interface compatibility)
func (c *Config) GetCache() CacheConfig {
	return c.Cache
}

// SetCache updates cache configuration (ConfigManager interface compatibility)
func (c *Config) SetCache(cache CacheConfig) {
	c.Cache = cache
}

// GetLogging returns logging configuration (ConfigManager interface compatibility)
func (c *Config) GetLogging() LoggingConfig {
	return c.Logging
}

// SetLogging updates logging configuration (ConfigManager interface compatibility)
func (c *Config) SetLogging(logging LoggingConfig) {
	c.Logging = logging
}

// GetTracing returns tracing configuration (ConfigManager interface compatibility)
func (c *Config) GetTracing() TracingConfig {
	return c.Tracing
}

// SetTracing updates tracing configuration (ConfigManager interface compatibility)
func (c *Config) SetTracing(tracing TracingConfig) {
	c.Tracing = tracing
}

// GetAddressbook returns addressbook configuration (ConfigManager interface compatibility)
func (c *Config) GetAddressbook() AddressbookConfig {
	return c.Addressbook
}

// SetAddressbook updates addressbook configuration (ConfigManager interface compatibility)
func (c *Config) SetAddressbook(addressbook AddressbookConfig) {
	c.Addressbook = addressbook
}
