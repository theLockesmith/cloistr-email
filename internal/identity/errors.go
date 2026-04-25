package identity

import "errors"

var (
	// ErrNoUnifiedAddress is returned when a user tries to send without a unified address
	ErrNoUnifiedAddress = errors.New("user does not have a unified address - registration required")

	// ErrAddressNotVerified is returned when the unified address hasn't been verified
	ErrAddressNotVerified = errors.New("unified address not verified")

	// ErrFromAddressMismatch is returned when sender tries to use someone else's address
	ErrFromAddressMismatch = errors.New("from address does not match sender's unified address")

	// ErrAddressOwnershipMismatch is returned when cloistr-me reports the pubkey doesn't own the address
	ErrAddressOwnershipMismatch = errors.New("address ownership verification failed: pubkey does not own this address")

	// ErrNpubAlreadyRegistered is returned when npub already has an address
	ErrNpubAlreadyRegistered = errors.New("npub already has a registered address")

	// ErrLocalPartTaken is returned when the desired local part is already in use
	ErrLocalPartTaken = errors.New("email address already taken")

	// ErrInvalidLocalPart is returned when the local part fails validation
	ErrInvalidLocalPart = errors.New("invalid local part")

	// ErrInvalidEmail is returned when the email format is invalid
	ErrInvalidEmail = errors.New("invalid email format")
)
