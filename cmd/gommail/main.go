package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"fyne.io/fyne/v2/app"

	"github.com/wltechblog/gommail/internal/config"
	"github.com/wltechblog/gommail/internal/logging"
	"github.com/wltechblog/gommail/internal/resources"
	"github.com/wltechblog/gommail/internal/ui"
)

// setupSignalHandlers sets up signal handlers for debugging and graceful shutdown
func setupSignalHandlers() {
	// Create a channel to receive OS signals
	sigChan := make(chan os.Signal, 1)

	// Register the channel to receive specific signals
	signal.Notify(sigChan, syscall.SIGQUIT)

	// Start a goroutine to handle signals
	go func() {
		for sig := range sigChan {
			switch sig {
			case syscall.SIGQUIT:
				// SIGQUIT (Ctrl+\) - dump stack trace
				logging.Info("Received SIGQUIT - dumping stack trace")

				// Get stack trace for all goroutines
				buf := make([]byte, 1024*1024) // 1MB buffer
				stackSize := runtime.Stack(buf, true)
				stackTrace := string(buf[:stackSize])

				// Log to both stderr and log file
				fmt.Fprintf(os.Stderr, "\n=== STACK TRACE (SIGQUIT) ===\n%s\n=== END STACK TRACE ===\n", stackTrace)
				logging.Info("Stack trace dump:\n%s", stackTrace)
			}
		}
	}()
}

// setupLogging configures the logging system based on command line flags and environment variables
func setupLogging(debugFlag, verboseFlag bool, logLevel, logFile string) {
	// Determine log level from various sources (priority: flag > env var > default)
	var level logging.LogLevel = logging.LevelInfo

	// Check command line flags first
	if debugFlag || verboseFlag {
		level = logging.LevelDebug
	} else if logLevel != "" {
		if parsedLevel, err := logging.ParseLevel(logLevel); err == nil {
			level = parsedLevel
		} else {
			fmt.Fprintf(os.Stderr, "Warning: Invalid log level '%s', using INFO\n", logLevel)
		}
	} else if os.Getenv("GOMMAIL_DEBUG") == "true" {
		// Fall back to environment variable
		level = logging.LevelDebug
	}

	// Set the log level
	logging.SetLevel(level)

	// Log the level being used (always output to stderr regardless of level)
	fmt.Fprintf(os.Stderr, "[gommail] Log level set to: %s\n", level.String())

	// Set up file logging if requested
	if logFile != "" {
		// Convert relative paths to absolute paths based on current working directory
		if !filepath.IsAbs(logFile) {
			cwd, err := os.Getwd()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Failed to get current directory: %v\n", err)
			} else {
				logFile = filepath.Join(cwd, logFile)
			}
		}

		if err := logging.SetupFileLoggingWithPath(logFile); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to setup file logging: %v\n", err)
		} else {
			logging.Info("File logging enabled: %s", logFile)
		}
	}

	// Log the startup information
	// Note: Debug() will check the log level internally - no need to check here
	logging.Debug("Debug logging enabled")
	logging.Debug("Command line args: %v", os.Args)
	logging.Debug("Environment GOMMAIL_DEBUG: %s", os.Getenv("GOMMAIL_DEBUG"))
	logging.Debug("Environment GOMMAIL_CONFIG_PATH: %s", os.Getenv("GOMMAIL_CONFIG_PATH"))
	logging.Debug("Environment GOMMAIL_CACHE_DIR: %s", os.Getenv("GOMMAIL_CACHE_DIR"))

	logging.Info("gommail client starting up...")
}

func main() {
	// Parse command line flags
	var (
		debugFlag     = flag.Bool("debug", false, "Enable debug logging")
		verboseFlag   = flag.Bool("verbose", false, "Enable verbose logging (alias for debug)")
		logLevel      = flag.String("log-level", "", "Set log level (debug, info, warn, error)")
		logFile       = flag.String("log-file", "", "Write logs to file (in addition to stderr)")
		profileFlag   = flag.String("profile", "default", "Profile name for independent configurations and data")
		versionFlag   = flag.Bool("version", false, "Show version information")
		helpFlag      = flag.Bool("help", false, "Show help information")
		imapTraceFlag = flag.Bool("imap-trace", false, "Enable IMAP protocol tracing (logs to files per account)")
	)
	flag.Parse()

	// Show version information
	if *versionFlag {
		fmt.Println("gommail client v0.3.0")
		fmt.Println("A simple email client written in Go")
		os.Exit(0)
	}

	// Show help information
	if *helpFlag {
		fmt.Println("gommail client - A simple email client")
		fmt.Println("\nUsage:")
		fmt.Printf("  %s [options]\n\n", os.Args[0])
		fmt.Println("Options:")
		flag.PrintDefaults()
		fmt.Println("\nLogging:")
		fmt.Println("  Default log level: INFO")
		fmt.Println("  Log levels: debug, info, warn, error")
		fmt.Println("  Priority: --debug/--verbose > --log-level > GOMMAIL_DEBUG > config file > default (info)")
		fmt.Println("\nEnvironment Variables:")
		fmt.Println("  GOMMAIL_DEBUG=true          Enable debug logging")
		fmt.Println("  GOMMAIL_CONFIG_PATH=path    Override config file path")
		fmt.Println("  GOMMAIL_CACHE_DIR=path      Override cache directory")
		fmt.Println("\nProfile Support:")
		fmt.Println("  Use --profile to run multiple independent instances:")
		fmt.Println("  ./gommail --profile work    # Work profile")
		fmt.Println("  ./gommail --profile personal # Personal profile")
		fmt.Println("  ./gommail                   # Default profile")
		fmt.Println("\nIMAP Tracing:")
		fmt.Println("  Use --imap-trace to enable protocol-level debugging:")
		fmt.Println("  ./gommail --imap-trace      # Logs IMAP traffic to files per account")
		fmt.Println("  Files are saved as: imap-trace-<account-name>-<date>.log")
		os.Exit(0)
	}

	// Initialize logging
	setupLogging(*debugFlag, *verboseFlag, *logLevel, *logFile)

	// Log IMAP tracing status
	if *imapTraceFlag {
		logging.Info("IMAP protocol tracing enabled")
	}

	// Setup signal handlers for debugging (SIGQUIT for stack traces)
	setupSignalHandlers()
	logging.Debug("Signal handlers initialized (SIGQUIT for stack traces)")

	// Create the Fyne application with profile-specific ID for preferences
	appID := fmt.Sprintf("com.wltechblog.gommail.%s", *profileFlag)
	myApp := app.NewWithID(appID)

	// Set application icon from embedded data
	iconResource := resources.GetAppIcon()
	myApp.SetIcon(iconResource)
	logging.Debug("Application icon set successfully from embedded data")

	// Log the profile being used
	logging.Info("Using profile: %s (app ID: %s)", *profileFlag, appID)

	// Check if this is the first run (using app-aware version)
	if config.IsFirstRunWithApp(myApp, *profileFlag) {
		logging.Info("First run detected for profile '%s' - starting setup wizard", *profileFlag)

		// Show the first-run wizard with callback to transition to main window
		wizard := ui.NewFirstRunWizard(myApp)
		wizard.SetOnComplete(func(cfg *config.Config) {
			if cfg != nil {
				// Create preferences config and save the configuration from the wizard
				prefsConfig := config.NewPreferencesConfig(myApp, *profileFlag)
				prefsConfig.FromConfig(cfg)
				if err := prefsConfig.Save(); err != nil {
					logging.Error("Error saving configuration: %v", err)
					myApp.Quit()
					return
				}

				logging.Info("Configuration saved successfully to preferences")

				// Create and show the main window
				mainWindow := ui.NewMainWindow(myApp, prefsConfig)
				mainWindow.Show()
			} else {
				// User cancelled the wizard - exit the application
				logging.Info("Setup wizard cancelled - exiting")
				myApp.Quit()
			}
		})

		// Show the wizard and run the app (single event loop)
		wizard.Show()
		myApp.Run()
		return
	}

	// Load configuration using the new factory
	configManager, err := config.LoadConfig(myApp, *profileFlag)
	if err != nil {
		logging.Error("Could not load config: %v", err)
		logging.Info("Please run the application again to restart the setup wizard")
		return
	}

	// Load the configuration data
	if err := configManager.Load(); err != nil {
		logging.Error("Could not load configuration data: %v", err)
		logging.Info("Please run the application again to restart the setup wizard")
		return
	}

	logging.Debug("Configuration loaded successfully")

	// Apply logging configuration from config file (unless overridden by command line)
	if !*debugFlag && !*verboseFlag && *logLevel == "" && os.Getenv("GOMMAIL_DEBUG") != "true" {
		loggingConfig := configManager.GetLogging()
		if parsedLevel, err := logging.ParseLevel(loggingConfig.Level); err == nil {
			logging.SetLevel(parsedLevel)
			logging.Debug("Applied logging level from configuration: %s", loggingConfig.Level)
		} else {
			logging.Warn("Invalid logging level in configuration '%s', keeping current level", loggingConfig.Level)
		}
	}

	// Apply command line tracing flag to configuration
	if *imapTraceFlag {
		tracing := configManager.GetTracing()
		tracing.IMAP.Enabled = true
		configManager.SetTracing(tracing)
		logging.Debug("IMAP tracing enabled via command line flag")
	}

	// Get accounts for validation
	accounts := configManager.GetAccounts()

	// Validate that we have at least one account configured
	if len(accounts) == 0 {
		logging.Info("No accounts configured - starting setup wizard")

		// Convert to Config struct for wizard compatibility
		cfg := &config.Config{
			Accounts: accounts,
			UI:       configManager.GetUI(),
			Cache:    configManager.GetCache(),
			Logging:  configManager.GetLogging(),
			Tracing:  configManager.GetTracing(),
		}

		// Show the first-run wizard to add an account
		wizard := ui.NewFirstRunWizard(myApp)
		wizard.SetExistingConfig(cfg)

		// Set up completion callback to transition to main window
		wizard.SetOnComplete(func(newCfg *config.Config) {
			if newCfg != nil {
				// Update the configuration manager with new data
				configManager.SetAccounts(newCfg.Accounts)
				configManager.SetUI(newCfg.UI)
				configManager.SetCache(newCfg.Cache)
				configManager.SetLogging(newCfg.Logging)
				configManager.SetTracing(newCfg.Tracing)

				if err := configManager.Save(); err != nil {
					logging.Error("Error saving configuration: %v", err)
				} else {
					logging.Info("Configuration saved successfully")
				}

				// Create and show the main window
				mainWindow := ui.NewMainWindow(myApp, configManager)
				mainWindow.Show()
			} else {
				// User cancelled the wizard - exit the application
				logging.Info("Setup wizard cancelled by user")
				myApp.Quit()
			}
		})

		// Show the wizard and run the app (single event loop)
		wizard.Show()
		myApp.Run()
	} else {
		logging.Info("Starting main application with %d configured account(s)", len(accounts))
		logging.Debug("Account names: %v", getAccountNamesFromList(accounts))

		// Create the main window directly and run the app (single event loop)
		mainWindow := ui.NewMainWindow(myApp, configManager)
		mainWindow.Show()
		myApp.Run()
	}

	logging.Info("gommail client shutting down")
}

// getAccountNamesFromList returns a slice of account names from a list of accounts
func getAccountNamesFromList(accounts []config.Account) []string {
	names := make([]string, len(accounts))
	for i, account := range accounts {
		names[i] = account.Name
	}
	return names
}
