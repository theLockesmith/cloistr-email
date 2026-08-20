package email

import (
	"context"
	"fmt"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/blossom"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/encryption"
	"github.com/nbd-wtf/go-nostr"
	"go.uber.org/zap"
)

// blossomAttachmentUploader stores inbound attachment bytes in Blossom, owned
// by the RECIPIENT.
//
// Ownership matters: Blossom auth is a signed per-user event, and the recipient
// is the only party to the delivery whose key this server can act for. The
// sender is an arbitrary stranger on the internet with no account here.
//
// This mirrors the outbound path (Service.uploadAttachments), including its
// limitation: signing goes through the user's bunker key, so a NIP-07 user —
// whose key the server never holds — cannot have blobs stored on their behalf.
// For those users UploadFor fails, and the caller records attachment metadata
// without a blob rather than dropping the attachment silently.
//
// Bytes are stored AS RECEIVED, not encrypted. That matches how the inbound
// body is stored (parsed.Body goes to the DB in the clear): mail arriving over
// plain SMTP from the open internet was never confidential in transit, and
// encrypting only the attachment would imply a guarantee the message as a whole
// does not have.
type blossomAttachmentUploader struct {
	enc        *encryption.EncryptionService
	servers    []blossom.Server
	redundancy int
	logger     *zap.Logger
}

// NewBlossomAttachmentUploader builds an uploader for inbound attachments.
// Returns nil when no servers are configured, so the caller can leave the
// processor's uploader unset and fall back to metadata-only storage.
func NewBlossomAttachmentUploader(enc *encryption.EncryptionService, servers []blossom.Server, redundancy int, logger *zap.Logger) AttachmentUploader {
	if enc == nil || len(servers) == 0 {
		return nil
	}
	return &blossomAttachmentUploader{
		enc:        enc,
		servers:    servers,
		redundancy: redundancy,
		logger:     logger,
	}
}

// UploadFor implements AttachmentUploader.
func (u *blossomAttachmentUploader) UploadFor(ctx context.Context, recipientPubkey string, data []byte, contentType string) (string, string, error) {
	if recipientPubkey == "" {
		return "", "", fmt.Errorf("no recipient pubkey to own the blob")
	}

	authSigner := blossom.NewEventAuthSigner(recipientPubkey, func(ctx context.Context, ev *nostr.Event) error {
		return u.enc.SignEventForUser(ctx, recipientPubkey, ev)
	})
	client := blossom.NewClient(authSigner, u.logger)

	desc, err := client.Upload(ctx, data, contentType, u.servers, u.redundancy)
	if err != nil {
		return "", "", err
	}

	url := ""
	if len(desc.Servers) > 0 {
		url = desc.Servers[0] + "/" + desc.SHA256
	}
	return desc.SHA256, url, nil
}
