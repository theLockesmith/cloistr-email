// Package identity handles unified address management and validation.
// It enforces that users must have a cloistr.xyz address to send email,
// and manages the npub ↔ email address mapping.
package identity

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"go.uber.org/zap"
)

// Domain is the email domain managed by this service
const Domain = "cloistr.xyz"

// AddressType indicates whether an address is internal or external
type AddressType int

const (
	// AddressTypeInternal is a @cloistr.xyz address
	AddressTypeInternal AddressType = iota

	// AddressTypeExternal is any other domain
	AddressTypeExternal
)

// UnifiedAddress represents the connection between an npub and an email address.
// This is the core identity concept - one npub maps to one @cloistr.xyz address.
type UnifiedAddress struct {
	// Npub is the user's Nostr public key (hex format)
	Npub string

	// LocalPart is the username portion (e.g., "alice" in alice@cloistr.xyz)
	LocalPart string

	// Email is the full email address (alice@cloistr.xyz)
	Email string

	// DisplayName is the user's chosen display name
	DisplayName string

	// Verified indicates the npub has been verified via NIP-46 authentication
	Verified bool
}

// ExternalRecipient represents an external email address that may have a known npub
type ExternalRecipient struct {
	// Email is the external email address
	Email string

	// Npub is the recipient's npub if discovered via NIP-05 (empty if unknown)
	Npub string

	// DiscoveryMethod indicates how the npub was discovered
	DiscoveryMethod string // "nip05", "manual", "none"

	// SupportsEncryption indicates if we can encrypt messages to this recipient
	SupportsEncryption bool
}

// AddressStore defines the interface for persisting unified addresses.
// This will be implemented by the PostgreSQL storage layer.
type AddressStore interface {
	// GetByNpub retrieves a unified address by npub
	GetByNpub(ctx context.Context, npub string) (*UnifiedAddress, error)

	// GetByEmail retrieves a unified address by email
	GetByEmail(ctx context.Context, email string) (*UnifiedAddress, error)

	// Create creates a new unified address mapping
	Create(ctx context.Context, addr *UnifiedAddress) error

	// Update updates an existing unified address
	Update(ctx context.Context, addr *UnifiedAddress) error

	// LocalPartExists checks if a local part is already taken
	LocalPartExists(ctx context.Context, localPart string) (bool, error)
}

// NIP05Resolver looks up npubs for external addresses
type NIP05Resolver interface {
	// ResolvePubkey looks up the npub for an email address via NIP-05
	ResolvePubkey(ctx context.Context, email string) (string, error)
}

// Service manages unified addresses and validates email permissions
type Service struct {
	store    AddressStore
	resolver NIP05Resolver
	logger   *zap.Logger
}

// NewService creates a new identity service
func NewService(store AddressStore, resolver NIP05Resolver, logger *zap.Logger) *Service {
	return &Service{
		store:    store,
		resolver: resolver,
		logger:   logger,
	}
}

// ValidateSender checks if a sender is allowed to send email.
// Returns an error if the sender doesn't have a verified unified address.
func (s *Service) ValidateSender(ctx context.Context, npub string) (*UnifiedAddress, error) {
	addr, err := s.store.GetByNpub(ctx, npub)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup sender: %w", err)
	}

	if addr == nil {
		return nil, ErrNoUnifiedAddress
	}

	if !addr.Verified {
		return nil, ErrAddressNotVerified
	}

	return addr, nil
}

// ValidateFromAddress ensures the sender is using their own address.
// Users can only send from their unified address, not arbitrary addresses.
func (s *Service) ValidateFromAddress(ctx context.Context, npub, fromAddress string) error {
	addr, err := s.ValidateSender(ctx, npub)
	if err != nil {
		return err
	}

	if !strings.EqualFold(addr.Email, fromAddress) {
		return ErrFromAddressMismatch
	}

	return nil
}

// ResolveRecipient resolves an email address to get encryption capability info.
// For internal addresses, looks up the unified address.
// For external addresses, attempts NIP-05 discovery.
func (s *Service) ResolveRecipient(ctx context.Context, email string) (*ExternalRecipient, error) {
	addrType := ClassifyAddress(email)

	recipient := &ExternalRecipient{
		Email:           email,
		DiscoveryMethod: "none",
	}

	if addrType == AddressTypeInternal {
		// Internal address - look up in our store
		addr, err := s.store.GetByEmail(ctx, email)
		if err != nil {
			return nil, fmt.Errorf("failed to lookup recipient: %w", err)
		}

		if addr != nil {
			recipient.Npub = addr.Npub
			recipient.DiscoveryMethod = "internal"
			recipient.SupportsEncryption = true
		}
	} else {
		// External address - try NIP-05 discovery
		if s.resolver != nil {
			npub, err := s.resolver.ResolvePubkey(ctx, email)
			if err == nil && npub != "" {
				recipient.Npub = npub
				recipient.DiscoveryMethod = "nip05"
				recipient.SupportsEncryption = true
			}
			// Log but don't fail if NIP-05 lookup fails
			if err != nil {
				s.logger.Debug("NIP-05 lookup failed for recipient",
					zap.String("email", email),
					zap.Error(err))
			}
		}
	}

	return recipient, nil
}

// ResolveRecipients resolves multiple email addresses
func (s *Service) ResolveRecipients(ctx context.Context, emails []string) (map[string]*ExternalRecipient, error) {
	results := make(map[string]*ExternalRecipient)

	for _, email := range emails {
		recipient, err := s.ResolveRecipient(ctx, email)
		if err != nil {
			return nil, err
		}
		results[email] = recipient
	}

	return results, nil
}

// RegisterAddress creates a new unified address for a user.
// This is called after successful NIP-46 authentication.
func (s *Service) RegisterAddress(ctx context.Context, npub, localPart, displayName string) (*UnifiedAddress, error) {
	// Validate local part format
	if err := ValidateLocalPart(localPart); err != nil {
		return nil, err
	}

	// Check if npub already has an address
	existing, err := s.store.GetByNpub(ctx, npub)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing address: %w", err)
	}
	if existing != nil {
		return nil, ErrNpubAlreadyRegistered
	}

	// Check if local part is taken
	taken, err := s.store.LocalPartExists(ctx, localPart)
	if err != nil {
		return nil, fmt.Errorf("failed to check local part: %w", err)
	}
	if taken {
		return nil, ErrLocalPartTaken
	}

	// Create the unified address
	addr := &UnifiedAddress{
		Npub:        npub,
		LocalPart:   strings.ToLower(localPart),
		Email:       fmt.Sprintf("%s@%s", strings.ToLower(localPart), Domain),
		DisplayName: displayName,
		Verified:    true, // Verified because they authenticated with NIP-46
	}

	if err := s.store.Create(ctx, addr); err != nil {
		return nil, fmt.Errorf("failed to create address: %w", err)
	}

	s.logger.Info("Registered unified address",
		zap.String("npub", npub[:16]+"..."),
		zap.String("email", addr.Email))

	return addr, nil
}

// ClassifyAddress determines if an email is internal or external
func ClassifyAddress(email string) AddressType {
	email = strings.ToLower(email)
	if strings.HasSuffix(email, "@"+Domain) {
		return AddressTypeInternal
	}
	return AddressTypeExternal
}

// ValidateLocalPart validates the local part of an email address
func ValidateLocalPart(localPart string) error {
	if localPart == "" {
		return ErrInvalidLocalPart
	}

	// Length constraints
	if len(localPart) < 3 {
		return fmt.Errorf("%w: must be at least 3 characters", ErrInvalidLocalPart)
	}
	if len(localPart) > 32 {
		return fmt.Errorf("%w: must be at most 32 characters", ErrInvalidLocalPart)
	}

	// Must start with a letter
	if !regexp.MustCompile(`^[a-zA-Z]`).MatchString(localPart) {
		return fmt.Errorf("%w: must start with a letter", ErrInvalidLocalPart)
	}

	// Only alphanumeric, dots, underscores, and hyphens
	if !regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9._-]*$`).MatchString(localPart) {
		return fmt.Errorf("%w: can only contain letters, numbers, dots, underscores, and hyphens", ErrInvalidLocalPart)
	}

	// No consecutive dots
	if strings.Contains(localPart, "..") {
		return fmt.Errorf("%w: cannot contain consecutive dots", ErrInvalidLocalPart)
	}

	// Reserved names
	reserved := []string{"admin", "root", "postmaster", "abuse", "noreply", "no-reply", "support", "help", "info", "webmaster", "hostmaster", "mailer-daemon"}
	for _, r := range reserved {
		if strings.EqualFold(localPart, r) {
			return fmt.Errorf("%w: '%s' is reserved", ErrInvalidLocalPart, localPart)
		}
	}

	return nil
}

// ValidateEmailFormat validates a full email address format
func ValidateEmailFormat(email string) error {
	if email == "" {
		return ErrInvalidEmail
	}

	// Basic format check
	emailRegex := regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)
	if !emailRegex.MatchString(email) {
		return ErrInvalidEmail
	}

	return nil
}
