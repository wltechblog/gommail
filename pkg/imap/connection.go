package imap

// Code in this file was mechanically split from worker.go.

import (
	"crypto/tls"
	"fmt"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/wltechblog/gommail/internal/email"
)

// handleConnect handles the connect command
func (w *IMAPWorker) handleConnect(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling connect command")

	// Check if already connected
	if w.commandClient != nil && w.idleClient != nil {
		w.logger.Debug("Already connected, returning success")
		return NewResponse(cmd.ID, true, map[string]interface{}{
			DataConnectionState: w.state,
		}, nil)
	}

	// Get server config from command parameters
	config, ok := cmd.GetServerConfig(ParamConfig)
	if !ok {
		// Use worker's default config
		config = w.config
	}

	if config == nil {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("no server configuration provided"))
	}

	// Update state to connecting
	w.setState(ConnectionStateConnecting, nil)

	// Create both IMAP clients (command and IDLE)
	w.logger.Debug("Connecting to IMAP server: %s:%d (TLS: %v)", config.Host, config.Port, config.TLS)

	// Create TLS configuration
	tlsConfig := &tls.Config{
		ServerName: config.Host,
	}

	// Helper function to create a client connection
	createClient := func(purpose string) (*imapclient.Client, error) {
		return w.dialClient(config, purpose, tlsConfig)
	}

	// Create command client
	commandClient, err := createClient("command")
	if err != nil {
		w.logger.Error("Failed to create command client: %v", err)
		w.setState(ConnectionStateFailed, err)
		w.incrementErrorCount()
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("command client creation failed: %w", err))
	}

	// Create IDLE client with a UnilateralDataHandler so the server's unsolicited
	// EXISTS/EXPUNGE responses during IDLE are delivered immediately rather than
	// only being noticed when the 29-minute timeout fires.
	w.logger.Debug("Creating IDLE client connection")
	idleOptions := w.idleClientOptions(tlsConfig)

	var idleClient *imapclient.Client
	if config.TLS {
		idleClient, err = imapclient.DialTLS(fmt.Sprintf("%s:%d", config.Host, config.Port), idleOptions)
	} else {
		idleClient, err = imapclient.DialStartTLS(fmt.Sprintf("%s:%d", config.Host, config.Port), idleOptions)
	}
	if err != nil {
		commandClient.Close()
		w.logger.Error("Failed to connect IDLE client: %v", err)
		w.setState(ConnectionStateFailed, err)
		w.incrementErrorCount()
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("IDLE client creation failed: %w", err))
	}
	if config.Username != "" && config.Password != "" {
		if err := idleClient.Login(config.Username, config.Password).Wait(); err != nil {
			idleClient.Close()
			commandClient.Close()
			w.logger.Error("Failed to authenticate IDLE client: %v", err)
			w.setState(ConnectionStateFailed, err)
			w.incrementErrorCount()
			return NewResponse(cmd.ID, false, nil, fmt.Errorf("IDLE client authentication failed: %w", err))
		}
	}

	// Create a dedicated fetch client. Bulk message fetches (whole-mailbox
	// envelope transfers) can run for minutes; running them on the command
	// client blocks every other command for the account. A dedicated
	// connection also lets an abandoned fetch be aborted instantly by
	// closing the connection instead of draining the wire.
	// Failure to create it is not fatal: bulk fetches fall back to the
	// command client (with its serialized semantics).
	fetchClient, err := createClient("fetch")
	if err != nil {
		w.logger.Warn("Failed to create fetch client (bulk fetches will use the command client): %v", err)
		fetchClient = nil
	}

	// Store clients and update state
	w.commandClient = commandClient
	w.idleClient = idleClient
	w.fetchClient = fetchClient
	w.mu.Lock()
	w.fetchSelectedFolder = ""
	w.mu.Unlock()
	w.setState(ConnectionStateConnected, nil)

	// If this is a reconnection (not initial connection), set the reconnection time
	w.mu.Lock()
	if w.reconnectCount > 0 {
		w.lastReconnectTime = time.Now()
		w.logger.Debug("Set reconnection grace period for account %s (duration: %v)", w.accountName, w.reconnectGracePeriod)
	}
	w.mu.Unlock()

	w.logger.Debug("Successfully connected to IMAP server for account: %s", w.accountName)
	return NewResponse(cmd.ID, true, map[string]interface{}{
		DataConnectionState: w.state,
	}, nil)
}

// dialClient creates an authenticated IMAP client connection for the given
// purpose ("command", "idle", "fetch").
func (w *IMAPWorker) dialClient(config *email.ServerConfig, purpose string, tlsConfig *tls.Config) (*imapclient.Client, error) {
	w.logger.Debug("Creating %s client connection", purpose)

	options := &imapclient.Options{
		TLSConfig: tlsConfig,
	}
	if w.tracer != nil {
		options.DebugWriter = w.tracer
		w.logger.Debug("IMAP tracing enabled for %s client", purpose)
	}

	var client *imapclient.Client
	var err error
	if config.TLS {
		client, err = imapclient.DialTLS(fmt.Sprintf("%s:%d", config.Host, config.Port), options)
	} else {
		client, err = imapclient.DialStartTLS(fmt.Sprintf("%s:%d", config.Host, config.Port), options)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to connect %s client: %w", purpose, err)
	}

	if config.Username != "" && config.Password != "" {
		w.logger.Debug("Authenticating %s client with username: %s", purpose, config.Username)
		if err := client.Login(config.Username, config.Password).Wait(); err != nil {
			client.Close()
			return nil, fmt.Errorf("authentication failed for %s client: %w", purpose, err)
		}
	}

	return client, nil
}

// ensureFetchClient returns the dedicated bulk-fetch client, recreating it if
// it is missing (e.g. after an aborted fetch closed it). Returns nil if it
// cannot be created; callers fall back to the command client.
func (w *IMAPWorker) ensureFetchClient() *imapclient.Client {
	if w.fetchClient != nil {
		return w.fetchClient
	}

	w.logger.Debug("Recreating fetch client connection")
	tlsConfig := &tls.Config{ServerName: w.config.Host}
	client, err := w.dialClient(w.config, "fetch", tlsConfig)
	if err != nil {
		w.logger.Warn("Failed to recreate fetch client (bulk fetches will use the command client): %v", err)
		return nil
	}

	w.mu.Lock()
	w.fetchClient = client
	w.fetchSelectedFolder = ""
	w.mu.Unlock()
	return client
}

// idleClientOptions returns the imapclient.Options used for the dedicated IDLE
// connection. The UnilateralDataHandler forwards server pushes (e.g. "* N
// EXISTS") to idleUpdateCh so idleMonitorLoop can react immediately instead of
// waiting for the IDLE timeout.
func (w *IMAPWorker) idleClientOptions(tlsConfig *tls.Config) *imapclient.Options {
	options := &imapclient.Options{
		TLSConfig: tlsConfig,
		UnilateralDataHandler: &imapclient.UnilateralDataHandler{
			Mailbox: func(data *imapclient.UnilateralDataMailbox) {
				// Server pushed a mailbox update (e.g. * N EXISTS) while we are IDLing.
				// NumMessages being non-nil means the message count changed, which is
				// the primary indicator that new mail has arrived.
				if data.NumMessages != nil {
					w.logger.Debug("IDLE: server pushed mailbox update (NumMessages=%d), signalling check",
						*data.NumMessages)
					// Non-blocking send: if a signal is already pending we don't need
					// another one – the monitor loop will check for new messages anyway.
					select {
					case w.idleUpdateCh <- struct{}{}:
					default:
					}
				}
			},
		},
	}
	if w.tracer != nil {
		options.DebugWriter = w.tracer
		w.logger.Debug("IMAP tracing enabled for IDLE client")
	}
	return options
}

// handleDisconnect handles the disconnect command
func (w *IMAPWorker) handleDisconnect(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling disconnect command")

	if w.commandClient == nil && w.idleClient == nil {
		w.logger.Debug("Already disconnected")
		return NewResponse(cmd.ID, true, map[string]interface{}{
			DataConnectionState: w.state,
		}, nil)
	}

	// Stop IDLE monitoring before disconnecting
	w.stopIDLEInternal()

	// Close both connections
	if w.commandClient != nil {
		w.logger.Debug("Closing command client connection")
		if err := w.commandClient.Close(); err != nil {
			w.logger.Warn("Error closing command client connection: %v", err)
		}
		w.commandClient = nil
	}

	if w.idleClient != nil {
		w.logger.Debug("Closing IDLE client connection")
		if err := w.idleClient.Close(); err != nil {
			w.logger.Warn("Error closing IDLE client connection: %v", err)
		}
		w.idleClient = nil
	}
	if w.fetchClient != nil {
		w.logger.Debug("Closing fetch client connection")
		if err := w.fetchClient.Close(); err != nil {
			w.logger.Warn("Error closing fetch client connection: %v", err)
		}
		w.fetchClient = nil
	}
	w.setState(ConnectionStateDisconnected, nil)

	// Clear selected folder and IDLE state
	w.mu.Lock()
	w.selectedFolder = ""
	w.fetchSelectedFolder = ""
	w.idleActive = false
	w.mu.Unlock()

	w.logger.Debug("Disconnected from IMAP server for account: %s", w.accountName)
	return NewResponse(cmd.ID, true, map[string]interface{}{
		DataConnectionState: w.state,
	}, nil)
}

// handleReconnect handles the reconnect command
func (w *IMAPWorker) handleReconnect(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling reconnect command")

	w.incrementReconnectCount()
	w.setState(ConnectionStateReconnecting, nil)

	// Disconnect first if connected
	if w.commandClient != nil {
		w.logger.Debug("Disconnecting command client before reconnect")
		w.commandClient.Close()
		w.commandClient = nil
	}
	if w.idleClient != nil {
		w.logger.Debug("Disconnecting IDLE client before reconnect")
		w.idleClient.Close()
		w.idleClient = nil
	}
	if w.fetchClient != nil {
		w.logger.Debug("Disconnecting fetch client before reconnect")
		w.fetchClient.Close()
		w.fetchClient = nil
	}

	// Clear state
	w.mu.Lock()
	w.selectedFolder = ""
	w.fetchSelectedFolder = ""
	w.idleActive = false
	w.mu.Unlock()

	// Attempt to reconnect
	connectCmd := NewCommandWithContext(cmd.Context, CmdConnect, cmd.Parameters)
	return w.handleConnect(connectCmd)
}
