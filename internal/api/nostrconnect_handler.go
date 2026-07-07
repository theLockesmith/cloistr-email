package api

import (
	"net/http"

	"git.aegis-hq.xyz/coldforge/cloistr-common/errors"
	"go.uber.org/zap"
)

// nostrConnectInitResponse is the response for POST /api/v2/auth/nostrconnect/init.
type nostrConnectInitResponse struct {
	// NostrConnectURI is the nostrconnect:// URI the client should forward to the
	// signer (e.g. via a deep link or QR code).
	NostrConnectURI string `json:"nostrconnect_uri"`
	// Nonce is a single-use identifier the client can poll against
	// GET /api/v2/auth/nip46/status to learn when the signer has ACK'd.
	Nonce string `json:"nonce"`
}

// nip46StatusResponse is the response for GET /api/v2/auth/nip46/status.
type nip46StatusResponse struct {
	Connected bool `json:"connected"`
}

// InitNostrConnect handles POST /api/v2/auth/nostrconnect/init.
//
// The caller must be authenticated (AuthMiddleware sets contextKeyUserID).
// The endpoint generates an ephemeral keypair, builds a nostrconnect:// URI
// pointing at the configured NostrConnect relay, persists the bootstrap state
// in Redis (TTL 90 s), and starts a background goroutine that completes the
// handshake when the signer publishes its kind-24133 ACK.  The URI and a
// single-use nonce are returned immediately.
func (h *Handler) InitNostrConnect(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug("InitNostrConnect: processing request")

	userPubkey := getUserID(r.Context())
	if userPubkey == "" {
		errors.Unauthorized("AUTH_REQUIRED", "not authenticated").WriteResponse(w)
		return
	}

	relayURL := h.config.NostrConnectRelay
	if relayURL == "" {
		relayURL = "wss://relay.cloistr.xyz"
	}

	uri, nonce, err := h.auth.InitiateNostrConnect(r.Context(), userPubkey, relayURL, "Cloistr Mail")
	if err != nil {
		h.logger.Error("InitNostrConnect: failed to initiate", zap.Error(err))
		errors.InternalError("INTERNAL_ERROR", "failed to initiate nostrconnect bootstrap").WriteResponse(w)
		return
	}

	h.logger.Info("NostrConnect bootstrap initiated",
		zap.String("user_pubkey", userPubkey[:min16(userPubkey)]+"..."),
		zap.String("nonce", nonce))

	h.respondJSON(w, http.StatusOK, nostrConnectInitResponse{
		NostrConnectURI: uri,
		Nonce:           nonce,
	})
}

// NIP46Status handles GET /api/v2/auth/nip46/status.
//
// Returns whether the authenticated user currently has a live NIP-46 bunker
// connection.  The client can poll this after forwarding the nostrconnect://
// URI to the signer to learn when the handshake completes.
func (h *Handler) NIP46Status(w http.ResponseWriter, r *http.Request) {
	userPubkey := getUserID(r.Context())
	if userPubkey == "" {
		errors.Unauthorized("AUTH_REQUIRED", "not authenticated").WriteResponse(w)
		return
	}

	h.respondJSON(w, http.StatusOK, nip46StatusResponse{
		Connected: h.auth.HasBunkerConnection(userPubkey),
	})
}

// min16 returns the minimum of 16 and len(s) for safe pubkey log truncation.
func min16(s string) int {
	if len(s) < 16 {
		return len(s)
	}
	return 16
}

