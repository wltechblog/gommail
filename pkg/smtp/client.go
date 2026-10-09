package smtp

import (
	"bytes"
	crand "crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net"
	"net/smtp"
	"path/filepath"
	"strings"
	"time"

	"github.com/emersion/go-message"

	"github.com/wltechblog/gommail/internal/email"
	"github.com/wltechblog/gommail/internal/logging"
)

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

	logger *logging.Logger
}

// NewClient creates a new SMTP client with enhanced features
func NewClient(config email.ServerConfig) *Client {
	return &Client{
		config:     config,
		authMethod: AuthPlain, // Default to PLAIN auth
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

// SMTP operation timeouts. Without them a stalled server hangs the sending
// goroutine (and any UI waiting on it) indefinitely.
const (
	smtpDialTimeout = 30 * time.Second
	smtpDataTimeout = 5 * time.Minute
)

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
		sendErr = c.sendWithSTARTTLS(addr, auth, sender, recipients, emailContent)
	}

	if sendErr != nil {
		c.logger.Error("Failed to send message '%s': %v", msg.Subject, sendErr)
		return sendErr
	}

	c.logger.Info("Successfully sent email message: %s", msg.Subject)
	return nil
}

// dial connects to the SMTP server with a dial timeout, returning a plain
// TCP connection ready for TLS handshake or STARTTLS negotiation.
func (c *Client) dial(addr string) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", addr, smtpDialTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", addr, err)
	}
	if err := conn.SetDeadline(time.Now().Add(smtpDataTimeout)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to set deadline: %w", err)
	}
	return conn, nil
}

// sendWithTLS sends email using a direct TLS connection (port 465)
func (c *Client) sendWithTLS(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	rawConn, err := c.dial(addr)
	if err != nil {
		return err
	}
	conn := tls.Client(rawConn, &tls.Config{ServerName: c.config.Host})
	if err := conn.Handshake(); err != nil {
		rawConn.Close()
		return fmt.Errorf("TLS handshake failed: %w", err)
	}
	defer conn.Close()
	return c.sendOnConn(conn, auth, from, to, msg)
}

// sendWithSTARTTLS sends email over a plain connection upgraded with STARTTLS.
// It replaces net/smtp.SendMail, which performs every network operation
// without any timeout.
func (c *Client) sendWithSTARTTLS(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	conn, err := c.dial(addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, c.config.Host)
	if err != nil {
		return fmt.Errorf("failed to create SMTP client: %w", err)
	}
	defer client.Quit()

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: c.config.Host}); err != nil {
			return fmt.Errorf("STARTTLS failed: %w", err)
		}
	}
	return c.sendOnConn(conn, auth, from, to, msg)
}

// sendOnConn performs the authenticated MAIL/RCPT/DATA exchange on an
// established connection.
func (c *Client) sendOnConn(conn net.Conn, auth smtp.Auth, from string, to []string, msg []byte) error {
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

// BuildMessage builds the MIME content of a message. It returns the same
// content that SendMessage delivers, e.g. for saving a copy to a Sent folder.
func (c *Client) BuildMessage(msg *email.Message) ([]byte, error) {
	return c.buildMessage(msg)
}

// buildMessage constructs the email message using go-message library for proper MIME handling
func (c *Client) buildMessage(msg *email.Message) ([]byte, error) {
	var buf bytes.Buffer

	// Create message header
	var h message.Header
	if msg.Date.IsZero() {
		// A zero Date formats as "Mon, 01 Jan 0001" which some servers and
		// clients reject; stamp the send time instead.
		h.Set("Date", time.Now().Format(time.RFC1123Z))
	} else {
		h.Set("Date", msg.Date.Format(time.RFC1123Z))
	}
	h.Set("Subject", msg.Subject)
	if msg.Headers["Message-ID"] == "" {
		h.Set("Message-ID", c.generateMessageID(msg))
	}

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

// generateMessageID creates a globally unique message ID of the form
// <timestamp-random@domain>. Uniqueness (not unpredictability) is the goal.
func (c *Client) generateMessageID(msg *email.Message) string {
	var randPart [8]byte
	if _, err := crand.Read(randPart[:]); err != nil {
		// Fall back to a counter-free combination; still unique per send.
		binary.BigEndian.PutUint64(randPart[:], uint64(time.Now().UnixNano()))
	}
	domain := "localhost"
	if len(msg.From) > 0 {
		if at := strings.LastIndex(msg.From[0].Email, "@"); at >= 0 {
			domain = msg.From[0].Email[at+1:]
		}
	} else if at := strings.LastIndex(c.config.Username, "@"); at >= 0 {
		domain = c.config.Username[at+1:]
	}
	return fmt.Sprintf("<%d-%s@%s>", time.Now().UnixNano(), hex.EncodeToString(randPart[:]), domain)
}

// writeMultipartWithAttachments writes a multipart/mixed message: the body
// first (nested in multipart/alternative when both plain text and HTML are
// present, as RFC 2046 requires), followed by one part per attachment.
func (c *Client) writeMultipartWithAttachments(mw *message.Writer, msg *email.Message) error {
	if msg.Body.HTML != "" && msg.Body.Text != "" {
		if err := c.writeMultipartAlternative(mw, msg); err != nil {
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

// writeAttachment writes an attachment to the message. go-message performs
// the base64 transfer encoding itself based on the Content-Transfer-Encoding
// header (wrapping lines at 76 characters); the data must be written raw.
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

	_, err = part.Write(attachment.Data)
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
