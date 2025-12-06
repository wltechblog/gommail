# gommail

Cross-platform IMAP/SMTP desktop email client for people who want a native Go + Fyne experience with modern conveniences such as a unified inbox, background sync, and safe HTML rendering.

## Project Status
- Daily-driver ready with multi-account support, idle monitoring, and resilient reconnection logic
- First public release: expect ongoing polishing, but the app is fully functional for common workflows
- GPLv2 licensed and developed in the open

## Highlights
- **Unified Inbox** spanning every configured account with real-time updates
- **Safe Message Viewer** that sanitizes HTML, converts inline images, and falls back to plaintext automatically
- **Robust IMAP Worker** with IDLE, resumable fetches, caching, and automatic recovery after network hiccups
- **Full SMTP Composer** with attachments, drafts, and per-account sender identities
- **Desktop UX** built on Fyne with three-pane layout, keyboard shortcuts, notifications, and a first-run wizard

## Requirements
- Go 1.24 or later
- Platform prerequisites for Fyne (GTK3 on Linux, latest Xcode tools on macOS, MSYS2 on Windows)
- IMAP and SMTP accounts that support classic username/password auth (OAuth2 is not yet available)

## Getting Started
```bash
# Clone and enter the workspace
git clone https://github.com/wltechblog/gommail.git
cd gommail

# Download modules
make deps

# Build everything (runs go test ./... as part of the default target)
make

# Launch the desktop client with hot reload style rebuilding
make run
```
The compiled binary is emitted to `build/gommail`. You can pass flags directly:
```bash
./build/gommail --profile work --debug --imap-trace --log-level info
```

## Configuration
- First launch opens a wizard that discovers IMAP/SMTP settings for common providers, validates credentials, and stores per-profile preferences
- YAML-based configuration is also supported via `config.example.yaml`; set `GOMMAIL_CONFIG_PATH` to override the location
- Cache and trace directories respect `GOMMAIL_CACHE_DIR` and `GOMMAIL_TRACE_DIR`
- Profiles keep accounts, caches, logs, and UI state isolated; switch with `--profile <name>`

## Key Features
- Multi-account account controller with live folder monitoring and manual refresh controls
- Unified message list with configurable sorting, unread highlighting, and per-row context menus
- Debounced message viewer that renders markdown, strips hostile CSS/JS, and displays inline attachments as placeholders
- Attachment manager with caching so frequently opened files stay available offline
- Structured logging and optional IMAP trace files to troubleshoot connectivity
- Desktop notifications per message with per-account branding assets

## Known Limitations
- OAuth2 and modern web auth flows are not implemented yet
- Message threading is basic (chronological view only)
- Fyne doesn't curently support text selection within the RichText widget, so copying message bodies is not supported in the viewer (you can copy from a reply!)

## Contributing
Bug reports and pull requests are welcome. Please run `make test` before submitting changes to keep the build healthy.

## License
GPL-2.0. See [LICENSE](LICENSE).
