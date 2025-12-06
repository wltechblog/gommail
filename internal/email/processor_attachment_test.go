package email

import (
	"strings"
	"testing"
)

func TestIsAttachmentPart(t *testing.T) {
	processor := NewMessageProcessor()

	tests := []struct {
		name               string
		contentDisposition string
		contentType        string
		expected           bool
		description        string
	}{
		{
			name:               "explicit_attachment",
			contentDisposition: "attachment; filename=\"document.pdf\"",
			contentType:        "application/pdf",
			expected:           true,
			description:        "Explicit attachment disposition should be treated as attachment",
		},
		{
			name:               "inline_text",
			contentDisposition: "inline",
			contentType:        "text/plain",
			expected:           false,
			description:        "Inline text should be treated as message content",
		},
		{
			name:               "inline_html",
			contentDisposition: "inline",
			contentType:        "text/html",
			expected:           false,
			description:        "Inline HTML should be treated as message content",
		},
		{
			name:               "inline_image",
			contentDisposition: "inline",
			contentType:        "image/jpeg",
			expected:           true,
			description:        "Inline non-text content should be treated as attachment",
		},
		{
			name:               "no_disposition_text",
			contentDisposition: "",
			contentType:        "text/plain",
			expected:           false,
			description:        "Text without disposition should be treated as message content",
		},
		{
			name:               "no_disposition_binary",
			contentDisposition: "",
			contentType:        "application/octet-stream",
			expected:           true,
			description:        "Binary content without disposition should be treated as attachment",
		},
		{
			name:               "no_headers",
			contentDisposition: "",
			contentType:        "",
			expected:           false,
			description:        "No headers should default to message content",
		},
		{
			name:               "malformed_disposition",
			contentDisposition: "invalid-disposition-header",
			contentType:        "text/plain",
			expected:           false,
			description:        "Malformed disposition with text should default to message content",
		},
		{
			name:               "multipart_alternative",
			contentDisposition: "",
			contentType:        "multipart/alternative; boundary=\"boundary123\"",
			expected:           false,
			description:        "Multipart content should be parsed recursively, not treated as attachment",
		},
		{
			name:               "multipart_related",
			contentDisposition: "",
			contentType:        "multipart/related; boundary=\"boundary456\"",
			expected:           false,
			description:        "Multipart/related should be parsed recursively, not treated as attachment",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := processor.isAttachmentPart(tt.contentDisposition, tt.contentType)
			if result != tt.expected {
				t.Errorf("isAttachmentPart(%q, %q) = %v, expected %v - %s",
					tt.contentDisposition, tt.contentType, result, tt.expected, tt.description)
			}
		})
	}
}

func TestParseMultipartMessage_InlineContent(t *testing.T) {
	processor := NewMessageProcessor()

	// Simulate a multipart message where content was incorrectly treated as attachment
	// This is similar to the issue shown in the screenshot
	rawMessage := `From: Josh Grebe <josh@example.com>
To: user@example.com
Subject: photos
Date: September 17, 2025 at 1:04 PM
Content-Type: multipart/mixed; boundary="boundary123"

--boundary123
Content-Type: text/plain; charset=US-ASCII
Content-Disposition: inline

Another photo!

--boundary123
Content-Type: image/jpeg
Content-Disposition: attachment; filename="image.jpg"
Content-Transfer-Encoding: base64

/9j/4AAQSkZJRgABAQEAYABgAAD/2wBDAAEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEB
--boundary123--
`

	msg, err := processor.ParseRawMessage([]byte(rawMessage))
	if err != nil {
		t.Fatalf("Failed to parse message: %v", err)
	}

	// The text content should be in the message body, not treated as attachment
	if msg.Body.Text == "" {
		t.Error("Expected message body text to be populated, but it was empty")
	}

	if !strings.Contains(msg.Body.Text, "Another photo!") {
		t.Errorf("Expected message body to contain 'Another photo!', got: %q", msg.Body.Text)
	}

	// There should be exactly one attachment (the image)
	if len(msg.Attachments) != 1 {
		t.Errorf("Expected 1 attachment, got %d", len(msg.Attachments))
	}

	if len(msg.Attachments) > 0 {
		attachment := msg.Attachments[0]
		if attachment.Filename != "image.jpg" {
			t.Errorf("Expected attachment filename 'image.jpg', got %q", attachment.Filename)
		}
		if attachment.ContentType != "image/jpeg" {
			t.Errorf("Expected attachment content type 'image/jpeg', got %q", attachment.ContentType)
		}
	}
}
