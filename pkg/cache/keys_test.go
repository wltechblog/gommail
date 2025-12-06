package cache

import (
	"reflect"
	"testing"
)

func TestGenerateKey(t *testing.T) {
	tests := []struct {
		name       string
		components []string
		want       string
	}{
		{
			name:       "empty components",
			components: []string{},
			want:       "",
		},
		{
			name:       "single component",
			components: []string{"test"},
			want:       "test",
		},
		{
			name:       "two components",
			components: []string{"account1", "messages"},
			want:       "account1:messages",
		},
		{
			name:       "three components",
			components: []string{"account1", "messages", "INBOX"},
			want:       "account1:messages:INBOX",
		},
		{
			name:       "multiple components",
			components: []string{"a", "b", "c", "d", "e"},
			want:       "a:b:c:d:e",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateKey(tt.components...)
			if got != tt.want {
				t.Errorf("GenerateKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGenerateAccountKey(t *testing.T) {
	tests := []struct {
		name       string
		accountKey string
		keyType    string
		identifier string
		want       string
	}{
		{
			name:       "messages key",
			accountKey: "user@example.com",
			keyType:    "messages",
			identifier: "INBOX",
			want:       "user@example.com:messages:INBOX",
		},
		{
			name:       "folders key",
			accountKey: "test@test.com",
			keyType:    "folders",
			identifier: "list",
			want:       "test@test.com:folders:list",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateAccountKey(tt.accountKey, tt.keyType, tt.identifier)
			if got != tt.want {
				t.Errorf("GenerateAccountKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGenerateMessagesKey(t *testing.T) {
	tests := []struct {
		name       string
		accountKey string
		folderName string
		want       string
	}{
		{
			name:       "inbox messages",
			accountKey: "user@example.com",
			folderName: "INBOX",
			want:       "user@example.com:messages:INBOX",
		},
		{
			name:       "sent messages",
			accountKey: "test@test.com",
			folderName: "Sent",
			want:       "test@test.com:messages:Sent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateMessagesKey(tt.accountKey, tt.folderName)
			if got != tt.want {
				t.Errorf("GenerateMessagesKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGenerateFoldersKey(t *testing.T) {
	tests := []struct {
		name       string
		accountKey string
		want       string
	}{
		{
			name:       "folders list",
			accountKey: "user@example.com",
			want:       "user@example.com:folders:list",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateFoldersKey(tt.accountKey)
			if got != tt.want {
				t.Errorf("GenerateFoldersKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGenerateSubscribedFoldersKey(t *testing.T) {
	tests := []struct {
		name       string
		accountKey string
		want       string
	}{
		{
			name:       "subscribed folders",
			accountKey: "user@example.com",
			want:       "user@example.com:folders_subscribed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateSubscribedFoldersKey(tt.accountKey)
			if got != tt.want {
				t.Errorf("GenerateSubscribedFoldersKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGenerateTrackingStateKey(t *testing.T) {
	tests := []struct {
		name       string
		accountKey string
		identifier string
		want       string
	}{
		{
			name:       "all folders tracking",
			accountKey: "user@example.com",
			identifier: "all_folders",
			want:       "user@example.com:tracking_state:all_folders",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateTrackingStateKey(tt.accountKey, tt.identifier)
			if got != tt.want {
				t.Errorf("GenerateTrackingStateKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGenerateAttachmentKey(t *testing.T) {
	tests := []struct {
		name         string
		attachmentID string
		want         string
	}{
		{
			name:         "attachment data",
			attachmentID: "abc123",
			want:         "attachment:abc123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateAttachmentKey(tt.attachmentID)
			if got != tt.want {
				t.Errorf("GenerateAttachmentKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGenerateAttachmentMetaKey(t *testing.T) {
	tests := []struct {
		name         string
		attachmentID string
		want         string
	}{
		{
			name:         "attachment metadata",
			attachmentID: "abc123",
			want:         "attachment_meta:abc123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateAttachmentMetaKey(tt.attachmentID)
			if got != tt.want {
				t.Errorf("GenerateAttachmentMetaKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGenerateAddressbookKey(t *testing.T) {
	tests := []struct {
		name       string
		accountKey string
		want       string
	}{
		{
			name:       "addressbook data",
			accountKey: "user@example.com",
			want:       "addressbook:user@example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateAddressbookKey(tt.accountKey)
			if got != tt.want {
				t.Errorf("GenerateAddressbookKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want []string
	}{
		{
			name: "empty key",
			key:  "",
			want: nil,
		},
		{
			name: "single component",
			key:  "test",
			want: []string{"test"},
		},
		{
			name: "two components",
			key:  "account:messages",
			want: []string{"account", "messages"},
		},
		{
			name: "three components",
			key:  "account:messages:INBOX",
			want: []string{"account", "messages", "INBOX"},
		},
		{
			name: "multiple components",
			key:  "a:b:c:d:e",
			want: []string{"a", "b", "c", "d", "e"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseKey(tt.key)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseKey() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Benchmark tests
func BenchmarkGenerateKey(b *testing.B) {
	for i := 0; i < b.N; i++ {
		GenerateKey("account", "messages", "INBOX")
	}
}

func BenchmarkGenerateMessagesKey(b *testing.B) {
	for i := 0; i < b.N; i++ {
		GenerateMessagesKey("user@example.com", "INBOX")
	}
}

func BenchmarkParseKey(b *testing.B) {
	key := "user@example.com:messages:INBOX"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ParseKey(key)
	}
}

