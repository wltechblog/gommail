package ui

// Code in this file was mechanically split from main_window.go.

import (
	"encoding/base64"
	"fmt"
	_ "image/gif"  // Register GIF format
	_ "image/jpeg" // Register JPEG format
	_ "image/png"  // Register PNG format
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/skratchdot/open-golang/open"

	"github.com/wltechblog/gommail/internal/email"
)

// createAttachmentWidget creates an interactive UI component for a single attachment
func (mw *MainWindow) createAttachmentWidget(attachment email.Attachment, attachmentID string, index int) fyne.CanvasObject {
	// Create attachment info section
	icon := mw.getAttachmentIcon(attachment.ContentType)
	nameLabel := widget.NewRichTextFromMarkdown(fmt.Sprintf("### %s %s", icon, attachment.Filename))
	infoLabel := widget.NewLabel(fmt.Sprintf("%s • %s", attachment.ContentType, formatFileSize(attachment.Size)))

	// Create action buttons
	openButton := widget.NewButton("Open", func() {
		mw.openAttachment(attachment)
	})

	saveButton := widget.NewButton("Save", func() {
		mw.saveAttachment(attachment)
	})

	var previewWidget fyne.CanvasObject

	// Create preview based on content type
	if strings.HasPrefix(attachment.ContentType, "image/") {
		// For images, create an image widget and view button
		previewWidget = mw.createImagePreview(attachment, attachmentID)

		viewButton := widget.NewButton("View Full Size", func() {
			mw.showImageFullSize(attachment, attachmentID)
		})

		buttonContainer := container.NewHBox(openButton, saveButton, viewButton)

		return container.NewVBox(
			nameLabel,
			infoLabel,
			previewWidget,
			buttonContainer,
			widget.NewSeparator(),
		)
	} else if strings.HasPrefix(attachment.ContentType, "text/") {
		// For text files, show preview content
		previewWidget = mw.createTextPreview(attachment, attachmentID)

		buttonContainer := container.NewHBox(openButton, saveButton)

		return container.NewVBox(
			nameLabel,
			infoLabel,
			previewWidget,
			buttonContainer,
			widget.NewSeparator(),
		)
	} else {
		// For other files, just show info and buttons
		buttonContainer := container.NewHBox(openButton, saveButton)

		return container.NewVBox(
			nameLabel,
			infoLabel,
			widget.NewLabel("Preview not available for this file type"),
			buttonContainer,
			widget.NewSeparator(),
		)
	}
}

// createImagePreview creates a thumbnail preview for image attachments
func (mw *MainWindow) createImagePreview(attachment email.Attachment, attachmentID string) fyne.CanvasObject {
	if mw.attachmentManager == nil || attachmentID == "" {
		return widget.NewLabel("Image preview not available")
	}

	// Get image data from attachment manager
	previewData, err := mw.attachmentManager.GetAttachmentPreview(attachmentID, 0) // Get full image data
	if err != nil {
		return widget.NewLabel("Failed to load image preview")
	}

	// Clean base64 data by removing line breaks and whitespace
	cleanedData := strings.ReplaceAll(string(previewData), "\r\n", "")
	cleanedData = strings.ReplaceAll(cleanedData, "\n", "")
	cleanedData = strings.ReplaceAll(cleanedData, "\r", "")
	cleanedData = strings.ReplaceAll(cleanedData, " ", "")

	// Try to decode base64 data
	var imageData []byte
	if decoded, err := base64.StdEncoding.DecodeString(cleanedData); err == nil && len(decoded) > 0 {
		// Successfully decoded base64, use decoded data
		imageData = decoded
	} else {
		// Not base64 encoded, use raw data
		imageData = previewData
	}

	// Create image resource from decoded data
	resource := fyne.NewStaticResource(attachment.Filename, imageData)

	if resource == nil {
		return widget.NewLabel("Failed to create image resource")
	}

	maxWidth := float32(600)
	maxHeight := float32(400)

	imageWidget := canvas.NewImageFromResource(resource)
	imageWidget.FillMode = canvas.ImageFillCover
	imageWidget.CornerRadius = theme.Size(theme.SizeNameSelectionRadius) * 2

	previewSize := fyne.NewSize(maxWidth, maxHeight)
	imageWidget.SetMinSize(previewSize)
	imageWidget.Resize(previewSize)

	background := canvas.NewRectangle(theme.Color(theme.ColorNameInputBackground))
	background.CornerRadius = imageWidget.CornerRadius
	background.SetMinSize(previewSize)
	background.Resize(previewSize)

	card := container.NewStack(background, imageWidget)
	imageContainer := container.NewCenter(card)

	// Create a button to view full size
	viewFullSizeBtn := widget.NewButton("View Full Size", func() {
		mw.showImageFullSize(attachment, attachmentID)
	})

	// Create the preview container with proper spacing
	previewContainer := container.NewVBox(
		widget.NewLabel("Image Preview:"),
		imageContainer,
		container.NewHBox(
			viewFullSizeBtn,
			widget.NewButton("Save Image", func() {
				mw.saveAttachment(attachment)
			}),
		),
	)

	return previewContainer
}

// createTextPreview creates a text preview for text attachments
func (mw *MainWindow) createTextPreview(attachment email.Attachment, attachmentID string) fyne.CanvasObject {
	if mw.attachmentManager == nil || attachmentID == "" {
		return widget.NewLabel("Text preview not available")
	}

	// Get text data from attachment manager
	previewData, err := mw.attachmentManager.GetAttachmentPreview(attachmentID, 500) // 500 chars max
	if err != nil {
		return widget.NewLabel("Failed to load text preview")
	}

	preview := string(previewData)
	if len(preview) > 500 {
		preview = preview[:500] + "..."
	}

	// Create text widget without scroll to avoid mouse wheel conflicts
	textWidget := widget.NewRichTextFromMarkdown(fmt.Sprintf("**Preview:**\n```\n%s\n```", preview))
	textWidget.Wrapping = fyne.TextWrapWord

	// Put it in a simple container with limited height - let the main viewer handle scrolling
	textContainer := container.NewBorder(nil, nil, nil, nil, textWidget)

	return textContainer
}

// showImageFullSize displays an image attachment in full size
func (mw *MainWindow) showImageFullSize(attachment email.Attachment, attachmentID string) {
	if mw.attachmentManager == nil || attachmentID == "" {
		dialog.ShowError(fmt.Errorf("image preview not available"), mw.window)
		return
	}

	// Get full image data
	rawImageData, err := mw.attachmentManager.GetAttachmentPreview(attachmentID, 0)
	if err != nil {
		dialog.ShowError(fmt.Errorf("failed to load image: %w", err), mw.window)
		return
	}

	// Clean base64 data by removing line breaks and whitespace
	cleanedData := strings.ReplaceAll(string(rawImageData), "\r\n", "")
	cleanedData = strings.ReplaceAll(cleanedData, "\n", "")
	cleanedData = strings.ReplaceAll(cleanedData, "\r", "")
	cleanedData = strings.ReplaceAll(cleanedData, " ", "")

	// Decode base64 data
	var imageData []byte
	if decoded, err := base64.StdEncoding.DecodeString(cleanedData); err == nil && len(decoded) > 0 {
		// Successfully decoded base64, use decoded data
		imageData = decoded
	} else {
		// Not base64 encoded, use raw data
		imageData = rawImageData
	}

	// Create image resource
	resource := fyne.NewStaticResource(attachment.Filename, imageData)

	// Create full-size image widget
	imageWidget := canvas.NewImageFromResource(resource)
	imageWidget.FillMode = canvas.ImageFillContain

	// Create scrollable container for large images
	scrollContainer := container.NewScroll(imageWidget)

	// Create dialog to show the image
	imageDialog := dialog.NewCustom(
		fmt.Sprintf("Image: %s", attachment.Filename),
		"Close",
		scrollContainer,
		mw.window,
	)

	// Set dialog size based on screen size
	imageDialog.Resize(fyne.NewSize(800, 600))
	imageDialog.Show()
}

// cacheAttachmentIfNeeded caches an attachment if it's not already cached and returns the attachment ID
func (mw *MainWindow) cacheAttachmentIfNeeded(msg *email.Message, attachment email.Attachment) string {
	if mw.attachmentManager == nil {
		return ""
	}

	// Generate attachment ID
	attachmentID := mw.attachmentManager.GenerateAttachmentID(msg.ID, attachment.Filename)

	// Check if already cached
	if mw.attachmentManager.IsAttachmentCached(attachmentID) {
		return attachmentID
	}

	// Cache the attachment
	_, err := mw.attachmentManager.CacheAttachment(msg.ID, attachment)
	if err != nil {
		mw.statusBar.SetText(fmt.Sprintf("Failed to cache attachment: %s", err.Error()))
		return ""
	}

	return attachmentID
}

// getAttachmentIcon returns an emoji icon for the attachment content type
func (mw *MainWindow) getAttachmentIcon(contentType string) string {
	switch {
	case strings.HasPrefix(contentType, "image/"):
		return "🖼️"
	case strings.HasPrefix(contentType, "text/"):
		return "📄"
	case strings.HasPrefix(contentType, "video/"):
		return "🎥"
	case strings.HasPrefix(contentType, "audio/"):
		return "🎵"
	case strings.Contains(contentType, "pdf"):
		return "📕"
	case strings.Contains(contentType, "zip") || strings.Contains(contentType, "archive"):
		return "📦"
	case strings.Contains(contentType, "word") || strings.Contains(contentType, "document"):
		return "📝"
	case strings.Contains(contentType, "excel") || strings.Contains(contentType, "spreadsheet"):
		return "📊"
	case strings.Contains(contentType, "powerpoint") || strings.Contains(contentType, "presentation"):
		return "📈"
	default:
		return "📎"
	}
}

// saveAttachments allows the user to save all attachments from the current message
func (mw *MainWindow) saveAttachments() {
	if mw.selectedMessage == nil {
		dialog.ShowInformation("No Message", "Please select a message to save attachments", mw.window)
		return
	}

	if len(mw.selectedMessage.Message.Attachments) == 0 {
		dialog.ShowInformation("No Attachments", "The selected message has no attachments", mw.window)
		return
	}

	// If only one attachment, save it directly
	if len(mw.selectedMessage.Message.Attachments) == 1 {
		mw.saveAttachment(mw.selectedMessage.Message.Attachments[0])
		return
	}

	// Multiple attachments - show selection dialog
	attachmentNames := make([]string, len(mw.selectedMessage.Message.Attachments))
	for i, attachment := range mw.selectedMessage.Message.Attachments {
		attachmentNames[i] = fmt.Sprintf("%s (%s)", attachment.Filename, formatFileSize(attachment.Size))
	}

	// Create selection dialog
	attachmentList := widget.NewList(
		func() int { return len(attachmentNames) },
		func() fyne.CanvasObject {
			return container.NewHBox(
				widget.NewCheck("", nil),
				widget.NewLabel("Attachment name"),
			)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			hbox := obj.(*fyne.Container)
			check := hbox.Objects[0].(*widget.Check)
			label := hbox.Objects[1].(*widget.Label)

			label.SetText(attachmentNames[id])
			check.SetChecked(false) // Default unchecked
		},
	)

	// Track selected attachments
	selectedAttachments := make(map[int]bool)

	// Update selection tracking when items are tapped
	attachmentList.OnSelected = func(id widget.ListItemID) {
		selectedAttachments[id] = !selectedAttachments[id]
		attachmentList.Refresh()
	}

	// Add save button
	saveButton := widget.NewButton("Save Selected", func() {
		for id, selected := range selectedAttachments {
			if selected && id < len(mw.selectedMessage.Message.Attachments) {
				mw.saveAttachment(mw.selectedMessage.Message.Attachments[id])
			}
		}
	})

	saveAllButton := widget.NewButton("Save All", func() {
		for _, attachment := range mw.selectedMessage.Message.Attachments {
			mw.saveAttachment(attachment)
		}
	})

	buttons := container.NewHBox(saveButton, saveAllButton)
	content := container.NewVBox(attachmentList, buttons)

	// Create dialog with the complete content
	saveDialog := dialog.NewCustom("Save Attachments", "Cancel", content, mw.window)
	saveDialog.Resize(fyne.NewSize(400, 300))
	saveDialog.Show()
}

// saveAttachment saves a single attachment to disk
func (mw *MainWindow) saveAttachment(attachment email.Attachment) {
	// Create a file save dialog with the attachment filename pre-populated
	saveDialog := dialog.NewFileSave(func(writer fyne.URIWriteCloser, err error) {
		if err != nil {
			dialog.ShowError(err, mw.window)
			return
		}
		if writer == nil {
			return
		}
		defer writer.Close()

		_, err = writer.Write(attachment.Data)
		if err != nil {
			dialog.ShowError(fmt.Errorf("failed to save attachment: %w", err), mw.window)
			return
		}

		mw.statusBar.SetText(fmt.Sprintf("Saved: %s", attachment.Filename))
	}, mw.window)

	// Sanitize the filename to prevent path traversal and other filesystem issues
	// Characters like / \ .. : * ? " < > | are replaced with _
	safeFilename := email.SanitizeFilename(attachment.Filename)
	saveDialog.SetFileName(safeFilename)
	saveDialog.Show()
}

// openAttachment saves an attachment to a temporary location and opens it with the system's default application
func (mw *MainWindow) openAttachment(attachment email.Attachment) {
	// Validate attachment safety before opening (defense-in-depth)
	// This prevents opening executable files and scripts that could be dangerous
	if err := email.ValidateAttachmentSafety(attachment.Filename); err != nil {
		dialog.ShowError(fmt.Errorf("cannot open attachment: %w", err), mw.window)
		mw.logger.Warn("Blocked attempt to open dangerous attachment: %s (%v)", attachment.Filename, err)
		return
	}

	// Create a temporary directory for the attachment
	tempDir := filepath.Join(os.TempDir(), "gommail-attachments")
	err := os.MkdirAll(tempDir, 0755)
	if err != nil {
		dialog.ShowError(fmt.Errorf("failed to create temporary directory: %w", err), mw.window)
		return
	}

	// Sanitize the filename to prevent path traversal
	safeFilename := email.SanitizeFilename(attachment.Filename)
	tempFilePath := filepath.Join(tempDir, safeFilename)

	// Write the attachment to the temporary file
	err = os.WriteFile(tempFilePath, attachment.Data, 0644)
	if err != nil {
		dialog.ShowError(fmt.Errorf("failed to save attachment to temporary file: %w", err), mw.window)
		return
	}

	// Open the file with the system's default application
	err = open.Start(tempFilePath)
	if err != nil {
		dialog.ShowError(fmt.Errorf("failed to open attachment: %w", err), mw.window)
		return
	}

	mw.statusBar.SetText(fmt.Sprintf("Opened: %s", attachment.Filename))
}

// updateAttachmentSection creates UI components for message attachments
func (mw *MainWindow) updateAttachmentSection(msg *email.Message) {
	// Delegate to MessageViewController with wrapper functions
	cacheFunc := func(attachment email.Attachment) string {
		return mw.cacheAttachmentIfNeeded(msg, attachment)
	}
	mw.messageViewController.UpdateAttachmentSection(msg, cacheFunc, mw.createAttachmentWidget)
}
