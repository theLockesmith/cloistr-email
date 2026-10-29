package email

import (
	"context"
	"fmt"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/blobseal"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/blossom"
	"go.uber.org/zap"
)

// sealedBlobContentType is the only type the server declares to Blossom for a
// sealed blob. The real type stays in Mail's database, so Blossom learns
// nothing about what the file is.
const sealedBlobContentType = "application/octet-stream"

// blossomAttachmentUploader stores inbound attachments on Blossom, sealed.
//
// Blossom serves any blob to anyone who knows its hash, so the bytes are
// encrypted first under a random per-blob key (internal/blobseal); the wrapped
// key is returned for the attachment row and never leaves Mail's database.
//
// Uploads are signed by Mail's own service key, not the recipient's: mail
// arrives while the recipient is offline, and the server holds no session for
// them. One key therefore owns every inbound blob. Its inventory (hashes, sizes,
// upload times) is pooled, but every blob in it is opaque ciphertext. Per-user
// ownership is a follow-up (docs/008-sealed-attachments.md).
type blossomAttachmentUploader struct {
	auth       blossom.AuthSigner
	sealer     *blobseal.Sealer
	servers    []blossom.Server
	redundancy int
	logger     *zap.Logger
}

// NewBlossomAttachmentUploader builds an uploader for inbound attachments.
// Returns nil unless a service key, a sealer and at least one server are all
// configured, so no configuration can upload an attachment unsealed. The caller
// then records attachment metadata only.
func NewBlossomAttachmentUploader(auth blossom.AuthSigner, sealer *blobseal.Sealer, servers []blossom.Server, redundancy int, logger *zap.Logger) AttachmentUploader {
	if auth == nil || sealer == nil || len(servers) == 0 {
		return nil
	}
	return &blossomAttachmentUploader{
		auth:       auth,
		sealer:     sealer,
		servers:    servers,
		redundancy: redundancy,
		logger:     logger,
	}
}

// UploadFor implements AttachmentUploader.
func (u *blossomAttachmentUploader) UploadFor(ctx context.Context, attachmentID string, data []byte) (StoredBlob, error) {
	blob, key, err := u.sealer.Seal(data, attachmentID)
	if err != nil {
		return StoredBlob{}, fmt.Errorf("seal attachment: %w", err)
	}
	desc, err := blossom.NewClient(u.auth, u.logger).Upload(ctx, blob, sealedBlobContentType, u.servers, u.redundancy)
	if err != nil {
		return StoredBlob{}, err
	}
	out := StoredBlob{SHA256: desc.SHA256, Key: key}
	if len(desc.Servers) > 0 {
		out.URL = desc.Servers[0] + "/" + desc.SHA256
	}
	return out, nil
}
