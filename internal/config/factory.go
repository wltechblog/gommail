package config

import (
	"fmt"

	"fyne.io/fyne/v2"

	"github.com/wltechblog/gommail/internal/logging"
)

// ConfigManager defines the interface that both Config and PreferencesConfig implement
type ConfigManager interface {
	Load() error
	Save() error
	GetAccounts() []Account
	SetAccounts(accounts []Account)
	GetUI() UIConfig
	SetUI(ui UIConfig)
	GetCache() CacheConfig
	SetCache(cache CacheConfig)
	GetLogging() LoggingConfig
	SetLogging(logging LoggingConfig)
	GetTracing() TracingConfig
	SetTracing(tracing TracingConfig)
	GetAddressbook() AddressbookConfig
	SetAddressbook(addressbook AddressbookConfig)
	UpdateServerValidation(accountName, serverType string, warnings []string, certificateHost string)
	AcceptCertificateWarnings(accountName, serverType string)
}

// ConfigFactory creates the appropriate configuration manager based on current state
type ConfigFactory struct {
	app     fyne.App
	logger  *logging.Logger
	profile string
}

// NewConfigFactory creates a new configuration factory
func NewConfigFactory(app fyne.App, profile string) *ConfigFactory {
	return &ConfigFactory{
		app:     app,
		logger:  logging.NewComponent("config"),
		profile: profile,
	}
}

// CreateConfigManager creates the appropriate configuration manager
// Priority: Preferences (if initialized) > YAML (if exists) > New Preferences
func (cf *ConfigFactory) CreateConfigManager() (ConfigManager, error) {
	cf.logger.Debug("Determining configuration manager type")

	// Check if Fyne preferences are already initialized
	prefsConfig := NewPreferencesConfig(cf.app, cf.profile)
	if prefsConfig.IsInitialized() {
		cf.logger.Info("Using existing Fyne preferences configuration")
		return prefsConfig, nil
	}

	// Check if YAML configuration exists
	if ConfigExists() {
		cf.logger.Info("YAML configuration found - performing migration to Fyne preferences")
		return cf.migrateFromYAML(prefsConfig)
	}

	// No existing configuration - use new preferences
	cf.logger.Info("No existing configuration found - using new Fyne preferences")
	return prefsConfig, nil
}

// migrateFromYAML migrates configuration from YAML to Fyne preferences
func (cf *ConfigFactory) migrateFromYAML(prefsConfig *PreferencesConfig) (ConfigManager, error) {
	cf.logger.Debug("Starting migration from YAML to Fyne preferences")

	// Load existing YAML configuration
	yamlConfig, err := Load()
	if err != nil {
		cf.logger.Error("Failed to load YAML configuration for migration: %v", err)
		return nil, fmt.Errorf("failed to load YAML configuration: %w", err)
	}

	cf.logger.Info("Loaded YAML configuration with %d accounts", len(yamlConfig.Accounts))

	// Convert to preferences format
	prefsConfig.FromConfig(yamlConfig)

	// Save to preferences
	if err := prefsConfig.Save(); err != nil {
		cf.logger.Error("Failed to save migrated configuration to preferences: %v", err)
		return nil, fmt.Errorf("failed to save migrated configuration: %w", err)
	}

	cf.logger.Info("Successfully migrated configuration from YAML to Fyne preferences")
	cf.logger.Debug("Migration completed - %d accounts migrated", len(yamlConfig.Accounts))

	return prefsConfig, nil
}

// LoadConfig is a convenience function that creates a factory and loads configuration
func LoadConfig(app fyne.App, profile string) (ConfigManager, error) {
	factory := NewConfigFactory(app, profile)
	return factory.CreateConfigManager()
}

// IsFirstRunWithApp determines if this is the first time the application is being run
// This version can check both YAML and Preferences for a specific profile
func IsFirstRunWithApp(app fyne.App, profile string) bool {
	logger := logging.NewComponent("config")

	// Check if YAML config exists
	yamlExists := ConfigExists()

	// Check if preferences are initialized for this profile
	prefsConfig := NewPreferencesConfig(app, profile)
	prefsExists := prefsConfig.IsInitialized()

	isFirst := !yamlExists && !prefsExists

	if isFirst {
		logger.Info("First run detected for profile '%s' - no configuration found", profile)
	} else if yamlExists {
		logger.Debug("YAML configuration exists - not first run")
	} else if prefsExists {
		logger.Debug("Preferences configuration exists for profile '%s' - not first run", profile)
	}

	return isFirst
}

// Default returns a default configuration compatible with both systems
func DefaultConfig() *Config {
	return Default()
}
