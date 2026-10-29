package email

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/encryption"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/storage"
	"github.com/google/uuid"
)

// MaxDraftBodyBytes caps a saved draft's body. Attachments are not saved with
// drafts, so this only has to fit text (or its client envelope).
const MaxDraftBodyBytes = 1 << 20

// ErrDraftNotFound: the id is not one of the caller's live drafts. A missing
// draft, someone else's draft and a sent or received message all look the same,
// so the caller learns nothing about ids it does not own.
var ErrDraftNotFound = storage.ErrDraftNotFound

// ErrDraftServerMode: a server-mode draft would need the server to encrypt it
// with the user's key, which the server does not hold for drafts (RFC-004).
// The client saves encrypted drafts as client envelopes instead.
var ErrDraftServerMode = errors.New("drafts cannot use server-side encryption; save as a client-encrypted draft")

// ErrDraftKeyNotOwner: an encrypted draft whose content key is not wrapped to
// its owner could never be reopened.
var ErrDraftKeyNotOwner = errors.New("encrypted draft is not readable by its owner")

// ErrDraftTooLarge: the body is over MaxDraftBodyBytes.
var ErrDraftTooLarge = errors.New("draft body is too large")

// DraftInput is a compose-screen draft. ID empty creates a draft; otherwise
// the caller's existing draft with that ID is overwritten.
type DraftInput struct {
	ID             string
	To             string
	CC             []string
	Subject        string
	Body           string
	EncryptionMode encryption.EncryptionMode
	InReplyTo      string
	References     []string
}

// SaveDraft creates or overwrites one of the caller's drafts.
//
// Unencrypted drafts are stored as written, the same as an unencrypted sent
// copy. Encrypted drafts must arrive as a client body envelope that the owner
// holds a key to; the server never sees their plaintext, and stores the
// placeholder subject (the real one is inside the envelope).
func (s *Service) SaveDraft(ctx context.Context, userNpub string, in *DraftInput) (*storage.Email, error) {
	if len(in.Body) > MaxDraftBodyBytes {
		return nil, ErrDraftTooLarge
	}

	d := &storage.Email{
		MailboxPubkey: userNpub,
		ToAddress:     strings.TrimSpace(in.To),
		Subject:       in.Subject,
		Body:          in.Body,
		SenderNpub:    stringPtr(userNpub),
		Direction:     "draft",
		Folder:        "Drafts",
		Status:        "active",
	}
	if len(d.ToAddress) > 255 {
		return nil, fmt.Errorf("%w: recipient address is too long", ErrDraftTooLarge)
	}

	switch in.EncryptionMode {
	case encryption.ModeNone, "":
		d.EncryptionMode = stringPtr(string(encryption.ModeNone))
	case encryption.ModeClientSide:
		holders, ok := encryption.BodyKeyHolders(in.Body)
		if !ok {
			return nil, ErrClientBodyNotEncrypted
		}
		if !holders[userNpub] {
			return nil, ErrDraftKeyNotOwner
		}
		d.Subject = EncryptedSubjectPlaceholder
		d.IsEncrypted = true
		d.EncryptionMode = stringPtr(string(encryption.ModeClientSide))
	case encryption.ModeServerSide:
		return nil, ErrDraftServerMode
	default:
		return nil, fmt.Errorf("invalid encryption mode %q", in.EncryptionMode)
	}

	if cc := strings.Join(in.CC, ", "); cc != "" {
		d.CC = &cc
	}
	if in.InReplyTo != "" {
		d.InReplyTo = stringPtr(in.InReplyTo)
	}
	if len(in.References) > 0 {
		d.References = stringPtr(strings.Join(in.References, " "))
	}

	if in.ID == "" {
		if _, err := s.db.EnsureMailbox(ctx, userNpub); err != nil {
			return nil, fmt.Errorf("ensure mailbox: %w", err)
		}
		if err := s.db.CreateEmail(ctx, d); err != nil {
			return nil, fmt.Errorf("create draft: %w", err)
		}
		return d, nil
	}

	// The id column is a UUID; anything else cannot be a draft, and passing it
	// to Postgres would surface as a 500 instead of "not found".
	if _, err := uuid.Parse(in.ID); err != nil {
		return nil, ErrDraftNotFound
	}
	d.ID = in.ID
	if err := s.db.UpdateDraft(ctx, d); err != nil {
		return nil, err
	}
	return d, nil
}
