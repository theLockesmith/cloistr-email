// Package storage provides the PostgreSQL adapter for identity.AddressStore.
// This adapter bridges cloistr-email's identity package with cloistr-me's
// addresses table schema.
package storage

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"git.coldforge.xyz/coldforge/cloistr-email/internal/identity"
)

// AddressStoreAdapter adapts PostgreSQL to implement identity.AddressStore.
// It translates between cloistr-email's UnifiedAddress and cloistr-me's
// addresses table schema.
type AddressStoreAdapter struct {
	db     *PostgreSQL
	domain string // default domain for new addresses (cloistr.xyz)
	logger *zap.Logger
}

// NewAddressStoreAdapter creates a new AddressStoreAdapter
func NewAddressStoreAdapter(db *PostgreSQL, domain string, logger *zap.Logger) *AddressStoreAdapter {
	return &AddressStoreAdapter{
		db:     db,
		domain: domain,
		logger: logger,
	}
}

// GetByNpub retrieves a unified address by npub
func (a *AddressStoreAdapter) GetByNpub(ctx context.Context, npub string) (*identity.UnifiedAddress, error) {
	addr, err := a.db.GetAddressByPubkey(ctx, npub)
	if err != nil {
		return nil, err
	}
	if addr == nil {
		return nil, nil
	}
	return a.toUnifiedAddress(addr), nil
}

// GetByEmail retrieves a unified address by email
func (a *AddressStoreAdapter) GetByEmail(ctx context.Context, email string) (*identity.UnifiedAddress, error) {
	addr, err := a.db.GetAddressByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	if addr == nil {
		return nil, nil
	}
	return a.toUnifiedAddress(addr), nil
}

// Create creates a new unified address mapping.
// NOTE: Address creation is handled by cloistr-me, not cloistr-email.
// This method exists to satisfy the interface but should not be called
// in normal operation. Users register addresses through cloistr-me.
func (a *AddressStoreAdapter) Create(ctx context.Context, addr *identity.UnifiedAddress) error {
	// Address registration is handled by cloistr-me
	// cloistr-email is read-only for addresses
	return fmt.Errorf("address creation is handled by cloistr-me, not cloistr-email")
}

// Update updates an existing unified address.
// NOTE: Address updates are handled by cloistr-me, not cloistr-email.
func (a *AddressStoreAdapter) Update(ctx context.Context, addr *identity.UnifiedAddress) error {
	// Address updates are handled by cloistr-me
	return fmt.Errorf("address updates are handled by cloistr-me, not cloistr-email")
}

// LocalPartExists checks if a local part is already taken
func (a *AddressStoreAdapter) LocalPartExists(ctx context.Context, localPart string) (bool, error) {
	return a.db.UsernameExists(ctx, localPart, a.domain)
}

// toUnifiedAddress converts storage.Address to identity.UnifiedAddress
func (a *AddressStoreAdapter) toUnifiedAddress(addr *Address) *identity.UnifiedAddress {
	displayName := ""
	if addr.DisplayName != nil {
		displayName = *addr.DisplayName
	}

	return &identity.UnifiedAddress{
		Npub:        addr.Pubkey,
		LocalPart:   addr.Username,
		Email:       addr.Email(),
		DisplayName: displayName,
		Verified:    addr.Verified,
	}
}

// Ensure AddressStoreAdapter implements identity.AddressStore
var _ identity.AddressStore = (*AddressStoreAdapter)(nil)
