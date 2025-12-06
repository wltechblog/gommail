package ui

import (
	"fmt"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/wltechblog/gommail/internal/config"
	"github.com/wltechblog/gommail/internal/email"
	"github.com/wltechblog/gommail/pkg/validation"
)

// WizardMode represents the mode in which the wizard is operating
type WizardMode int

const (
	// FirstRunMode is used when setting up the first account (no existing config)
	FirstRunMode WizardMode = iota
	// AddAccountMode is used when adding an additional account to existing config
	AddAccountMode
)

// NewAccountWizard handles both first-run setup and adding additional accounts
type NewAccountWizard struct {
	app       fyne.App
	window    fyne.Window
	config    *config.Config
	validator *validation.ServerValidator
	mode      WizardMode

	// Current step
	currentStep int
	maxSteps    int

	// Completion callback
	onComplete func(*config.Config)

	// UI components
	content        *fyne.Container
	stepContainers []fyne.CanvasObject // Pre-created step containers to preserve form state
	stepLabel      *widget.Label
	progressBar    *widget.ProgressBar
	backButton     *widget.Button
	nextButton     *widget.Button

	// Account form fields
	accountNameEntry *widget.Entry
	emailEntry       *widget.Entry
	displayNameEntry *widget.Entry
	passwordEntry    *widget.Entry

	// IMAP settings
	imapHostEntry        *widget.Entry
	imapPortEntry        *widget.Entry
	imapEncryptionSelect *widget.Select
	imapUsernameEntry    *widget.Entry
	imapPasswordEntry    *widget.Entry

	// SMTP settings
	smtpHostEntry        *widget.Entry
	smtpPortEntry        *widget.Entry
	smtpEncryptionSelect *widget.Select
	smtpUsernameEntry    *widget.Entry
	smtpPasswordEntry    *widget.Entry

	// Validation results
	validationResults *widget.RichText
	validateButton    *widget.Button

	// Gmail application password alert
	gmailAlert *widget.Card

	// Result
	resultConfig *config.Config
}

// NewFirstRunWizard creates a new first-run wizard (backward compatibility)
func NewFirstRunWizard(app fyne.App) *NewAccountWizard {
	return NewNewAccountWizard(app, nil, FirstRunMode)
}

// NewNewAccountWizard creates a new account wizard
func NewNewAccountWizard(app fyne.App, existingConfig *config.Config, mode WizardMode) *NewAccountWizard {
	var cfg *config.Config
	if mode == FirstRunMode || existingConfig == nil {
		cfg = config.Default()
	} else {
		cfg = existingConfig
	}

	wizard := &NewAccountWizard{
		app:       app,
		config:    cfg,
		validator: validation.NewServerValidator(),
		mode:      mode,
		maxSteps:  4, // Welcome, Account Info, Server Settings, Validation
	}

	wizard.createWindow()
	wizard.createUI()
	wizard.showStep(0)

	return wizard
}

// SetExistingConfig sets an existing configuration to modify
func (w *NewAccountWizard) SetExistingConfig(cfg *config.Config) {
	w.config = cfg
	w.mode = AddAccountMode
}

// ShowAndRun displays the wizard and returns the configured settings
// DEPRECATED: Use Show() and SetOnComplete() callback instead to avoid GLFW initialization issues
func (w *NewAccountWizard) ShowAndRun() *config.Config {
	w.window.ShowAndRun()
	return w.resultConfig
}

// Show displays the wizard window without running the event loop
func (w *NewAccountWizard) Show() {
	w.window.Show()
}

// SetOnComplete sets a callback function to be called when the wizard completes
func (w *NewAccountWizard) SetOnComplete(callback func(*config.Config)) {
	w.onComplete = callback
}

// Close closes the wizard window
func (w *NewAccountWizard) Close() {
	if w.window != nil {
		w.window.Close()
	}
}

// createWindow creates the wizard window
func (w *NewAccountWizard) createWindow() {
	var title string
	if w.mode == FirstRunMode {
		title = "gommail client setup"
	} else {
		title = "Add New Account"
	}

	w.window = w.app.NewWindow(title)
	w.window.Resize(fyne.NewSize(850, 650)) // Optimized size to prevent horizontal scrollbars
	w.window.SetFixedSize(false)            // Allow resizing if needed
	w.window.CenterOnScreen()

	// Handle window close
	w.window.SetCloseIntercept(func() {
		var message string
		if w.mode == FirstRunMode {
			message = "Are you sure you want to exit the setup wizard?\nYour configuration will not be saved."
		} else {
			message = "Are you sure you want to cancel adding this account?\nYour changes will not be saved."
		}

		dialog.ShowConfirm("Exit Setup", message,
			func(confirmed bool) {
				if confirmed {
					w.resultConfig = nil

					// Call completion callback with nil to indicate cancellation
					if w.onComplete != nil {
						w.onComplete(nil)
					}

					w.window.Close()
				}
			}, w.window)
	})
}

// createUI creates the wizard UI components
func (w *NewAccountWizard) createUI() {
	// Header with progress
	w.stepLabel = widget.NewLabel("Step 1 of 4: Welcome")
	w.progressBar = widget.NewProgressBar()
	w.progressBar.SetValue(0.25)

	// Navigation buttons
	w.backButton = widget.NewButton("Back", w.goBack)
	w.nextButton = widget.NewButton("Next", w.goNext)
	w.backButton.Disable()

	// Create all form widgets first
	w.createFormWidgets()

	// Pre-create all step containers to preserve form state
	w.stepContainers = make([]fyne.CanvasObject, w.maxSteps)
	w.stepContainers[0] = w.createWelcomeStep()
	w.stepContainers[1] = w.createAccountInfoStep()
	w.stepContainers[2] = w.createServerSettingsStep()
	w.stepContainers[3] = w.createValidationStep()

	// Main content area
	w.content = container.NewVBox()

	w.window.SetContent(w.content)
}

// createFormWidgets creates all form widgets once to preserve state
func (w *NewAccountWizard) createFormWidgets() {
	// Account form fields
	w.accountNameEntry = widget.NewEntry()
	w.accountNameEntry.SetPlaceHolder("e.g., Work Email, Personal")

	w.emailEntry = widget.NewEntry()
	w.emailEntry.SetPlaceHolder("your.email@example.com")
	w.emailEntry.OnChanged = func(email string) {
		// Auto-fill display name and account name if empty
		if w.displayNameEntry.Text == "" && strings.Contains(email, "@") {
			parts := strings.Split(email, "@")
			w.displayNameEntry.SetText(parts[0])
		}
		if w.accountNameEntry.Text == "" && strings.Contains(email, "@") {
			domain := strings.Split(email, "@")[1]
			// Use a simple title case instead of deprecated strings.Title
			domainParts := strings.Split(domain, ".")
			if len(domainParts) > 0 {
				name := domainParts[0]
				if len(name) > 0 {
					w.accountNameEntry.SetText(strings.ToUpper(name[:1]) + strings.ToLower(name[1:]))
				}
			}

			// Update Gmail alert when email changes
			w.updateGmailAlert()
		}
	}

	w.displayNameEntry = widget.NewEntry()
	w.displayNameEntry.SetPlaceHolder("Your Name")

	w.passwordEntry = widget.NewPasswordEntry()
	w.passwordEntry.SetPlaceHolder("Your email password")

	// IMAP settings
	w.imapHostEntry = widget.NewEntry()
	w.imapHostEntry.SetPlaceHolder("imap.example.com")
	w.imapHostEntry.OnChanged = func(string) {
		w.updateGmailAlert()
	}

	w.imapPortEntry = widget.NewEntry()
	w.imapPortEntry.SetText("993")

	// IMAP encryption options
	w.imapEncryptionSelect = widget.NewSelect(
		[]string{"Direct TLS/SSL (Recommended)", "STARTTLS", "No Encryption (Insecure)"},
		func(selected string) {
			// Update port based on encryption selection
			switch selected {
			case "Direct TLS/SSL (Recommended)":
				w.imapPortEntry.SetText("993")
			case "STARTTLS":
				w.imapPortEntry.SetText("143")
			case "No Encryption (Insecure)":
				w.imapPortEntry.SetText("143")
			}
		})
	w.imapEncryptionSelect.SetSelected("Direct TLS/SSL (Recommended)")

	w.imapUsernameEntry = widget.NewEntry()
	w.imapUsernameEntry.SetPlaceHolder("Usually same as email")

	w.imapPasswordEntry = widget.NewPasswordEntry()
	w.imapPasswordEntry.SetPlaceHolder("Usually same as email password")

	// SMTP settings
	w.smtpHostEntry = widget.NewEntry()
	w.smtpHostEntry.SetPlaceHolder("smtp.example.com")
	w.smtpHostEntry.OnChanged = func(string) {
		w.updateGmailAlert()
	}

	w.smtpPortEntry = widget.NewEntry()
	w.smtpPortEntry.SetText("587")

	// SMTP encryption options
	w.smtpEncryptionSelect = widget.NewSelect(
		[]string{"STARTTLS (Recommended)", "Direct TLS/SSL", "No Encryption (Insecure)"},
		func(selected string) {
			// Update port based on encryption selection
			switch selected {
			case "STARTTLS (Recommended)":
				w.smtpPortEntry.SetText("587")
			case "Direct TLS/SSL":
				w.smtpPortEntry.SetText("465")
			case "No Encryption (Insecure)":
				w.smtpPortEntry.SetText("25")
			}
		})
	w.smtpEncryptionSelect.SetSelected("STARTTLS (Recommended)")

	w.smtpUsernameEntry = widget.NewEntry()
	w.smtpUsernameEntry.SetPlaceHolder("Usually same as email")

	w.smtpPasswordEntry = widget.NewPasswordEntry()
	w.smtpPasswordEntry.SetPlaceHolder("Usually same as email password")

	// Buttons
	w.validateButton = widget.NewButton("🔍 Test Connection", w.validateSettings)

	// Validation results
	w.validationResults = widget.NewRichText()
	w.validationResults.ParseMarkdown("Click 'Test Connection' to validate your server settings.")

	// Gmail application password alert
	w.createGmailAlert()
}

// showStep displays the specified step
func (w *NewAccountWizard) showStep(step int) {
	w.currentStep = step

	// Create context-aware step labels
	var stepNames []string
	if w.mode == FirstRunMode {
		stepNames = []string{"Welcome", "Account Information", "Server Settings", "Validation"}
	} else {
		stepNames = []string{"Welcome", "Account Information", "Server Settings", "Validation"}
	}

	if step >= 0 && step < len(stepNames) {
		w.stepLabel.SetText(fmt.Sprintf("Step %d of %d: %s", step+1, w.maxSteps, stepNames[step]))
	} else {
		w.stepLabel.SetText(fmt.Sprintf("Step %d of %d", step+1, w.maxSteps))
	}

	w.progressBar.SetValue(float64(step+1) / float64(w.maxSteps))

	// Update navigation buttons
	w.backButton.Enable()
	if step == 0 {
		w.backButton.Disable()
	}

	var finishText string
	if w.mode == FirstRunMode {
		finishText = "Complete Setup"
	} else {
		finishText = "Add Account"
	}

	if step == w.maxSteps-1 {
		w.nextButton.SetText(finishText)
	} else {
		w.nextButton.SetText("Next")
	}

	// Trigger auto-population when navigating to server settings step
	if step == 2 { // Server settings step
		w.autoPopulateServerSettings()
		w.updateGmailAlert() // Update Gmail alert visibility
	}

	// Use pre-created step container to preserve form state
	var stepContent fyne.CanvasObject
	if step >= 0 && step < len(w.stepContainers) {
		stepContent = w.stepContainers[step]
	} else {
		stepContent = widget.NewLabel("Unknown step")
	}

	// Update the center content
	w.content.RemoveAll()

	// Try a different approach: Use a split container
	// Create header
	header := container.NewVBox(
		w.stepLabel,
		w.progressBar,
		widget.NewSeparator(),
	)

	// Create footer
	footer := container.NewVBox(
		widget.NewSeparator(),
		container.NewBorder(nil, nil, w.backButton, w.nextButton),
	)

	// Create a layout that actually works - use absolute positioning
	// Create a container that fills the entire window
	fullContainer := container.NewWithoutLayout()

	// Add all elements to the container
	fullContainer.Add(header)
	fullContainer.Add(stepContent)
	fullContainer.Add(footer)

	w.content.Add(fullContainer)

	// Position elements manually - this should finally work
	windowSize := w.window.Canvas().Size()
	headerHeight := float32(80)
	footerHeight := float32(60)
	contentHeight := windowSize.Height - headerHeight - footerHeight

	// Position header at top
	header.Move(fyne.NewPos(0, 0))
	header.Resize(fyne.NewSize(windowSize.Width, headerHeight))

	// Position content in middle - this gets the most space
	stepContent.Move(fyne.NewPos(0, headerHeight))
	stepContent.Resize(fyne.NewSize(windowSize.Width, contentHeight))

	// Position footer at bottom
	footer.Move(fyne.NewPos(0, windowSize.Height-footerHeight))
	footer.Resize(fyne.NewSize(windowSize.Width, footerHeight))
}

// createWelcomeStep creates the welcome step
func (w *NewAccountWizard) createWelcomeStep() fyne.CanvasObject {
	var welcomeText string
	if w.mode == FirstRunMode {
		welcomeText = `# Welcome to gommail client

This wizard will help you set up your first email account.

## What you'll need:

- Your email address and password
- Your email provider's server settings (we'll try to detect these automatically)

## Supported providers:

- **Gmail** - Automatic configuration
- **Outlook/Hotmail** - Automatic configuration
- **Yahoo** - Automatic configuration
- **iCloud** - Automatic configuration
- **Custom servers** - Manual configuration with validation

Click **Next** to begin setting up your email account.`
	} else {
		welcomeText = `# Add New Account

This wizard will help you add a new email account to your existing configuration.

## What you'll need:

- Your email address and password
- Your email provider's server settings (we'll try to detect these automatically)

## Supported providers:

- **Gmail** - Automatic configuration
- **Outlook/Hotmail** - Automatic configuration
- **Yahoo** - Automatic configuration
- **iCloud** - Automatic configuration
- **Custom servers** - Manual configuration with validation

Click **Next** to begin adding your new account.`
	}

	title := widget.NewRichTextFromMarkdown(welcomeText)
	title.Wrapping = fyne.TextWrapWord

	// Simple padded container with scroll - sizing handled by parent
	return container.NewScroll(container.NewPadded(title))
}

// createAccountInfoStep creates the account information step
func (w *NewAccountWizard) createAccountInfoStep() fyne.CanvasObject {
	// Use pre-created widgets - they already have placeholders and event handlers set
	form := &widget.Form{
		Items: []*widget.FormItem{
			{Text: "Account Name:", Widget: w.accountNameEntry},
			{Text: "Email Address:", Widget: w.emailEntry},
			{Text: "Display Name:", Widget: w.displayNameEntry},
			{Text: "Password:", Widget: w.passwordEntry},
		},
	}

	info := widget.NewRichTextFromMarkdown(`## Account Information

Please enter your email account details. The account name is just for your reference - you can use any name you like.

**Note:** For Gmail, Yahoo, and other providers that use app passwords, please use your app-specific password instead of your regular account password.`)
	info.Wrapping = fyne.TextWrapWord

	return container.NewScroll(container.NewVBox(
		container.NewPadded(info),
		container.NewPadded(form),
	))
}

// createServerSettingsStep creates the server settings step
func (w *NewAccountWizard) createServerSettingsStep() fyne.CanvasObject {
	// Auto-populate credentials and detect server settings when step is created
	w.autoPopulateServerSettings()

	imapForm := &widget.Form{
		Items: []*widget.FormItem{
			{Text: "IMAP Host:", Widget: w.imapHostEntry},
			{Text: "IMAP Port:", Widget: w.imapPortEntry},
			{Text: "IMAP Encryption:", Widget: w.imapEncryptionSelect},
			{Text: "IMAP Username:", Widget: w.imapUsernameEntry},
			{Text: "IMAP Password:", Widget: w.imapPasswordEntry},
		},
	}

	smtpForm := &widget.Form{
		Items: []*widget.FormItem{
			{Text: "SMTP Host:", Widget: w.smtpHostEntry},
			{Text: "SMTP Port:", Widget: w.smtpPortEntry},
			{Text: "SMTP Encryption:", Widget: w.smtpEncryptionSelect},
			{Text: "SMTP Username:", Widget: w.smtpUsernameEntry},
			{Text: "SMTP Password:", Widget: w.smtpPasswordEntry},
		},
	}

	// Create server settings description with proper text wrapping
	serverDescription := widget.NewRichTextFromMarkdown("## Server Settings\n\nServer settings have been automatically detected based on your email address. Username and password fields are pre-filled with your account credentials.\n\n**Supported providers:** Gmail, Outlook, Yahoo, iCloud, Google Workspace, Microsoft 365, and other hosted email services via MX lookup.")
	serverDescription.Wrapping = fyne.TextWrapWord

	// Create additional info with proper text wrapping
	additionalInfo := widget.NewRichTextFromMarkdown("*You can modify any settings below if needed. TLS encryption is enabled by default for security.*")
	additionalInfo.Wrapping = fyne.TextWrapWord

	return container.NewScroll(container.NewVBox(
		container.NewPadded(serverDescription),
		container.NewPadded(additionalInfo),
		container.NewPadded(w.gmailAlert), // Gmail application password alert
		widget.NewSeparator(),
		container.NewPadded(widget.NewCard("IMAP Settings (Incoming Mail)", "", imapForm)),
		container.NewPadded(widget.NewCard("SMTP Settings (Outgoing Mail)", "", smtpForm)),
	))
}

// autoPopulateServerSettings automatically populates server settings when navigating to the step
func (w *NewAccountWizard) autoPopulateServerSettings() {
	email := w.emailEntry.Text
	password := w.passwordEntry.Text

	// Always populate username and password fields with account credentials
	if email != "" {
		w.imapUsernameEntry.SetText(email)
		w.smtpUsernameEntry.SetText(email)
	}
	if password != "" {
		w.imapPasswordEntry.SetText(password)
		w.smtpPasswordEntry.SetText(password)
	}

	// Auto-detect server settings if email is provided and server fields are empty
	if email != "" && (w.imapHostEntry.Text == "" || w.smtpHostEntry.Text == "") {
		w.performAutoDetection(email)
	}
}

// performAutoDetection performs the actual server detection
func (w *NewAccountWizard) performAutoDetection(email string) {
	// Perform detection in background to avoid blocking UI
	go func() {
		imapConfig, smtpConfig, _ := w.validator.DetectServerSettings(email)

		// Update UI on main thread using fyne.Do to avoid threading errors
		if imapConfig != nil && smtpConfig != nil {
			fyne.Do(func() {
				// Only update if fields are currently empty to avoid overwriting user changes
				if w.imapHostEntry.Text == "" {
					w.imapHostEntry.SetText(imapConfig.Host)
				}
				if w.imapPortEntry.Text == "993" || w.imapPortEntry.Text == "" { // Default or empty
					w.imapPortEntry.SetText(strconv.Itoa(imapConfig.Port))
				}
				// Set IMAP encryption based on TLS setting
				if imapConfig.TLS {
					w.imapEncryptionSelect.SetSelected("Direct TLS/SSL (Recommended)")
				} else {
					w.imapEncryptionSelect.SetSelected("STARTTLS")
				}

				if w.smtpHostEntry.Text == "" {
					w.smtpHostEntry.SetText(smtpConfig.Host)
				}
				if w.smtpPortEntry.Text == "587" || w.smtpPortEntry.Text == "" { // Default or empty
					w.smtpPortEntry.SetText(strconv.Itoa(smtpConfig.Port))
				}
				// Set SMTP encryption based on TLS setting
				if smtpConfig.TLS {
					w.smtpEncryptionSelect.SetSelected("Direct TLS/SSL")
				} else {
					w.smtpEncryptionSelect.SetSelected("STARTTLS (Recommended)")
				}
			})
		}

		// Update Gmail alert after server detection
		fyne.Do(func() {
			w.updateGmailAlert()
		})
	}()
}

// createValidationStep creates the validation step
func (w *NewAccountWizard) createValidationStep() fyne.CanvasObject {
	// Use pre-created widgets - set initial content if needed
	// We'll just ensure the validation results widget is ready to use

	// Create instructions
	instructions := widget.NewRichTextFromMarkdown("## Validation\n\nLet's test your server settings to make sure everything works correctly.")

	// Create a compact header section (instructions + button)
	headerSection := container.NewVBox(
		container.NewPadded(instructions),
		container.NewPadded(w.validateButton),
		widget.NewSeparator(),
	)

	// Create results area that will take up most of the available space
	resultsContent := container.NewPadded(w.validationResults)
	resultsScroll := container.NewScroll(resultsContent)

	// Use Border layout to give most space to results
	return container.NewBorder(
		headerSection, // Top: compact header (fixed size)
		nil,           // Bottom: none
		nil, nil,      // Left/Right: none
		resultsScroll, // Center: results area (expands to fill remaining space)
	)
}

// validateSettings validates the current server settings
func (w *NewAccountWizard) validateSettings() {
	// Build configurations from form data
	imapConfig, smtpConfig, err := w.buildServerConfigs()
	if err != nil {
		w.validationResults.ParseMarkdown(fmt.Sprintf("**Configuration Error:** %v", err))
		return
	}

	w.validateButton.SetText("Testing...")
	w.validateButton.Disable()
	w.validationResults.ParseMarkdown("Testing server connections...")

	go func() {
		defer func() {
			// Update UI elements on the main thread
			fyne.Do(func() {
				w.validateButton.SetText("Test Connection")
				w.validateButton.Enable()
			})
		}()

		var results strings.Builder
		results.WriteString("# Validation Results\n\n")

		// Test IMAP
		results.WriteString("## IMAP Server (Incoming Mail)\n")
		imapResult, err := w.validator.ValidateIMAPServer(*imapConfig)
		if err != nil {
			results.WriteString(fmt.Sprintf("**Error:** %v\n\n", err))
		} else {
			w.formatValidationResult(&results, imapResult, "IMAP")
			// Handle certificate errors (not just warnings) - must be called on UI thread
			if !imapResult.CertificateValid && len(imapResult.CertificateIssues) > 0 && imapResult.CanConnect {
				// Capture the result for use in the UI thread
				imapResultCopy := imapResult
				fyne.Do(func() {
					w.handleCertificateWarnings("IMAP", imapResultCopy)
				})
			}
		}

		// Test SMTP
		results.WriteString("## SMTP Server (Outgoing Mail)\n")
		smtpResult, err := w.validator.ValidateSMTPServer(*smtpConfig)
		if err != nil {
			results.WriteString(fmt.Sprintf("**Error:** %v\n\n", err))
		} else {
			w.formatValidationResult(&results, smtpResult, "SMTP")
			// Handle certificate errors (not just warnings) - must be called on UI thread
			if !smtpResult.CertificateValid && len(smtpResult.CertificateIssues) > 0 && smtpResult.CanConnect {
				// Capture the result for use in the UI thread
				smtpResultCopy := smtpResult
				fyne.Do(func() {
					w.handleCertificateWarnings("SMTP", smtpResultCopy)
				})
			}
		}

		// Update UI on main thread using fyne.Do to avoid threading errors
		fyne.Do(func() {
			w.validationResults.ParseMarkdown(results.String())
		})
	}()
}

// formatValidationResult formats a validation result for display
func (w *NewAccountWizard) formatValidationResult(results *strings.Builder, result *validation.ServerValidationResult, serverType string) {
	results.WriteString(fmt.Sprintf("### %s Server Results\n\n", serverType))

	if result.CanConnect {
		results.WriteString("✅ **Connection successful**\n\n")
	} else {
		results.WriteString("❌ **Connection failed**\n")
		if result.ConnectError != "" {
			results.WriteString(fmt.Sprintf("**Error:** %s\n\n", result.ConnectError))
		}
		return // Don't show other details if connection failed
	}

	// Security information
	results.WriteString("**Security Features:**\n")
	if result.SupportsTLS {
		results.WriteString("- 🔒 TLS/SSL encryption supported\n")
	}
	if result.SupportsSTARTTLS {
		results.WriteString("- 🔐 STARTTLS encryption supported\n")
	}
	results.WriteString("\n")

	// Certificate validation
	if len(result.CertificateIssues) > 0 {
		results.WriteString("**Certificate Status:**\n")
		results.WriteString("⚠️ Issues found:\n")
		for _, issue := range result.CertificateIssues {
			results.WriteString(fmt.Sprintf("- %s\n", issue))
		}

		// Show hostname suggestion if available
		if result.SuggestedHost != "" {
			results.WriteString(fmt.Sprintf("\n💡 **Suggestion:** Try using hostname '%s' instead\n", result.SuggestedHost))
		}
		results.WriteString("\n")
	} else if result.CertificateValid {
		results.WriteString("**Certificate Status:**\n")
		results.WriteString("✅ Certificate is valid and trusted\n\n")
	}

	// Authentication methods
	if len(result.AuthMethods) > 0 {
		results.WriteString("**Supported Authentication:**\n")
		for _, method := range result.AuthMethods {
			results.WriteString(fmt.Sprintf("- %s\n", method))
		}
		results.WriteString("\n")
	}

	results.WriteString("---\n\n")
}

// buildServerConfigs builds server configurations from form data
func (w *NewAccountWizard) buildServerConfigs() (*email.ServerConfig, *email.ServerConfig, error) {
	// Parse IMAP port
	imapPort, err := strconv.Atoi(w.imapPortEntry.Text)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid IMAP port: %s", w.imapPortEntry.Text)
	}

	// Parse SMTP port
	smtpPort, err := strconv.Atoi(w.smtpPortEntry.Text)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid SMTP port: %s", w.smtpPortEntry.Text)
	}

	// Determine IMAP TLS setting from encryption selection
	// Note: For "No Encryption", we set TLS=false and the validation logic
	// will try plain connections when TLS/STARTTLS fail
	imapTLS := false
	switch w.imapEncryptionSelect.Selected {
	case "Direct TLS/SSL (Recommended)":
		imapTLS = true
	case "STARTTLS":
		imapTLS = false
	case "No Encryption (Insecure)":
		imapTLS = false
	}

	imapConfig := &email.ServerConfig{
		Host:     w.imapHostEntry.Text,
		Port:     imapPort,
		Username: w.imapUsernameEntry.Text,
		Password: w.imapPasswordEntry.Text,
		TLS:      imapTLS,
	}

	// Determine SMTP TLS setting from encryption selection
	// Note: For "No Encryption", we set TLS=false and the validation logic
	// will try plain connections when TLS/STARTTLS fail
	smtpTLS := false
	switch w.smtpEncryptionSelect.Selected {
	case "Direct TLS/SSL":
		smtpTLS = true
	case "STARTTLS (Recommended)":
		smtpTLS = false
	case "No Encryption (Insecure)":
		smtpTLS = false
	}

	smtpConfig := &email.ServerConfig{
		Host:     w.smtpHostEntry.Text,
		Port:     smtpPort,
		Username: w.smtpUsernameEntry.Text,
		Password: w.smtpPasswordEntry.Text,
		TLS:      smtpTLS,
	}

	return imapConfig, smtpConfig, nil
}

// goBack navigates to the previous step
func (w *NewAccountWizard) goBack() {
	if w.currentStep > 0 {
		w.showStep(w.currentStep - 1)
	}
}

// goNext navigates to the next step or finishes the wizard
func (w *NewAccountWizard) goNext() {
	if w.currentStep == w.maxSteps-1 {
		// Finish the wizard
		w.finishWizard()
	} else {
		// Validate current step before proceeding
		if w.validateCurrentStep() {
			w.showStep(w.currentStep + 1)
		}
	}
}

// validateCurrentStep validates the current step's input
func (w *NewAccountWizard) validateCurrentStep() bool {
	switch w.currentStep {
	case 1: // Account info step
		if w.emailEntry.Text == "" {
			dialog.ShowError(fmt.Errorf("please enter your email address"), w.window)
			return false
		}
		if w.passwordEntry.Text == "" {
			dialog.ShowError(fmt.Errorf("please enter your password"), w.window)
			return false
		}
		if w.accountNameEntry.Text == "" {
			dialog.ShowError(fmt.Errorf("please enter an account name"), w.window)
			return false
		}
		return true

	case 2: // Server settings step
		if w.imapHostEntry.Text == "" || w.smtpHostEntry.Text == "" {
			dialog.ShowError(fmt.Errorf("please enter server hostnames"), w.window)
			return false
		}
		if w.imapPortEntry.Text == "" || w.smtpPortEntry.Text == "" {
			dialog.ShowError(fmt.Errorf("please enter server ports"), w.window)
			return false
		}
		return true

	default:
		return true
	}
}

// finishWizard completes the wizard and creates the configuration
func (w *NewAccountWizard) finishWizard() {
	// Build the final configuration
	imapConfig, smtpConfig, err := w.buildServerConfigs()
	if err != nil {
		dialog.ShowError(fmt.Errorf("configuration error: %v", err), w.window)
		return
	}

	// Create the account
	account := config.Account{
		Name:        w.accountNameEntry.Text,
		Email:       w.emailEntry.Text,
		DisplayName: w.displayNameEntry.Text,
		IMAP: config.ServerConfig{
			Host:     imapConfig.Host,
			Port:     imapConfig.Port,
			Username: imapConfig.Username,
			Password: imapConfig.Password,
			TLS:      imapConfig.TLS,
		},
		SMTP: config.ServerConfig{
			Host:     smtpConfig.Host,
			Port:     smtpConfig.Port,
			Username: smtpConfig.Username,
			Password: smtpConfig.Password,
			TLS:      smtpConfig.TLS,
		},
	}

	// Add account to configuration
	w.config.Accounts = append(w.config.Accounts, account)

	// Set result
	w.resultConfig = w.config

	// Call completion callback if set
	if w.onComplete != nil {
		w.onComplete(w.config)
	}

	// Close the wizard window
	w.window.Close()
}

// handleCertificateWarnings shows a dialog for certificate issues and gets user consent
func (w *NewAccountWizard) handleCertificateWarnings(serverType string, result *validation.ServerValidationResult) {
	if len(result.CertificateIssues) == 0 {
		return
	}

	var message strings.Builder
	message.WriteString(fmt.Sprintf("The %s server has certificate issues:\n\n", serverType))

	for _, issue := range result.CertificateIssues {
		message.WriteString(fmt.Sprintf("• %s\n", issue))
	}

	message.WriteString("\nThis could indicate a security risk, but the connection is still functional.")

	if result.SuggestedHost != "" {
		message.WriteString(fmt.Sprintf("\n\nSuggestion: Try using hostname '%s' instead.", result.SuggestedHost))

		// Show dialog with hostname suggestion
		dialog.ShowConfirm("Certificate Warning",
			message.String()+"\n\nWould you like to update the hostname?",
			func(useNewHost bool) {
				if useNewHost {
					w.updateHostname(serverType, result.SuggestedHost)
				} else {
					w.showCertificateAcceptDialog(serverType, result)
				}
			}, w.window)
	} else {
		w.showCertificateAcceptDialog(serverType, result)
	}
}

// showCertificateAcceptDialog shows a dialog asking if the user wants to accept certificate issues
func (w *NewAccountWizard) showCertificateAcceptDialog(serverType string, result *validation.ServerValidationResult) {
	var message strings.Builder
	message.WriteString(fmt.Sprintf("The %s server has certificate issues:\n\n", serverType))

	for _, issue := range result.CertificateIssues {
		message.WriteString(fmt.Sprintf("• %s\n", issue))
	}

	message.WriteString("\nDo you want to continue anyway? This will ignore certificate errors for this server.")

	dialog.ShowConfirm("Accept Certificate Issues?", message.String(),
		func(accept bool) {
			if accept {
				// Mark to ignore certificate errors in the final configuration
				// This will be handled when creating the account
			}
		}, w.window)
}

// updateHostname updates the hostname for the specified server type
func (w *NewAccountWizard) updateHostname(serverType, newHostname string) {
	switch strings.ToLower(serverType) {
	case "imap":
		w.imapHostEntry.SetText(newHostname)
	case "smtp":
		w.smtpHostEntry.SetText(newHostname)
	}

	dialog.ShowInformation("Hostname Updated",
		fmt.Sprintf("Updated %s hostname to %s. Please test the connection again.", serverType, newHostname),
		w.window)
}

// createGmailAlert creates the Gmail application password alert widget
func (w *NewAccountWizard) createGmailAlert() {
	alertText := `**⚠️ Gmail Application Password Required**

Gmail requires an **Application Password** instead of your regular Gmail password for email clients.

**Steps to create an Application Password:**
1. Go to your Google Account settings
2. Enable 2-Step Verification (if not already enabled)
3. Generate an Application Password for "Mail"
4. Use that password in this setup (not your regular Gmail password)

[📖 **Click here for detailed instructions**](https://support.google.com/mail/answer/185833)`

	alertContent := widget.NewRichTextFromMarkdown(alertText)
	alertContent.Wrapping = fyne.TextWrapWord

	w.gmailAlert = widget.NewCard("", "", alertContent)
	w.gmailAlert.Hide() // Initially hidden
}

// isGmailAccount checks if the current email configuration is for Gmail
func (w *NewAccountWizard) isGmailAccount() bool {
	email := strings.ToLower(w.emailEntry.Text)

	// Check direct Gmail domains
	if strings.HasSuffix(email, "@gmail.com") || strings.HasSuffix(email, "@googlemail.com") {
		return true
	}

	// Check if server settings indicate Gmail
	imapHost := strings.ToLower(w.imapHostEntry.Text)
	smtpHost := strings.ToLower(w.smtpHostEntry.Text)

	if strings.Contains(imapHost, "gmail.com") || strings.Contains(smtpHost, "gmail.com") {
		return true
	}

	// Check if auto-detection would result in Gmail (for Google Workspace domains)
	if email != "" && strings.Contains(email, "@") {
		_, _, provider := w.validator.DetectServerSettings(email)
		if provider == "Gmail" || provider == "MX-Detected" {
			// For MX-Detected, check if it resolves to Google
			imapConfig, smtpConfig, _ := w.validator.DetectServerSettings(email)
			if imapConfig != nil && smtpConfig != nil {
				if strings.Contains(strings.ToLower(imapConfig.Host), "gmail.com") ||
					strings.Contains(strings.ToLower(smtpConfig.Host), "gmail.com") {
					return true
				}
			}
		}
	}

	return false
}

// updateGmailAlert shows or hides the Gmail alert based on current settings
func (w *NewAccountWizard) updateGmailAlert() {
	if w.gmailAlert == nil {
		return
	}

	if w.isGmailAccount() {
		w.gmailAlert.Show()
	} else {
		w.gmailAlert.Hide()
	}
}
