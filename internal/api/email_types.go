package api

import "github.com/coldforge/coldforge-email/internal/encryption"

// EncryptionModeRequest represents the encryption mode from API requests
type EncryptionModeRequest string

const (
	// EncryptionModeNone - no encryption requested
	EncryptionModeNone EncryptionModeRequest = "none"

	// EncryptionModeServer - server-side encryption via NIP-46
	// Server uses the user's bunker to encrypt the message
	EncryptionModeServer EncryptionModeRequest = "server"

	// EncryptionModeClient - client-side encryption via NIP-07
	// Client sends pre-encrypted ciphertext, server stores it as-is
	EncryptionModeClient EncryptionModeRequest = "client"
)

// SendEmailRequestV2 is the enhanced request body for sending email.
// Supports both NIP-46 (server-side) and NIP-07 (client-side) encryption.
type SendEmailRequestV2 struct {
	// To is the recipient's email address (required)
	To []string `json:"to"`

	// CC recipients (optional)
	CC []string `json:"cc,omitempty"`

	// BCC recipients (optional)
	BCC []string `json:"bcc,omitempty"`

	// Subject line (required)
	Subject string `json:"subject"`

	// Body is the email body (required for unencrypted or server-encrypted)
	// If EncryptionMode is "client", this should be empty
	Body string `json:"body,omitempty"`

	// HTMLBody is an optional HTML version of the body
	HTMLBody string `json:"html_body,omitempty"`

	// EncryptionMode specifies how encryption should be handled
	// "none" - send in plaintext
	// "server" - server encrypts using NIP-46 bunker
	// "client" - client has pre-encrypted using NIP-07
	EncryptionMode EncryptionModeRequest `json:"encryption_mode"`

	// PreEncryptedBody is the ciphertext when EncryptionMode is "client"
	// This is the NIP-44 encrypted content from the client's browser extension
	PreEncryptedBody string `json:"pre_encrypted_body,omitempty"`

	// RecipientPubkeys maps email addresses to their Nostr pubkeys
	// Required when encryption is requested (either mode)
	// Client should discover these via NIP-05 before sending
	RecipientPubkeys map[string]string `json:"recipient_pubkeys,omitempty"`

	// InReplyTo is the Message-ID of the email being replied to (optional)
	InReplyTo string `json:"in_reply_to,omitempty"`

	// References is a list of Message-IDs for threading (optional)
	References []string `json:"references,omitempty"`
}

// SendEmailResponseV2 is the response for the enhanced send email endpoint
type SendEmailResponseV2 struct {
	// Status indicates the overall result
	Status string `json:"status"`

	// MessageID is the assigned message ID
	MessageID string `json:"message_id,omitempty"`

	// EncryptionMode that was used
	EncryptionMode EncryptionModeRequest `json:"encryption_mode"`

	// RecipientResults shows per-recipient status
	RecipientResults []RecipientSendResult `json:"recipient_results,omitempty"`

	// Error message if status is "error"
	Error string `json:"error,omitempty"`
}

// RecipientSendResult contains the send status for a single recipient
type RecipientSendResult struct {
	// Email is the recipient's email address
	Email string `json:"email"`

	// Success indicates if delivery was accepted for this recipient
	Success bool `json:"success"`

	// Encrypted indicates if the message to this recipient was encrypted
	Encrypted bool `json:"encrypted"`

	// Error message if this recipient failed
	Error string `json:"error,omitempty"`
}

// GetEmailResponseV2 is the enhanced response for retrieving an email
type GetEmailResponseV2 struct {
	ID          string `json:"id"`
	From        string `json:"from"`
	To          []string `json:"to"`
	CC          []string `json:"cc,omitempty"`
	Subject     string `json:"subject"`

	// Body contains plaintext if:
	// - Message was not encrypted
	// - Message was server-encrypted and user has NIP-46 signer
	// Empty if message requires client-side decryption
	Body string `json:"body,omitempty"`

	// HTMLBody contains HTML version if available
	HTMLBody string `json:"html_body,omitempty"`

	// EncryptedBody contains ciphertext when:
	// - Message was client-side encrypted (NIP-07)
	// - Message was server-encrypted but user can't decrypt (no NIP-46)
	// Client should decrypt this using NIP-07
	EncryptedBody string `json:"encrypted_body,omitempty"`

	// IsEncrypted indicates if the message is/was encrypted
	IsEncrypted bool `json:"is_encrypted"`

	// EncryptionMode indicates how the message was encrypted
	EncryptionMode encryption.EncryptionMode `json:"encryption_mode,omitempty"`

	// RequiresClientDecryption is true if the client needs to decrypt
	// using NIP-07. Body will be empty and EncryptedBody will contain ciphertext.
	RequiresClientDecryption bool `json:"requires_client_decryption"`

	// SenderPubkey for decryption (hex format)
	SenderPubkey string `json:"sender_pubkey,omitempty"`

	// Metadata
	MessageID  string   `json:"message_id,omitempty"`
	InReplyTo  string   `json:"in_reply_to,omitempty"`
	References []string `json:"references,omitempty"`

	CreatedAt string `json:"created_at"`
	ReadAt    string `json:"read_at,omitempty"`
	Folder    string `json:"folder"`
}

// EncryptionCapabilityResponse describes a user's encryption capabilities
type EncryptionCapabilityResponse struct {
	// Npub is the user's Nostr public key (hex)
	Npub string `json:"npub"`

	// HasNIP46 indicates if the user has an active NIP-46 bunker connection
	// If true, server can encrypt/decrypt on their behalf
	HasNIP46 bool `json:"has_nip46"`

	// PreferredMode is the recommended encryption mode for this user
	PreferredMode encryption.EncryptionMode `json:"preferred_mode"`

	// CanServerEncrypt indicates server can encrypt for this user
	CanServerEncrypt bool `json:"can_server_encrypt"`

	// CanServerDecrypt indicates server can decrypt for this user
	CanServerDecrypt bool `json:"can_server_decrypt"`
}

// DecryptRequestV2 is a request to decrypt an email body client-side
type DecryptRequestV2 struct {
	// EmailID is the email to get decryption info for
	EmailID string `json:"email_id"`
}

// DecryptResponseV2 provides data needed for client-side decryption
type DecryptResponseV2 struct {
	// Ciphertext to decrypt
	Ciphertext string `json:"ciphertext"`

	// SenderPubkey for NIP-44 decryption
	SenderPubkey string `json:"sender_pubkey"`

	// Algorithm identifier (e.g., "nip44-v2")
	Algorithm string `json:"algorithm"`
}

// RegisterAddressRequest is the request to register a unified address
type RegisterAddressRequest struct {
	// LocalPart is the desired username (e.g., "alice" for alice@coldforge.xyz)
	LocalPart string `json:"local_part"`

	// DisplayName is the user's display name
	DisplayName string `json:"display_name"`
}

// RegisterAddressResponse is the response after registering an address
type RegisterAddressResponse struct {
	// Email is the full email address (alice@coldforge.xyz)
	Email string `json:"email"`

	// LocalPart is the username portion
	LocalPart string `json:"local_part"`

	// Verified indicates if the address is verified
	Verified bool `json:"verified"`
}

// UnifiedAddressResponse describes a user's unified address
type UnifiedAddressResponse struct {
	// Npub is the user's Nostr public key (hex)
	Npub string `json:"npub"`

	// Email is their unified address (alice@coldforge.xyz)
	Email string `json:"email,omitempty"`

	// LocalPart is the username portion
	LocalPart string `json:"local_part,omitempty"`

	// DisplayName is the user's display name
	DisplayName string `json:"display_name,omitempty"`

	// HasAddress indicates if the user has registered an address
	HasAddress bool `json:"has_address"`

	// Verified indicates if the address is verified
	Verified bool `json:"verified"`
}
