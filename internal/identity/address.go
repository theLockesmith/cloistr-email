// Package identity handles unified address management and validation.
// It enforces that users must have a cloistr.xyz address to send email,
// and manages the npub ↔ email address mapping.
package identity

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

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
// An npub may own SEVERAL addresses (one primary plus aliases); all of them
// deliver into that npub's single mailbox, and any of them may be used as a
// send-from identity.
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

	// IsPrimary marks the canonical send-from address for this npub. Exactly
	// one owned address is primary; the rest are aliases.
	IsPrimary bool
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
	// GetByNpub retrieves the npub's PRIMARY unified address
	GetByNpub(ctx context.Context, npub string) (*UnifiedAddress, error)

	// ListByNpub retrieves every ACTIVE address owned by the npub (primary
	// first). These are the addresses the npub may legitimately send from.
	ListByNpub(ctx context.Context, npub string) ([]*UnifiedAddress, error)

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

// AddressVerifier verifies address ownership via cloistr-me internal API.
// This is optional - if nil, ownership is assumed valid (for backwards compatibility).
type AddressVerifier interface {
	VerifyAddressOwnership(ctx context.Context, pubkey, address string) (bool, error)
}

// Service manages unified addresses and validates email permissions
type Service struct {
	store    AddressStore
	resolver NIP05Resolver
	verifier AddressVerifier
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

// WithVerifier sets the address verifier for ownership checks via cloistr-me
func (s *Service) WithVerifier(verifier AddressVerifier) *Service {
	s.verifier = verifier
	return s
}

// ValidateSender checks if a sender is allowed to send email.
// Returns an error if the sender doesn't have a verified unified address.
// If a verifier is configured, also validates ownership via cloistr-me API.
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

	// If verifier is configured, also verify ownership via cloistr-me API
	// This ensures cloistr-me (authoritative source) confirms the mapping
	if s.verifier != nil {
		owned, err := s.verifier.VerifyAddressOwnership(ctx, npub, addr.Email)
		if err != nil {
			s.logger.Warn("cloistr-me verification failed, allowing based on local data",
				zap.String("email", addr.Email),
				zap.Error(err))
			// Don't fail if cloistr-me is unreachable - fall back to local data
		} else if !owned {
			s.logger.Warn("cloistr-me reports address not owned by pubkey",
				zap.String("email", addr.Email),
				zap.String("npub", npub[:16]+"..."))
			return nil, ErrAddressOwnershipMismatch
		}
	}

	return addr, nil
}

// ResolveFromAddress validates that npub may send as fromAddress and returns
// the matching owned address.
//
// An empty fromAddress means "use my primary". Otherwise the address must be
// one the npub actually owns — any active alias qualifies, not just the
// primary — which is what makes send-from-alias possible.
func (s *Service) ResolveFromAddress(ctx context.Context, npub, fromAddress string) (*UnifiedAddress, error) {
	// No explicit From: fall back to the primary address (and its full
	// verification path, including the cloistr-me ownership cross-check).
	if strings.TrimSpace(fromAddress) == "" {
		return s.ValidateSender(ctx, npub)
	}

	owned, err := s.store.ListByNpub(ctx, npub)
	if err != nil {
		return nil, fmt.Errorf("failed to list sender addresses: %w", err)
	}
	if len(owned) == 0 {
		return nil, ErrNoUnifiedAddress
	}

	var match *UnifiedAddress
	for _, addr := range owned {
		if strings.EqualFold(addr.Email, fromAddress) {
			match = addr
			break
		}
	}
	if match == nil {
		return nil, ErrFromAddressMismatch
	}
	if !match.Verified {
		return nil, ErrAddressNotVerified
	}

	// Cross-check ownership with cloistr-me (authoritative) when configured.
	if s.verifier != nil {
		owned, err := s.verifier.VerifyAddressOwnership(ctx, npub, match.Email)
		if err != nil {
			s.logger.Warn("cloistr-me verification failed, allowing based on local data",
				zap.String("email", match.Email),
				zap.Error(err))
		} else if !owned {
			s.logger.Warn("cloistr-me reports address not owned by pubkey",
				zap.String("email", match.Email),
				zap.String("npub", npub[:16]+"..."))
			return nil, ErrAddressOwnershipMismatch
		}
	}

	return match, nil
}

// ValidateFromAddress ensures the sender may send as fromAddress.
func (s *Service) ValidateFromAddress(ctx context.Context, npub, fromAddress string) error {
	_, err := s.ResolveFromAddress(ctx, npub, fromAddress)
	return err
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

// ClassifyAddress determines if an email is internal (a served domain) or
// external. Internal = the address' domain is in the served-domains set.
func ClassifyAddress(email string) AddressType {
	email = strings.ToLower(email)
	if at := strings.LastIndex(email, "@"); at >= 0 && isServedDomain(email[at+1:]) {
		return AddressTypeInternal
	}
	return AddressTypeExternal
}

// servedDomains registry (multi-domain / BYO). Defaults to the built-in Domain
// until SetServedDomains is called at startup from the served-domains table.
var (
	servedDomainsMu sync.RWMutex
	servedDomains   = map[string]bool{Domain: true}
)

// SetServedDomains replaces the internal-domain set. Empty input keeps the
// built-in default (Domain). Matched case-insensitively.
func SetServedDomains(domains []string) {
	m := make(map[string]bool, len(domains))
	for _, d := range domains {
		if d = strings.ToLower(strings.TrimSpace(d)); d != "" {
			m[d] = true
		}
	}
	if len(m) == 0 {
		m[Domain] = true
	}
	servedDomainsMu.Lock()
	servedDomains = m
	servedDomainsMu.Unlock()
}

// isServedDomain reports whether domain is one this instance serves.
func isServedDomain(domain string) bool {
	servedDomainsMu.RLock()
	defer servedDomainsMu.RUnlock()
	return servedDomains[domain]
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
