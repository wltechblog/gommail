package controllers

import (
	"strings"
	"testing"
	"time"

	"github.com/wltechblog/gommail/internal/email"
)

// mockAttachmentManager is a mock implementation of AttachmentManager for testing
type mockAttachmentManager struct {
	data map[string]*email.ViewableAttachment
}

func (m *mockAttachmentManager) GetCachedAttachment(attachmentID string) (*email.ViewableAttachment, error) {
	if data, ok := m.data[attachmentID]; ok {
		return data, nil
	}
	return nil, nil
}

func newMockAttachmentManager() *mockAttachmentManager {
	return &mockAttachmentManager{
		data: make(map[string]*email.ViewableAttachment),
	}
}

func TestNewMessageViewController(t *testing.T) {
	mockMgr := newMockAttachmentManager()
	mvc := NewMessageViewController(mockMgr, true)

	if mvc == nil {
		t.Fatal("NewMessageViewController returned nil")
	}

	if mvc.messageViewer == nil {
		t.Error("messageViewer not initialized")
	}

	if mvc.attachmentSection == nil {
		t.Error("attachmentSection not initialized")
	}

	if mvc.messageContainer == nil {
		t.Error("messageContainer not initialized")
	}

	if !mvc.showHTMLContent {
		t.Error("showHTMLContent should be true when showHTMLByDefault is true")
	}
}

func TestToggleHTMLView(t *testing.T) {
	mockMgr := newMockAttachmentManager()
	mvc := NewMessageViewController(mockMgr, true)

	// Track callback invocations
	callbackCalled := false
	var callbackValue bool

	mvc.SetOnViewToggled(func(showHTML bool) {
		callbackCalled = true
		callbackValue = showHTML
	})

	// Initial state should be true
	if !mvc.IsShowingHTML() {
		t.Error("Initial state should be showing HTML")
	}

	// Toggle to false
	mvc.ToggleHTMLView()

	if mvc.IsShowingHTML() {
		t.Error("After toggle, should not be showing HTML")
	}

	if !callbackCalled {
		t.Error("Callback should have been called")
	}

	if callbackValue {
		t.Error("Callback should have been called with false")
	}

	// Toggle back to true
	callbackCalled = false
	mvc.ToggleHTMLView()

	if !mvc.IsShowingHTML() {
		t.Error("After second toggle, should be showing HTML")
	}

	if !callbackCalled {
		t.Error("Callback should have been called on second toggle")
	}

	if !callbackValue {
		t.Error("Callback should have been called with true")
	}
}

func TestUpdateMessageViewer(t *testing.T) {
	t.Skip("Skipping test that requires Fyne app initialization")
	mockMgr := newMockAttachmentManager()
	mvc := NewMessageViewController(mockMgr, true)

	testContent := "# Test Message\n\nThis is test content."
	mvc.UpdateMessageViewer(testContent)

	// The RichText widget should have been updated
	// We can't easily test the internal state, but we can verify no panic occurred
}

func TestClearMessageView(t *testing.T) {
	t.Skip("Skipping test that requires Fyne app initialization")
	mockMgr := newMockAttachmentManager()
	mvc := NewMessageViewController(mockMgr, true)

	// Add some content first
	mvc.UpdateMessageViewer("# Test Message")

	// Clear the view
	mvc.ClearMessageView()

	// Verify attachment section is empty
	if len(mvc.attachmentSection.Objects) != 0 {
		t.Errorf("Attachment section should be empty after clear, got %d objects", len(mvc.attachmentSection.Objects))
	}
}

func TestHTMLToMarkdown_SimpleHTML(t *testing.T) {
	mockMgr := newMockAttachmentManager()
	mvc := NewMessageViewController(mockMgr, true)

	html := "<p>Hello <strong>world</strong>!</p>"
	result := mvc.HTMLToMarkdown(html)

	if !strings.Contains(result, "Hello") {
		t.Error("Result should contain 'Hello'")
	}

	if !strings.Contains(result, "**world**") {
		t.Error("Result should contain '**world**' (bold markdown)")
	}
}

func TestHTMLToMarkdown_Links(t *testing.T) {
	mockMgr := newMockAttachmentManager()
	mvc := NewMessageViewController(mockMgr, true)

	html := `<p>Visit <a href="https://example.com">our website</a></p>`
	result := mvc.HTMLToMarkdown(html)

	if !strings.Contains(result, "[our website](https://example.com)") {
		t.Errorf("Result should contain markdown link, got: %s", result)
	}
}

func TestHTMLToMarkdown_Headers(t *testing.T) {
	mockMgr := newMockAttachmentManager()
	mvc := NewMessageViewController(mockMgr, true)

	html := `<h1>Title</h1><h2>Subtitle</h2><p>Content</p>`
	result := mvc.HTMLToMarkdown(html)

	if !strings.Contains(result, "# Title") {
		t.Error("Result should contain '# Title'")
	}

	if !strings.Contains(result, "## Subtitle") {
		t.Error("Result should contain '## Subtitle'")
	}
}

func TestHTMLToMarkdown_Lists(t *testing.T) {
	mockMgr := newMockAttachmentManager()
	mvc := NewMessageViewController(mockMgr, true)

	html := `<ul><li>Item 1</li><li>Item 2</li></ul>`
	result := mvc.HTMLToMarkdown(html)

	if !strings.Contains(result, "- Item 1") {
		t.Error("Result should contain '- Item 1'")
	}

	if !strings.Contains(result, "- Item 2") {
		t.Error("Result should contain '- Item 2'")
	}
}

func TestHTMLToMarkdown_RemovesStyleContent(t *testing.T) {
	mockMgr := newMockAttachmentManager()
	mvc := NewMessageViewController(mockMgr, true)

	html := `<style>.x { color: red; }</style><p>Message</p>`
	result := mvc.HTMLToMarkdown(html)

	if strings.Contains(result, ".x") {
		t.Error("Result should not include CSS content")
	}

	if !strings.Contains(result, "Message") {
		t.Error("Result should retain visible text")
	}
}

func TestHTMLToMarkdown_InlineImagePlaceholder(t *testing.T) {
	mockMgr := newMockAttachmentManager()
	mvc := NewMessageViewController(mockMgr, true)

	html := `<p>Hello<img src="cid:logo" alt="Logo"></p>`
	result := mvc.HTMLToMarkdown(html)

	if !strings.Contains(result, "Inline image: Logo") {
		t.Errorf("Result should describe inline image, got %s", result)
	}
}

func TestHTMLToPlainText(t *testing.T) {
	mockMgr := newMockAttachmentManager()
	mvc := NewMessageViewController(mockMgr, true)

	html := `<p>Hello <strong>world</strong>!</p><br><div>Test content</div>`
	result := mvc.HTMLToPlainText(html)

	// Should remove all HTML tags
	if strings.Contains(result, "<") || strings.Contains(result, ">") {
		t.Error("Result should not contain HTML tags")
	}

	if !strings.Contains(result, "Hello") {
		t.Error("Result should contain 'Hello'")
	}

	if !strings.Contains(result, "world") {
		t.Error("Result should contain 'world'")
	}
}

func TestDecodeHTMLEntities(t *testing.T) {
	mockMgr := newMockAttachmentManager()
	mvc := NewMessageViewController(mockMgr, true)

	tests := []struct {
		input    string
		expected string
	}{
		{"Hello&nbsp;World", "Hello World"},
		{"&lt;tag&gt;", "<tag>"},
		{"&amp;", "&"},
		{"&quot;test&quot;", "\"test\""},
		{"&#39;test&#39;", "'test'"},
		{"&mdash;", "—"},
		{"&ndash;", "–"},
		{"&hellip;", "…"},
	}

	for _, tt := range tests {
		result := mvc.decodeHTMLEntities(tt.input)
		if result != tt.expected {
			t.Errorf("decodeHTMLEntities(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestFormatTextForMarkdown(t *testing.T) {
	mockMgr := newMockAttachmentManager()
	mvc := NewMessageViewController(mockMgr, true)

	// Test single line breaks become double
	input := "Line 1\nLine 2\nLine 3"
	result := mvc.FormatTextForMarkdown(input)

	// Should have double line breaks between lines
	if !strings.Contains(result, "Line 1\n\nLine 2") {
		t.Error("Should have double line breaks between lines")
	}
}

func TestDisplayMessage_HTMLContent(t *testing.T) {
	t.Skip("Skipping test that requires Fyne app initialization")
	mockMgr := newMockAttachmentManager()
	mvc := NewMessageViewController(mockMgr, true) // HTML view enabled

	msg := &email.Message{
		Subject: "Test Subject",
		From: []email.Address{
			{Name: "Sender", Email: "sender@example.com"},
		},
		To: []email.Address{
			{Name: "Recipient", Email: "recipient@example.com"},
		},
		Date: time.Now(),
		Body: email.MessageBody{
			Text: "Plain text content",
			HTML: "<p>HTML <strong>content</strong></p>",
		},
	}

	formatAddresses := func(addrs []email.Address) string {
		if len(addrs) == 0 {
			return ""
		}
		return addrs[0].Name + " <" + addrs[0].Email + ">"
	}

	getDisplayDate := func(m *email.Message) string {
		return m.Date.Format("January 2, 2006 at 3:04 PM")
	}

	mvc.DisplayMessage(msg, formatAddresses, getDisplayDate)

	// Verify the message was displayed (no panic)
	// We can't easily verify the exact content without accessing internal state
}

func TestDisplayMessage_PlainTextOnly(t *testing.T) {
	t.Skip("Skipping test that requires Fyne app initialization")
	mockMgr := newMockAttachmentManager()
	mvc := NewMessageViewController(mockMgr, false) // Plain text view

	msg := &email.Message{
		Subject: "Test Subject",
		From: []email.Address{
			{Name: "Sender", Email: "sender@example.com"},
		},
		To: []email.Address{
			{Name: "Recipient", Email: "recipient@example.com"},
		},
		Date: time.Now(),
		Body: email.MessageBody{
			Text: "Plain text content",
			HTML: "<p>HTML content</p>",
		},
	}

	formatAddresses := func(addrs []email.Address) string {
		if len(addrs) == 0 {
			return ""
		}
		return addrs[0].Email
	}

	getDisplayDate := func(m *email.Message) string {
		return m.Date.Format("2006-01-02")
	}

	mvc.DisplayMessage(msg, formatAddresses, getDisplayDate)

	// Verify the message was displayed (no panic)
}

func TestGetters(t *testing.T) {
	mockMgr := newMockAttachmentManager()
	mvc := NewMessageViewController(mockMgr, true)

	if mvc.GetMessageViewer() == nil {
		t.Error("GetMessageViewer should not return nil")
	}

	if mvc.GetAttachmentSection() == nil {
		t.Error("GetAttachmentSection should not return nil")
	}

	if mvc.GetMessageContainer() == nil {
		t.Error("GetMessageContainer should not return nil")
	}
}
