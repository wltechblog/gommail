package email

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

var whitespaceRe = regexp.MustCompile(`\s+`)

// MessageProcessor handles parsing and processing of email messages
type MessageProcessor struct {
	// Configuration options for processing
	MaxAttachmentSize int64 // Maximum attachment size in bytes
	SanitizeHTML      bool  // Whether to sanitize HTML content
	// SaveFailedMessages enables dumping raw messages that fail to parse
	// to /tmp for debugging. Disabled by default: failed messages can
	// contain anything, including private content.
	SaveFailedMessages bool

	// Attachment management
	AttachmentManager *AttachmentManager
}

// NewMessageProcessor creates a new message processor with default settings
func NewMessageProcessor() *MessageProcessor {
	return &MessageProcessor{
		MaxAttachmentSize: 10 * 1024 * 1024, // 10MB default
		SanitizeHTML:      true,
	}
}

// NewMessageProcessorWithAttachments creates a message processor with attachment management
func NewMessageProcessorWithAttachments(cacheDir, downloadDir string) *MessageProcessor {
	processor := NewMessageProcessor()
	processor.AttachmentManager = NewAttachmentManager(cacheDir, downloadDir)
	return processor
}

// ParseRawMessage parses a raw email message (RFC 5322 format) into our Message structure
func (p *MessageProcessor) ParseRawMessage(rawMessage []byte) (*Message, error) {
	// Parse the message using go-message
	reader := bytes.NewReader(rawMessage)
	msg, err := mail.ReadMessage(reader)
	if err != nil {
		// Save the failed message for debugging
		p.saveFailedMessage(rawMessage, err)
		return nil, fmt.Errorf("failed to parse message: %w", err)
	}

	// Create our message structure
	emailMsg := &Message{
		Headers: make(map[string]string),
	}

	// Parse headers
	err = p.parseHeaders(msg.Header, emailMsg)
	if err != nil {
		// Save the failed message for debugging
		p.saveFailedMessage(rawMessage, err)
		return nil, fmt.Errorf("failed to parse headers: %w", err)
	}

	// Parse body and attachments
	err = p.parseBodyWithEncoding(msg.Body, msg.Header, emailMsg)
	if err != nil {
		// Save the failed message for debugging
		p.saveFailedMessage(rawMessage, err)
		return nil, fmt.Errorf("failed to parse body: %w", err)
	}

	return emailMsg, nil
}

// parseHeaders extracts headers from the mail message
func (p *MessageProcessor) parseHeaders(header mail.Header, msg *Message) error {
	// Extract standard headers
	if subject := header.Get("Subject"); subject != "" {
		msg.Subject = subject
	}

	if dateStr := header.Get("Date"); dateStr != "" {
		if date, err := mail.ParseDate(dateStr); err == nil {
			msg.Date = date
		}
	}

	if messageID := header.Get("Message-ID"); messageID != "" {
		msg.ID = messageID
	}

	// Parse address headers
	if from := header.Get("From"); from != "" {
		if addresses, err := p.parseAddresses(from); err == nil {
			msg.From = addresses
		}
	}

	if to := header.Get("To"); to != "" {
		if addresses, err := p.parseAddresses(to); err == nil {
			msg.To = addresses
		}
	}

	if cc := header.Get("Cc"); cc != "" {
		if addresses, err := p.parseAddresses(cc); err == nil {
			msg.CC = addresses
		}
	}

	if replyTo := header.Get("Reply-To"); replyTo != "" {
		if addresses, err := p.parseAddresses(replyTo); err == nil {
			msg.ReplyTo = addresses
		}
	}

	// Store all headers for reference
	for key, values := range header {
		if len(values) > 0 {
			msg.Headers[key] = values[0]
		}
	}

	return nil
}

// parseAddresses parses email addresses from a header value
func (p *MessageProcessor) parseAddresses(addressStr string) ([]Address, error) {
	addresses, err := mail.ParseAddressList(addressStr)
	if err != nil {
		return nil, err
	}

	var result []Address
	for _, addr := range addresses {
		result = append(result, Address{
			Name:  addr.Name,
			Email: addr.Address,
		})
	}

	return result, nil
}

// parseBodyWithEncoding parses message body with Content-Transfer-Encoding support
func (p *MessageProcessor) parseBodyWithEncoding(body io.Reader, header mail.Header, msg *Message) error {
	// Get content type and transfer encoding
	contentType := header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = "text/plain"
		params = make(map[string]string)
	}

	transferEncoding := strings.ToLower(header.Get("Content-Transfer-Encoding"))

	// Decode content based on transfer encoding
	decodedReader, err := p.getDecodedReader(body, transferEncoding)
	if err != nil {
		return fmt.Errorf("failed to decode content: %w", err)
	}

	switch {
	case strings.HasPrefix(mediaType, "text/"):
		return p.parseTextPartWithDecoding(decodedReader, mediaType, msg)
	case strings.HasPrefix(mediaType, "multipart/"):
		return p.parseMultipart(decodedReader, mediaType, params, msg)
	default:
		// Treat as attachment
		return p.parseAttachmentWithEncoding(decodedReader, mediaType, "", "unknown", msg)
	}
}

// parseTextPartWithDecoding parses a text or HTML part from decoded content
func (p *MessageProcessor) parseTextPartWithDecoding(body io.Reader, mediaType string, msg *Message) error {
	content, err := io.ReadAll(body)
	if err != nil {
		return err
	}

	contentStr := strings.TrimSpace(string(content))

	if mediaType == "text/html" {
		msg.Body.HTML = contentStr
		if p.SanitizeHTML {
			msg.Body.HTML = p.sanitizeHTML(contentStr)
		}
		// Only extract plain text from HTML if we don't already have plain text
		// This preserves the original plain text in multipart/alternative messages
		if msg.Body.Text == "" {
			msg.Body.Text = p.extractTextFromHTML(contentStr)
		}
	} else {
		msg.Body.Text = contentStr
	}

	return nil
}

// parseMultipart parses multipart messages
func (p *MessageProcessor) parseMultipart(body io.Reader, mediaType string, params map[string]string, msg *Message) error {
	boundary := params["boundary"]
	if boundary == "" {
		return fmt.Errorf("multipart message missing boundary")
	}

	// Use standard library multipart reader
	reader := multipart.NewReader(body, boundary)
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read multipart: %w", err)
		}

		// Convert multipart.Part header to mail.Header
		header := make(mail.Header)
		for key, values := range part.Header {
			header[key] = values
		}

		// Check if this part is an attachment by examining Content-Disposition and Content-Type
		contentDisposition := header.Get("Content-Disposition")
		contentType := header.Get("Content-Type")
		isAttachment := p.isAttachmentPart(contentDisposition, contentType)

		if isAttachment {
			// Parse as attachment with encoding support
			transferEncoding := strings.ToLower(header.Get("Content-Transfer-Encoding"))
			filename := p.extractFilenameFromContentDisposition(contentDisposition)
			if filename == "" {
				filename = p.extractFilenameFromContentType(contentType)
			}
			err = p.parseAttachmentWithEncoding(part, contentType, transferEncoding, filename, msg)
		} else {
			// Parse as message content (text/html part)
			err = p.parseBodyWithEncoding(part, header, msg)
		}

		if err != nil {
			return err
		}
	}

	return nil
}

// parseAttachmentWithEncoding parses an attachment with Content-Transfer-Encoding support
func (p *MessageProcessor) parseAttachmentWithEncoding(body io.Reader, contentType, transferEncoding, filename string, msg *Message) error {
	// Decode attachment data based on transfer encoding
	decodedReader, err := p.getDecodedReader(body, transferEncoding)
	if err != nil {
		return fmt.Errorf("failed to decode attachment: %w", err)
	}

	// Read decoded attachment data. The limit is max+1 so exactly-max
	// attachments pass while anything larger is rejected after reading at
	// most one byte beyond the limit - instead of buffering an arbitrarily
	// large attachment in memory before the size check fires.
	var data []byte
	if p.MaxAttachmentSize > 0 {
		data, err = io.ReadAll(io.LimitReader(decodedReader, p.MaxAttachmentSize+1))
	} else {
		data, err = io.ReadAll(decodedReader)
	}
	if err != nil {
		return err
	}

	// Check size limit
	if p.MaxAttachmentSize > 0 && int64(len(data)) > p.MaxAttachmentSize {
		return fmt.Errorf("attachment too large: %d bytes (max: %d)", len(data), p.MaxAttachmentSize)
	}

	// Extract filename from Content-Disposition if not provided
	if filename == "" || filename == "unknown" {
		filename = p.extractFilenameFromContentType(contentType)
	}

	attachment := Attachment{
		Filename:    filename,
		ContentType: contentType,
		Size:        int64(len(data)),
		Data:        data,
	}

	msg.Attachments = append(msg.Attachments, attachment)
	return nil
}

// getDecodedReader returns a reader that decodes content based on transfer encoding
func (p *MessageProcessor) getDecodedReader(body io.Reader, transferEncoding string) (io.Reader, error) {
	switch transferEncoding {
	case "base64":
		// Create a base64 decoder
		return base64.NewDecoder(base64.StdEncoding, body), nil
	case "quoted-printable":
		// Create a quoted-printable decoder
		return quotedprintable.NewReader(body), nil
	case "7bit", "8bit", "binary", "":
		// No encoding or pass-through encodings
		return body, nil
	default:
		// Unknown encoding, treat as binary
		return body, nil
	}
}

// isAttachmentPart determines if a multipart section is an attachment based on Content-Disposition and Content-Type
func (p *MessageProcessor) isAttachmentPart(contentDisposition, contentType string) bool {
	// Parse Content-Disposition header if present
	if contentDisposition != "" {
		mediaType, _, err := mime.ParseMediaType(contentDisposition)
		if err == nil {
			// Explicit "attachment" disposition means it's definitely an attachment
			if mediaType == "attachment" {
				return true
			}
			// Explicit "inline" disposition means it should be displayed inline
			if mediaType == "inline" {
				// However, if it's inline but not a text type, treat as attachment
				// This handles cases like inline images that should be downloadable
				if contentType != "" {
					parsedContentType, _, err := mime.ParseMediaType(contentType)
					if err == nil && !strings.HasPrefix(parsedContentType, "text/") {
						return true
					}
				}
				return false
			}
		}
	}

	// If no Content-Disposition or it's not recognized, use Content-Type to decide
	if contentType != "" {
		parsedContentType, _, err := mime.ParseMediaType(contentType)
		if err == nil {
			// Text content should be displayed as message body
			if strings.HasPrefix(parsedContentType, "text/") {
				return false
			}
			// Multipart content should be parsed recursively, not treated as attachment
			if strings.HasPrefix(parsedContentType, "multipart/") {
				return false
			}
			// Non-text, non-multipart content without explicit inline disposition is likely an attachment
			return true
		}
	}

	// Default: if we can't determine, assume it's message content
	return false
}

// extractFilenameFromContentDisposition extracts filename from Content-Disposition header
func (p *MessageProcessor) extractFilenameFromContentDisposition(contentDisposition string) string {
	if contentDisposition == "" {
		return ""
	}

	// Parse Content-Disposition header
	_, params, err := mime.ParseMediaType(contentDisposition)
	if err != nil {
		return ""
	}

	// Try different filename parameters (RFC 2183 and RFC 2231)
	if filename, ok := params["filename"]; ok {
		return filename
	}
	if filename, ok := params["filename*"]; ok {
		return filename
	}

	return ""
}

// extractFilenameFromContentType attempts to extract filename from content type
func (p *MessageProcessor) extractFilenameFromContentType(contentType string) string {
	// Parse content type for filename parameter
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "attachment"
	}

	if filename, ok := params["name"]; ok {
		return filename
	}

	// Generate filename based on content type
	switch {
	case strings.HasPrefix(contentType, "text/plain"):
		return "attachment.txt"
	case strings.HasPrefix(contentType, "text/html"):
		return "attachment.html"
	case strings.HasPrefix(contentType, "image/jpeg"):
		return "attachment.jpg"
	case strings.HasPrefix(contentType, "image/png"):
		return "attachment.png"
	case strings.HasPrefix(contentType, "application/pdf"):
		return "attachment.pdf"
	default:
		return "attachment"
	}
}

// sanitizeHTML removes potentially dangerous HTML elements and attributes
func (p *MessageProcessor) sanitizeHTML(html string) string {
	// Parse HTML
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		// Never return the original untouched: unparseable input gets the
		// regex-based fallback strip instead.
		return stripDangerousHTMLRegex(html)
	}

	// Remove script tags
	doc.Find("script").Remove()

	// Remove dangerous attributes
	dangerousAttrs := []string{"onclick", "onload", "onerror", "onmouseover", "onfocus", "onblur"}
	for _, attr := range dangerousAttrs {
		doc.Find(fmt.Sprintf("[%s]", attr)).RemoveAttr(attr)
	}

	// Remove javascript: links. The href scheme is case-insensitive and may
	// carry leading whitespace or control characters, so inspect each anchor
	// rather than relying on a case-sensitive prefix selector.
	doc.Find("a[href]").Each(func(i int, s *goquery.Selection) {
		href := strings.TrimSpace(s.AttrOr("href", ""))
		if strings.HasPrefix(strings.ToLower(href), "javascript:") {
			s.RemoveAttr("href")
		}
	})

	// Get sanitized HTML
	sanitized, err := doc.Html()
	if err != nil {
		return stripDangerousHTMLRegex(html)
	}

	return sanitized
}

var (
	scriptBlockRe = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	scriptOpenRe  = regexp.MustCompile(`(?is)<script[^>]*>`)
	eventAttrRe   = regexp.MustCompile(`(?i)\s+on[a-z]+\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
	jsHrefRe      = regexp.MustCompile(`(?i)href\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
)

// stripDangerousHTMLRegex is the fallback sanitizers for input goquery
// cannot parse. It is deliberately conservative: script blocks and event
// handler attributes are removed outright, and any href whose value looks
// like a javascript: URI (case-insensitively) is dropped.
func stripDangerousHTMLRegex(html string) string {
	html = scriptBlockRe.ReplaceAllString(html, "")
	html = scriptOpenRe.ReplaceAllString(html, "")
	html = eventAttrRe.ReplaceAllString(html, "")
	html = jsHrefRe.ReplaceAllStringFunc(html, func(match string) string {
		quote := strings.IndexAny(match, "\"'")
		if quote < 0 {
			return match // unquoted value: jsHrefRe value parsing below handles it
		}
		value := strings.TrimSpace(match[quote+1 : len(match)-1])
		if strings.HasPrefix(strings.ToLower(value), "javascript:") {
			return ""
		}
		return match
	})
	return html
}

// extractTextFromHTML extracts plain text from HTML content
func (p *MessageProcessor) extractTextFromHTML(html string) string {
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
	text = whitespaceRe.ReplaceAllString(text, " ")
	text = strings.TrimSpace(text)

	return text
}

// saveFailedMessage saves a message that failed to parse to a temporary
// file for debugging. It only runs when SaveFailedMessages is enabled:
// failed messages contain the user's private mail, so nothing is written
// to disk by default.
func (p *MessageProcessor) saveFailedMessage(rawMessage []byte, parseError error) {
	if !p.SaveFailedMessages {
		return
	}

	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	filename := fmt.Sprintf("/tmp/failed-message-%s.eml", timestamp)

	err := os.WriteFile(filename, rawMessage, 0600)
	if err != nil {
		fmt.Printf("WARNING: Failed to save problematic message to %s: %v\n", filename, err)
		return
	}

	// Also write an error log file with the parse error
	errorFilename := fmt.Sprintf("/tmp/failed-message-%s.error.txt", timestamp)
	errorContent := fmt.Sprintf("Parse Error: %v\n\nMessage Size: %d bytes\nTimestamp: %s\n",
		parseError, len(rawMessage), time.Now().Format(time.RFC3339))
	err = os.WriteFile(errorFilename, []byte(errorContent), 0600)
	if err != nil {
		fmt.Printf("WARNING: Failed to save error log to %s: %v\n", errorFilename, err)
	}
}
