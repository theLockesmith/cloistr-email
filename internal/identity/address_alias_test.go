package identity

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"
)

const aliasTestNpub = "ac16282f720514d926a57b5c13f02d1f4e32bd6fe3e00f713f50964571685f62"

// aliasStore is an AddressStore backed by a fixed set of owned addresses.
type aliasStore struct{ owned []*UnifiedAddress }

func (s aliasStore) GetByNpub(ctx context.Context, npub string) (*UnifiedAddress, error) {
	for _, a := range s.owned {
		if a.Npub == npub && a.IsPrimary {
			return a, nil
		}
	}
	return nil, nil
}
func (s aliasStore) ListByNpub(ctx context.Context, npub string) ([]*UnifiedAddress, error) {
	var out []*UnifiedAddress
	for _, a := range s.owned {
		if a.Npub == npub {
			out = append(out, a)
		}
	}
	return out, nil
}
func (s aliasStore) GetByEmail(ctx context.Context, email string) (*UnifiedAddress, error) {
	for _, a := range s.owned {
		if a.Email == email {
			return a, nil
		}
	}
	return nil, nil
}
func (s aliasStore) Create(ctx context.Context, addr *UnifiedAddress) error { return nil }
func (s aliasStore) Update(ctx context.Context, addr *UnifiedAddress) error { return nil }
func (s aliasStore) LocalPartExists(ctx context.Context, localPart string) (bool, error) {
	return false, nil
}

type aliasResolver struct{}

func (aliasResolver) ResolvePubkey(ctx context.Context, email string) (string, error) {
	return "", nil
}

// denyVerifier stands in for cloistr-me reporting the pubkey does NOT own it.
type denyVerifier struct{}

func (denyVerifier) VerifyAddressOwnership(ctx context.Context, pubkey, address string) (bool, error) {
	return false, nil
}

func aliasFixture() []*UnifiedAddress {
	return []*UnifiedAddress{
		{Npub: aliasTestNpub, LocalPart: "fraiyr", Email: "fraiyr@cloistr.xyz", Verified: true, IsPrimary: true},
		{Npub: aliasTestNpub, LocalPart: "chuck", Email: "chuck@cloistr.xyz", Verified: true},
		{Npub: aliasTestNpub, LocalPart: "support", Email: "support@aegis-hq.xyz", Verified: true},
		{Npub: aliasTestNpub, LocalPart: "pending", Email: "pending@cloistr.xyz", Verified: false},
	}
}

func TestResolveFromAddress(t *testing.T) {
	logger := zap.NewNop()
	svc := NewService(aliasStore{owned: aliasFixture()}, aliasResolver{}, logger)

	tests := []struct {
		name      string
		from      string
		wantEmail string
		wantErr   error
	}{
		{
			name:      "empty from falls back to primary",
			from:      "",
			wantEmail: "fraiyr@cloistr.xyz",
		},
		{
			name:      "explicit primary is allowed",
			from:      "fraiyr@cloistr.xyz",
			wantEmail: "fraiyr@cloistr.xyz",
		},
		{
			name:      "owned alias is allowed (the point of alias send-from)",
			from:      "chuck@cloistr.xyz",
			wantEmail: "chuck@cloistr.xyz",
		},
		{
			name:      "owned alias on a different domain is allowed",
			from:      "support@aegis-hq.xyz",
			wantEmail: "support@aegis-hq.xyz",
		},
		{
			name:      "match is case-insensitive",
			from:      "Chuck@Cloistr.XYZ",
			wantEmail: "chuck@cloistr.xyz",
		},
		{
			name:    "address the sender does not own is rejected",
			from:    "someoneelse@cloistr.xyz",
			wantErr: ErrFromAddressMismatch,
		},
		{
			name:    "owned but unverified address is rejected",
			from:    "pending@cloistr.xyz",
			wantErr: ErrAddressNotVerified,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, err := svc.ResolveFromAddress(context.Background(), aliasTestNpub, tt.from)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if addr.Email != tt.wantEmail {
				t.Errorf("email = %q, want %q", addr.Email, tt.wantEmail)
			}
		})
	}
}

func TestResolveFromAddressNoAddresses(t *testing.T) {
	svc := NewService(aliasStore{}, aliasResolver{}, zap.NewNop())

	_, err := svc.ResolveFromAddress(context.Background(), aliasTestNpub, "nobody@cloistr.xyz")
	if !errors.Is(err, ErrNoUnifiedAddress) {
		t.Fatalf("err = %v, want ErrNoUnifiedAddress", err)
	}
}

// A sender must not be able to use an alias that cloistr-me (the authority)
// says they do not own, even when the local addresses table lists it.
func TestResolveFromAddressVerifierRejects(t *testing.T) {
	svc := NewService(aliasStore{owned: aliasFixture()}, aliasResolver{}, zap.NewNop()).
		WithVerifier(denyVerifier{})

	_, err := svc.ResolveFromAddress(context.Background(), aliasTestNpub, "chuck@cloistr.xyz")
	if !errors.Is(err, ErrAddressOwnershipMismatch) {
		t.Fatalf("err = %v, want ErrAddressOwnershipMismatch", err)
	}
}

// ValidateFromAddress is the boolean wrapper used by callers that only care
// whether the send is permitted.
func TestValidateFromAddressWrapper(t *testing.T) {
	svc := NewService(aliasStore{owned: aliasFixture()}, aliasResolver{}, zap.NewNop())

	if err := svc.ValidateFromAddress(context.Background(), aliasTestNpub, "chuck@cloistr.xyz"); err != nil {
		t.Errorf("owned alias should validate, got %v", err)
	}
	if err := svc.ValidateFromAddress(context.Background(), aliasTestNpub, "nope@cloistr.xyz"); !errors.Is(err, ErrFromAddressMismatch) {
		t.Errorf("err = %v, want ErrFromAddressMismatch", err)
	}
}
