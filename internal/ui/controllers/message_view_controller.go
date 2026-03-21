// Package controllers provides UI controller implementations
package controllers

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/PuerkitoBio/goquery"
	"github.com/wltechblog/gommail/internal/email"
	"github.com/wltechblog/gommail/internal/logging"
)

// maxMessageDisplayBytes is the maximum number of bytes of markdown content that
// will be passed to RichText.ParseMarkdown. Fyne's text layout (binarySearch +
// HarfBuzz shaping) is O(n log n) in content length and runs on the locked UI
// thread; emails above ~100 KB can freeze the application for minutes or longer.
const maxMessageDisplayBytes = 100 * 1024 // 100 KB

// MessageViewControllerImpl implements the MessageViewController interface.
// It manages message display, rendering, and attachment handling.
type MessageViewControllerImpl struct {
	// UI components
	messageViewer     *widget.RichText
	attachmentSection *fyne.Container
	messageContainer  *fyne.Container

	// View state
	showHTMLContent bool

	// Dependencies
	attachmentManager AttachmentManager

	// Callbacks
	onViewToggled func(showHTML bool)

	// Logger
	logger *logging.Logger
}

// AttachmentManager interface for attachment operations
type AttachmentManager interface {
	GetCachedAttachment(attachmentID string) (*email.ViewableAttachment, error)
}

// NewMessageViewController creates a new MessageViewController instance.
func NewMessageViewController(attachmentManager AttachmentManager, showHTMLByDefault bool) *MessageViewControllerImpl {
	messageViewer := widget.NewRichText()
	messageViewer.Wrapping = fyne.TextWrapWord
	attachmentSection := container.NewVBox()
	messageContainer := container.NewBorder(
		nil, attachmentSection, nil, nil,
		messageViewer,
	)

	return &MessageViewControllerImpl{
		messageViewer:     messageViewer,
		attachmentSection: attachmentSection,
		messageContainer:  messageContainer,
		showHTMLContent:   showHTMLByDefault,
		attachmentManager: attachmentManager,
		logger:            logging.NewComponent("message-view"),
	}
}

// SetOnViewToggled sets the callback for when the HTML/Text view is toggled.
func (mvc *MessageViewControllerImpl) SetOnViewToggled(callback func(showHTML bool)) {
	mvc.onViewToggled = callback
}

// DisplayMessage displays a message with headers and body content.
func (mvc *MessageViewControllerImpl) DisplayMessage(msg *email.Message, formatAddresses func([]email.Address) string, getDisplayDate func(*email.Message) string) {
	var content strings.Builder

	// Message header
	content.WriteString(fmt.Sprintf("# %s\n\n", msg.Subject))

	// Use blockquote format for headers
	content.WriteString(fmt.Sprintf("> **From:** %s\n", formatAddresses(msg.From)))
	content.WriteString(fmt.Sprintf("> **To:** %s\n", formatAddresses(msg.To)))
	if len(msg.CC) > 0 {
		content.WriteString(fmt.Sprintf("> **CC:** %s\n", formatAddresses(msg.CC)))
	}
	if len(msg.ReplyTo) > 0 {
		content.WriteString(fmt.Sprintf("> **Reply-To:** %s\n", formatAddresses(msg.ReplyTo)))
	}
	content.WriteString(fmt.Sprintf("> **Date:** %s\n\n", getDisplayDate(msg)))

	// Message body
	content.WriteString("---\n\n")

	mvc.logger.Debug("Message has Text: %t (len=%d), HTML: %t (len=%d)",
		msg.Body.Text != "", len(msg.Body.Text),
		msg.Body.HTML != "", len(msg.Body.HTML))
	mvc.logger.Debug("showHTMLContent = %t", mvc.showHTMLContent)

	// Determine what content to show
	var (
		displayContent    string
		contentIsMarkdown bool
	)

	if mvc.showHTMLContent && msg.Body.HTML != "" {
		displayContent = mvc.HTMLToMarkdown(msg.Body.HTML)
		contentIsMarkdown = true
		mvc.logger.Debug("Using HTML content (converted to markdown)")
	} else if msg.Body.Text != "" {
		displayContent = mvc.FormatTextForMarkdown(mvc.linkifyBareURLs(msg.Body.Text))
		contentIsMarkdown = true
		mvc.logger.Debug("Using plain text content")
	} else if msg.Body.HTML != "" {
		displayContent = mvc.HTMLToMarkdown(msg.Body.HTML)
		contentIsMarkdown = true
		mvc.logger.Debug("Using HTML content (only option, converted to markdown)")
	} else {
		displayContent = "*No message content available*"
		mvc.logger.Debug("No content available")
	}

	// Format and display the content
	if contentIsMarkdown {
		content.WriteString(displayContent)
	} else {
		content.WriteString(displayContent)
	}

	mvc.UpdateMessageViewer(content.String())
}

// UpdateMessageViewer updates the message viewer with markdown content.
// Content larger than maxMessageDisplayBytes is truncated to prevent the UI
// thread from hanging in Fyne's text-layout code.
func (mvc *MessageViewControllerImpl) UpdateMessageViewer(markdownContent string) {
	if len(markdownContent) > maxMessageDisplayBytes {
		originalLen := len(markdownContent)
		// Truncate at a newline boundary so we don't cut a markdown element in half.
		truncPoint := strings.LastIndexByte(markdownContent[:maxMessageDisplayBytes], '\n')
		if truncPoint < 1 {
			truncPoint = maxMessageDisplayBytes
		}
		mvc.logger.Warn("Message content too large (%d KB), truncating to %d KB for display",
			originalLen/1024, truncPoint/1024)
		markdownContent = markdownContent[:truncPoint] +
			fmt.Sprintf("\n\n---\n\n*⚠️ Message truncated: full content is %d KB, only the first %d KB is shown.*",
				originalLen/1024, truncPoint/1024)
	}
	mvc.messageViewer.ParseMarkdown(markdownContent)
}

// ClearMessageView clears the message viewer.
func (mvc *MessageViewControllerImpl) ClearMessageView() {
	mvc.UpdateMessageViewer("Select a message to view")
	mvc.attachmentSection.Objects = nil
	mvc.attachmentSection.Refresh()
}

// UpdateAttachmentSection creates UI components for message attachments.
func (mvc *MessageViewControllerImpl) UpdateAttachmentSection(msg *email.Message, cacheAttachment func(email.Attachment) string, createWidget func(email.Attachment, string, int) fyne.CanvasObject) {
	// Clear existing attachment components
	mvc.attachmentSection.Objects = nil

	if len(msg.Attachments) == 0 {
		mvc.attachmentSection.Refresh()
		return
	}

	// Add attachments header
	attachmentHeader := widget.NewRichTextFromMarkdown("---\n\n**Attachments:**")
	mvc.attachmentSection.Add(attachmentHeader)

	// Process each attachment
	for i, attachment := range msg.Attachments {
		attachmentID := cacheAttachment(attachment)
		attachmentWidget := createWidget(attachment, attachmentID, i)
		mvc.attachmentSection.Add(attachmentWidget)
	}

	mvc.attachmentSection.Refresh()
}

// ToggleHTMLView toggles between HTML and plain text view.
func (mvc *MessageViewControllerImpl) ToggleHTMLView() {
	mvc.showHTMLContent = !mvc.showHTMLContent
	mvc.logger.Debug("HTML view toggled to: %t", mvc.showHTMLContent)

	if mvc.onViewToggled != nil {
		mvc.onViewToggled(mvc.showHTMLContent)
	}
}

// IsShowingHTML returns whether HTML view is enabled.
func (mvc *MessageViewControllerImpl) IsShowingHTML() bool {
	return mvc.showHTMLContent
}

func (mvc *MessageViewControllerImpl) HTMLToMarkdown(html string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		mvc.logger.Error("Failed to parse HTML: %v", err)
		return mvc.HTMLToPlainText(html)
	}

	mvc.prepareHTMLDocument(doc)

	var result strings.Builder
	mvc.processHTMLNode(doc.Selection, &result, 0)
	cleaned := strings.TrimSpace(result.String())
	if cleaned == "" {
		return mvc.HTMLToPlainText(html)
	}
	cleaned = regexp.MustCompile(`[ \t]+\n`).ReplaceAllString(cleaned, "\n")
	cleaned = regexp.MustCompile(`\n\s*\n\s*\n+`).ReplaceAllString(cleaned, "\n\n")

	return cleaned
}

func (mvc *MessageViewControllerImpl) prepareHTMLDocument(doc *goquery.Document) {
	mvc.annotateReplyAndQuoteStructure(doc)

	doc.Find("script, style, head, meta, title").Each(func(_ int, s *goquery.Selection) {
		s.Remove()
	})
	doc.Find("link").Each(func(_ int, s *goquery.Selection) {
		rel, _ := s.Attr("rel")
		rel = strings.ToLower(strings.TrimSpace(rel))
		if rel == "" || strings.Contains(rel, "stylesheet") || strings.Contains(rel, "preload") || strings.Contains(rel, "import") {
			s.Remove()
		}
	})
	doc.Find("*").Each(func(_ int, s *goquery.Selection) {
		for _, attr := range []string{"style", "class", "id", "onclick", "onload", "onerror", "onmouseover", "onfocus", "onmouseenter", "onmouseleave"} {
			s.RemoveAttr(attr)
		}
	})
}

func (mvc *MessageViewControllerImpl) annotateReplyAndQuoteStructure(doc *goquery.Document) {
	doc.Find("*").Each(func(_ int, s *goquery.Selection) {
		nodeName := strings.ToLower(goquery.NodeName(s))
		className, _ := s.Attr("class")
		idValue, _ := s.Attr("id")
		normalizedClass := strings.ToLower(className)
		normalizedID := strings.ToLower(idValue)
		text := mvc.normalizedNodeText(s)
		hasQuoteAncestor := mvc.hasQuoteAncestor(s)

		if !hasQuoteAncestor && mvc.isQuoteWrapper(nodeName, normalizedClass, normalizedID) {
			s.SetAttr("data-gommail-quote", "true")
		}
		if !hasQuoteAncestor && mvc.isReplyHeaderNode(nodeName, text, normalizedClass, normalizedID) {
			s.SetAttr("data-gommail-reply-header", "true")
		}
		if !hasQuoteAncestor && mvc.isOriginalMessageSeparator(nodeName, text) {
			s.SetAttr("data-gommail-separator", "true")
		}
	})
}

func (mvc *MessageViewControllerImpl) normalizedNodeText(s *goquery.Selection) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s.Text())), " ")
}

func (mvc *MessageViewControllerImpl) hasQuoteAncestor(s *goquery.Selection) bool {
	for parent := s.Parent(); parent != nil && parent.Length() > 0; parent = parent.Parent() {
		nodeName := strings.ToLower(goquery.NodeName(parent))
		className, _ := parent.Attr("class")
		idValue, _ := parent.Attr("id")
		if mvc.isQuoteWrapper(nodeName, strings.ToLower(className), strings.ToLower(idValue)) {
			return true
		}
	}
	return false
}

func (mvc *MessageViewControllerImpl) isQuoteWrapper(nodeName, className, idValue string) bool {
	if nodeName == "blockquote" {
		return true
	}

	quoteHints := []string{
		"gmail_quote",
		"protonmail_quote",
		"yahoo_quoted",
		"moz-cite-prefix",
		"divrplyfwdmsg",
		"replyforward",
		"mailquote",
	}

	for _, hint := range quoteHints {
		if strings.Contains(className, hint) || strings.Contains(idValue, hint) {
			return true
		}
	}

	return false
}

func (mvc *MessageViewControllerImpl) isReplyHeaderNode(nodeName, text, className, idValue string) bool {
	if text == "" {
		return false
	}

	if strings.Contains(className, "gmail_attr") || strings.Contains(className, "moz-cite-prefix") || strings.Contains(idValue, "replyforward") {
		return true
	}

	if nodeName != "div" && nodeName != "p" && nodeName != "span" {
		return false
	}

	replyHeaderPatterns := []*regexp.Regexp{regexp.MustCompile(`(?i)^on .+ wrote:?$`)}

	for _, pattern := range replyHeaderPatterns {
		if pattern.MatchString(text) {
			return true
		}
	}

	return false
}

func (mvc *MessageViewControllerImpl) isOriginalMessageSeparator(nodeName, text string) bool {
	if nodeName == "hr" {
		return true
	}

	if text == "" {
		return false
	}

	separatorPatterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)^-+\s*original message\s*-+$`),
		regexp.MustCompile(`(?i)^begin forwarded message:?$`),
	}

	for _, pattern := range separatorPatterns {
		if pattern.MatchString(text) {
			return true
		}
	}

	return false
}

func (mvc *MessageViewControllerImpl) HTMLToPlainText(html string) string {
	// Check if this looks like mostly CSS content
	cssPatterns := []string{
		`@media`, `@font-face`, `@keyframes`,
		`{[^}]*:[^}]*}`,        // CSS rules
		`\.[a-zA-Z0-9_-]+\s*{`, // Class selectors
		`#[a-zA-Z0-9_-]+\s*{`,  // ID selectors
	}

	cssCount := 0
	for _, pattern := range cssPatterns {
		re := regexp.MustCompile(pattern)
		cssCount += len(re.FindAllString(html, -1))
	}

	// If we detect significant CSS content, use standard HTML to text conversion
	if cssCount > 5 {
		mvc.logger.Debug("Detected CSS-heavy content, using standard HTML to text conversion")
		return mvc.standardHTMLToText(html)
	}

	return mvc.standardHTMLToText(html)
}

// standardHTMLToText performs standard HTML to text conversion.
func (mvc *MessageViewControllerImpl) standardHTMLToText(html string) string {
	// Remove CSS style blocks and inline styles
	cssBlockRegex := regexp.MustCompile(`(?s)<style[^>]*>.*?</style>`)
	html = cssBlockRegex.ReplaceAllString(html, "")

	inlineStyleRegex := regexp.MustCompile(`\s+style="[^"]*"`)
	html = inlineStyleRegex.ReplaceAllString(html, "")

	// Remove script tags
	scriptRegex := regexp.MustCompile(`(?s)<script[^>]*>.*?</script>`)
	html = scriptRegex.ReplaceAllString(html, "")

	// Convert common HTML elements to text equivalents
	html = regexp.MustCompile(`<br\s*/?>|<BR\s*/?>`).ReplaceAllString(html, "\n")
	html = regexp.MustCompile(`<p[^>]*>`).ReplaceAllString(html, "\n\n")
	html = regexp.MustCompile(`</p>`).ReplaceAllString(html, "\n")
	html = regexp.MustCompile(`<div[^>]*>`).ReplaceAllString(html, "\n")
	html = regexp.MustCompile(`</div>`).ReplaceAllString(html, "\n")

	// Remove all remaining HTML tags
	tagRegex := regexp.MustCompile(`<[^>]+>`)
	text := tagRegex.ReplaceAllString(html, "")

	// Decode HTML entities
	text = mvc.decodeHTMLEntities(text)

	// Clean up whitespace
	text = regexp.MustCompile(`\n\s*\n\s*\n+`).ReplaceAllString(text, "\n\n")
	text = strings.TrimSpace(text)

	return text
}

// FormatTextForMarkdown formats text content for markdown display.
func (mvc *MessageViewControllerImpl) FormatTextForMarkdown(text string) string {
	// Replace single line breaks with double line breaks for markdown
	lines := strings.Split(text, "\n")
	var formatted strings.Builder

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			formatted.WriteString("\n")
		} else {
			formatted.WriteString(trimmed)
			if i < len(lines)-1 {
				formatted.WriteString("\n\n")
			}
		}
	}

	return formatted.String()
}

func (mvc *MessageViewControllerImpl) linkifyBareURLs(text string) string {
	urlRegex := regexp.MustCompile(`(?i)(https?://[^\s<>()]+|mailto:[^\s<>()]+)`)

	return urlRegex.ReplaceAllStringFunc(text, func(match string) string {
		trimmed := strings.TrimRight(match, ".,;:!?")
		trailing := strings.TrimPrefix(match, trimmed)
		if trimmed == "" {
			return match
		}
		if strings.Contains(trimmed, "](") {
			return match
		}
		return mvc.markdownLink(trimmed, trimmed) + trailing
	})
}

func (mvc *MessageViewControllerImpl) markdownLink(target, label string) string {
	target = strings.TrimSpace(target)
	label = strings.Join(strings.Fields(strings.TrimSpace(label)), " ")
	if target == "" {
		return label
	}
	if label == "" || mvc.looksLikeURL(label) || strings.EqualFold(label, target) {
		label = mvc.shortenURLForDisplay(target)
	}
	return fmt.Sprintf("[%s](%s)", label, target)
}

func (mvc *MessageViewControllerImpl) looksLikeURL(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "mailto:")
}

func (mvc *MessageViewControllerImpl) shortenURLForDisplay(target string) string {
	trimmed := strings.TrimSpace(target)
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "mailto:") {
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) == 2 && parts[1] != "" {
			return parts[1]
		}
		return trimmed
	}

	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return mvc.truncateDisplayText(trimmed, 48)
	}

	display := parsed.Host
	segments := strings.FieldsFunc(strings.Trim(parsed.EscapedPath(), "/"), func(r rune) bool { return r == '/' })
	maxSegments := 2
	if len(segments) > 0 {
		visibleCount := len(segments)
		if visibleCount > maxSegments {
			visibleCount = maxSegments
		}
		visible := segments[:visibleCount]
		display += "/" + strings.Join(visible, "/")
	}
	if len(segments) > maxSegments || parsed.RawQuery != "" || parsed.Fragment != "" {
		display += "/…"
	}

	return mvc.truncateDisplayText(display, 48)
}

func (mvc *MessageViewControllerImpl) truncateDisplayText(value string, maxLen int) string {
	if len(value) <= maxLen || maxLen < 2 {
		return value
	}
	return value[:maxLen-1] + "…"
}

func (mvc *MessageViewControllerImpl) renderBlockquoteMarkdown(s *goquery.Selection, depth int) string {
	var quoted strings.Builder
	mvc.processHTMLNode(s, &quoted, depth)
	raw := strings.TrimSpace(quoted.String())
	if raw == "" {
		return ""
	}

	lines := strings.Split(raw, "\n")
	formatted := make([]string, 0, len(lines))
	lastWasBlank := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if !lastWasBlank {
				formatted = append(formatted, ">")
				lastWasBlank = true
			}
			continue
		}
		formatted = append(formatted, "> "+trimmed)
		lastWasBlank = false
	}

	return strings.Join(formatted, "\n")
}

func (mvc *MessageViewControllerImpl) renderReplyHeaderMarkdown(s *goquery.Selection, depth int) string {
	var header strings.Builder
	mvc.processHTMLNode(s, &header, depth)
	raw := strings.TrimSpace(header.String())
	if raw == "" {
		return ""
	}

	lines := strings.Split(raw, "\n")
	formatted := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		formatted = append(formatted, "> "+trimmed)
	}

	if len(formatted) == 0 {
		return ""
	}

	return strings.Join(formatted, "\n")
}

// GetMessageViewer returns the message viewer widget.
func (mvc *MessageViewControllerImpl) GetMessageViewer() *widget.RichText {
	return mvc.messageViewer
}

// GetAttachmentSection returns the attachment section container.
func (mvc *MessageViewControllerImpl) GetAttachmentSection() *fyne.Container {
	return mvc.attachmentSection
}

// GetMessageContainer returns the message container.
func (mvc *MessageViewControllerImpl) GetMessageContainer() *fyne.Container {
	return mvc.messageContainer
}

// processHTMLNode recursively processes HTML nodes and converts them to markdown.
func (mvc *MessageViewControllerImpl) processHTMLNode(s *goquery.Selection, result *strings.Builder, depth int) {
	s.Contents().Each(func(i int, child *goquery.Selection) {
		if quoteAttr, _ := child.Attr("data-gommail-quote"); quoteAttr == "true" {
			quoted := mvc.renderBlockquoteMarkdown(child, depth+1)
			if quoted != "" {
				result.WriteString("\n\n")
				result.WriteString(quoted)
				result.WriteString("\n\n")
			}
			return
		}
		if separatorAttr, _ := child.Attr("data-gommail-separator"); separatorAttr == "true" {
			result.WriteString("\n\n---\n\n")
			if label := strings.TrimSpace(child.Text()); label != "" && !regexp.MustCompile(`(?i)^hr$`).MatchString(strings.ToLower(goquery.NodeName(child))) {
				if !regexp.MustCompile(`(?i)^-+\s*original message\s*-+$`).MatchString(label) {
					result.WriteString("**")
					result.WriteString(label)
					result.WriteString("**\n\n")
				}
			}
			return
		}
		if replyHeaderAttr, _ := child.Attr("data-gommail-reply-header"); replyHeaderAttr == "true" {
			header := mvc.renderReplyHeaderMarkdown(child, depth+1)
			if header != "" {
				result.WriteString("\n\n---\n\n")
				result.WriteString(header)
				result.WriteString("\n\n")
			}
			return
		}

		if goquery.NodeName(child) == "#text" {
			text := strings.Join(strings.Fields(child.Text()), " ")
			if text != "" {
				result.WriteString(mvc.linkifyBareURLs(text))
				result.WriteString(" ")
			}
		} else {
			nodeName := goquery.NodeName(child)
			if mvc.shouldSkipNode(nodeName) {
				return
			}
			switch nodeName {
			case "p", "div":
				mvc.processHTMLNode(child, result, depth+1)
				result.WriteString("\n\n")
			case "br":
				result.WriteString("\n")
			case "a":
				href, exists := child.Attr("href")
				text := strings.TrimSpace(child.Text())
				if exists && href != "" {
					if text == "" {
						text = href
					}
					result.WriteString(mvc.markdownLink(href, text))
				} else {
					result.WriteString(mvc.linkifyBareURLs(text))
				}
				result.WriteString(" ")
			case "strong", "b":
				text := strings.TrimSpace(child.Text())
				if text != "" {
					result.WriteString(fmt.Sprintf("**%s**", text))
					result.WriteString(" ")
				}
			case "em", "i":
				text := strings.TrimSpace(child.Text())
				if text != "" {
					result.WriteString(fmt.Sprintf("*%s*", text))
					result.WriteString(" ")
				}
			case "h1", "h2", "h3", "h4", "h5", "h6":
				text := strings.TrimSpace(child.Text())
				if text != "" {
					level := nodeName[1] - '0'
					result.WriteString(strings.Repeat("#", int(level)))
					result.WriteString(" ")
					result.WriteString(text)
					result.WriteString("\n\n")
				}
			case "ul", "ol":
				result.WriteString("\n\n")
				mvc.processHTMLNode(child, result, depth+1)
				result.WriteString("\n")
			case "li":
				indentLevel := 0
				if depth > 1 {
					indentLevel = (depth - 2) / 2
				}
				result.WriteString(strings.Repeat("  ", indentLevel))
				result.WriteString("- ")
				mvc.processHTMLNode(child, result, depth+1)
				result.WriteString("\n")
			case "img":
				alt, _ := child.Attr("alt")
				alt = strings.TrimSpace(alt)
				if alt == "" {
					alt = "image"
				}
				src, _ := child.Attr("src")
				src = strings.TrimSpace(src)
				lowerSrc := strings.ToLower(src)
				switch {
				case src == "":
					result.WriteString(fmt.Sprintf("_[Image: %s]_", alt))
				case strings.HasPrefix(lowerSrc, "cid:"):
					result.WriteString(fmt.Sprintf("_[Inline image: %s]_", alt))
				default:
					result.WriteString(fmt.Sprintf("_[Image: %s (%s)]_", alt, src))
				}
				result.WriteString("\n\n")
			default:
				mvc.processHTMLNode(child, result, depth+1)
			}
		}
	})
}

func (mvc *MessageViewControllerImpl) shouldSkipNode(name string) bool {
	switch name {
	case "style", "script", "link", "meta", "title", "head":
		return true
	}
	return false
}

// decodeHTMLEntities decodes common HTML entities.
func (mvc *MessageViewControllerImpl) decodeHTMLEntities(text string) string {
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", "\"")
	text = strings.ReplaceAll(text, "&#39;", "'")
	text = strings.ReplaceAll(text, "&apos;", "'")
	text = strings.ReplaceAll(text, "&mdash;", "—")
	text = strings.ReplaceAll(text, "&ndash;", "–")
	text = strings.ReplaceAll(text, "&hellip;", "…")
	return text
}
