package smtp

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/smtp"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"

	"github.com/wltechblog/gommail/internal/email"
	"github.com/wltechblog/gommail/internal/logging"
)

// QueuedMessage represents a message in the send queue
type QueuedMessage struct {
	Message   *email.Message
	Attempts  int
	NextRetry time.Time
	Error     error
}

// AuthMethod represents different authentication methods
type AuthMethod int

const (
	AuthPlain AuthMethod = iota
	AuthLogin
)

// Client implements the SMTPClient interface with enhanced features
type Client struct {
	config     email.ServerConfig
	authMethod AuthMethod
	queue      []*QueuedMessage
	queueMutex sync.RWMutex
	maxRetries int
	retryDelay time.Duration

	logger *logging.Logger
}

// NewClient creates a new SMTP client with enhanced features
func NewClient(config email.ServerConfig) *Client {
	return &Client{
		config:     config,
		authMethod: AuthPlain, // Default to PLAIN auth
		queue:      make([]*QueuedMessage, 0),
		maxRetries: 3,
		retryDelay: time.Minute * 5,
		logger:     logging.NewComponent("smtp"),
	}
}

// NewClientWithAuth creates a new SMTP client with specific authentication method
func NewClientWithAuth(config email.ServerConfig, authMethod AuthMethod) *Client {
	client := NewClient(config)
	client.authMethod = authMethod
	client.logger.Debug("Created SMTP client with auth method: %d", authMethod)
	return client
}

// Connect establishes a connection to the SMTP server
func (c *Client) Connect() error {
	// For SMTP, we don't maintain persistent connections
	// Connection is established per send operation
	return nil
}

// Disconnect closes the connection to the SMTP server
func (c *Client) Disconnect() error {
	// No persistent connection to close
	return nil
}

// SendMessage sends an email message via SMTP
func (c *Client) SendMessage(msg *email.Message) error {
	c.logger.Info("Sending email message: %s", msg.Subject)
	c.logger.Debug("Message ID: %s", msg.ID)

	// Build the email message
	c.logger.Debug("Building email message")
	emailContent, err := c.buildMessage(msg)
	if err != nil {
		c.logger.Error("Failed to build message: %v", err)
		return fmt.Errorf("failed to build message: %w", err)
	}
	c.logger.Debug("Email message built successfully (%d bytes)", len(emailContent))

	// Prepare recipients
	var recipients []string
	for _, addr := range msg.To {
		recipients = append(recipients, addr.Email)
	}
	for _, addr := range msg.CC {
		recipients = append(recipients, addr.Email)
	}
	for _, addr := range msg.BCC {
		recipients = append(recipients, addr.Email)
	}

	if len(recipients) == 0 {
		c.logger.Error("No recipients specified for message: %s", msg.Subject)
		return fmt.Errorf("no recipients specified")
	}

	c.logger.Debug("Recipients: %v", recipients)

	// Determine sender
	var sender string
	if len(msg.From) > 0 {
		sender = msg.From[0].Email
	} else {
		sender = c.config.Username
	}
	c.logger.Debug("Sender: %s", sender)

	// Send the message
	addr := fmt.Sprintf("%s:%d", c.config.Host, c.config.Port)
	c.logger.Debug("Connecting to SMTP server: %s (TLS: %v)", addr, c.config.TLS)

	var auth smtp.Auth
	if c.config.Username != "" && c.config.Password != "" {
		auth = c.createAuth()
		c.logger.Debug("Using authentication for user: %s", c.config.Username)
	} else {
		c.logger.Debug("No authentication configured")
	}

	var sendErr error
	if c.config.TLS {
		// Use TLS connection
		c.logger.Debug("Using direct TLS connection")
		sendErr = c.sendWithTLS(addr, auth, sender, recipients, emailContent)
	} else {
		// Use plain connection with STARTTLS
		c.logger.Debug("Using STARTTLS connection")
		sendErr = smtp.SendMail(addr, auth, sender, recipients, emailContent)
	}

	if sendErr != nil {
		c.logger.Error("Failed to send message '%s': %v", msg.Subject, sendErr)
		return sendErr
	}

	c.logger.Info("Successfully sent email message: %s", msg.Subject)
	return nil
}

// sendWithTLS sends email using direct TLS connection
func (c *Client) sendWithTLS(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	// Connect with TLS
	conn, err := tls.Dial("tcp", addr, &tls.Config{
		ServerName: c.config.Host,
	})
	if err != nil {
		return fmt.Errorf("failed to connect with TLS: %w", err)
	}
	defer conn.Close()

	// Create SMTP client
	client, err := smtp.NewClient(conn, c.config.Host)
	if err != nil {
		return fmt.Errorf("failed to create SMTP client: %w", err)
	}
	defer client.Quit()

	// Authenticate if credentials provided
	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("authentication failed: %w", err)
		}
	}

	// Set sender
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("failed to set sender: %w", err)
	}

	// Set recipients
	for _, recipient := range to {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("failed to set recipient %s: %w", recipient, err)
		}
	}

	// Send message data
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("failed to get data writer: %w", err)
	}

	_, err = writer.Write(msg)
	if err != nil {
		return fmt.Errorf("failed to write message data: %w", err)
	}

	return writer.Close()
}

// buildMessage constructs the email message using go-message library for proper MIME handling
func (c *Client) buildMessage(msg *email.Message) ([]byte, error) {
	var buf bytes.Buffer

	// Create message header
	var h message.Header
	h.Set("Date", msg.Date.Format(time.RFC1123Z))
	h.Set("Subject", msg.Subject)

	// Set addresses
	if len(msg.From) > 0 {
		h.Set("From", c.formatAddresses(msg.From))
	}
	if len(msg.To) > 0 {
		h.Set("To", c.formatAddresses(msg.To))
	}
	if len(msg.CC) > 0 {
		h.Set("Cc", c.formatAddresses(msg.CC))
	}
	if len(msg.ReplyTo) > 0 {
		h.Set("Reply-To", c.formatAddresses(msg.ReplyTo))
	}

	// Add custom headers if present
	for key, value := range msg.Headers {
		h.Set(key, value)
	}

	// Set content type based on message structure
	if len(msg.Attachments) > 0 {
		h.Set("Content-Type", "multipart/mixed")
	} else if msg.Body.HTML != "" && msg.Body.Text != "" {
		h.Set("Content-Type", "multipart/alternative")
	} else if msg.Body.HTML != "" {
		h.Set("Content-Type", "text/html; charset=utf-8")
	} else {
		h.Set("Content-Type", "text/plain; charset=utf-8")
	}

	// Create message writer
	mw, err := message.CreateWriter(&buf, h)
	if err != nil {
		return nil, fmt.Errorf("failed to create message writer: %w", err)
	}

	// Handle attachments
	if len(msg.Attachments) > 0 {
		// Create multipart/mixed for attachments
		err = c.writeMultipartWithAttachments(mw, msg)
	} else if msg.Body.HTML != "" && msg.Body.Text != "" {
		// Create multipart/alternative for HTML + text
		err = c.writeMultipartAlternative(mw, msg)
	} else {
		// Single part message
		err = c.writeSinglePart(mw, msg)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to write message content: %w", err)
	}

	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("failed to close message writer: %w", err)
	}

	return buf.Bytes(), nil
}

// formatAddresses formats email addresses for headers
func (c *Client) formatAddresses(addresses []email.Address) string {
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

// generateBoundary generates a boundary string for multipart messages
func (c *Client) generateBoundary() string {
	return fmt.Sprintf("boundary_%d", time.Now().UnixNano())
}

// convertToMailAddresses converts our email.Address to mail.Address
func (c *Client) convertToMailAddresses(addresses []email.Address) []*mail.Address {
	var result []*mail.Address
	for _, addr := range addresses {
		mailAddr := &mail.Address{
			Name:    addr.Name,
			Address: addr.Email,
		}
		result = append(result, mailAddr)
	}
	return result
}

// writeMultipartWithAttachments writes a multipart/mixed message with attachments
func (c *Client) writeMultipartWithAttachments(mw *message.Writer, msg *email.Message) error {
	// The message writer should already be set up for multipart/mixed
	// Write message body part first
	if msg.Body.HTML != "" && msg.Body.Text != "" {
		// Create multipart/alternative for the body
		var bodyHeader message.Header
		bodyHeader.Set("Content-Type", "multipart/alternative")
		bodyPart, err := mw.CreatePart(bodyHeader)
		if err != nil {
			return err
		}

		err = c.writeMultipartAlternative(bodyPart, msg)
		bodyPart.Close()
		if err != nil {
			return err
		}
	} else {
		// Single body part
		var bodyHeader message.Header
		var content string

		if msg.Body.HTML != "" {
			bodyHeader.Set("Content-Type", "text/html; charset=utf-8")
			content = msg.Body.HTML
		} else {
			bodyHeader.Set("Content-Type", "text/plain; charset=utf-8")
			content = msg.Body.Text
		}

		bodyPart, err := mw.CreatePart(bodyHeader)
		if err != nil {
			return err
		}

		_, err = io.WriteString(bodyPart, content)
		bodyPart.Close()
		if err != nil {
			return err
		}
	}

	// Write attachments
	for _, attachment := range msg.Attachments {
		err := c.writeAttachment(mw, attachment)
		if err != nil {
			return err
		}
	}

	return nil
}

// writeMultipartAlternative writes a multipart/alternative message (text + HTML)
func (c *Client) writeMultipartAlternative(mw *message.Writer, msg *email.Message) error {
	var h message.Header
	h.Set("Content-Type", "multipart/alternative")
	mpw, err := mw.CreatePart(h)
	if err != nil {
		return err
	}
	defer mpw.Close()

	// Write text part
	if msg.Body.Text != "" {
		var textHeader message.Header
		textHeader.Set("Content-Type", "text/plain; charset=utf-8")
		textPart, err := mpw.CreatePart(textHeader)
		if err != nil {
			return err
		}
		_, err = io.WriteString(textPart, msg.Body.Text)
		textPart.Close()
		if err != nil {
			return err
		}
	}

	// Write HTML part
	if msg.Body.HTML != "" {
		var htmlHeader message.Header
		htmlHeader.Set("Content-Type", "text/html; charset=utf-8")
		htmlPart, err := mpw.CreatePart(htmlHeader)
		if err != nil {
			return err
		}
		_, err = io.WriteString(htmlPart, msg.Body.HTML)
		htmlPart.Close()
		if err != nil {
			return err
		}
	}

	return nil
}

// writeSinglePart writes a single part message (text or HTML)
func (c *Client) writeSinglePart(mw *message.Writer, msg *email.Message) error {
	var content string

	if msg.Body.HTML != "" {
		content = msg.Body.HTML
	} else {
		content = msg.Body.Text
	}

	// For single part messages, write directly to the message writer
	_, err := io.WriteString(mw, content)
	return err
}

// writeAttachment writes an attachment to the message
func (c *Client) writeAttachment(mw *message.Writer, attachment email.Attachment) error {
	var h message.Header

	// Set content type
	contentType := attachment.ContentType
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(attachment.Filename))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
	}

	h.Set("Content-Type", contentType)
	h.Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", attachment.Filename))
	h.Set("Content-Transfer-Encoding", "base64")

	part, err := mw.CreatePart(h)
	if err != nil {
		return err
	}
	defer part.Close()

	// Encode attachment data as base64
	encoder := base64.NewEncoder(base64.StdEncoding, part)
	defer encoder.Close()

	_, err = encoder.Write(attachment.Data)
	return err
}

// createAuth creates the appropriate authentication mechanism based on the configured method
func (c *Client) createAuth() smtp.Auth {
	switch c.authMethod {
	case AuthLogin:
		return c.createLoginAuth()
	case AuthPlain:
		fallthrough
	default:
		return smtp.PlainAuth("", c.config.Username, c.config.Password, c.config.Host)
	}
}

// createLoginAuth creates LOGIN authentication (similar to PLAIN but different protocol)
func (c *Client) createLoginAuth() smtp.Auth {
	// LOGIN auth is not directly supported by net/smtp, but we can implement it
	// For now, fall back to PLAIN auth as it's widely supported
	return smtp.PlainAuth("", c.config.Username, c.config.Password, c.config.Host)
}

// QueueMessage adds a message to the send queue for later delivery
func (c *Client) QueueMessage(msg *email.Message) error {
	c.queueMutex.Lock()
	defer c.queueMutex.Unlock()

	c.logger.Debug("Queuing message for later delivery: %s", msg.Subject)

	queuedMsg := &QueuedMessage{
		Message:   msg,
		Attempts:  0,
		NextRetry: time.Now(),
	}

	c.queue = append(c.queue, queuedMsg)
	c.logger.Info("Message queued successfully: %s (queue size: %d)", msg.Subject, len(c.queue))
	return nil
}

// ProcessQueue attempts to send all queued messages
func (c *Client) ProcessQueue() error {
	c.queueMutex.Lock()
	defer c.queueMutex.Unlock()

	if len(c.queue) == 0 {
		c.logger.Debug("Queue is empty, nothing to process")
		return nil
	}

	c.logger.Info("Processing queue with %d messages", len(c.queue))

	var remainingQueue []*QueuedMessage
	var lastError error
	processed := 0
	sent := 0
	failed := 0
	dropped := 0

	for _, queuedMsg := range c.queue {
		if time.Now().Before(queuedMsg.NextRetry) {
			c.logger.Debug("Message '%s' not ready for retry (next retry: %v)",
				queuedMsg.Message.Subject, queuedMsg.NextRetry)
			remainingQueue = append(remainingQueue, queuedMsg)
			continue
		}

		processed++
		c.logger.Debug("Processing queued message '%s' (attempt %d/%d)",
			queuedMsg.Message.Subject, queuedMsg.Attempts+1, c.maxRetries)

		err := c.SendMessage(queuedMsg.Message)
		if err != nil {
			queuedMsg.Attempts++
			queuedMsg.Error = err
			lastError = err
			failed++

			if queuedMsg.Attempts < c.maxRetries {
				// Schedule retry with exponential backoff
				backoff := c.retryDelay * time.Duration(queuedMsg.Attempts)
				queuedMsg.NextRetry = time.Now().Add(backoff)
				remainingQueue = append(remainingQueue, queuedMsg)
				c.logger.Warn("Message '%s' failed (attempt %d/%d), scheduling retry in %v: %v",
					queuedMsg.Message.Subject, queuedMsg.Attempts, c.maxRetries, backoff, err)
			} else {
				dropped++
				c.logger.Error("Message '%s' dropped after %d failed attempts: %v",
					queuedMsg.Message.Subject, queuedMsg.Attempts, err)
			}
		} else {
			sent++
			c.logger.Debug("Successfully sent queued message: %s", queuedMsg.Message.Subject)
		}
		// If successful, message is not added back to queue
	}

	c.queue = remainingQueue
	c.logger.Info("Queue processing complete: processed=%d, sent=%d, failed=%d, dropped=%d, remaining=%d",
		processed, sent, failed, dropped, len(remainingQueue))
	return lastError
}

// GetQueueStatus returns information about the current queue
func (c *Client) GetQueueStatus() (total int, pending int, failed int) {
	c.queueMutex.RLock()
	defer c.queueMutex.RUnlock()

	total = len(c.queue)
	for _, msg := range c.queue {
		if msg.Error != nil {
			failed++
		} else {
			pending++
		}
	}
	return
}

// ClearQueue removes all messages from the queue
func (c *Client) ClearQueue() {
	c.queueMutex.Lock()
	defer c.queueMutex.Unlock()
	c.queue = c.queue[:0]
}

// SendMessageWithDSN sends a message with delivery status notification options
func (c *Client) SendMessageWithDSN(msg *email.Message, dsnOptions string) error {
	// Add DSN headers to the message
	if msg.Headers == nil {
		msg.Headers = make(map[string]string)
	}

	// Add Return-Receipt-To header for read receipts
	if len(msg.From) > 0 {
		msg.Headers["Return-Receipt-To"] = msg.From[0].Email
	}

	// Add DSN options if specified
	if dsnOptions != "" {
		msg.Headers["Delivery-Status-Notification"] = dsnOptions
	}

	return c.SendMessage(msg)
}

// SendMessageAsync sends a message asynchronously by adding it to the queue
func (c *Client) SendMessageAsync(msg *email.Message) error {
	return c.QueueMessage(msg)
}

// SetRetryPolicy configures the retry policy for queued messages
func (c *Client) SetRetryPolicy(maxRetries int, retryDelay time.Duration) {
	c.queueMutex.Lock()
	defer c.queueMutex.Unlock()
	c.maxRetries = maxRetries
	c.retryDelay = retryDelay
}

// ValidateMessage performs basic validation on a message before sending
func (c *Client) ValidateMessage(msg *email.Message) error {
	if msg == nil {
		return fmt.Errorf("message is nil")
	}

	if msg.Subject == "" {
		return fmt.Errorf("message subject is empty")
	}

	if len(msg.To) == 0 && len(msg.CC) == 0 && len(msg.BCC) == 0 {
		return fmt.Errorf("no recipients specified")
	}

	if len(msg.From) == 0 {
		return fmt.Errorf("no sender specified")
	}

	if msg.Body.Text == "" && msg.Body.HTML == "" {
		return fmt.Errorf("message body is empty")
	}

	// Validate email addresses
	for _, addr := range msg.From {
		if !c.isValidEmail(addr.Email) {
			return fmt.Errorf("invalid sender email: %s", addr.Email)
		}
	}

	for _, addr := range msg.To {
		if !c.isValidEmail(addr.Email) {
			return fmt.Errorf("invalid recipient email: %s", addr.Email)
		}
	}

	return nil
}

// isValidEmail performs basic email validation
func (c *Client) isValidEmail(email string) bool {
	// Basic validation - contains @ and has parts before and after
	parts := strings.Split(email, "@")
	return len(parts) == 2 && len(parts[0]) > 0 && len(parts[1]) > 0 && strings.Contains(parts[1], ".")
}
