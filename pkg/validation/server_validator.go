package validation

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/wltechblog/gommail/internal/email"
)

// ServerValidationResult contains the results of server validation
type ServerValidationResult struct {
	// Connection status
	CanConnect   bool   `json:"can_connect"`
	ConnectError string `json:"connect_error,omitempty"`

	// TLS/SSL capabilities
	SupportsTLS      bool  `json:"supports_tls"`
	SupportsSTARTTLS bool  `json:"supports_starttls"`
	SupportsPlain    bool  `json:"supports_plain"`
	RecommendedTLS   bool  `json:"recommended_tls"`
	DetectedPorts    []int `json:"detected_ports"`

	// Certificate information
	CertificateValid  bool       `json:"certificate_valid"`
	CertificateIssues []string   `json:"certificate_issues,omitempty"`
	CertificateExpiry *time.Time `json:"certificate_expiry,omitempty"`

	// Hostname validation
	HostnameMatch   bool   `json:"hostname_match"`
	CertificateHost string `json:"certificate_host,omitempty"`
	SuggestedHost   string `json:"suggested_host,omitempty"`

	// Server capabilities
	AuthMethods []string `json:"auth_methods,omitempty"`
	ServerInfo  string   `json:"server_info,omitempty"`
}

// ServerValidator provides server validation functionality
type ServerValidator struct {
	timeout time.Duration
}

// NewServerValidator creates a new server validator
func NewServerValidator() *ServerValidator {
	return &ServerValidator{
		timeout: 10 * time.Second,
	}
}

// ValidateIMAPServer validates an IMAP server configuration
func (v *ServerValidator) ValidateIMAPServer(config email.ServerConfig) (*ServerValidationResult, error) {
	result := &ServerValidationResult{
		DetectedPorts:     []int{},
		CertificateIssues: []string{},
		AuthMethods:       []string{},
	}

	// Test standard IMAP ports if not specified
	ports := []int{config.Port}
	if config.Port == 0 {
		ports = []int{993, 143} // TLS and plain IMAP ports
	}

	var lastError error
	for _, port := range ports {
		testConfig := config
		testConfig.Port = port

		// Try TLS first (port 993 or explicit TLS)
		if port == 993 || config.TLS {
			if err := v.testIMAPTLS(&testConfig, result); err == nil {
				result.CanConnect = true
				result.SupportsTLS = true
				result.RecommendedTLS = true
				result.DetectedPorts = append(result.DetectedPorts, port)
				break
			} else {
				lastError = err
			}
		}

		// Try STARTTLS (port 143 or explicit STARTTLS)
		if port == 143 || !config.TLS {
			if err := v.testIMAPSTARTTLS(&testConfig, result); err == nil {
				result.CanConnect = true
				result.SupportsSTARTTLS = true
				result.DetectedPorts = append(result.DetectedPorts, port)
				if !result.SupportsTLS {
					result.RecommendedTLS = false
				}
				break
			} else {
				lastError = err
			}
		}

		// Try plain connection (port 143 only, for unencrypted)
		if port == 143 {
			if err := v.testIMAPPlain(&testConfig, result); err == nil {
				result.CanConnect = true
				result.SupportsPlain = true
				result.DetectedPorts = append(result.DetectedPorts, port)
				result.RecommendedTLS = false
				break
			} else {
				lastError = err
			}
		}
	}

	if !result.CanConnect && lastError != nil {
		result.ConnectError = lastError.Error()
	}

	return result, nil
}

// ValidateSMTPServer validates an SMTP server configuration
func (v *ServerValidator) ValidateSMTPServer(config email.ServerConfig) (*ServerValidationResult, error) {
	result := &ServerValidationResult{
		DetectedPorts:     []int{},
		CertificateIssues: []string{},
		AuthMethods:       []string{},
	}

	// Test standard SMTP ports if not specified
	ports := []int{config.Port}
	if config.Port == 0 {
		ports = []int{587, 465, 25} // STARTTLS, TLS, and plain SMTP ports
	}

	var lastError error
	for _, port := range ports {
		testConfig := config
		testConfig.Port = port

		// Try TLS first (port 465 or explicit TLS)
		if port == 465 || config.TLS {
			if err := v.testSMTPTLS(&testConfig, result); err == nil {
				result.CanConnect = true
				result.SupportsTLS = true
				result.RecommendedTLS = true
				result.DetectedPorts = append(result.DetectedPorts, port)
				break
			} else {
				lastError = err
			}
		}

		// Try STARTTLS (port 587 or 25)
		if port == 587 || port == 25 || !config.TLS {
			if err := v.testSMTPSTARTTLS(&testConfig, result); err == nil {
				result.CanConnect = true
				result.SupportsSTARTTLS = true
				result.DetectedPorts = append(result.DetectedPorts, port)
				if port == 587 {
					result.RecommendedTLS = false // STARTTLS is acceptable for 587
				}
				break
			} else {
				lastError = err
			}
		}

		// Try plain connection (port 25 only, for unencrypted)
		if port == 25 {
			if err := v.testSMTPPlain(&testConfig, result); err == nil {
				result.CanConnect = true
				result.SupportsPlain = true
				result.DetectedPorts = append(result.DetectedPorts, port)
				result.RecommendedTLS = false
				break
			} else {
				lastError = err
			}
		}
	}

	if !result.CanConnect && lastError != nil {
		result.ConnectError = lastError.Error()
	}

	return result, nil
}

// testIMAPTLS tests IMAP connection with direct TLS
func (v *ServerValidator) testIMAPTLS(config *email.ServerConfig, result *ServerValidationResult) error {
	addr := fmt.Sprintf("%s:%d", config.Host, config.Port)

	// First, validate the certificate by making a direct TLS connection
	if err := v.validateTLSCertificate(config.Host, config.Port, result); err != nil {
		// Certificate validation failed, but we might still be able to connect
		// Continue with IMAP connection attempt
	}

	tlsConfig := &tls.Config{
		ServerName:         config.Host,
		InsecureSkipVerify: true, // We already validated manually above
	}

	client, err := imapclient.DialTLS(addr, &imapclient.Options{
		TLSConfig: tlsConfig,
	})
	if err != nil {
		return fmt.Errorf("TLS connection failed: %w", err)
	}
	defer client.Close()

	result.SupportsTLS = true

	// Test authentication if credentials provided
	if config.Username != "" && config.Password != "" {
		if err := client.Login(config.Username, config.Password).Wait(); err != nil {
			result.AuthMethods = append(result.AuthMethods, "LOGIN (failed)")
		} else {
			result.AuthMethods = append(result.AuthMethods, "LOGIN")
		}
	}

	return nil
}

// testIMAPSTARTTLS tests IMAP connection with STARTTLS
func (v *ServerValidator) testIMAPSTARTTLS(config *email.ServerConfig, result *ServerValidationResult) error {
	addr := fmt.Sprintf("%s:%d", config.Host, config.Port)

	// For STARTTLS, we need to validate the certificate after the STARTTLS upgrade
	// Since go-imap/v2 doesn't expose the TLS connection, we'll do a separate validation
	// after confirming STARTTLS works

	tlsConfig := &tls.Config{
		ServerName:         config.Host,
		InsecureSkipVerify: true, // We'll validate manually
	}

	client, err := imapclient.DialStartTLS(addr, &imapclient.Options{
		TLSConfig: tlsConfig,
	})
	if err != nil {
		return fmt.Errorf("STARTTLS connection failed: %w", err)
	}
	defer client.Close()

	result.SupportsSTARTTLS = true

	// Now validate the certificate by making a separate STARTTLS connection
	if err := v.validateSTARTTLSCertificate(config.Host, config.Port, "imap", result); err != nil {
		// Certificate validation failed, but connection works
		// The certificate issues are already recorded in result
	}

	// Test authentication if credentials provided
	if config.Username != "" && config.Password != "" {
		if err := client.Login(config.Username, config.Password).Wait(); err != nil {
			result.AuthMethods = append(result.AuthMethods, "LOGIN (failed)")
		} else {
			result.AuthMethods = append(result.AuthMethods, "LOGIN")
		}
	}

	return nil
}

// testIMAPPlain tests IMAP connection without encryption (plain text)
func (v *ServerValidator) testIMAPPlain(config *email.ServerConfig, result *ServerValidationResult) error {
	addr := fmt.Sprintf("%s:%d", config.Host, config.Port)

	// Connect without TLS
	conn, err := net.DialTimeout("tcp", addr, v.timeout)
	if err != nil {
		return fmt.Errorf("plain connection failed: %w", err)
	}
	defer conn.Close()

	// Create IMAP client on plain connection
	client := imapclient.New(conn, &imapclient.Options{})
	defer client.Close()

	// Test authentication if credentials provided
	if config.Username != "" && config.Password != "" {
		if err := client.Login(config.Username, config.Password).Wait(); err != nil {
			result.AuthMethods = append(result.AuthMethods, "LOGIN (failed)")
		} else {
			result.AuthMethods = append(result.AuthMethods, "LOGIN")
		}
	}

	return nil
}

// testSMTPTLS tests SMTP connection with direct TLS
func (v *ServerValidator) testSMTPTLS(config *email.ServerConfig, result *ServerValidationResult) error {
	addr := fmt.Sprintf("%s:%d", config.Host, config.Port)

	tlsConfig := &tls.Config{
		ServerName:         config.Host,
		InsecureSkipVerify: true, // We'll validate manually
	}

	conn, err := tls.Dial("tcp", addr, tlsConfig)
	if err != nil {
		return fmt.Errorf("TLS connection failed: %w", err)
	}
	defer conn.Close()

	// Validate certificate
	v.validateCertificate(conn, config.Host, result)

	// Create SMTP client
	client, err := smtp.NewClient(conn, config.Host)
	if err != nil {
		return fmt.Errorf("SMTP client creation failed: %w", err)
	}
	defer client.Quit()

	// Get server info
	result.ServerInfo = "SMTP over TLS"

	// Test authentication if credentials provided
	if config.Username != "" && config.Password != "" {
		auth := smtp.PlainAuth("", config.Username, config.Password, config.Host)
		if err := client.Auth(auth); err != nil {
			result.AuthMethods = append(result.AuthMethods, "PLAIN (failed)")
		} else {
			result.AuthMethods = append(result.AuthMethods, "PLAIN")
		}
	}

	return nil
}

// testSMTPSTARTTLS tests SMTP connection with STARTTLS
func (v *ServerValidator) testSMTPSTARTTLS(config *email.ServerConfig, result *ServerValidationResult) error {
	addr := fmt.Sprintf("%s:%d", config.Host, config.Port)

	// Connect to server
	conn, err := net.DialTimeout("tcp", addr, v.timeout)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer conn.Close()

	// Create SMTP client
	client, err := smtp.NewClient(conn, config.Host)
	if err != nil {
		return fmt.Errorf("SMTP client creation failed: %w", err)
	}
	defer client.Quit()

	// Try STARTTLS
	tlsConfig := &tls.Config{
		ServerName:         config.Host,
		InsecureSkipVerify: true, // We'll validate manually
	}

	if err := client.StartTLS(tlsConfig); err != nil {
		return fmt.Errorf("STARTTLS failed: %w", err)
	}

	result.ServerInfo = "SMTP with STARTTLS"

	// Now validate the certificate by making a separate STARTTLS connection
	if err := v.validateSTARTTLSCertificate(config.Host, config.Port, "smtp", result); err != nil {
		// Certificate validation failed, but connection works
		// The certificate issues are already recorded in result
	}

	// Test authentication if credentials provided
	if config.Username != "" && config.Password != "" {
		auth := smtp.PlainAuth("", config.Username, config.Password, config.Host)
		if err := client.Auth(auth); err != nil {
			result.AuthMethods = append(result.AuthMethods, "PLAIN (failed)")
		} else {
			result.AuthMethods = append(result.AuthMethods, "PLAIN")
		}
	}

	return nil
}

// testSMTPPlain tests SMTP connection without encryption (plain text)
func (v *ServerValidator) testSMTPPlain(config *email.ServerConfig, result *ServerValidationResult) error {
	addr := fmt.Sprintf("%s:%d", config.Host, config.Port)

	// Connect without TLS
	conn, err := net.DialTimeout("tcp", addr, v.timeout)
	if err != nil {
		return fmt.Errorf("plain connection failed: %w", err)
	}
	defer conn.Close()

	// Create SMTP client
	client, err := smtp.NewClient(conn, config.Host)
	if err != nil {
		return fmt.Errorf("SMTP client creation failed: %w", err)
	}
	defer client.Quit()

	// Test authentication if credentials provided
	if config.Username != "" && config.Password != "" {
		auth := smtp.PlainAuth("", config.Username, config.Password, config.Host)
		if err := client.Auth(auth); err != nil {
			result.AuthMethods = append(result.AuthMethods, "PLAIN (failed)")
		} else {
			result.AuthMethods = append(result.AuthMethods, "PLAIN")
		}
	}

	return nil
}

// validateCertificate validates the TLS certificate
func (v *ServerValidator) validateCertificate(conn *tls.Conn, expectedHost string, result *ServerValidationResult) {
	state := conn.ConnectionState()
	v.validateCertificateFromState(state, expectedHost, result)
}

// validateCertificateFromState validates the TLS certificate from a connection state
func (v *ServerValidator) validateCertificateFromState(state tls.ConnectionState, expectedHost string, result *ServerValidationResult) {
	if len(state.PeerCertificates) == 0 {
		result.CertificateIssues = append(result.CertificateIssues, "No certificate provided")
		return
	}

	cert := state.PeerCertificates[0]
	result.CertificateExpiry = &cert.NotAfter

	// Check if certificate is expired
	now := time.Now()
	var hasActualErrors bool
	if now.After(cert.NotAfter) {
		result.CertificateIssues = append(result.CertificateIssues,
			fmt.Sprintf("Certificate expired on %s", cert.NotAfter.Format("2006-01-02")))
		hasActualErrors = true
	} else if now.Add(30 * 24 * time.Hour).After(cert.NotAfter) {
		result.CertificateIssues = append(result.CertificateIssues,
			fmt.Sprintf("Certificate expires soon on %s", cert.NotAfter.Format("2006-01-02")))
		// This is just a warning, not an actual error
	}

	// Check if certificate is self-signed
	if cert.Issuer.String() == cert.Subject.String() {
		result.CertificateIssues = append(result.CertificateIssues, "Certificate is self-signed")
		hasActualErrors = true
	}

	// Validate hostname
	if v.validateHostname(cert, expectedHost, result) {
		hasActualErrors = true
	}

	// Try to verify certificate chain
	roots, err := x509.SystemCertPool()
	if err != nil {
		result.CertificateIssues = append(result.CertificateIssues, "Cannot access system certificate store")
		hasActualErrors = true
	} else {
		opts := x509.VerifyOptions{
			Roots:         roots,
			Intermediates: x509.NewCertPool(),
		}

		// Add intermediate certificates
		for i := 1; i < len(state.PeerCertificates); i++ {
			opts.Intermediates.AddCert(state.PeerCertificates[i])
		}

		if _, err := cert.Verify(opts); err != nil {
			result.CertificateIssues = append(result.CertificateIssues,
				fmt.Sprintf("Certificate verification failed: %v", err))
			hasActualErrors = true
		}
	}

	// Set certificate validity based on actual errors, not warnings
	// A certificate that expires soon is still valid
	result.CertificateValid = !hasActualErrors
}

// validateHostname validates the certificate hostname against the expected hostname
// Returns true if there are actual errors (not just warnings)
func (v *ServerValidator) validateHostname(cert *x509.Certificate, expectedHost string, result *ServerValidationResult) bool {
	// Check if the certificate matches the expected hostname
	if err := cert.VerifyHostname(expectedHost); err == nil {
		result.HostnameMatch = true
		return false
	}

	result.HostnameMatch = false

	// Find the best certificate hostname to suggest
	certHost := v.findBestCertificateHostname(cert, expectedHost)
	result.CertificateHost = certHost

	// Add hostname mismatch to certificate issues
	if certHost != "" && certHost != expectedHost {
		if v.isValidHostnameAndSameIP(expectedHost, certHost) {
			result.SuggestedHost = certHost
			result.CertificateIssues = append(result.CertificateIssues,
				fmt.Sprintf("Certificate hostname '%s' doesn't match server hostname '%s', but they resolve to the same IP address", certHost, expectedHost))
			// This is a warning, not an error - the hostname resolves to the same IP
			return false
		} else {
			result.CertificateIssues = append(result.CertificateIssues,
				fmt.Sprintf("Certificate hostname '%s' doesn't match server hostname '%s'", certHost, expectedHost))
			// This is an actual error - different hostnames that don't resolve to same IP
			return true
		}
	} else if certHost == "" {
		result.CertificateIssues = append(result.CertificateIssues,
			"Certificate does not specify a hostname")
		// This is an actual error - no hostname in certificate
		return true
	}

	return false
}

// findBestCertificateHostname finds the best hostname from the certificate to suggest
func (v *ServerValidator) findBestCertificateHostname(cert *x509.Certificate, expectedHost string) string {
	// Collect all possible hostnames from the certificate
	var candidates []string

	// Add Common Name if present
	if cert.Subject.CommonName != "" {
		candidates = append(candidates, cert.Subject.CommonName)
	}

	// Add all DNS names
	candidates = append(candidates, cert.DNSNames...)

	// Remove duplicates and filter out invalid hostnames
	seen := make(map[string]bool)
	var validCandidates []string

	for _, candidate := range candidates {
		if !seen[candidate] && v.isValidHostname(candidate) {
			seen[candidate] = true
			validCandidates = append(validCandidates, candidate)
		}
	}

	if len(validCandidates) == 0 {
		return ""
	}

	// Try to find the best match
	// 1. First, look for exact matches
	for _, candidate := range validCandidates {
		if candidate == expectedHost {
			return candidate
		}
	}

	// 2. Look for subdomain matches (e.g., mail.example.com vs imap.example.com)
	expectedParts := strings.Split(expectedHost, ".")
	if len(expectedParts) >= 2 {
		expectedDomain := strings.Join(expectedParts[1:], ".")

		for _, candidate := range validCandidates {
			candidateParts := strings.Split(candidate, ".")
			if len(candidateParts) >= 2 {
				candidateDomain := strings.Join(candidateParts[1:], ".")
				if candidateDomain == expectedDomain {
					return candidate
				}
			}
		}
	}

	// 3. Return the first valid candidate
	return validCandidates[0]
}

// isValidHostname checks if a hostname is valid and resolvable
func (v *ServerValidator) isValidHostname(hostname string) bool {
	// Basic hostname validation
	if hostname == "" || len(hostname) > 253 {
		return false
	}

	// Check for valid characters and structure
	if strings.HasPrefix(hostname, ".") || strings.HasSuffix(hostname, ".") {
		return false
	}

	// Try to resolve the hostname
	_, err := net.LookupIP(hostname)
	return err == nil
}

// isValidHostnameAndSameIP checks if a hostname is valid and resolves to the same IP as another hostname
func (v *ServerValidator) isValidHostnameAndSameIP(host1, host2 string) bool {
	if !v.isValidHostname(host2) {
		return false
	}

	return v.hostnamesPointToSameIP(host1, host2)
}

// validateTLSCertificate validates the TLS certificate for a direct TLS connection
func (v *ServerValidator) validateTLSCertificate(host string, port int, result *ServerValidationResult) error {
	addr := fmt.Sprintf("%s:%d", host, port)

	// Connect with TLS to get certificate information
	dialer := &net.Dialer{Timeout: v.timeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: true, // We'll validate manually
	})
	if err != nil {
		return fmt.Errorf("TLS connection failed: %w", err)
	}
	defer conn.Close()

	// Validate the certificate
	v.validateCertificate(conn, host, result)
	return nil
}

// validateSTARTTLSCertificate validates the certificate for a STARTTLS connection
func (v *ServerValidator) validateSTARTTLSCertificate(host string, port int, protocol string, result *ServerValidationResult) error {
	addr := fmt.Sprintf("%s:%d", host, port)

	if protocol == "smtp" {
		return v.validateSMTPSTARTTLSCertificate(addr, host, result)
	} else if protocol == "imap" {
		return v.validateIMAPSTARTTLSCertificate(addr, host, result)
	}

	return fmt.Errorf("unsupported protocol: %s", protocol)
}

// ValidateServerCertificate validates the TLS certificate for a server
func (v *ServerValidator) ValidateServerCertificate(host string, port int) (*ServerValidationResult, error) {
	result := &ServerValidationResult{
		CertificateIssues: []string{},
	}

	addr := fmt.Sprintf("%s:%d", host, port)

	// Connect with TLS to get certificate information
	dialer := &net.Dialer{Timeout: v.timeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: true, // We'll validate manually
	})
	if err != nil {
		result.ConnectError = fmt.Sprintf("TLS connection failed: %v", err)
		return result, err
	}
	defer conn.Close()

	result.CanConnect = true
	result.SupportsTLS = true

	// Validate the certificate
	v.validateCertificate(conn, host, result)

	return result, nil
}

// validateSMTPSTARTTLSCertificate validates SMTP STARTTLS certificate
func (v *ServerValidator) validateSMTPSTARTTLSCertificate(addr, host string, result *ServerValidationResult) error {
	// We need to create a custom connection that allows us to access the TLS state
	// after STARTTLS is established

	// Connect to server
	conn, err := net.DialTimeout("tcp", addr, v.timeout)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer conn.Close()

	// Create SMTP client
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("SMTP client creation failed: %w", err)
	}
	defer client.Quit()

	// Create a custom TLS config that will allow us to capture the certificate
	var capturedCerts []*x509.Certificate
	tlsConfig := &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: true, // We'll validate manually
		VerifyConnection: func(cs tls.ConnectionState) error {
			// Capture the certificates for later validation
			capturedCerts = cs.PeerCertificates
			return nil
		},
	}

	// Try STARTTLS
	if err := client.StartTLS(tlsConfig); err != nil {
		return fmt.Errorf("STARTTLS failed: %w", err)
	}

	// Now validate the captured certificates
	if len(capturedCerts) == 0 {
		result.CertificateIssues = append(result.CertificateIssues, "No certificate provided")
		return nil
	}

	// Create a TLS connection state for validation
	state := tls.ConnectionState{
		PeerCertificates: capturedCerts,
	}

	// Validate the certificate using the connection state
	v.validateCertificateFromState(state, host, result)
	return nil
}

// validateIMAPSTARTTLSCertificate validates IMAP STARTTLS certificate
func (v *ServerValidator) validateIMAPSTARTTLSCertificate(addr, host string, result *ServerValidationResult) error {
	// Unfortunately, go-imap/v2 doesn't expose the TLS connection state after STARTTLS
	// We have a few options:
	// 1. Try direct TLS connection (works if server supports both STARTTLS and direct TLS)
	// 2. Skip certificate validation for IMAP STARTTLS (not ideal)
	// 3. Use a custom IMAP STARTTLS implementation (complex)

	// First, try direct TLS connection to the same port
	// This works for servers that support both STARTTLS and direct TLS
	tlsConn, err := tls.Dial("tcp", addr, &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: true,
	})
	if err != nil {
		// Direct TLS failed, try connecting to the TLS port (993) if we're on STARTTLS port (143)
		if addr == fmt.Sprintf("%s:143", host) {
			tlsAddr := fmt.Sprintf("%s:993", host)
			tlsConn, err = tls.Dial("tcp", tlsAddr, &tls.Config{
				ServerName:         host,
				InsecureSkipVerify: true,
			})
			if err != nil {
				// Both direct TLS attempts failed
				// This is expected for servers that only support STARTTLS on port 143
				// We'll add a more informative message
				result.CertificateIssues = append(result.CertificateIssues,
					"Certificate validation skipped for IMAP STARTTLS (server only supports STARTTLS)")
				return nil
			}
		} else {
			// Not the standard STARTTLS port, and direct TLS failed
			result.CertificateIssues = append(result.CertificateIssues,
				"Certificate validation skipped for IMAP STARTTLS (server only supports STARTTLS)")
			return nil
		}
	}
	defer tlsConn.Close()

	// Validate the certificate
	v.validateCertificate(tlsConn, host, result)
	return nil
}

// hostnamesPointToSameIP checks if two hostnames resolve to the same IP address
func (v *ServerValidator) hostnamesPointToSameIP(host1, host2 string) bool {
	ips1, err1 := net.LookupIP(host1)
	ips2, err2 := net.LookupIP(host2)

	if err1 != nil || err2 != nil {
		return false
	}

	// Check if any IP from host1 matches any IP from host2
	for _, ip1 := range ips1 {
		for _, ip2 := range ips2 {
			if ip1.Equal(ip2) {
				return true
			}
		}
	}

	return false
}

// DetectServerSettings attempts to auto-detect server settings for common providers
func (v *ServerValidator) DetectServerSettings(emailAddress string) (imapConfig, smtpConfig *email.ServerConfig, provider string) {
	if !strings.Contains(emailAddress, "@") {
		return nil, nil, ""
	}

	domain := strings.ToLower(strings.Split(emailAddress, "@")[1])

	switch domain {
	case "gmail.com", "googlemail.com":
		return &email.ServerConfig{
				Host: "imap.gmail.com",
				Port: 993,
				TLS:  true,
			}, &email.ServerConfig{
				Host: "smtp.gmail.com",
				Port: 587,
				TLS:  false, // Uses STARTTLS
			}, "Gmail"

	case "outlook.com", "hotmail.com", "live.com", "msn.com":
		return &email.ServerConfig{
				Host: "outlook.office365.com",
				Port: 993,
				TLS:  true,
			}, &email.ServerConfig{
				Host: "smtp-mail.outlook.com",
				Port: 587,
				TLS:  false, // Uses STARTTLS
			}, "Outlook"

	case "yahoo.com", "yahoo.co.uk", "yahoo.ca":
		return &email.ServerConfig{
				Host: "imap.mail.yahoo.com",
				Port: 993,
				TLS:  true,
			}, &email.ServerConfig{
				Host: "smtp.mail.yahoo.com",
				Port: 587,
				TLS:  false, // Uses STARTTLS
			}, "Yahoo"

	case "icloud.com", "me.com", "mac.com":
		return &email.ServerConfig{
				Host: "imap.mail.me.com",
				Port: 993,
				TLS:  true,
			}, &email.ServerConfig{
				Host: "smtp.mail.me.com",
				Port: 587,
				TLS:  false, // Uses STARTTLS
			}, "iCloud"

	default:
		// Try MX lookup first for better server detection
		imapHost, smtpHost := v.detectServersByMX(domain)

		// If MX lookup provided useful results, use them
		if imapHost != "" && smtpHost != "" {
			return &email.ServerConfig{
					Host: imapHost,
					Port: 993,
					TLS:  true, // Direct TLS for IMAP port 993
				}, &email.ServerConfig{
					Host: smtpHost,
					Port: 587,
					TLS:  false, // STARTTLS for SMTP port 587 (encrypted)
				}, "MX-Detected"
		}

		// Fall back to common server naming patterns
		baseDomain := domain
		if strings.HasPrefix(domain, "mail.") {
			baseDomain = domain[5:]
		}

		return &email.ServerConfig{
				Host: "imap." + baseDomain,
				Port: 993,
				TLS:  true, // Direct TLS for IMAP port 993
			}, &email.ServerConfig{
				Host: "smtp." + baseDomain,
				Port: 587,
				TLS:  false, // STARTTLS for SMTP port 587 (encrypted)
			}, "Generic"
	}
}

// detectServersByMX attempts to detect mail servers using MX record lookups
func (v *ServerValidator) detectServersByMX(domain string) (imapHost, smtpHost string) {
	// Look up MX records for the domain
	mxRecords, err := net.LookupMX(domain)
	if err != nil || len(mxRecords) == 0 {
		return "", ""
	}

	// Sort MX records by priority (lower number = higher priority)
	// Go's LookupMX already returns them sorted by priority

	for _, mx := range mxRecords {
		mxHost := strings.TrimSuffix(mx.Host, ".")

		// Check if this MX record points to a known provider
		if imapHost, smtpHost := v.detectKnownProviderFromMX(mxHost); imapHost != "" && smtpHost != "" {
			return imapHost, smtpHost
		}
	}

	// If no known provider found, try to derive server names from the primary MX record
	if len(mxRecords) > 0 {
		primaryMX := strings.TrimSuffix(mxRecords[0].Host, ".")
		return v.deriveServerNamesFromMX(primaryMX, domain)
	}

	return "", ""
}

// detectKnownProviderFromMX checks if an MX record belongs to a known email provider
func (v *ServerValidator) detectKnownProviderFromMX(mxHost string) (imapHost, smtpHost string) {
	mxHost = strings.ToLower(mxHost)

	// Google Workspace / Gmail
	if strings.Contains(mxHost, "google.com") || strings.Contains(mxHost, "googlemail.com") {
		return "imap.gmail.com", "smtp.gmail.com"
	}

	// Microsoft 365 / Outlook
	if strings.Contains(mxHost, "outlook.com") || strings.Contains(mxHost, "office365.com") ||
		strings.Contains(mxHost, "microsoft.com") || strings.Contains(mxHost, "protection.outlook.com") {
		return "outlook.office365.com", "smtp-mail.outlook.com"
	}

	// Yahoo
	if strings.Contains(mxHost, "yahoo.com") || strings.Contains(mxHost, "yahoodns.net") {
		return "imap.mail.yahoo.com", "smtp.mail.yahoo.com"
	}

	// Apple iCloud
	if strings.Contains(mxHost, "icloud.com") || strings.Contains(mxHost, "me.com") || strings.Contains(mxHost, "mac.com") {
		return "imap.mail.me.com", "smtp.mail.me.com"
	}

	// Zoho
	if strings.Contains(mxHost, "zoho.com") || strings.Contains(mxHost, "zohomail.com") {
		return "imap.zoho.com", "smtp.zoho.com"
	}

	// ProtonMail (uses custom bridge, but we can detect it)
	if strings.Contains(mxHost, "protonmail.com") || strings.Contains(mxHost, "protonmail.ch") {
		// ProtonMail requires bridge software, return empty to fall back to manual config
		return "", ""
	}

	return "", ""
}

// deriveServerNamesFromMX attempts to derive IMAP/SMTP server names from MX record
func (v *ServerValidator) deriveServerNamesFromMX(mxHost, domain string) (imapHost, smtpHost string) {
	// Try to extract the base domain from the MX record
	var baseDomain string

	// Common patterns in MX records:
	// mail.example.com -> example.com
	// mx1.example.com -> example.com
	// example-com.mail.protection.outlook.com -> use original domain
	// aspmx.l.google.com -> use original domain (Google Workspace)

	if strings.Contains(mxHost, "protection.outlook.com") ||
		strings.Contains(mxHost, "google.com") ||
		strings.Contains(mxHost, "yahoodns.net") {
		// These are hosted services, use the original domain
		baseDomain = domain
	} else if strings.HasPrefix(mxHost, "mail.") {
		// mail.example.com -> example.com
		baseDomain = mxHost[5:]
	} else if strings.HasPrefix(mxHost, "mx.") || strings.HasPrefix(mxHost, "mx1.") || strings.HasPrefix(mxHost, "mx2.") {
		// mx.example.com -> example.com, mx1.example.com -> example.com
		parts := strings.Split(mxHost, ".")
		if len(parts) >= 2 {
			baseDomain = strings.Join(parts[1:], ".")
		}
	} else {
		// Use the MX host as-is, might be the mail server itself
		baseDomain = mxHost
	}

	// Generate IMAP and SMTP server names
	// Try the MX host first (it might be the mail server)
	imapCandidates := []string{
		mxHost,
		"imap." + baseDomain,
		"mail." + baseDomain,
	}

	smtpCandidates := []string{
		mxHost,
		"smtp." + baseDomain,
		"mail." + baseDomain,
	}

	// Return the first candidates (we'll validate them later)
	return imapCandidates[0], smtpCandidates[0]
}
