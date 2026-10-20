package api

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// AddressEnsurer gives a pubkey its @cloistr.xyz address if it has none.
// cloistr-me owns addresses; identity.CloistrMeClient implements this.
type AddressEnsurer interface {
	EnsureAddress(ctx context.Context, pubkey string) error
}

// ensureAddressTimeout bounds how long sign-in waits on cloistr-me.
const ensureAddressTimeout = 3 * time.Second

// WithAddressEnsurer makes sign-in ask cloistr-me for the user's address when
// they have none. Without it, someone who signed up through Mail had no address
// (so could not send or receive) until they happened to open the Me dashboard.
func WithAddressEnsurer(e AddressEnsurer) HandlerOption {
	return func(h *Handler) {
		h.addressEnsurer = e
	}
}

// ensureAddressOnSignIn asks cloistr-me for the pubkey's address when the shared
// addresses table has no active one. It never fails sign-in: on error or timeout
// the user signs in without an address and the next sign-in tries again.
func (h *Handler) ensureAddressOnSignIn(ctx context.Context, pubkey string) {
	if h.addressEnsurer == nil {
		return
	}
	if h.db != nil {
		addr, err := h.db.GetAddressByPubkey(ctx, pubkey)
		if err == nil && addr != nil {
			return
		}
	}
	ctx, cancel := context.WithTimeout(ctx, ensureAddressTimeout)
	defer cancel()
	if err := h.addressEnsurer.EnsureAddress(ctx, pubkey); err != nil {
		h.logger.Warn("could not ensure an address on sign-in; signing in without one",
			zap.String("pubkey", pubkey), zap.Error(err))
	}
}
