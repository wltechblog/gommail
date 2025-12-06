package email

import (
	"strings"
	"testing"
)

func TestNewMessageProcessor(t *testing.T) {
	processor := NewMessageProcessor()

	if processor == nil {
		t.Fatal("NewMessageProcessor returned nil")
	}

	if processor.MaxAttachmentSize != 10*1024*1024 {
		t.Errorf("Expected MaxAttachmentSize to be 10MB, got %d", processor.MaxAttachmentSize)
	}

	if !processor.SanitizeHTML {
		t.Error("Expected SanitizeHTML to be true by default")
	}
}

func TestParseRawMessage_Simple(t *testing.T) {
	processor := NewMessageProcessor()

	rawMessage := `From: sender@example.com
To: recipient@example.com
Subject: Test Message
Date: Mon, 02 Jan 2006 15:04:05 -0700
Message-ID: <test@example.com>

This is a simple test message.
`

	msg, err := processor.ParseRawMessage([]byte(rawMessage))
	if err != nil {
		t.Fatalf("ParseRawMessage failed: %v", err)
	}

	if msg.Subject != "Test Message" {
		t.Errorf("Expected subject 'Test Message', got '%s'", msg.Subject)
	}

	if len(msg.From) != 1 || msg.From[0].Email != "sender@example.com" {
		t.Errorf("Expected from 'sender@example.com', got %v", msg.From)
	}

	if len(msg.To) != 1 || msg.To[0].Email != "recipient@example.com" {
		t.Errorf("Expected to 'recipient@example.com', got %v", msg.To)
	}

	if msg.ID != "<test@example.com>" {
		t.Errorf("Expected ID '<test@example.com>', got '%s'", msg.ID)
	}

	if msg.Body.Text != "This is a simple test message." {
		t.Errorf("Expected body text 'This is a simple test message.', got '%s'", msg.Body.Text)
	}
}

func TestParseAddresses(t *testing.T) {
	processor := NewMessageProcessor()

	tests := []struct {
		input    string
		expected []Address
	}{
		{
			input: "john@example.com",
			expected: []Address{
				{Email: "john@example.com"},
			},
		},
		{
			input: "John Doe <john@example.com>",
			expected: []Address{
				{Name: "John Doe", Email: "john@example.com"},
			},
		},
		{
			input: "john@example.com, Jane Smith <jane@example.com>",
			expected: []Address{
				{Email: "john@example.com"},
				{Name: "Jane Smith", Email: "jane@example.com"},
			},
		},
	}

	for _, test := range tests {
		addresses, err := processor.parseAddresses(test.input)
		if err != nil {
			t.Errorf("parseAddresses failed for '%s': %v", test.input, err)
			continue
		}

		if len(addresses) != len(test.expected) {
			t.Errorf("Expected %d addresses, got %d for input '%s'", len(test.expected), len(addresses), test.input)
			continue
		}

		for i, addr := range addresses {
			if addr.Name != test.expected[i].Name || addr.Email != test.expected[i].Email {
				t.Errorf("Expected address %v, got %v for input '%s'", test.expected[i], addr, test.input)
			}
		}
	}
}

func TestSanitizeHTML(t *testing.T) {
	processor := NewMessageProcessor()

	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "<p>Hello World</p>",
			expected: "<html><head></head><body><p>Hello World</p></body></html>",
		},
		{
			input:    "<p onclick=\"alert('xss')\">Hello</p>",
			expected: "<html><head></head><body><p>Hello</p></body></html>",
		},
		{
			input:    "<script>alert('xss')</script><p>Hello</p>",
			expected: "<html><head></head><body><p>Hello</p></body></html>",
		},
		{
			input:    "<a href=\"javascript:alert('xss')\">Click me</a>",
			expected: "<html><head></head><body><a>Click me</a></body></html>",
		},
	}

	for _, test := range tests {
		result := processor.sanitizeHTML(test.input)
		if result != test.expected {
			t.Errorf("sanitizeHTML failed for '%s'\nExpected: %s\nGot: %s", test.input, test.expected, result)
		}
	}
}

func TestExtractTextFromHTML(t *testing.T) {
	processor := NewMessageProcessor()

	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "<p>Hello World</p>",
			expected: "Hello World",
		},
		{
			input:    "<div><p>Hello</p><p>World</p></div>",
			expected: "Hello World",
		},
		{
			input:    "<p>Hello   \n\n   World</p>",
			expected: "Hello World",
		},
		{
			input:    "<p>Line 1</p><br><p>Line 2</p>",
			expected: "Line 1 Line 2",
		},
	}

	for _, test := range tests {
		result := processor.extractTextFromHTML(test.input)
		if result != test.expected {
			t.Errorf("extractTextFromHTML failed for '%s'\nExpected: '%s'\nGot: '%s'", test.input, test.expected, result)
		}
	}
}

func TestParseRawMessage_WithAttachment(t *testing.T) {
	processor := NewMessageProcessor()

	// Create a multipart message with text content and an attachment
	rawMessage := `From: sender@example.com
To: recipient@example.com
Subject: Test Message with Attachment
Date: Mon, 02 Jan 2006 15:04:05 -0700
Message-ID: <test-attachment@example.com>
Content-Type: multipart/mixed; boundary="boundary123"

--boundary123
Content-Type: text/plain; charset=utf-8

This is the actual message content that should be displayed.
It should not be mixed with attachment data.

--boundary123
Content-Type: text/plain; charset=utf-8
Content-Disposition: attachment; filename="test.txt"

This is attachment content that should NOT appear in the message body.
It should be parsed as an attachment instead.

--boundary123--
`

	msg, err := processor.ParseRawMessage([]byte(rawMessage))
	if err != nil {
		t.Fatalf("ParseRawMessage failed: %v", err)
	}

	// Verify basic message properties
	if msg.Subject != "Test Message with Attachment" {
		t.Errorf("Expected subject 'Test Message with Attachment', got '%s'", msg.Subject)
	}

	if len(msg.From) != 1 || msg.From[0].Email != "sender@example.com" {
		t.Errorf("Expected from 'sender@example.com', got %v", msg.From)
	}

	// Verify message body contains only the actual message content, not attachment data
	expectedBodyText := "This is the actual message content that should be displayed.\nIt should not be mixed with attachment data."
	if msg.Body.Text != expectedBodyText {
		t.Errorf("Expected body text:\n'%s'\nGot:\n'%s'", expectedBodyText, msg.Body.Text)
	}

	// Verify that attachment data is NOT in the message body
	if strings.Contains(msg.Body.Text, "This is attachment content that should NOT appear") {
		t.Error("Message body incorrectly contains attachment content")
	}

	// Verify attachment was parsed correctly
	if len(msg.Attachments) != 1 {
		t.Fatalf("Expected 1 attachment, got %d", len(msg.Attachments))
	}

	attachment := msg.Attachments[0]
	if attachment.Filename != "test.txt" {
		t.Errorf("Expected attachment filename 'test.txt', got '%s'", attachment.Filename)
	}

	if attachment.ContentType != "text/plain; charset=utf-8" {
		t.Errorf("Expected attachment content type 'text/plain; charset=utf-8', got '%s'", attachment.ContentType)
	}

	expectedAttachmentContent := "This is attachment content that should NOT appear in the message body.\nIt should be parsed as an attachment instead."
	actualAttachmentContent := strings.TrimSpace(string(attachment.Data))
	if actualAttachmentContent != expectedAttachmentContent {
		t.Errorf("Expected attachment content:\n'%s'\nGot:\n'%s'", expectedAttachmentContent, actualAttachmentContent)
	}
}

func TestParseRawMessage_WithBase64EncodedContent(t *testing.T) {
	processor := NewMessageProcessor()

	// Create a multipart message with base64 encoded text content and attachment
	// This tests the Content-Transfer-Encoding handling
	rawMessage := `From: sender@example.com
To: recipient@example.com
Subject: Test Message with Base64 Content
Date: Mon, 02 Jan 2006 15:04:05 -0700
Message-ID: <test-base64@example.com>
Content-Type: multipart/mixed; boundary="base64boundary"

--base64boundary
Content-Type: text/plain; charset=utf-8
Content-Transfer-Encoding: base64

VGhpcyBpcyB0aGUgYWN0dWFsIG1lc3NhZ2UgY29udGVudCB0aGF0IHNob3VsZCBiZSBkZWNvZGVk
IGZyb20gYmFzZTY0IGVuY29kaW5nLg==

--base64boundary
Content-Type: text/plain; charset=utf-8
Content-Disposition: attachment; filename="encoded.txt"
Content-Transfer-Encoding: base64

VGhpcyBpcyBhdHRhY2htZW50IGNvbnRlbnQgdGhhdCBzaG91bGQgYWxzbyBiZSBkZWNvZGVkLg==

--base64boundary--
`

	msg, err := processor.ParseRawMessage([]byte(rawMessage))
	if err != nil {
		t.Fatalf("ParseRawMessage failed: %v", err)
	}

	// Verify basic message properties
	if msg.Subject != "Test Message with Base64 Content" {
		t.Errorf("Expected subject 'Test Message with Base64 Content', got '%s'", msg.Subject)
	}

	// Verify message body is properly decoded from base64
	expectedBodyText := "This is the actual message content that should be decoded from base64 encoding."
	if msg.Body.Text != expectedBodyText {
		t.Errorf("Expected decoded body text:\n'%s'\nGot:\n'%s'", expectedBodyText, msg.Body.Text)
	}

	// Verify that base64 encoded data is NOT in the message body
	if strings.Contains(msg.Body.Text, "VGhpcyBpcyB0aGUgYWN0dWFsIG1lc3NhZ2U=") {
		t.Error("Message body incorrectly contains base64 encoded content")
	}

	// Verify attachment was parsed and decoded correctly
	if len(msg.Attachments) != 1 {
		t.Fatalf("Expected 1 attachment, got %d", len(msg.Attachments))
	}

	attachment := msg.Attachments[0]
	if attachment.Filename != "encoded.txt" {
		t.Errorf("Expected attachment filename 'encoded.txt', got '%s'", attachment.Filename)
	}

	expectedAttachmentContent := "This is attachment content that should also be decoded."
	actualAttachmentContent := strings.TrimSpace(string(attachment.Data))
	if actualAttachmentContent != expectedAttachmentContent {
		t.Errorf("Expected decoded attachment content:\n'%s'\nGot:\n'%s'", expectedAttachmentContent, actualAttachmentContent)
	}

	// Verify attachment data is not base64 encoded
	if strings.Contains(string(attachment.Data), "VGhpcyBpcyBhdHRhY2htZW50IGNvbnRlbnQ=") {
		t.Error("Attachment data incorrectly contains base64 encoded content")
	}
}

func TestParseRawMessage_WithQuotedPrintableHTML(t *testing.T) {
	processor := NewMessageProcessor()

	// Create a message with quoted-printable encoded HTML content
	rawMessage := `From: sender@example.com
To: recipient@example.com
Subject: Test HTML Message
Content-Type: text/html; charset=utf-8
Content-Transfer-Encoding: quoted-printable

<html><body>=09=09=09<p>Sign In to Highlights</p>=09=09=09<p>Click the button below to sign in.</p></body></html>
`

	msg, err := processor.ParseRawMessage([]byte(rawMessage))
	if err != nil {
		t.Fatalf("ParseRawMessage failed: %v", err)
	}

	// Verify basic message properties
	if msg.Subject != "Test HTML Message" {
		t.Errorf("Expected subject 'Test HTML Message', got '%s'", msg.Subject)
	}

	// Verify HTML content is properly decoded (should not contain =09)
	if strings.Contains(msg.Body.HTML, "=09") {
		t.Errorf("HTML content still contains quoted-printable encoding: %s", msg.Body.HTML)
	}

	// Verify HTML content is properly decoded (should contain tabs)
	if !strings.Contains(msg.Body.HTML, "\t\t\t<p>Sign In to Highlights</p>") {
		t.Errorf("HTML content does not contain properly decoded tabs: %s", msg.Body.HTML)
	}

	// Verify HTML content contains the expected text
	if !strings.Contains(msg.Body.HTML, "Sign In to Highlights") {
		t.Errorf("HTML content does not contain expected text: %s", msg.Body.HTML)
	}
}

func TestParseRawMessage_WithQuotedPrintableMultipart(t *testing.T) {
	processor := NewMessageProcessor()

	// Create a multipart message with quoted-printable encoded HTML content
	rawMessage := `From: sender@example.com
To: recipient@example.com
Subject: Test Multipart Message
Content-Type: multipart/alternative; boundary="boundary123"

--boundary123
Content-Type: text/plain; charset=utf-8
Content-Transfer-Encoding: quoted-printable

This is the plain text version.

--boundary123
Content-Type: text/html; charset=utf-8
Content-Transfer-Encoding: quoted-printable

<html><body>=09=09=09<p>Sign In to Highlights</p>=09=09=09<p>Click the button below to sign in.</p></body></html>

--boundary123--
`

	msg, err := processor.ParseRawMessage([]byte(rawMessage))
	if err != nil {
		t.Fatalf("ParseRawMessage failed: %v", err)
	}

	// Verify basic message properties
	if msg.Subject != "Test Multipart Message" {
		t.Errorf("Expected subject 'Test Multipart Message', got '%s'", msg.Subject)
	}

	// Verify HTML content is properly decoded (should not contain =09)
	if strings.Contains(msg.Body.HTML, "=09") {
		t.Errorf("HTML content still contains quoted-printable encoding: %s", msg.Body.HTML)
	}

	// Verify HTML content is properly decoded (should contain tabs)
	if !strings.Contains(msg.Body.HTML, "\t\t\t<p>Sign In to Highlights</p>") {
		t.Errorf("HTML content does not contain properly decoded tabs: %s", msg.Body.HTML)
	}

	// Verify plain text content is also properly decoded
	if !strings.Contains(msg.Body.Text, "This is the plain text version.") {
		t.Errorf("Plain text content not found: %s", msg.Body.Text)
	}
}

func TestParseRawMessage_WithNestedMultipart(t *testing.T) {
	processor := NewMessageProcessor()

	// Create a nested multipart message (multipart/related containing multipart/alternative)
	// This mimics the structure from the user's example
	rawMessage := `From: sender@example.com
To: recipient@example.com
Subject: Ticket Assigned to you
MIME-Version: 1.0
Content-Type: multipart/related; boundary="=_outer_boundary"

This is a message in Mime Format.  If you see this, your mail reader does not support this format.

--=_outer_boundary
Content-Type: multipart/alternative; boundary="=_inner_boundary"

This is a message in Mime Format.  If you see this, your mail reader does not support this format.

--=_inner_boundary
Content-Type: text/plain; charset=utf-8
Content-Transfer-Encoding: base64

SGkgSm9zaCwgVGlja2V0ICM3NTUyMjAgaGFzIGJlZW4gYXNzaWduZWQgdG8geW91IGJ5IE1pa2Ug
SC4KCkZyb206ICBTdGV2ZSBIb2x0Z3Jld2UgPHN0ZXZlQGh1bWNvbWFyaW5lLmNvbT4gICAgIFN1
YmplY3Q6ICAgZW1haWwKClRvIHZpZXcvcmVzcG9uZCB0byB0aGUgdGlja2V0LCBwbGVhc2UgbG9n
aW4gdG8gdGhlIHN1cHBvcnQgdGlja2V0IHN5c3RlbQpZb3VyIGZyaWVuZGx5IEN1c3RvbWVyIFN1
cHBvcnQgU3lzdGVt

--=_inner_boundary
Content-Type: text/html; charset=utf-8
Content-Transfer-Encoding: base64

PGh0bWw+PGJvZHk+PHA+SGkgSm9zaCwgVGlja2V0ICM3NTUyMjAgaGFzIGJlZW4gYXNzaWduZWQg
dG8geW91IGJ5IE1pa2UgSC48L3A+PC9ib2R5PjwvaHRtbD4=

--=_inner_boundary--

--=_outer_boundary
Content-Type: image/png; name="powered-by-osticket.png"
Content-Transfer-Encoding: base64
Content-Disposition: inline; filename="powered-by-osticket.png"

iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9
awAAAABJRU5ErkJggg==

--=_outer_boundary--
`

	msg, err := processor.ParseRawMessage([]byte(rawMessage))
	if err != nil {
		t.Fatalf("ParseRawMessage failed: %v", err)
	}

	// Verify basic message properties
	if msg.Subject != "Ticket Assigned to you" {
		t.Errorf("Expected subject 'Ticket Assigned to you', got '%s'", msg.Subject)
	}

	// Verify plain text content is properly decoded from base64
	if !strings.Contains(msg.Body.Text, "Hi Josh, Ticket #755220 has been assigned to you by Mike H.") {
		t.Errorf("Plain text content not found or not properly decoded. Got: %s", msg.Body.Text)
	}

	// Verify HTML content is properly decoded from base64
	if !strings.Contains(msg.Body.HTML, "<p>Hi Josh, Ticket #755220 has been assigned to you by Mike H.</p>") {
		t.Errorf("HTML content not found or not properly decoded. Got: %s", msg.Body.HTML)
	}

	// Verify we have one attachment (the image)
	if len(msg.Attachments) != 1 {
		t.Errorf("Expected 1 attachment, got %d", len(msg.Attachments))
	}

	// Verify the attachment is the image
	if len(msg.Attachments) > 0 {
		if msg.Attachments[0].Filename != "powered-by-osticket.png" {
			t.Errorf("Expected attachment filename 'powered-by-osticket.png', got '%s'", msg.Attachments[0].Filename)
		}
		if !strings.HasPrefix(msg.Attachments[0].ContentType, "image/png") {
			t.Errorf("Expected attachment content type 'image/png', got '%s'", msg.Attachments[0].ContentType)
		}
	}
}

func TestParseRawMessage_WithLinksInHTML(t *testing.T) {
	processor := NewMessageProcessor()

	// Create a message with HTML content containing links
	rawMessage := `From: sender@example.com
To: recipient@example.com
Subject: Test Message with Links
Content-Type: text/html; charset=utf-8
Content-Transfer-Encoding: quoted-printable

<html><body>
<p>PRE ORDER NOW.</p>
<p><a href=3D"https://tbrdy4.fm72.fdske.com/e/c/01k5cpe2dv22wfqq4pjkbfrg7p/01k5cpe2dv22wfqq4pjnqm9mep">Link 1</a></p>
<p><a href=3D"https://tbrdy4.fm72.fdske.com/e/c/01k5cpe2dv22wfqq4pjkbfrg7p/01k5cpe2dv22wfqq4pjsm7m6c0">Link 2</a></p>
</body></html>
`

	msg, err := processor.ParseRawMessage([]byte(rawMessage))
	if err != nil {
		t.Fatalf("ParseRawMessage failed: %v", err)
	}

	// Print the HTML content to see what we got
	t.Logf("HTML content: %s", msg.Body.HTML)

	// Verify HTML content is properly decoded (should not contain =3D)
	if strings.Contains(msg.Body.HTML, "=3D") {
		t.Errorf("HTML content still contains quoted-printable encoding: %s", msg.Body.HTML)
	}

	// Verify HTML content contains proper links
	if !strings.Contains(msg.Body.HTML, `href="https://`) {
		t.Errorf("HTML content does not contain properly decoded links: %s", msg.Body.HTML)
	}
}
