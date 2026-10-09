package ui

import "github.com/wltechblog/gommail/internal/config"

// mockConfigManager implements config.ConfigManager for testing
type mockConfigManager struct {
	accounts    []config.Account
	ui          config.UIConfig
	cache       config.CacheConfig
	logging     config.LoggingConfig
	tracing     config.TracingConfig
	addressbook config.AddressbookConfig
}

func (m *mockConfigManager) Load() error                             { return nil }
func (m *mockConfigManager) Save() error                             { return nil }
func (m *mockConfigManager) GetAccounts() []config.Account           { return m.accounts }
func (m *mockConfigManager) SetAccounts(accounts []config.Account)   { m.accounts = accounts }
func (m *mockConfigManager) GetUI() config.UIConfig                  { return m.ui }
func (m *mockConfigManager) SetUI(ui config.UIConfig)                { m.ui = ui }
func (m *mockConfigManager) GetCache() config.CacheConfig            { return m.cache }
func (m *mockConfigManager) SetCache(cache config.CacheConfig)       { m.cache = cache }
func (m *mockConfigManager) GetLogging() config.LoggingConfig        { return m.logging }
func (m *mockConfigManager) SetLogging(logging config.LoggingConfig) { m.logging = logging }
func (m *mockConfigManager) GetTracing() config.TracingConfig        { return m.tracing }
func (m *mockConfigManager) SetTracing(tracing config.TracingConfig) { m.tracing = tracing }
func (m *mockConfigManager) GetAddressbook() config.AddressbookConfig {
	return m.addressbook
}
func (m *mockConfigManager) SetAddressbook(addressbook config.AddressbookConfig) {
	m.addressbook = addressbook
}
func (m *mockConfigManager) UpdateServerValidation(accountName, serverType string, warnings []string, certificateHost string) {
}
func (m *mockConfigManager) AcceptCertificateWarnings(accountName, serverType string) {}

func newMockConfigManager(accounts []config.Account) config.ConfigManager {
	return &mockConfigManager{
		accounts: accounts,
		ui: config.UIConfig{
			WindowSize: struct {
				Width  int `yaml:"width"`
				Height int `yaml:"height"`
			}{Width: 1200, Height: 800},
			DefaultMessageView: "html",
		},
		cache: config.CacheConfig{
			Directory:   "/tmp/test-cache",
			Compression: true,
			MaxSizeMB:   100,
		},
		logging: config.LoggingConfig{
			Level:  "info",
			Format: "text",
		},
		tracing: config.TracingConfig{
			IMAP: config.IMAPTracingConfig{
				Enabled:   false,
				Directory: "",
			},
		},
		addressbook: config.AddressbookConfig{
			AutoCollectEnabled: true,
		},
	}
}
