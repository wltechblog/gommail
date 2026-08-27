package ui

import (
	"strings"

	"github.com/wltechblog/gommail/internal/email"
)

// mergeStats reports the outcome of a unified-inbox merge.
type mergeStats struct {
	Added     int // messages appended (no prior entry for the account/UID)
	Replaced  int // stale entries whose UID was reused by a different message
	Duplicate int // incoming messages that were already present (true duplicates)
}

// hasMessageID reports whether a Message.ID is a real RFC 5322 Message-ID
// (angle-bracketed, from the IMAP envelope) rather than the numeric-UID
// fallback assigned when a message has no Message-ID.
func hasMessageID(id string) bool {
	return strings.HasPrefix(id, "<") && strings.HasSuffix(id, ">")
}

// mergeIntoUnifiedInbox merges incoming messages into the existing unified
// inbox list and returns the updated list plus outcome statistics.
//
// Duplicate detection is identity-aware:
//
//   - A message is a TRUE duplicate when the same Message-ID already exists
//     for the account, or when the (account, UID) slot is occupied by an entry
//     with the same identity. Such messages are skipped.
//   - When the (account, UID) slot is occupied by an entry with a DIFFERENT
//     identity, the UID has been reused (mailbox re-created, stale cache from
//     a re-added account). The stale entry is replaced in place by the new
//     message. Without this, every new message whose UID collides with a
//     stale entry would be silently dropped from the message list.
//
// existing is never mutated; a new slice is returned when changes occur.
func mergeIntoUnifiedInbox(existing []email.MessageIndexItem, incoming []email.MessageIndexItem) ([]email.MessageIndexItem, mergeStats) {
	var stats mergeStats
	if len(incoming) == 0 {
		return existing, stats
	}

	// (account, uid) -> index into the working list
	uidIndex := make(map[string]map[uint32]int)
	// (account, message-id) -> true for identity-based duplicate detection
	idSet := make(map[string]map[string]bool)

	for i, msg := range existing {
		key := msg.AccountName
		if uidIndex[key] == nil {
			uidIndex[key] = make(map[uint32]int)
		}
		uidIndex[key][msg.Message.UID] = i
		if hasMessageID(msg.Message.ID) {
			if idSet[key] == nil {
				idSet[key] = make(map[string]bool)
			}
			idSet[key][msg.Message.ID] = true
		}
	}

	result := existing
	changed := false

	for _, newMsg := range incoming {
		key := newMsg.AccountName
		newID := newMsg.Message.ID
		newHasID := hasMessageID(newID)

		// Same Message-ID already present for this account: true duplicate
		// (e.g. the background full-refresh won a race with the IDLE
		// notification and already added this very message).
		if newHasID && idSet[key] != nil && idSet[key][newID] {
			stats.Duplicate++
			continue
		}

		if idx, occupied := uidIndex[key][newMsg.Message.UID]; occupied {
			oldID := result[idx].Message.ID
			if oldID == newID || (!hasMessageID(oldID) && !newHasID) {
				// Same identity (or neither side has a Message-ID): the UID
				// match is trustworthy.
				stats.Duplicate++
				continue
			}

			// UID reused by a different message: replace the stale entry.
			if !hasMessageID(oldID) {
				// Drop the old fallback ID from the identity set if present.
				delete(idSet[key], oldID)
			}
			result[idx] = newMsg
			changed = true
			stats.Replaced++
			if newHasID {
				if idSet[key] == nil {
					idSet[key] = make(map[string]bool)
				}
				idSet[key][newID] = true
			}
			continue
		}

		// No prior entry: append.
		result = append(result, newMsg)
		changed = true
		stats.Added++
		if uidIndex[key] == nil {
			uidIndex[key] = make(map[uint32]int)
		}
		uidIndex[key][newMsg.Message.UID] = len(result) - 1
		if newHasID {
			if idSet[key] == nil {
				idSet[key] = make(map[string]bool)
			}
			idSet[key][newID] = true
		}
	}

	if !changed {
		// Keep the original slice (and its capacity) when nothing changed.
		return existing, stats
	}
	return result, stats
}
