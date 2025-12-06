package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"github.com/wltechblog/gommail/internal/config"
)

func TestSettingsWindow_DefaultMessageViewIntegration(t *testing.T) {
	// Create test app and config
	app := test.NewApp()
	cfg := config.Default()

	// Test HTML default
	cfg.UI.DefaultMessageView = "html"

	opts := SettingsOptions{
		OnSaved:  func(*config.Config) {},
		OnClosed: func() {},
	}

	// Create settings window
	sw := NewSettingsWindow(app, cfg, opts)

	// Verify the select widget is properly initialized
	if sw.defaultMessageViewSelect == nil {
		t.Fatal("defaultMessageViewSelect should not be nil")
	}

	// Check that the HTML option is selected
	if sw.defaultMessageViewSelect.Selected != "html" {
		t.Errorf("Expected 'html' to be selected, got '%s'", sw.defaultMessageViewSelect.Selected)
	}

	// Test changing to text
	sw.defaultMessageViewSelect.SetSelected("text")
	sw.saveSettings()

	// Verify config was updated
	if cfg.UI.DefaultMessageView != "text" {
		t.Errorf("Expected config.UI.DefaultMessageView to be 'text', got '%s'", cfg.UI.DefaultMessageView)
	}
}

func TestSettingsWindow_DefaultMessageViewLoadSettings(t *testing.T) {
	// Create test app and config
	app := test.NewApp()
	cfg := config.Default()

	// Test with empty default (should default to "html")
	cfg.UI.DefaultMessageView = ""

	opts := SettingsOptions{
		OnSaved:  func(*config.Config) {},
		OnClosed: func() {},
	}

	// Create settings window
	sw := NewSettingsWindow(app, cfg, opts)

	// Verify the select widget defaults to "html" when config is empty
	if sw.defaultMessageViewSelect.Selected != "html" {
		t.Errorf("Expected 'html' to be selected when config is empty, got '%s'", sw.defaultMessageViewSelect.Selected)
	}

	// Test with text preference
	cfg.UI.DefaultMessageView = "text"
	sw.loadSettings()

	// Verify the select widget is updated
	if sw.defaultMessageViewSelect.Selected != "text" {
		t.Errorf("Expected 'text' to be selected after loadSettings, got '%s'", sw.defaultMessageViewSelect.Selected)
	}
}

func TestSettingsWindow_UITabContainsMessageViewSetting(t *testing.T) {
	// Create test app and config
	app := test.NewApp()
	cfg := config.Default()

	opts := SettingsOptions{
		OnSaved:  func(*config.Config) {},
		OnClosed: func() {},
	}

	// Create settings window
	sw := NewSettingsWindow(app, cfg, opts)

	// Create UI tab
	uiTab := sw.createUITab()

	// Verify UI tab was created successfully
	if uiTab == nil {
		t.Fatal("UI tab should not be nil")
	}

	// Verify the default message view select widget exists
	if sw.defaultMessageViewSelect == nil {
		t.Fatal("defaultMessageViewSelect should be created in UI tab")
	}

	// Verify it has the correct options
	options := sw.defaultMessageViewSelect.Options
	if len(options) != 2 {
		t.Errorf("Expected 2 options, got %d", len(options))
	}

	expectedOptions := []string{"html", "text"}
	for i, expected := range expectedOptions {
		if i >= len(options) || options[i] != expected {
			t.Errorf("Expected option %d to be '%s', got '%s'", i, expected, options[i])
		}
	}
}

func TestDefaultMessageViewHandling(t *testing.T) {
	// Test that empty DefaultMessageView defaults to HTML
	cfg := newMockConfigManager([]config.Account{})
	ui := cfg.GetUI()
	ui.DefaultMessageView = "" // Empty - should default to HTML
	cfg.SetUI(ui)

	// Test main window initialization logic
	showHTMLContent := cfg.GetUI().DefaultMessageView != "text"
	if !showHTMLContent {
		t.Error("Expected showHTMLContent to be true when DefaultMessageView is empty")
	}

	// Test with explicit "html" setting
	ui = cfg.GetUI()
	ui.DefaultMessageView = "html"
	cfg.SetUI(ui)
	showHTMLContent = cfg.GetUI().DefaultMessageView != "text"
	if !showHTMLContent {
		t.Error("Expected showHTMLContent to be true when DefaultMessageView is 'html'")
	}

	// Test with explicit "text" setting
	ui = cfg.GetUI()
	ui.DefaultMessageView = "text"
	cfg.SetUI(ui)
	showHTMLContent = cfg.GetUI().DefaultMessageView != "text"
	if showHTMLContent {
		t.Error("Expected showHTMLContent to be false when DefaultMessageView is 'text'")
	}
}
