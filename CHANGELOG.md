# Changelog

## 2025-12-06
### Changed
- Upgraded `fyne.io/fyne/v2` to `v2.7.1` to pick up performance fixes, improved GridWrap navigation, image border radius rendering, and faster dialogs.
- Refreshed indirect dependencies pulled in by the Fyne upgrade to keep the UI stack consistent.

### Testing
- `make test`

## 2026-10-09
### Added
- Text selection and clipboard copy in the message viewer (drag-select, Ctrl+C, right-click Copy via `RichText.Selectable`, fyne-io/fyne#5946); Fyne pinned to the develop commit that introduced it until v2.9 is tagged.
- `make test-race` target.

### Fixed
- Worker shutdown no longer panics (send on closed channel); fetch waiters are drained on both stop paths.
- Removed the second reconnection path that raced on worker connection state; reconnects are event-loop-serialized and deduplicated.
- Cache invalidation actually deletes entries now (previously a silent no-op leaving stale lists up to 24h); cache size limits are enforced.
- New-message check has a watchdog so a stalled server cannot freeze an account; its callback no longer runs on the event loop.
- SMTP: attachments were base64-encoded twice (corrupt at the recipient); text/html now nest in multipart/alternative under multipart/mixed; dial/data timeouts; Message-ID generated; zero Date no longer serialized.
- Email parsing: attachment reads bounded by the size limit before buffering; failed-message /tmp dumps are opt-in; HTML sanitizer falls back to a regex strip on parse failure and handles case-mixed `javascript:` URIs.
- UI: search-window goroutines wrap widget updates in `fyne.Do`; batch move/delete no longer blocks the UI thread; `messages`/`readTimer`/`freshFetch` races; typed-nil SMTP client assertions; UTF-8-safe sender truncation; selection manager stays in sync with list reloads.
- Config saves are atomic (temp+rename) and the preferences account cache is mutex-guarded.

### Changed
- ~2,400 lines of dead code removed (duplicate HTML converter, SMTP queue subsystem, controller stubs, unused cache helpers, example file).
- worker.go split into topic files (connection, health, folders, fetch, convert, idle, flags, search, cache); main_window.go split into unified_inbox, message_ops, account_session, attachments_ui, message_list_widgets, keyboard.
- Deduplicated account lookup, SMTP-client construction, reply/forward composition, folder-select boilerplate, mark read/unread, and the wrapper's duplicate cache layer.
- The MessageListController owns sort state; MainWindow's mirrored enum and conversion boilerplate are gone; version string has a single source of truth.
