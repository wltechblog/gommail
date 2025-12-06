package config

import (
	"testing"

	"fyne.io/fyne/v2/test"
)

func TestAddressbookConfigIntegration(t *testing.T) {
	// Create test app
	app := test.NewApp()
	defer app.Quit()

	// Create preferences config
	pc := NewPreferencesConfig(app, "test-addressbook")

	// Test default addressbook configuration
	defaultConfig := pc.GetAddressbook()
	if !defaultConfig.AutoCollectEnabled {
		t.Error("Expected auto-collect to be enabled by default")
	}

	// Test setting addressbook configuration
	newConfig := AddressbookConfig{
		AutoCollectEnabled: false,
	}
	pc.SetAddressbook(newConfig)

	// Verify the setting was applied
	retrievedConfig := pc.GetAddressbook()
	if retrievedConfig.AutoCollectEnabled {
		t.Error("Expected auto-collect to be disabled after setting")
	}

	// Test persistence through save/load cycle
	err := pc.Save()
	if err != nil {
		t.Fatalf("Failed to save configuration: %v", err)
	}

	// Create new instance and load
	pc2 := NewPreferencesConfig(app, "test-addressbook")
	err = pc2.Load()
	if err != nil {
		t.Fatalf("Failed to load configuration: %v", err)
	}

	// Verify persistence
	persistedConfig := pc2.GetAddressbook()
	if persistedConfig.AutoCollectEnabled {
		t.Error("Expected auto-collect to remain disabled after save/load cycle")
	}
}

func TestAddressbookConfigInToConfigFromConfig(t *testing.T) {
	// Create test app
	app := test.NewApp()
	defer app.Quit()

	// Create preferences config
	pc := NewPreferencesConfig(app, "test-conversion")

	// Set addressbook configuration
	addressbookConfig := AddressbookConfig{
		AutoCollectEnabled: false,
	}
	pc.SetAddressbook(addressbookConfig)

	// Convert to standard Config
	standardConfig := pc.ToConfig()
	if standardConfig.Addressbook.AutoCollectEnabled {
		t.Error("Expected auto-collect to be disabled in converted config")
	}

	// Create new preferences config and load from standard config
	pc2 := NewPreferencesConfig(app, "test-conversion-2")
	pc2.FromConfig(standardConfig)

	// Verify the configuration was transferred
	retrievedConfig := pc2.GetAddressbook()
	if retrievedConfig.AutoCollectEnabled {
		t.Error("Expected auto-collect to be disabled after FromConfig")
	}
}

func TestAddressbookConfigManagerInterface(t *testing.T) {
	// Create test app
	app := test.NewApp()
	defer app.Quit()

	// Create preferences config as ConfigManager interface
	var configMgr ConfigManager = NewPreferencesConfig(app, "test-interface")

	// Test that addressbook methods are available through interface
	defaultConfig := configMgr.GetAddressbook()
	if !defaultConfig.AutoCollectEnabled {
		t.Error("Expected auto-collect to be enabled by default through interface")
	}

	// Test setting through interface
	newConfig := AddressbookConfig{
		AutoCollectEnabled: false,
	}
	configMgr.SetAddressbook(newConfig)

	// Verify through interface
	retrievedConfig := configMgr.GetAddressbook()
	if retrievedConfig.AutoCollectEnabled {
		t.Error("Expected auto-collect to be disabled after setting through interface")
	}
}
