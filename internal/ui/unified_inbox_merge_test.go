package ui

import (
	"testing"

	"github.com/wltechblog/gommail/internal/email"
)

func item(account string, uid uint32, id string, subject string) email.MessageIndexItem {
	return email.MessageIndexItem{
		AccountName: account,
		FolderName:  "INBOX",
		Message: email.Message{
			UID:     uid,
			ID:      id,
			Subject: subject,
		},
	}
}

// TestMergeIntoUnifiedInbox_UIDCollisionWithStaleEntry reproduces the reported
// breakage: a new message arrives with UID 1 in an account's INBOX, but the
// unified inbox list contains a stale cached entry for the same account with
// the reused UID 1 (different message). The new message must be kept —
// replacing the stale entry — instead of being silently dropped as a
// "duplicate".
func TestMergeIntoUnifiedInbox_UIDCollisionWithStaleEntry(t *testing.T) {
	existing := []email.MessageIndexItem{
		item("other", 42, "<other-42@x>", "other account msg"),
		item("Josh at WLTechBlog", 1, "<stale-old-1@x>", "stale cached msg"),
		item("Josh at WLTechBlog", 7, "<josh-7@x>", "kept msg"),
	}

	incoming := []email.MessageIndexItem{
		item("Josh at WLTechBlog", 1, "<brand-new-1@x>", "NEW message"),
	}

	merged, stats := mergeIntoUnifiedInbox(existing, incoming)

	if stats.Duplicate != 0 {
		t.Errorf("new message wrongly classified as duplicate: %+v", stats)
	}
	if stats.Replaced != 1 || stats.Added != 0 {
		t.Errorf("expected 1 replacement, got %+v", stats)
	}
	if len(merged) != 3 {
		t.Fatalf("expected list length to stay 3, got %d", len(merged))
	}

	// The stale entry at index 1 must now be the new message.
	if merged[1].Message.Subject != "NEW message" || merged[1].Message.ID != "<brand-new-1@x>" {
		t.Errorf("stale entry not replaced: %+v", merged[1].Message)
	}
	// Neighbors untouched.
	if merged[0].Message.Subject != "other account msg" || merged[2].Message.Subject != "kept msg" {
		t.Errorf("neighboring entries were disturbed: %v / %v", merged[0].Message.Subject, merged[2].Message.Subject)
	}
}

// TestMergeIntoUnifiedInbox_TrueDuplicateByID verifies a genuinely duplicate
// message (same Message-ID) is skipped even under a different UID — e.g. when
// the background full-refresh won a race with the IDLE notification.
func TestMergeIntoUnifiedInbox_TrueDuplicateByID(t *testing.T) {
	existing := []email.MessageIndexItem{
		item("Josh at WLTechBlog", 1, "<same-1@x>", "msg"),
	}
	incoming := []email.MessageIndexItem{
		item("Josh at WLTechBlog", 1, "<same-1@x>", "msg"), // same UID, same ID
		item("Josh at WLTechBlog", 2, "<same-1@x>", "msg"), // different UID, same ID
	}

	merged, stats := mergeIntoUnifiedInbox(existing, incoming)

	if stats.Duplicate != 2 || stats.Added != 0 || stats.Replaced != 0 {
		t.Errorf("expected both to be duplicates, got %+v", stats)
	}
	if len(merged) != 1 {
		t.Errorf("expected list unchanged, got %d entries", len(merged))
	}
}

// TestMergeIntoUnifiedInbox_PlainAdd verifies ordinary appends and
// account-scoped UID isolation.
func TestMergeIntoUnifiedInbox_PlainAdd(t *testing.T) {
	existing := []email.MessageIndexItem{
		item("acct A", 1, "<a-1@x>", "a1"),
	}

	incoming := []email.MessageIndexItem{
		item("acct A", 2, "<a-2@x>", "a2"),   // new UID for A: add
		item("acct B", 1, "<b-1@x>", "b1"),   // UID 1 again but different account: add
		item("acct A", 1, "<a-1@x>", "a1du"), // exact duplicate: skip
	}

	merged, stats := mergeIntoUnifiedInbox(existing, incoming)

	if stats.Added != 2 || stats.Duplicate != 1 {
		t.Errorf("expected 2 added 1 duplicate, got %+v", stats)
	}
	if len(merged) != 3 {
		t.Errorf("expected 3 entries, got %d", len(merged))
	}
}

// TestMergeIntoUnifiedInbox_FallbackIDHeuristic verifies messages without a
// Message-ID (numeric-UID fallback ID) still dedup correctly on (account, UID)
// and are treated as duplicates only when identities are indistinguishable.
func TestMergeIntoUnifiedInbox_FallbackIDHeuristic(t *testing.T) {
	existing := []email.MessageIndexItem{
		item("acct", 5, "5", "no message-id"),
	}

	// Same UID, fallback ID: assume duplicate (nothing distinguishes them).
	merged, stats := mergeIntoUnifiedInbox(existing, []email.MessageIndexItem{item("acct", 5, "5", "no message-id")})
	if stats.Duplicate != 1 || len(merged) != 1 {
		t.Errorf("fallback-ID duplicate not detected: %+v, len=%d", stats, len(merged))
	}

	// Same UID but the incoming message HAS a Message-ID: the old entry is
	// stale (server mailbox without message-ids cached earlier) — replace.
	merged, stats = mergeIntoUnifiedInbox(existing, []email.MessageIndexItem{item("acct", 5, "<real-5@x>", "identified")})
	if stats.Replaced != 1 || len(merged) != 1 {
		t.Errorf("expected stale fallback entry to be replaced: %+v, len=%d", stats, len(merged))
	}
	if merged[0].Message.ID != "<real-5@x>" {
		t.Errorf("replacement did not take effect: %+v", merged[0].Message)
	}
}

// TestMergeIntoUnifiedInbox_DoesNotMutateExistingOnDuplicates guards the
// no-change path: when everything is a duplicate the original slice must be
// returned untouched (callers rely on stability for change detection).
func TestMergeIntoUnifiedInbox_DoesNotMutateExistingOnDuplicates(t *testing.T) {
	existing := []email.MessageIndexItem{
		item("acct", 1, "<a-1@x>", "a1"),
	}

	merged, stats := mergeIntoUnifiedInbox(existing, []email.MessageIndexItem{item("acct", 1, "<a-1@x>", "a1")})
	if stats.Added != 0 || stats.Replaced != 0 || stats.Duplicate != 1 {
		t.Errorf("unexpected stats: %+v", stats)
	}
	if len(merged) != 1 || &merged[0] != &existing[0] {
		t.Errorf("expected original slice returned unchanged")
	}
}
