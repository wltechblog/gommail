package imap

import (
	"fmt"
	"log"
	"time"

	"github.com/wltechblog/gommail/internal/email"
)

// ExampleUsage demonstrates how to use the ClientWrapper
func ExampleUsage() {
	// Configure IMAP server settings
	config := email.ServerConfig{
		Host:     "imap.gmail.com",
		Port:     993,
		Username: "user@gmail.com",
		Password: "app-password",
		TLS:      true,
	}

	// Create the client wrapper (implements email.IMAPClient interface)
	client := NewClientWrapper(config)

	// Start the underlying worker
	if err := client.Start(); err != nil {
		log.Fatalf("Failed to start client: %v", err)
	}
	defer client.Stop()

	// Set up monitoring callbacks
	client.SetNewMessageCallback(func(folder string, messages []email.Message) {
		fmt.Printf("New messages in %s: %d\n", folder, len(messages))
		for _, msg := range messages {
			fmt.Printf("  - From: %s, Subject: %s\n", msg.From, msg.Subject)
		}
	})

	client.SetConnectionStateCallback(func(event email.ConnectionEvent) {
		fmt.Printf("Connection state changed: %s\n", event.State.String())
		if event.Error != nil {
			fmt.Printf("  Error: %v\n", event.Error)
		}
	})

	// Connect to the server
	if err := client.Connect(); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer client.Disconnect()

	// Subscribe to default folders
	if err := client.SubscribeToDefaultFolders("Sent", "Trash"); err != nil {
		log.Printf("Warning: Failed to subscribe to default folders: %v", err)
	}

	// List subscribed folders
	folders, err := client.ListSubscribedFolders()
	if err != nil {
		log.Fatalf("Failed to list folders: %v", err)
	}

	fmt.Printf("Subscribed folders (%d):\n", len(folders))
	for _, folder := range folders {
		fmt.Printf("  - %s (%d messages)\n", folder.Name, folder.MessageCount)
	}

	// Fetch messages from INBOX
	messages, err := client.FetchMessages("INBOX", 10)
	if err != nil {
		log.Fatalf("Failed to fetch messages: %v", err)
	}

	fmt.Printf("\nRecent messages in INBOX (%d):\n", len(messages))
	for i, msg := range messages {
		// Check if message is read by looking for \Seen flag
		isRead := false
		for _, flag := range msg.Flags {
			if flag == "\\Seen" {
				isRead = true
				break
			}
		}

		fromStr := "Unknown"
		if len(msg.From) > 0 {
			fromStr = msg.From[0].Email
			if msg.From[0].Name != "" {
				fromStr = fmt.Sprintf("%s <%s>", msg.From[0].Name, msg.From[0].Email)
			}
		}

		fmt.Printf("  %d. From: %s\n", i+1, fromStr)
		fmt.Printf("     Subject: %s\n", msg.Subject)
		fmt.Printf("     Date: %s\n", msg.Date.Format("2006-01-02 15:04:05"))
		fmt.Printf("     UID: %d, Read: %t\n", msg.UID, isRead)
		fmt.Println()
	}

	// Start IDLE monitoring for INBOX
	if err := client.StartMonitoring("INBOX"); err != nil {
		log.Printf("Warning: Failed to start monitoring: %v", err)
	} else {
		fmt.Println("Started IDLE monitoring for INBOX")

		// Mark initial sync as complete so we get notifications for new messages
		client.MarkInitialSyncComplete("INBOX")
	}

	// Example of message operations
	if len(messages) > 0 {
		firstMsg := messages[0]

		// Mark as read
		if err := client.MarkAsRead("INBOX", firstMsg.UID); err != nil {
			log.Printf("Failed to mark message as read: %v", err)
		} else {
			fmt.Printf("Marked message %d as read\n", firstMsg.UID)
		}

		// Fetch full message details
		fullMsg, err := client.FetchMessage("INBOX", firstMsg.UID)
		if err != nil {
			log.Printf("Failed to fetch full message: %v", err)
		} else {
			bodyLength := len(fullMsg.Body.Text) + len(fullMsg.Body.HTML)
			fmt.Printf("Full message body length: %d bytes (Text: %d, HTML: %d)\n",
				bodyLength, len(fullMsg.Body.Text), len(fullMsg.Body.HTML))
		}
	}

	// Example of search functionality
	searchCriteria := email.SearchCriteria{
		From:          "noreply",
		CaseSensitive: false,
		UseRegex:      false,
	}

	searchResults, err := client.SearchMessagesInFolder("INBOX", searchCriteria)
	if err != nil {
		log.Printf("Search failed: %v", err)
	} else {
		fmt.Printf("Found %d messages from 'noreply'\n", len(searchResults))
	}

	// Display health status
	healthStatus := client.GetHealthStatus()
	fmt.Printf("\nHealth Status:\n")
	fmt.Printf("  State: %s\n", healthStatus["state"])
	fmt.Printf("  Error Count: %d\n", healthStatus["error_count"])
	fmt.Printf("  Health Check Interval: %s\n", healthStatus["health_check_interval"])

	// Display monitoring status
	fmt.Printf("\nMonitoring Status:\n")
	fmt.Printf("  Active: %t\n", client.IsMonitoring())
	fmt.Printf("  Paused: %t\n", client.IsMonitoringPaused())
	fmt.Printf("  Folder: %s\n", client.GetMonitoredFolder())

	// Simulate running for a while to receive notifications
	fmt.Println("\nListening for new messages for 30 seconds...")
	time.Sleep(30 * time.Second)

	// Stop monitoring
	client.StopMonitoring()
	fmt.Println("Stopped monitoring")
}

// ExampleWithCustomConfiguration shows advanced configuration options
func ExampleWithCustomConfiguration() {
	config := email.ServerConfig{
		Host:     "imap.example.com",
		Port:     993,
		Username: "user@example.com",
		Password: "password",
		TLS:      true,
	}

	// Create client with custom cache (if available)
	client := NewClientWrapper(config)

	// Start the worker
	if err := client.Start(); err != nil {
		log.Fatalf("Failed to start client: %v", err)
	}
	defer client.Stop()

	// Configure health checking
	client.SetHealthCheckInterval(15 * time.Second)
	client.SetReconnectConfig(
		2*time.Second,  // initial delay
		60*time.Second, // max delay
		5,              // max attempts
	)

	// Connect and use the client
	if err := client.Connect(); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer client.Disconnect()

	fmt.Println("Client configured with custom settings")

	// The client is now ready to use with enhanced health checking
	// and reconnection capabilities
}

// ExampleErrorHandling demonstrates proper error handling
func ExampleErrorHandling() {
	config := email.ServerConfig{
		Host:     "invalid.server.com",
		Port:     993,
		Username: "user@invalid.com",
		Password: "wrong-password",
		TLS:      true,
	}

	client := NewClientWrapper(config)

	// Set up error monitoring
	client.SetConnectionStateCallback(func(event email.ConnectionEvent) {
		switch event.State {
		case email.ConnectionStateConnecting:
			fmt.Println("Connecting to server...")
		case email.ConnectionStateConnected:
			fmt.Println("Successfully connected!")
		case email.ConnectionStateReconnecting:
			fmt.Printf("Reconnecting (attempt %d)...\n", event.Attempt)
		case email.ConnectionStateFailed:
			fmt.Printf("Connection failed: %v\n", event.Error)
		case email.ConnectionStateDisconnected:
			fmt.Println("Disconnected from server")
		}
	})

	if err := client.Start(); err != nil {
		log.Printf("Failed to start client: %v", err)
		return
	}
	defer client.Stop()

	// This will likely fail, demonstrating error handling
	if err := client.Connect(); err != nil {
		log.Printf("Expected connection failure: %v", err)

		// Check health status for more details
		healthStatus := client.GetHealthStatus()
		fmt.Printf("Health status: %+v\n", healthStatus)
	}
}
