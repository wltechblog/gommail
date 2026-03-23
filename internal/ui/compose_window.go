package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/PuerkitoBio/goquery"
	"github.com/wltechblog/gommail/internal/addressbook"
	"github.com/wltechblog/gommail/internal/config"
	"github.com/wltechblog/gommail/internal/email"
	"github.com/wltechblog/gommail/pkg/smtp"
)

// ComposeWindow represents the message composition window
type ComposeWindow struct {
	app    fyne.App
	window fyne.Window
	config *config.Config

	// Email components
	smtpClient     *smtp.Client
	account        *config.Account
	addressbookMgr *addressbook.Manager

	// UI components
	fromSelect     *widget.Select
	toEntry        *AutocompleteEntry
	ccEntry        *AutocompleteEntry
	bccEntry       *AutocompleteEntry
	subjectEntry   *widget.Entry
	bodyEntry      *widget.Entry
	attachmentList *widget.List
	statusBar      *widget.Label

	// Data
	attachments []email.Attachment
	isDraft     bool
	draftPath   string

	// Tooltip layer enabled
	tooltipLayerEnabled bool

	// Callbacks
	onSent   func()
	onClosed func()
}

// ComposeOptions contains options for creating a compose window
type ComposeOptions struct {
	Account        *config.Account
	SMTPClient     *smtp.Client
	AddressbookMgr *addressbook.Manager
	ReplyTo        *email.Message
	ReplyAll       bool // True for reply all, false for regular reply
	Forward        *email.Message
	Recipients     []email.Address
	Subject        string
	Body           string
	SelectedFrom   string // Pre-select a specific from address (email) in the From dropdown
	OnSent         func()
	OnClosed       func()
}

// NewComposeWindow creates a new message composition window
func NewComposeWindow(app fyne.App, cfg *config.Config, opts ComposeOptions) *ComposeWindow {
	window := app.NewWindow("Compose Message")
	window.Resize(fyne.NewSize(800, 600))

	cw := &ComposeWindow{
		app:                 app,
		window:              window,
		config:              cfg,
		smtpClient:          opts.SMTPClient,
		account:             opts.Account,
		addressbookMgr:      opts.AddressbookMgr,
		attachments:         make([]email.Attachment, 0),
		tooltipLayerEnabled: true,
		onSent:              opts.OnSent,
		onClosed:            opts.OnClosed,
	}

	cw.setupUI()
	cw.setupContent(opts)

	// Handle window close
	window.SetCloseIntercept(func() {
		cw.handleClose()
	})

	return cw
}

// setupUI initializes the user interface
func (cw *ComposeWindow) setupUI() {
	// From selector (personality selection)
	fromOptions := make([]string, 0)
	if cw.account != nil {
		// Add default account identity
		fromOptions = append(fromOptions, fmt.Sprintf("%s <%s>", cw.account.DisplayName, cw.account.Email))

		// Add personalities
		for _, personality := range cw.account.Personalities {
			fromOptions = append(fromOptions, fmt.Sprintf("%s <%s>", personality.DisplayName, personality.Email))
		}
	}

	cw.fromSelect = widget.NewSelect(fromOptions, nil)
	if len(fromOptions) > 0 {
		// Try to select the default persona, otherwise fall back to first option
		cw.selectDefaultPersona(fromOptions)
	}

	// Recipient fields with autocompletion
	accountName := ""
	if cw.account != nil {
		accountName = cw.account.Name
	}

	if cw.addressbookMgr != nil {
		cw.toEntry = NewAutocompleteEntry(cw.addressbookMgr, accountName, cw.window.Canvas())
		cw.ccEntry = NewAutocompleteEntry(cw.addressbookMgr, accountName, cw.window.Canvas())
		cw.bccEntry = NewAutocompleteEntry(cw.addressbookMgr, accountName, cw.window.Canvas())
	} else {
		// Fallback to regular entries if no addressbook manager
		cw.toEntry = &AutocompleteEntry{Entry: *widget.NewEntry()}
		cw.ccEntry = &AutocompleteEntry{Entry: *widget.NewEntry()}
		cw.bccEntry = &AutocompleteEntry{Entry: *widget.NewEntry()}
	}

	cw.toEntry.SetPlaceHolder("recipient@example.com, another@example.com")
	cw.toEntry.MultiLine = false

	cw.ccEntry.SetPlaceHolder("cc@example.com")
	cw.ccEntry.MultiLine = false

	cw.bccEntry.SetPlaceHolder("bcc@example.com")
	cw.bccEntry.MultiLine = false

	// Subject field
	cw.subjectEntry = widget.NewEntry()
	cw.subjectEntry.SetPlaceHolder("Message subject")
	cw.subjectEntry.MultiLine = false

	// Body field
	cw.bodyEntry = widget.NewMultiLineEntry()
	cw.bodyEntry.SetPlaceHolder("Type your message here...")
	cw.bodyEntry.Wrapping = fyne.TextWrapWord

	// Attachment list
	cw.attachmentList = widget.NewList(
		func() int {
			return len(cw.attachments)
		},
		func() fyne.CanvasObject {
			return container.NewHBox(
				widget.NewIcon(theme.DocumentIcon()),
				widget.NewLabel("filename.txt"),
				widget.NewLabel("(1.2 KB)"),
				widget.NewButtonWithIcon("", theme.DeleteIcon(), nil),
			)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id < len(cw.attachments) {
				attachment := cw.attachments[id]
				hbox := obj.(*fyne.Container)

				nameLabel := hbox.Objects[1].(*widget.Label)
				nameLabel.SetText(attachment.Filename)

				sizeLabel := hbox.Objects[2].(*widget.Label)
				sizeLabel.SetText(fmt.Sprintf("(%s)", formatFileSize(attachment.Size)))

				deleteBtn := hbox.Objects[3].(*widget.Button)
				deleteBtn.OnTapped = func() {
					cw.removeAttachment(id)
				}
			}
		},
	)

	// Status bar
	cw.statusBar = widget.NewLabel("Ready to compose")

	// Create toolbar with tooltip-enabled buttons

	// Create tooltip-enabled buttons
	sendWithTooltip := CreateTooltipButtonWithIcon("Send", "Send message", theme.MailSendIcon(), func() {
		cw.sendMessage()
	})
	saveDraftWithTooltip := CreateTooltipButtonWithIcon("Save Draft", "Save as draft", theme.DocumentSaveIcon(), func() {
		cw.saveDraft()
	})
	attachWithTooltip := CreateTooltipButtonWithIcon("Attach", "Add attachment", theme.ContentAddIcon(), func() {
		cw.addAttachment()
	})
	cancelWithTooltip := CreateTooltipButtonWithIcon("Cancel", "Cancel and close", theme.CancelIcon(), func() {
		cw.window.Close()
	})

	// Create toolbar container
	toolbar := container.NewHBox(
		sendWithTooltip,
		widget.NewSeparator(),
		saveDraftWithTooltip,
		attachWithTooltip,
		widget.NewSeparator(),
		cancelWithTooltip,
	)

	// Layout the UI
	headerForm := container.NewVBox(
		container.NewBorder(nil, nil, widget.NewLabel("From:"), nil, cw.fromSelect),
		container.NewBorder(nil, nil, widget.NewLabel("To:"), nil, cw.toEntry),
		container.NewBorder(nil, nil, widget.NewLabel("CC:"), nil, cw.ccEntry),
		container.NewBorder(nil, nil, widget.NewLabel("BCC:"), nil, cw.bccEntry),
		container.NewBorder(nil, nil, widget.NewLabel("Subject:"), nil, cw.subjectEntry),
	)

	bodyContainer := container.NewBorder(
		widget.NewLabel("Message:"),
		nil, nil, nil,
		cw.bodyEntry,
	)

	// Create a more compact attachment container
	attachmentContainer := container.NewBorder(
		widget.NewLabel("Attachments:"),
		nil, nil, nil,
		cw.attachmentList,
	)

	// Use a different layout approach - give more space to the message body
	// Create the body/attachment split first and configure it
	bodyAttachmentSplit := container.NewVSplit(
		bodyContainer,
		attachmentContainer,
	)
	bodyAttachmentSplit.SetOffset(0.8) // Give 80% to body, 20% to attachments

	// Split between header (25%) and body+attachments (75%)
	mainContent := container.NewVSplit(
		headerForm,
		bodyAttachmentSplit,
	)
	mainContent.SetOffset(0.25) // Give less space to header, more to body

	content := container.NewBorder(
		toolbar, cw.statusBar, nil, nil,
		mainContent,
	)

	// Wrap content with tooltip layer
	contentWithTooltips := AddTooltipLayer(content, cw.window.Canvas())
	cw.window.SetContent(contentWithTooltips)
}

// setupContent initializes the compose window with provided options
func (cw *ComposeWindow) setupContent(opts ComposeOptions) {
	// Set recipients
	if len(opts.Recipients) > 0 {
		var toAddresses []string
		for _, addr := range opts.Recipients {
			if addr.Name != "" {
				toAddresses = append(toAddresses, fmt.Sprintf("%s <%s>", addr.Name, addr.Email))
			} else {
				toAddresses = append(toAddresses, addr.Email)
			}
		}
		cw.toEntry.SetText(strings.Join(toAddresses, ", "))
	}

	// Set subject
	if opts.Subject != "" {
		cw.subjectEntry.SetText(opts.Subject)
	}

	// Set body
	if opts.Body != "" {
		cw.bodyEntry.SetText(opts.Body)
	}

	// Pre-select a specific from address if provided (e.g. when composing from unified inbox)
	if opts.SelectedFrom != "" {
		for _, option := range cw.fromSelect.Options {
			if strings.Contains(strings.ToLower(option), strings.ToLower(opts.SelectedFrom)) {
				cw.fromSelect.SetSelected(option)
				break
			}
		}
	}

	// Handle reply/forward setup
	if opts.ReplyTo != nil {
		cw.setupReply(opts.ReplyTo, opts.ReplyAll)
	} else if opts.Forward != nil {
		cw.setupForward(opts.Forward)
	}
}

// Show displays the compose window
func (cw *ComposeWindow) Show() {
	cw.window.Show()
}

// setupReply configures the compose window for replying to a message
func (cw *ComposeWindow) setupReply(originalMsg *email.Message, replyAll bool) {
	// Set subject with "Re:" prefix
	subject := originalMsg.Subject
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	cw.subjectEntry.SetText(subject)

	// Set recipients
	if replyAll {
		// Reply All: Include sender + all original recipients (except ourselves)
		var recipients []string

		// Add sender (From field)
		if len(originalMsg.From) > 0 {
			sender := originalMsg.From[0]
			if sender.Name != "" {
				recipients = append(recipients, fmt.Sprintf("%s <%s>", sender.Name, sender.Email))
			} else {
				recipients = append(recipients, sender.Email)
			}
		}

		// Add original To recipients (excluding ourselves)
		for _, addr := range originalMsg.To {
			if !cw.isOurAddress(addr.Email) {
				if addr.Name != "" {
					recipients = append(recipients, fmt.Sprintf("%s <%s>", addr.Name, addr.Email))
				} else {
					recipients = append(recipients, addr.Email)
				}
			}
		}

		// Add original CC recipients (excluding ourselves) to CC field
		var ccRecipients []string
		for _, addr := range originalMsg.CC {
			if !cw.isOurAddress(addr.Email) {
				if addr.Name != "" {
					ccRecipients = append(ccRecipients, fmt.Sprintf("%s <%s>", addr.Name, addr.Email))
				} else {
					ccRecipients = append(ccRecipients, addr.Email)
				}
			}
		}

		cw.toEntry.SetText(strings.Join(recipients, ", "))
		if len(ccRecipients) > 0 {
			cw.ccEntry.SetText(strings.Join(ccRecipients, ", "))
		}
	} else {
		// Regular Reply: Only reply to sender
		if len(originalMsg.From) > 0 {
			sender := originalMsg.From[0]
			if sender.Name != "" {
				cw.toEntry.SetText(fmt.Sprintf("%s <%s>", sender.Name, sender.Email))
			} else {
				cw.toEntry.SetText(sender.Email)
			}
		}
	}

	// Create quoted reply body
	var replyBody strings.Builder
	replyBody.WriteString("\n\n")
	replyBody.WriteString(fmt.Sprintf("On %s, %s wrote:\n",
		originalMsg.Date.Format("January 2, 2006 at 3:04 PM"),
		cw.formatAddresses(originalMsg.From)))

	// Quote original message - always use plain text
	originalText := originalMsg.Body.Text
	if originalText == "" && originalMsg.Body.HTML != "" {
		// Extract plain text from HTML
		originalText = cw.extractTextFromHTML(originalMsg.Body.HTML)
	}

	lines := strings.Split(originalText, "\n")
	for _, line := range lines {
		replyBody.WriteString("> " + line + "\n")
	}

	cw.bodyEntry.SetText(replyBody.String())

	// Auto-select persona based on original To address
	cw.selectPersonaForReply(originalMsg)
}

// isOurAddress checks if an email address belongs to the current account or its personalities
func (cw *ComposeWindow) isOurAddress(email string) bool {
	if cw.account == nil {
		return false
	}

	// Check main account email
	if strings.EqualFold(email, cw.account.Email) {
		return true
	}

	// Check personality emails
	for _, personality := range cw.account.Personalities {
		if strings.EqualFold(email, personality.Email) {
			return true
		}
	}

	return false
}

// selectPersonaForReply automatically selects the appropriate persona when replying
// by checking if the original To address matches any of our persona email addresses
func (cw *ComposeWindow) selectPersonaForReply(originalMsg *email.Message) {
	if cw.account == nil || len(originalMsg.To) == 0 {
		return
	}

	// Check each To address from the original message
	for _, toAddr := range originalMsg.To {
		// First check if it matches the main account email
		if strings.EqualFold(toAddr.Email, cw.account.Email) {
			// Select the main account identity (first option)
			if len(cw.fromSelect.Options) > 0 {
				cw.fromSelect.SetSelected(cw.fromSelect.Options[0])
				return
			}
		}

		// Check if it matches any personality email
		for _, personality := range cw.account.Personalities {
			if strings.EqualFold(toAddr.Email, personality.Email) {
				// Find and select this personality in the dropdown
				personalityStr := fmt.Sprintf("%s <%s>", personality.DisplayName, personality.Email)
				for _, option := range cw.fromSelect.Options {
					if option == personalityStr {
						cw.fromSelect.SetSelected(option)
						return
					}
				}
			}
		}
	}
}

// selectDefaultPersona selects the default persona from the available options
func (cw *ComposeWindow) selectDefaultPersona(fromOptions []string) {
	if cw.account == nil {
		// No account, just select first option
		if len(fromOptions) > 0 {
			cw.fromSelect.SetSelected(fromOptions[0])
		}
		return
	}

	// Look for a default personality
	defaultPersonality := cw.account.GetDefaultPersonality()
	if defaultPersonality != nil {
		// Find the matching option for the default personality
		defaultPersonalityStr := fmt.Sprintf("%s <%s>", defaultPersonality.DisplayName, defaultPersonality.Email)
		for _, option := range fromOptions {
			if option == defaultPersonalityStr {
				cw.fromSelect.SetSelected(option)
				return
			}
		}
	}

	// No default personality found, select the first option (main account identity)
	if len(fromOptions) > 0 {
		cw.fromSelect.SetSelected(fromOptions[0])
	}
}

// setupForward configures the compose window for forwarding a message
func (cw *ComposeWindow) setupForward(originalMsg *email.Message) {
	// Set subject with "Fwd:" prefix
	subject := originalMsg.Subject
	if !strings.HasPrefix(strings.ToLower(subject), "fwd:") && !strings.HasPrefix(strings.ToLower(subject), "fw:") {
		subject = "Fwd: " + subject
	}
	cw.subjectEntry.SetText(subject)

	// Create forwarded message body
	var forwardBody strings.Builder
	forwardBody.WriteString("\n\n---------- Forwarded message ----------\n")
	forwardBody.WriteString(fmt.Sprintf("From: %s\n", cw.formatAddresses(originalMsg.From)))
	forwardBody.WriteString(fmt.Sprintf("Date: %s\n", originalMsg.Date.Format("January 2, 2006 at 3:04 PM")))
	forwardBody.WriteString(fmt.Sprintf("Subject: %s\n", originalMsg.Subject))
	forwardBody.WriteString(fmt.Sprintf("To: %s\n", cw.formatAddresses(originalMsg.To)))
	if len(originalMsg.CC) > 0 {
		forwardBody.WriteString(fmt.Sprintf("CC: %s\n", cw.formatAddresses(originalMsg.CC)))
	}
	forwardBody.WriteString("\n")

	// Add original message body - always use plain text
	originalText := originalMsg.Body.Text
	if originalText == "" && originalMsg.Body.HTML != "" {
		// Extract plain text from HTML
		originalText = cw.extractTextFromHTML(originalMsg.Body.HTML)
	}
	forwardBody.WriteString(originalText)

	cw.bodyEntry.SetText(forwardBody.String())

	// Copy attachments from original message
	for _, attachment := range originalMsg.Attachments {
		cw.attachments = append(cw.attachments, attachment)
	}
	cw.attachmentList.Refresh()
}

// addAttachment opens a file dialog to add an attachment
func (cw *ComposeWindow) addAttachment() {
	dialog.ShowFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil {
			dialog.ShowError(err, cw.window)
			return
		}
		if reader == nil {
			return
		}
		defer reader.Close()

		// Read file data
		data, err := os.ReadFile(reader.URI().Path())
		if err != nil {
			dialog.ShowError(fmt.Errorf("failed to read file: %w", err), cw.window)
			return
		}

		// Create attachment
		filename := filepath.Base(reader.URI().Path())
		attachment := email.Attachment{
			Filename:    filename,
			ContentType: "", // Will be determined by SMTP client
			Size:        int64(len(data)),
			Data:        data,
		}

		cw.attachments = append(cw.attachments, attachment)
		cw.attachmentList.Refresh()
		cw.statusBar.SetText(fmt.Sprintf("Added attachment: %s", filename))
	}, cw.window)
}

// removeAttachment removes an attachment at the specified index
func (cw *ComposeWindow) removeAttachment(index int) {
	if index >= 0 && index < len(cw.attachments) {
		filename := cw.attachments[index].Filename
		cw.attachments = append(cw.attachments[:index], cw.attachments[index+1:]...)
		cw.attachmentList.Refresh()
		cw.statusBar.SetText(fmt.Sprintf("Removed attachment: %s", filename))
	}
}

// sendMessage validates and sends the composed message
func (cw *ComposeWindow) sendMessage() {
	cw.statusBar.SetText("Sending message...")

	// Build message from form data
	msg, err := cw.buildMessage()
	if err != nil {
		dialog.ShowError(fmt.Errorf("failed to build message: %w", err), cw.window)
		cw.statusBar.SetText("Failed to send message")
		return
	}

	// Validate message
	if cw.smtpClient != nil {
		if err := cw.smtpClient.ValidateMessage(msg); err != nil {
			dialog.ShowError(fmt.Errorf("message validation failed: %w", err), cw.window)
			cw.statusBar.SetText("Message validation failed")
			return
		}
	}

	// Send message
	go func() {
		var err error
		if cw.smtpClient != nil {
			err = cw.smtpClient.SendMessage(msg)
		} else {
			err = fmt.Errorf("no SMTP client available")
		}

		// Update UI on main thread
		fyne.Do(func() {
			if err != nil {
				cw.statusBar.SetText("Failed to send message")
				dialog.ShowError(fmt.Errorf("failed to send message: %w", err), cw.window)
			} else {
				cw.statusBar.SetText("Message sent successfully!")

				// Auto-collect contacts from sent message
				if cw.addressbookMgr != nil && cw.account != nil {
					if err := cw.addressbookMgr.AutoCollectFromMessage(msg, cw.account.Name); err != nil {
						// Log error but don't show to user as it's not critical
						fmt.Printf("Warning: Failed to auto-collect contacts: %v\n", err)
					}
				}

				// Clean up draft if it exists
				if cw.isDraft && cw.draftPath != "" {
					os.Remove(cw.draftPath)
				}

				// Call success callback
				if cw.onSent != nil {
					cw.onSent()
				}

				// Close window after successful send
				cw.window.Close()
			}
		})
	}()
}

// saveDraft saves the current message as a draft
func (cw *ComposeWindow) saveDraft() {
	cw.statusBar.SetText("Saving draft...")

	// Validate that we can build the message
	_, err := cw.buildMessage()
	if err != nil {
		dialog.ShowError(fmt.Errorf("failed to build draft: %w", err), cw.window)
		cw.statusBar.SetText("Failed to save draft")
		return
	}

	// Create drafts directory if it doesn't exist
	draftsDir := filepath.Join(cw.config.Cache.Directory, "drafts")
	if err := os.MkdirAll(draftsDir, 0755); err != nil {
		dialog.ShowError(fmt.Errorf("failed to create drafts directory: %w", err), cw.window)
		return
	}

	// Generate draft filename
	if cw.draftPath == "" {
		timestamp := time.Now().Format("20060102_150405")
		cw.draftPath = filepath.Join(draftsDir, fmt.Sprintf("draft_%s.json", timestamp))
	}

	// Save draft (simplified - in a real implementation, you'd use proper serialization)
	draftData := fmt.Sprintf(`{
		"to": "%s",
		"cc": "%s",
		"bcc": "%s",
		"subject": "%s",
		"body": "%s",
		"attachments": %d
	}`, cw.toEntry.Text, cw.ccEntry.Text, cw.bccEntry.Text,
		cw.subjectEntry.Text, strings.ReplaceAll(cw.bodyEntry.Text, `"`, `\"`),
		len(cw.attachments))

	if err := os.WriteFile(cw.draftPath, []byte(draftData), 0644); err != nil {
		dialog.ShowError(fmt.Errorf("failed to save draft: %w", err), cw.window)
		cw.statusBar.SetText("Failed to save draft")
		return
	}

	cw.isDraft = true
	cw.statusBar.SetText("Draft saved")
}

// handleClose handles the window close event
func (cw *ComposeWindow) handleClose() {
	// Check if there's unsaved content
	hasContent := cw.toEntry.Text != "" || cw.ccEntry.Text != "" || cw.bccEntry.Text != "" ||
		cw.subjectEntry.Text != "" || cw.bodyEntry.Text != "" || len(cw.attachments) > 0

	if hasContent && !cw.isDraft {
		dialog.ShowConfirm("Unsaved Changes",
			"You have unsaved changes. Do you want to save as draft before closing?",
			func(save bool) {
				if save {
					cw.saveDraft()
				}
				if cw.onClosed != nil {
					cw.onClosed()
				}
				cw.window.Close()
			}, cw.window)
	} else {
		if cw.onClosed != nil {
			cw.onClosed()
		}
		cw.window.Close()
	}
}

// buildMessage creates an email.Message from the form data
func (cw *ComposeWindow) buildMessage() (*email.Message, error) {
	// Parse From address
	fromAddresses, err := cw.parseAddresses(cw.fromSelect.Selected)
	if err != nil {
		return nil, fmt.Errorf("invalid from address: %w", err)
	}

	// Parse recipient addresses
	toAddresses, err := cw.parseAddresses(cw.toEntry.Text)
	if err != nil {
		return nil, fmt.Errorf("invalid to addresses: %w", err)
	}

	ccAddresses, err := cw.parseAddresses(cw.ccEntry.Text)
	if err != nil {
		return nil, fmt.Errorf("invalid cc addresses: %w", err)
	}

	bccAddresses, err := cw.parseAddresses(cw.bccEntry.Text)
	if err != nil {
		return nil, fmt.Errorf("invalid bcc addresses: %w", err)
	}

	// Create message body (plain text only)
	var body email.MessageBody
	body.Text = cw.bodyEntry.Text

	// Add signature if available
	if cw.account != nil && len(cw.account.Personalities) > 0 {
		// Find selected personality
		selectedFrom := cw.fromSelect.Selected
		for _, personality := range cw.account.Personalities {
			personalityStr := fmt.Sprintf("%s <%s>", personality.DisplayName, personality.Email)
			if selectedFrom == personalityStr && personality.Signature != "" {
				if body.Text != "" {
					body.Text += "\n\n--\n" + personality.Signature
				}
				break
			}
		}
	}

	// Create message
	msg := &email.Message{
		ID:          fmt.Sprintf("compose_%d", time.Now().UnixNano()),
		Subject:     cw.subjectEntry.Text,
		From:        fromAddresses,
		To:          toAddresses,
		CC:          ccAddresses,
		BCC:         bccAddresses,
		Date:        time.Now(),
		Body:        body,
		Attachments: cw.attachments,
		Headers:     make(map[string]string),
	}

	return msg, nil
}

// parseAddresses parses a comma-separated string of email addresses
func (cw *ComposeWindow) parseAddresses(addressStr string) ([]email.Address, error) {
	if strings.TrimSpace(addressStr) == "" {
		return nil, nil
	}

	var addresses []email.Address
	parts := strings.Split(addressStr, ",")

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		var addr email.Address

		// Check if it's in "Name <email>" format
		if strings.Contains(part, "<") && strings.Contains(part, ">") {
			nameEnd := strings.Index(part, "<")
			emailStart := nameEnd + 1
			emailEnd := strings.Index(part, ">")

			if nameEnd > 0 && emailEnd > emailStart {
				addr.Name = strings.TrimSpace(part[:nameEnd])
				addr.Email = strings.TrimSpace(part[emailStart:emailEnd])
			} else {
				return nil, fmt.Errorf("invalid address format: %s", part)
			}
		} else {
			// Just an email address
			addr.Email = part
		}

		// Basic email validation
		if !strings.Contains(addr.Email, "@") || !strings.Contains(addr.Email, ".") {
			return nil, fmt.Errorf("invalid email address: %s", addr.Email)
		}

		addresses = append(addresses, addr)
	}

	return addresses, nil
}

// formatAddresses formats a slice of addresses for display
func (cw *ComposeWindow) formatAddresses(addresses []email.Address) string {
	if len(addresses) == 0 {
		return ""
	}

	var formatted []string
	for _, addr := range addresses {
		if addr.Name != "" {
			formatted = append(formatted, fmt.Sprintf("%s <%s>", addr.Name, addr.Email))
		} else {
			formatted = append(formatted, addr.Email)
		}
	}

	return strings.Join(formatted, ", ")
}

// extractTextFromHTML extracts plain text from HTML content
func (cw *ComposeWindow) extractTextFromHTML(html string) string {
	// Parse HTML
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return html // Return original if parsing fails
	}

	// Add spaces after block elements to preserve word boundaries
	doc.Find("p, div, br, h1, h2, h3, h4, h5, h6").Each(func(i int, s *goquery.Selection) {
		s.AfterHtml(" ")
	})

	// Extract text content
	text := doc.Text()

	// Clean up whitespace
	text = regexp.MustCompile(`\s+`).ReplaceAllString(text, " ")
	text = strings.TrimSpace(text)

	return text
}
