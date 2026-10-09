package imap

// Code in this file was mechanically split from worker.go.

import (
	"bytes"
	"fmt"
	"io"
	"net/mail"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/wltechblog/gommail/internal/email"
)

// convertFetchDataToMessage converts IMAP fetch data to email.Message format (exact copy of working client.go code)
func (w *IMAPWorker) convertFetchDataToMessage(msg *imapclient.FetchMessageData) email.Message {
	emailMsg := email.Message{
		ID: fmt.Sprintf("%d", msg.SeqNum), // Use sequence number as ID for now
	}

	// Process all fetch item data
	for {
		item := msg.Next()
		if item == nil {
			break
		}

		switch data := item.(type) {
		case imapclient.FetchItemDataEnvelope:
			if data.Envelope != nil {
				w.populateFromEnvelope(&emailMsg, data.Envelope)
			}
		case imapclient.FetchItemDataFlags:
			emailMsg.Flags = w.convertFlags(data.Flags)
		case imapclient.FetchItemDataUID:
			emailMsg.UID = uint32(data.UID)
			emailMsg.ID = fmt.Sprintf("%d", data.UID) // Use UID as ID instead of sequence number
		case imapclient.FetchItemDataRFC822Size:
			emailMsg.Size = int64(data.Size)
		case imapclient.FetchItemDataInternalDate:
			emailMsg.InternalDate = data.Time // Set the actual arrival date from server
		case imapclient.FetchItemDataBodySection:
			// Handle body section data
			if data.Literal != nil {
				bodyBytes, err := io.ReadAll(data.Literal)
				if err != nil {
					continue
				}

				// Check if this is a header-only section
				if data.Section != nil && data.Section.Specifier == imap.PartSpecifierHeader {
					// This is just the headers section - parse headers directly without body parsing
					// Don't use ParseRawMessage() as it will fail on multipart messages without body
					reader := bytes.NewReader(bodyBytes)
					msg, err := mail.ReadMessage(reader)
					if err != nil {
						w.logger.Warn("Failed to read header section (UID %d, size %d bytes): %v", emailMsg.UID, len(bodyBytes), err)
					} else {
						// Extract headers directly
						if emailMsg.Headers == nil {
							emailMsg.Headers = make(map[string]string)
						}
						for key := range msg.Header {
							emailMsg.Headers[key] = msg.Header.Get(key)
						}
						w.logger.Debug("Parsed %d headers from header section", len(emailMsg.Headers))
					}
				} else {
					// This is the full message body - parse everything
					processor := email.NewMessageProcessor()
					parsedMsg, err := processor.ParseRawMessage(bodyBytes)
					if err != nil {
						w.logger.Warn("Failed to parse message body (UID %d, size %d bytes): %v", emailMsg.UID, len(bodyBytes), err)
						w.logger.Warn("Raw message saved to /tmp/failed-message-*.eml for analysis")
						emailMsg.Body = email.MessageBody{
							Text: fmt.Sprintf("Error parsing message body: %v", err),
						}
					} else {
						emailMsg.Body = parsedMsg.Body
						emailMsg.Attachments = parsedMsg.Attachments
						// Also merge headers from the full message if we don't have them yet
						if emailMsg.Headers == nil {
							emailMsg.Headers = make(map[string]string)
						}
						for key, value := range parsedMsg.Headers {
							emailMsg.Headers[key] = value
						}
					}
				}
			}
		}
	}
	return emailMsg
}

// populateFromEnvelope populates message fields from IMAP envelope
func (w *IMAPWorker) populateFromEnvelope(msg *email.Message, env *imap.Envelope) {
	msg.Subject = env.Subject
	msg.Date = env.Date
	msg.From = w.convertAddresses(env.From)
	msg.To = w.convertAddresses(env.To)
	msg.CC = w.convertAddresses(env.Cc)
	msg.BCC = w.convertAddresses(env.Bcc)
	msg.ReplyTo = w.convertAddresses(env.ReplyTo)

	// Use Message-ID if available
	if env.MessageID != "" {
		msg.ID = env.MessageID
	}
}

// convertAddresses converts IMAP addresses to email.Address format
func (w *IMAPWorker) convertAddresses(imapAddrs []imap.Address) []email.Address {
	if len(imapAddrs) == 0 {
		return nil
	}

	addrs := make([]email.Address, len(imapAddrs))
	for i, addr := range imapAddrs {
		addrs[i] = email.Address{
			Name:  addr.Name,
			Email: addr.Mailbox + "@" + addr.Host,
		}
	}
	return addrs
}

// convertFlags converts IMAP flags to string slice
func (w *IMAPWorker) convertFlags(imapFlags []imap.Flag) []string {
	if len(imapFlags) == 0 {
		return nil
	}

	flags := make([]string, len(imapFlags))
	for i, flag := range imapFlags {
		flags[i] = string(flag)
	}
	return flags
}

// convertAttributes converts IMAP folder attributes to string slice
func (w *IMAPWorker) convertAttributes(attrs []imap.MailboxAttr) []string {
	var result []string
	for _, attr := range attrs {
		result = append(result, string(attr))
	}
	return result
}
