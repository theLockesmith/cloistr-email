package transport

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/smtp"
	"sort"
	"strings"
	"time"

	"git.coldforge.xyz/coldforge/cloistr-email/internal/encryption"
	"git.coldforge.xyz/coldforge/cloistr-email/internal/signing"
	"go.uber.org/zap"
)

// SMTPConfig contains SMTP server configuration
type SMTPConfig struct {
	// Host is the SMTP server hostname
	Host string

	// Port is the SMTP server port (typically 587 for submission)
	Port int

	// Username for SMTP authentication
	Username string

	// Password for SMTP authentication
	Password string

	// UseTLS enables STARTTLS
	UseTLS bool

	// InsecureSkipVerify skips TLS certificate verification (for testing)
	InsecureSkipVerify bool

	// Timeout for SMTP operations
	Timeout time.Duration

	// LocalName is the hostname to use in HELO/EHLO (defaults to "localhost")
	LocalName string
}

// SMTPTransport implements Transport using SMTP submission to Stalwart
type SMTPTransport struct {
	config    *SMTPConfig
	encryptor *encryption.EmailEncryptor
	logger    *zap.Logger
}

// NewSMTPTransport creates a new SMTP transport
func NewSMTPTransport(config *SMTPConfig, encryptor *encryption.EmailEncryptor, logger *zap.Logger) *SMTPTransport {
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	if config.LocalName == "" {
		config.LocalName = "localhost"
	}

	return &SMTPTransport{
		config:    config,
		encryptor: encryptor,
		logger:    logger,
	}
}

func (t *SMTPTransport) Type() TransportType {
	return TransportSMTP
}

// Send delivers a message via SMTP
func (t *SMTPTransport) Send(ctx context.Context, msg *Message) (*DeliveryResult, error) {
	t.logger.Debug("Sending email via SMTP",
		zap.String("from", msg.FromAddress),
		zap.Int("recipients", len(msg.ToAddresses)+len(msg.CCAddresses)+len(msg.BCCAddresses)))

	result := &DeliveryResult{
		Transport: TransportSMTP,
	}

	// Collect all recipients
	allRecipients := make([]string, 0, len(msg.ToAddresses)+len(msg.CCAddresses)+len(msg.BCCAddresses))
	allRecipients = append(allRecipients, msg.ToAddresses...)
	allRecipients = append(allRecipients, msg.CCAddresses...)
	allRecipients = append(allRecipients, msg.BCCAddresses...)

	if len(allRecipients) == 0 {
		return nil, fmt.Errorf("no recipients specified")
	}

	// Build the raw email message
	rawEmail, err := t.buildRawEmail(ctx, msg)
	if err != nil {
		result.Error = err
		return result, err
	}

	// Connect and send
	if err := t.sendViaSMTP(ctx, msg.FromAddress, allRecipients, rawEmail); err != nil {
		result.Error = err
		// Mark all recipients as failed
		for _, addr := range allRecipients {
			result.Recipients = append(result.Recipients, RecipientResult{
				Address: addr,
				Success: false,
				Error:   err,
			})
		}
		return result, err
	}

	// Success
	result.Success = true
	result.MessageID = msg.MessageID
	for _, addr := range allRecipients {
		encrypted := msg.IsPreEncrypted || (msg.EncryptionRequested && msg.RecipientPubkeys[addr] != "")
		result.Recipients = append(result.Recipients, RecipientResult{
			Address:   addr,
			Success:   true,
			Encrypted: encrypted,
		})
	}

	t.logger.Info("Email sent successfully via SMTP",
		zap.String("message_id", msg.MessageID),
		zap.Int("recipients", len(allRecipients)))

	return result, nil
}

// buildRawEmail constructs the RFC 5322 formatted email
func (t *SMTPTransport) buildRawEmail(ctx context.Context, msg *Message) ([]byte, error) {
	var sb strings.Builder

	// Generate Message-ID if not provided
	messageID := msg.MessageID
	if messageID == "" {
		messageID = t.generateMessageID(msg.FromAddress)
	}
	msg.MessageID = messageID // Store for later use

	// Build headers map for signing
	dateStr := time.Now().UTC().Format(time.RFC1123Z)
	headersForSigning := map[string]string{
		"message-id": messageID,
		"date":       dateStr,
		"from":       msg.FromAddress,
		"to":         strings.Join(msg.ToAddresses, ", "),
		"subject":    msg.Subject,
	}

	if len(msg.CCAddresses) > 0 {
		headersForSigning["cc"] = strings.Join(msg.CCAddresses, ", ")
	}

	if msg.InReplyTo != "" {
		headersForSigning["in-reply-to"] = "<" + msg.InReplyTo + ">"
	}

	if len(msg.References) > 0 {
		refs := make([]string, len(msg.References))
		for i, ref := range msg.References {
			refs[i] = "<" + ref + ">"
		}
		headersForSigning["references"] = strings.Join(refs, " ")
	}

	// Standard headers
	sb.WriteString(fmt.Sprintf("Message-ID: <%s>\r\n", messageID))
	sb.WriteString(fmt.Sprintf("Date: %s\r\n", dateStr))
	sb.WriteString(fmt.Sprintf("From: %s\r\n", msg.FromAddress))
	sb.WriteString(fmt.Sprintf("To: %s\r\n", strings.Join(msg.ToAddresses, ", ")))

	if len(msg.CCAddresses) > 0 {
		sb.WriteString(fmt.Sprintf("Cc: %s\r\n", strings.Join(msg.CCAddresses, ", ")))
	}

	// Note: BCC recipients are not included in headers (that's how BCC works)

	sb.WriteString(fmt.Sprintf("Subject: %s\r\n", msg.Subject))

	// Threading headers
	if msg.InReplyTo != "" {
		sb.WriteString(fmt.Sprintf("In-Reply-To: <%s>\r\n", msg.InReplyTo))
	}
	if len(msg.References) > 0 {
		refs := make([]string, len(msg.References))
		for i, ref := range msg.References {
			refs[i] = "<" + ref + ">"
		}
		sb.WriteString(fmt.Sprintf("References: %s\r\n", strings.Join(refs, " ")))
	}

	// Custom headers
	for k, v := range msg.Headers {
		sb.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
	}

	// MIME headers
	sb.WriteString("MIME-Version: 1.0\r\n")

	// Determine body content and encryption
	body := msg.Body
	isEncrypted := msg.IsPreEncrypted

	// Handle server-side encryption if requested and not pre-encrypted
	if msg.EncryptionRequested && !msg.IsPreEncrypted && t.encryptor != nil {
		// For now, we encrypt for the first recipient with a known pubkey
		// A more sophisticated approach would create per-recipient copies
		for _, addr := range msg.ToAddresses {
			if pubkey, ok := msg.RecipientPubkeys[addr]; ok && pubkey != "" {
				encryptedBody, err := t.encryptor.EncryptEmailBody(ctx, msg.SenderPubkey, pubkey, msg.Body)
				if err != nil {
					t.logger.Warn("Failed to encrypt email body, sending unencrypted",
						zap.String("recipient", addr),
						zap.Error(err))
					continue
				}
				body = encryptedBody
				isEncrypted = true

				// Add encryption headers
				sb.WriteString(fmt.Sprintf("%s: true\r\n", encryption.HeaderNostrEncrypted))
				sb.WriteString(fmt.Sprintf("%s: %s\r\n", encryption.HeaderNostrSender, msg.SenderPubkey))
				sb.WriteString(fmt.Sprintf("%s: %s\r\n", encryption.HeaderNostrRecipient, pubkey))
				sb.WriteString(fmt.Sprintf("%s: %s\r\n", encryption.HeaderNostrAlgorithm, encryption.AlgorithmNIP44))
				break
			}
		}
	} else if msg.IsPreEncrypted {
		// Pre-encrypted (NIP-07 client-side encryption)
		sb.WriteString(fmt.Sprintf("%s: true\r\n", encryption.HeaderNostrEncrypted))
		if msg.SenderPubkey != "" {
			sb.WriteString(fmt.Sprintf("%s: %s\r\n", encryption.HeaderNostrSender, msg.SenderPubkey))
		}
		// For pre-encrypted, recipient pubkeys should be in the message
		for _, addr := range msg.ToAddresses {
			if pubkey, ok := msg.RecipientPubkeys[addr]; ok && pubkey != "" {
				sb.WriteString(fmt.Sprintf("%s: %s\r\n", encryption.HeaderNostrRecipient, pubkey))
				break
			}
		}
		sb.WriteString(fmt.Sprintf("%s: %s\r\n", encryption.HeaderNostrAlgorithm, encryption.AlgorithmNIP44))
	}

	// Sign the email if a signer is provided (RFC-002)
	if msg.Signer != nil {
		sigHeaders, err := t.signEmail(ctx, headersForSigning, body, msg.Signer)
		if err != nil {
			t.logger.Warn("Failed to sign email, sending unsigned",
				zap.Error(err))
		} else if sigHeaders != nil {
			for k, v := range sigHeaders {
				sb.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
			}
		}
	}

	// Content type and encoding
	if msg.HTMLBody != "" && !isEncrypted {
		// Multipart for HTML + plain text (only if not encrypted)
		boundary := t.generateBoundary()
		sb.WriteString(fmt.Sprintf("Content-Type: multipart/alternative; boundary=\"%s\"\r\n", boundary))
		sb.WriteString("\r\n")

		// Plain text part
		sb.WriteString(fmt.Sprintf("--%s\r\n", boundary))
		sb.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
		sb.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
		sb.WriteString("\r\n")
		sb.WriteString(body)
		sb.WriteString("\r\n")

		// HTML part
		sb.WriteString(fmt.Sprintf("--%s\r\n", boundary))
		sb.WriteString("Content-Type: text/html; charset=utf-8\r\n")
		sb.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
		sb.WriteString("\r\n")
		sb.WriteString(msg.HTMLBody)
		sb.WriteString("\r\n")

		sb.WriteString(fmt.Sprintf("--%s--\r\n", boundary))
	} else {
		// Simple single-part message
		sb.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
		sb.WriteString("Content-Transfer-Encoding: base64\r\n")
		sb.WriteString("\r\n")
		sb.WriteString(base64.StdEncoding.EncodeToString([]byte(body)))
	}

	return []byte(sb.String()), nil
}

// sendViaSMTP handles the SMTP connection and message submission
func (t *SMTPTransport) sendViaSMTP(ctx context.Context, from string, to []string, message []byte) error {
	addr := fmt.Sprintf("%s:%d", t.config.Host, t.config.Port)

	t.logger.Debug("Connecting to SMTP server", zap.String("addr", addr))

	// Create connection with timeout
	dialer := &net.Dialer{Timeout: t.config.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to connect to SMTP server: %w", err)
	}

	// Create SMTP client
	client, err := smtp.NewClient(conn, t.config.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to create SMTP client: %w", err)
	}
	defer client.Close()

	// Set local hostname
	if err := client.Hello(t.config.LocalName); err != nil {
		return fmt.Errorf("HELO failed: %w", err)
	}

	// STARTTLS if configured
	if t.config.UseTLS {
		if ok, _ := client.Extension("STARTTLS"); ok {
			tlsConfig := &tls.Config{
				ServerName:         t.config.Host,
				InsecureSkipVerify: t.config.InsecureSkipVerify,
			}
			if err := client.StartTLS(tlsConfig); err != nil {
				return fmt.Errorf("STARTTLS failed: %w", err)
			}
		}
	}

	// Authenticate if credentials provided
	if t.config.Username != "" && t.config.Password != "" {
		auth := smtp.PlainAuth("", t.config.Username, t.config.Password, t.config.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("authentication failed: %w", err)
		}
	}

	// Set sender
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM failed: %w", err)
	}

	// Set recipients
	for _, recipient := range to {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("RCPT TO failed for %s: %w", recipient, err)
		}
	}

	// Send message data
	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("DATA command failed: %w", err)
	}

	_, err = wc.Write(message)
	if err != nil {
		wc.Close()
		return fmt.Errorf("failed to write message: %w", err)
	}

	if err := wc.Close(); err != nil {
		return fmt.Errorf("failed to close data writer: %w", err)
	}

	// Quit gracefully
	client.Quit()

	return nil
}

// CanDeliver checks if this transport can deliver to the given address
// SMTP can deliver to any valid email address
func (t *SMTPTransport) CanDeliver(ctx context.Context, address string) (bool, error) {
	// Basic email format validation
	if !strings.Contains(address, "@") {
		return false, nil
	}
	parts := strings.Split(address, "@")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false, nil
	}
	return true, nil
}

// Health checks if the SMTP server is reachable
func (t *SMTPTransport) Health(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", t.config.Host, t.config.Port)

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("cannot connect to SMTP server: %w", err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, t.config.Host)
	if err != nil {
		return fmt.Errorf("SMTP handshake failed: %w", err)
	}
	defer client.Close()

	return client.Noop()
}

// generateMessageID creates a unique message ID
func (t *SMTPTransport) generateMessageID(from string) string {
	domain := "coldforge.xyz"
	if parts := strings.Split(from, "@"); len(parts) == 2 {
		domain = parts[1]
	}
	return fmt.Sprintf("%d.%s@%s", time.Now().UnixNano(), randomString(8), domain)
}

// generateBoundary creates a MIME boundary string
func (t *SMTPTransport) generateBoundary() string {
	return fmt.Sprintf("----=_Part_%d_%s", time.Now().UnixNano(), randomString(8))
}

// randomString generates a random alphanumeric string
func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[time.Now().UnixNano()%int64(len(letters))]
		time.Sleep(time.Nanosecond) // Ensure different values
	}
	return string(b)
}

// signEmail signs the email using the provided signer and returns the signature headers
func (t *SMTPTransport) signEmail(ctx context.Context, headers map[string]string, body string, signer signing.Signer) (map[string]string, error) {
	if signer == nil {
		return nil, nil
	}

	// Default headers to sign (RFC-002)
	defaultSignedHeaders := []string{"from", "to", "date", "message-id", "subject"}
	optionalSignedHeaders := []string{"cc", "in-reply-to", "references"}

	// Determine which headers to sign
	var headersToSign []string
	for _, h := range defaultSignedHeaders {
		if _, ok := headers[h]; ok {
			headersToSign = append(headersToSign, h)
		}
	}
	for _, h := range optionalSignedHeaders {
		if _, ok := headers[h]; ok {
			headersToSign = append(headersToSign, h)
		}
	}

	// Sort for deterministic ordering
	sort.Strings(headersToSign)

	// Canonicalize headers
	var parts []string
	for _, name := range headersToSign {
		if value, ok := headers[name]; ok {
			canonicalName := strings.ToLower(name)
			canonicalValue := strings.TrimSpace(value)
			canonicalValue = strings.ReplaceAll(canonicalValue, "\r\n", "\n")
			canonicalValue = strings.ReplaceAll(canonicalValue, "\r", "\n")
			parts = append(parts, fmt.Sprintf("%s:%s", canonicalName, canonicalValue))
		}
	}
	headerBlock := strings.Join(parts, "\n")

	// Canonicalize body
	bodyCanonical := t.canonicalizeBody(body)

	// Hash the body
	bodyHash := sha256.Sum256([]byte(bodyCanonical))

	// Combine headers and body hash
	canonical := append([]byte(headerBlock+"\n"), bodyHash[:]...)

	// Sign
	sig, err := signer.Sign(ctx, canonical)
	if err != nil {
		return nil, fmt.Errorf("failed to sign email: %w", err)
	}

	t.logger.Debug("Email signed successfully",
		zap.String("pubkey", truncatePubkey(signer.PublicKey())),
		zap.Strings("signed_headers", headersToSign))

	return map[string]string{
		signing.HeaderNostrPubkey:        signer.PublicKey(),
		signing.HeaderNostrSig:           sig,
		signing.HeaderNostrSignedHeaders: strings.Join(headersToSign, ";"),
	}, nil
}

// canonicalizeBody applies RFC-002 canonicalization to email body
func (t *SMTPTransport) canonicalizeBody(body string) string {
	// Normalize line endings
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")

	// Trim whitespace at end of each line
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}

	// Remove trailing blank lines
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	return strings.Join(lines, "\n")
}

// truncatePubkey truncates a pubkey for logging
func truncatePubkey(pubkey string) string {
	if len(pubkey) <= 16 {
		return pubkey
	}
	return pubkey[:16] + "..."
}
