package ui

import (
	"testing"

	"github.com/wltechblog/gommail/internal/config"
)

func TestNewAccountWizard_FirstRunMode(t *testing.T) {
	// Test the mode constants
	if FirstRunMode != 0 {
		t.Errorf("Expected FirstRunMode to be 0, got %v", FirstRunMode)
	}

	if AddAccountMode != 1 {
		t.Errorf("Expected AddAccountMode to be 1, got %v", AddAccountMode)
	}
}

func TestNewAccountWizard_ConfigHandling(t *testing.T) {
	// Test config creation for first run
	cfg1 := config.Default()
	if len(cfg1.Accounts) != 0 {
		t.Errorf("Expected empty accounts list for default config, got %d accounts", len(cfg1.Accounts))
	}

	// Test config with existing accounts
	cfg2 := config.Default()
	cfg2.Accounts = []config.Account{
		{
			Name:        "Existing Account",
			Email:       "existing@example.com",
			DisplayName: "Existing User",
		},
	}

	if len(cfg2.Accounts) != 1 {
		t.Errorf("Expected 1 account in test config, got %d accounts", len(cfg2.Accounts))
	}

	if cfg2.Accounts[0].Name != "Existing Account" {
		t.Errorf("Expected account name to be 'Existing Account', got %s", cfg2.Accounts[0].Name)
	}
}

func TestWizardMode_Constants(t *testing.T) {
	// Test that the wizard mode constants are properly defined
	var mode WizardMode

	mode = FirstRunMode
	if mode != 0 {
		t.Errorf("Expected FirstRunMode to have value 0, got %v", mode)
	}

	mode = AddAccountMode
	if mode != 1 {
		t.Errorf("Expected AddAccountMode to have value 1, got %v", mode)
	}
}

func TestConfigDefaults(t *testing.T) {
	// Test that default config is properly initialized
	cfg := config.Default()

	if cfg == nil {
		t.Fatal("Expected default config to be created, got nil")
	}

	if len(cfg.Accounts) != 0 {
		t.Errorf("Expected empty accounts list in default config, got %d accounts", len(cfg.Accounts))
	}

	if cfg.UI.Theme != "auto" {
		t.Errorf("Expected default theme to be 'auto', got '%s'", cfg.UI.Theme)
	}

	if cfg.UI.DefaultMessageView != "html" {
		t.Errorf("Expected default message view to be 'html', got '%s'", cfg.UI.DefaultMessageView)
	}
}
