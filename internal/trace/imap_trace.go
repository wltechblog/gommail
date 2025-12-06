package trace

import (
"fmt"
"io"
"os"
"path/filepath"
"sync"
"time"

"github.com/wltechblog/gommail/internal/logging"
)

// IMAPTracer manages IMAP protocol tracing for multiple accounts
type IMAPTracer struct {
enabled   bool
traceDir  string
tracers   map[string]*AccountTracer
mu        sync.RWMutex
logger    *logging.Logger
}

// AccountTracer handles tracing for a single account
type AccountTracer struct {
accountName string
file        *os.File
writer      io.Writer
mu          sync.Mutex
logger      *logging.Logger
}

// NewIMAPTracer creates a new IMAP tracer
func NewIMAPTracer(enabled bool, traceDir string) *IMAPTracer {
if traceDir == "" {
// Default to a traces subdirectory in the current working directory
cwd, err := os.Getwd()
if err != nil {
traceDir = "."
} else {
traceDir = filepath.Join(cwd, "traces")
}
}

return &IMAPTracer{
enabled:  enabled,
traceDir: traceDir,
tracers:  make(map[string]*AccountTracer),
logger:   logging.NewComponent("imap-trace"),
}
}

// IsEnabled returns whether IMAP tracing is enabled
func (t *IMAPTracer) IsEnabled() bool {
return t.enabled
}

// GetAccountTracer returns a tracer for the specified account
func (t *IMAPTracer) GetAccountTracer(accountName string) io.Writer {
if !t.enabled {
return nil
}

t.mu.Lock()
defer t.mu.Unlock()

// Check if we already have a tracer for this account
if tracer, exists := t.tracers[accountName]; exists {
return tracer
}

// Create a new tracer for this account
tracer, err := t.createAccountTracer(accountName)
if err != nil {
t.logger.Error("Failed to create account tracer for %s: %v", accountName, err)
return nil
}

t.tracers[accountName] = tracer
return tracer
}

// createAccountTracer creates a new account tracer with file management
func (t *IMAPTracer) createAccountTracer(accountName string) (*AccountTracer, error) {
// Create trace directory if it doesn't exist
if err := os.MkdirAll(t.traceDir, 0755); err != nil {
return nil, fmt.Errorf("failed to create trace directory %s: %w", t.traceDir, err)
}

// Create trace file with timestamp
timestamp := time.Now().Format("2006-01-02")
filename := fmt.Sprintf("imap-trace-%s-%s.log", accountName, timestamp)
filepath := filepath.Join(t.traceDir, filename)

file, err := os.OpenFile(filepath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
if err != nil {
return nil, fmt.Errorf("failed to create trace file %s: %w", filepath, err)
}

tracer := &AccountTracer{
accountName: accountName,
file:        file,
writer:      &timestampedWriter{file: file, accountName: accountName},
logger:      logging.NewComponent(fmt.Sprintf("imap-trace-%s", accountName)),
}

tracer.logger.Info("Started IMAP tracing for account %s: %s", accountName, filepath)

// Write header to the trace file
header := fmt.Sprintf("=== IMAP Trace Started for Account: %s at %s ===\n",
accountName, time.Now().Format("2006-01-02 15:04:05"))
if _, err := file.WriteString(header); err != nil {
tracer.logger.Warn("Failed to write trace header: %v", err)
}

return tracer, nil
}

// Write implements io.Writer for AccountTracer
func (a *AccountTracer) Write(p []byte) (int, error) {
a.mu.Lock()
defer a.mu.Unlock()

if a.writer == nil {
return 0, fmt.Errorf("tracer writer is nil")
}

return a.writer.Write(p)
}

// Close closes the account tracer and its file
func (a *AccountTracer) Close() error {
a.mu.Lock()
defer a.mu.Unlock()

if a.file != nil {
// Write footer
footer := fmt.Sprintf("=== IMAP Trace Ended for Account: %s at %s ===\n",
a.accountName, time.Now().Format("2006-01-02 15:04:05"))
a.file.WriteString(footer)

err := a.file.Close()
a.file = nil
a.writer = nil
return err
}
return nil
}

// Close closes all account tracers
func (t *IMAPTracer) Close() error {
t.mu.Lock()
defer t.mu.Unlock()

var lastErr error
for _, tracer := range t.tracers {
if err := tracer.Close(); err != nil {
lastErr = err
t.logger.Error("Failed to close tracer for account %s: %v", tracer.accountName, err)
}
}

t.tracers = make(map[string]*AccountTracer)
return lastErr
}

// timestampedWriter wraps a writer to add timestamps to each line
type timestampedWriter struct {
file        *os.File
accountName string
}

// Write adds timestamps to the beginning of each line
func (tw *timestampedWriter) Write(p []byte) (int, error) {
if len(p) == 0 {
return 0, nil
}

timestamp := time.Now().Format("2006-01-02 15:04:05.000")

// Detect direction based on common IMAP patterns
direction := tw.detectDirection(p)

// Split input into lines and add timestamps
lines := splitLines(p)
totalWritten := 0

for i, line := range lines {
if len(line) == 0 && i == len(lines)-1 {
// Skip empty last line (from splitting)
continue
}

// Format: [timestamp] [direction] line
prefixed := fmt.Sprintf("[%s] [%s] %s\n", timestamp, direction, string(line))
n, err := tw.file.WriteString(prefixed)
totalWritten += n
if err != nil {
return totalWritten, err
}
}

return len(p), nil // Return original length as expected by io.Writer
}

// detectDirection attempts to detect if this is a client->server or server->client message
func (tw *timestampedWriter) detectDirection(p []byte) string {
data := string(p)

// Common server responses
serverPatterns := []string{
"* OK", "* NO", "* BAD", "* PREAUTH", "* BYE",
"* CAPABILITY", "* LIST", "* LSUB", "* STATUS", "* SEARCH",
"* FLAGS", "* EXISTS", "* RECENT", "* EXPUNGE", "* FETCH",
}

// Check for server responses first (they often start with *)
for _, pattern := range serverPatterns {
if len(data) >= len(pattern) && 
   (data[:len(pattern)] == pattern || 
    (len(data) > len(pattern) && data[:len(pattern)+1] == pattern+" ")) {
return "S->C"
}
}

// Check for client commands (usually start with tag + command)
clientPatterns := []string{
"LOGIN", "SELECT", "EXAMINE", "CREATE", "DELETE", "RENAME",
"SUBSCRIBE", "UNSUBSCRIBE", "LIST", "LSUB", "STATUS", "APPEND",
"CHECK", "CLOSE", "EXPUNGE", "SEARCH", "FETCH", "STORE",
"COPY", "MOVE", "IDLE", "DONE", "CAPABILITY", "NOOP", "LOGOUT",
}

for _, pattern := range clientPatterns {
if containsWord(data, pattern) {
return "C->S"
}
}

// Default to unknown if we can't determine
return "?->?"
}

// containsWord checks if a word exists in the string (case insensitive, word boundary)
func containsWord(text, word string) bool {
// Simple word boundary check - look for the word surrounded by spaces or at start/end
text = " " + text + " "
word = " " + word + " "

// Convert to uppercase for case-insensitive comparison
return len(text) >= len(word) && 
   (text[:len(word)] == word || 
    findSubstring(text, word))
}

// findSubstring performs a simple substring search
func findSubstring(text, substr string) bool {
if len(substr) > len(text) {
return false
}

for i := 0; i <= len(text)-len(substr); i++ {
if text[i:i+len(substr)] == substr {
return true
}
}
return false
}

// splitLines splits byte data into lines, preserving the original content
func splitLines(data []byte) [][]byte {
if len(data) == 0 {
return [][]byte{}
}

var lines [][]byte
start := 0

for i, b := range data {
if b == '\n' {
// Include everything up to but not including the \n
lines = append(lines, data[start:i])
start = i + 1
}
}

// Add remaining data if any
if start < len(data) {
lines = append(lines, data[start:])
}

return lines
}
