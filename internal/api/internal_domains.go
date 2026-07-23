package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-common/errors"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/domains"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/storage"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/transport"
)

// DomainRegistry is the subset of *domains.Registry the handler needs
// (reload/publish after writes). Kept as an interface for testability.
type DomainRegistry interface {
	PublishReload(ctx context.Context) error
}

// InternalDomainHandler serves the internal domain-admin API consumed by the
// admin page (cloistr-me). It is guarded by a shared bearer secret — this API
// mutates served domains and DKIM keys and must never be exposed publicly.
//
// Private keys never leave this service: responses carry only the public DKIM
// DNS record. Key generation happens here, in the same process that signs.
type InternalDomainHandler struct {
	db       *storage.PostgreSQL
	registry DomainRegistry
	dns      domains.DNSChecker
	secret   string
	logger   *zap.Logger
}

// NewInternalDomainHandler builds the handler. secret is the required bearer
// token (INTERNAL_API_SECRET); an empty secret means the API is disabled and
// routes should not be registered.
func NewInternalDomainHandler(db *storage.PostgreSQL, registry DomainRegistry, dns domains.DNSChecker, secret string, logger *zap.Logger) *InternalDomainHandler {
	return &InternalDomainHandler{db: db, registry: registry, dns: dns, secret: secret, logger: logger}
}

// AuthMiddleware enforces the internal bearer secret in constant time.
func (h *InternalDomainHandler) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.secret == "" {
			errors.InternalError("INTERNAL_API_DISABLED", "internal API not configured").WriteResponse(w)
			return
		}
		auth := r.Header.Get("Authorization")
		parts := strings.SplitN(auth, " ", 2)
		// Hash both sides to a fixed 32 bytes before comparing so the
		// constant-time compare doesn't early-return on length and leak the
		// secret's length via timing.
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" || !secretsEqual(parts[1], h.secret) {
			errors.Unauthorized("INTERNAL_AUTH_FAILED", "invalid internal API credentials").WriteResponse(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// secretsEqual compares two secrets in constant time without leaking their
// length: both are SHA-256'd to a fixed 32 bytes first.
func secretsEqual(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

// --- wire types (never expose dkim_private_key) ---

// DomainResponse is the admin-facing view of a served domain.
type DomainResponse struct {
	Domain        string `json:"domain"`
	DKIMSelector  string `json:"dkim_selector"`
	Verified      bool   `json:"verified"`
	Active        bool   `json:"active"`
	HasDKIMKey    bool   `json:"has_dkim_key"`
	DKIMRecordName  string `json:"dkim_record_name,omitempty"`
	DKIMRecordValue string `json:"dkim_record_value,omitempty"`
	CreatedAt     string `json:"created_at,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

type createDomainRequest struct {
	Domain   string `json:"domain"`
	Selector string `json:"selector,omitempty"`
	// DKIMPrivateKey optionally imports an existing PEM private key instead of
	// generating a new one. Use it to onboard keys created out-of-band (e.g.
	// scripts/generate-dkim-keys.sh) or BYO-domain keys whose DNS is already
	// published. When empty, a fresh keypair is generated server-side.
	DKIMPrivateKey string `json:"dkim_private_key,omitempty"`
}

func (h *InternalDomainHandler) toResponse(d *storage.Domain) DomainResponse {
	resp := DomainResponse{
		Domain:       d.Domain,
		DKIMSelector: d.DKIMSelector,
		Verified:     d.Verified,
		Active:       d.Active,
		HasDKIMKey:   d.DKIMPrivateKey != nil && *d.DKIMPrivateKey != "",
		CreatedAt:    d.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:    d.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
	// Derive the public DNS record from the stored key so the UI can always
	// show what to publish — without ever seeing the private key.
	if resp.HasDKIMKey {
		if signer, err := transport.NewDKIMSigner(&transport.DKIMConfig{
			Domain: d.Domain, Selector: d.DKIMSelector, PrivateKey: *d.DKIMPrivateKey,
		}); err == nil {
			resp.DKIMRecordName = signer.DNSRecordName()
			resp.DKIMRecordValue = signer.GenerateDKIMDNSRecord()
		}
	}
	return resp
}

// ListDomains: GET /internal/v1/domains
func (h *InternalDomainHandler) ListDomains(w http.ResponseWriter, r *http.Request) {
	ds, err := h.db.ListAllDomains(r.Context())
	if err != nil {
		errors.InternalError("INTERNAL_ERROR", "failed to list domains").WriteResponse(w)
		return
	}
	out := make([]DomainResponse, 0, len(ds))
	for _, d := range ds {
		out = append(out, h.toResponse(d))
	}
	h.respondJSON(w, http.StatusOK, map[string]interface{}{"domains": out})
}

// CreateDomain: POST /internal/v1/domains
// Registers a domain with a DKIM key — either imported from the request
// (dkim_private_key) or freshly generated — and returns the DNS records to
// publish. The domain starts pending (verified=false, active=false).
func (h *InternalDomainHandler) CreateDomain(w http.ResponseWriter, r *http.Request) {
	var req createDomainRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errors.BadRequest("INVALID_INPUT", "invalid request body").WriteResponse(w)
		return
	}
	domain := strings.ToLower(strings.TrimSpace(req.Domain))
	if domain == "" || !strings.Contains(domain, ".") {
		errors.BadRequest("VALIDATION_FAILED", "a valid domain is required").WriteResponse(w)
		return
	}
	selector := strings.TrimSpace(req.Selector)
	if selector == "" {
		selector = "mail"
	}

	if existing, err := h.db.GetDomain(r.Context(), domain); err != nil {
		errors.InternalError("INTERNAL_ERROR", "failed to check domain").WriteResponse(w)
		return
	} else if existing != nil {
		errors.Conflict("DOMAIN_EXISTS", "domain already registered").WriteResponse(w)
		return
	}

	// Import a supplied key, or generate one. Importing validates the PEM by
	// loading it into a signer (which is also how we derive its DNS record), so
	// a malformed key is rejected before it reaches the DB.
	var privatePEM string
	if imported := strings.TrimSpace(req.DKIMPrivateKey); imported != "" {
		if _, err := transport.NewDKIMSigner(&transport.DKIMConfig{
			Domain: domain, Selector: selector, PrivateKey: imported,
		}); err != nil {
			errors.BadRequest("INVALID_DKIM_KEY", "dkim_private_key is not a valid RSA private key").WriteResponse(w)
			return
		}
		privatePEM = imported
	} else {
		key, err := transport.GenerateDKIMKey(domain, selector, 0)
		if err != nil {
			h.logger.Error("DKIM keygen failed", zap.String("domain", domain), zap.Error(err))
			errors.InternalError("KEYGEN_FAILED", "failed to generate DKIM key").WriteResponse(w)
			return
		}
		privatePEM = key.PrivatePEM
	}

	d := &storage.Domain{
		Domain: domain, DKIMSelector: selector,
		DKIMPrivateKey: &privatePEM, Verified: false, Active: false,
	}
	if err := h.db.UpsertDomain(r.Context(), d); err != nil {
		errors.InternalError("INTERNAL_ERROR", "failed to save domain").WriteResponse(w)
		return
	}

	h.logger.Info("domain registered (pending DNS)", zap.String("domain", domain))
	// Not active yet, so no reload needed. Return the records to publish.
	h.respondJSON(w, http.StatusCreated, h.toResponse(d))
}

// VerifyDomain: POST /internal/v1/domains/{domain}/verify
func (h *InternalDomainHandler) VerifyDomain(w http.ResponseWriter, r *http.Request) {
	d := h.load(w, r)
	if d == nil {
		return
	}
	if d.DKIMPrivateKey == nil || *d.DKIMPrivateKey == "" {
		errors.BadRequest("NO_DKIM_KEY", "domain has no DKIM key to verify").WriteResponse(w)
		return
	}
	signer, err := transport.NewDKIMSigner(&transport.DKIMConfig{
		Domain: d.Domain, Selector: d.DKIMSelector, PrivateKey: *d.DKIMPrivateKey,
	})
	if err != nil {
		errors.InternalError("INTERNAL_ERROR", "failed to load DKIM key").WriteResponse(w)
		return
	}

	status := domains.CheckDomain(r.Context(), h.dns, d.Domain, signer.DNSRecordName(), signer.GenerateDKIMDNSRecord())
	if status.Verified() && !d.Verified {
		d.Verified = true
		if err := h.db.UpsertDomain(r.Context(), d); err != nil {
			errors.InternalError("INTERNAL_ERROR", "failed to persist verification").WriteResponse(w)
			return
		}
		// Signing is gated on verified, so an already-active domain only starts
		// signing once verification lands — reload to build its signer now.
		if d.Active {
			if err := h.registry.PublishReload(r.Context()); err != nil {
				h.logger.Warn("domain reload publish failed", zap.Error(err))
			}
		}
	}
	h.respondJSON(w, http.StatusOK, map[string]interface{}{
		"domain": h.toResponse(d),
		"dns":    status,
	})
}

// ActivateDomain: POST /internal/v1/domains/{domain}/activate
func (h *InternalDomainHandler) ActivateDomain(w http.ResponseWriter, r *http.Request) {
	h.setActive(w, r, true)
}

// DeactivateDomain: POST /internal/v1/domains/{domain}/deactivate
func (h *InternalDomainHandler) DeactivateDomain(w http.ResponseWriter, r *http.Request) {
	h.setActive(w, r, false)
}

func (h *InternalDomainHandler) setActive(w http.ResponseWriter, r *http.Request, active bool) {
	d := h.load(w, r)
	if d == nil {
		return
	}
	if active && !d.Verified {
		errors.BadRequest("NOT_VERIFIED", "verify DNS before activating").WriteResponse(w)
		return
	}
	d.Active = active
	if err := h.db.UpsertDomain(r.Context(), d); err != nil {
		errors.InternalError("INTERNAL_ERROR", "failed to update domain").WriteResponse(w)
		return
	}
	// Propagate to every replica's live signer map.
	if err := h.registry.PublishReload(r.Context()); err != nil {
		h.logger.Warn("domain reload publish failed", zap.Error(err))
	}
	h.respondJSON(w, http.StatusOK, h.toResponse(d))
}

// RotateDKIM: POST /internal/v1/domains/{domain}/rotate-dkim
// Generates a fresh keypair+selector and returns the new DNS record. The domain
// is set back to unverified (the new record must be published + verified);
// callers keep the old selector's DNS live until cutover.
func (h *InternalDomainHandler) RotateDKIM(w http.ResponseWriter, r *http.Request) {
	d := h.load(w, r)
	if d == nil {
		return
	}
	var req createDomainRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	selector := strings.TrimSpace(req.Selector)
	if selector == "" || strings.EqualFold(selector, d.DKIMSelector) {
		errors.BadRequest("VALIDATION_FAILED", "a new, distinct selector is required for rotation").WriteResponse(w)
		return
	}

	key, err := transport.GenerateDKIMKey(d.Domain, selector, 0)
	if err != nil {
		errors.InternalError("KEYGEN_FAILED", "failed to generate DKIM key").WriteResponse(w)
		return
	}
	d.DKIMSelector = selector
	d.DKIMPrivateKey = &key.PrivatePEM
	d.Verified = false
	if err := h.db.UpsertDomain(r.Context(), d); err != nil {
		errors.InternalError("INTERNAL_ERROR", "failed to save rotated key").WriteResponse(w)
		return
	}
	// Rotation sets verified=false, so on reload an active domain drops out of
	// the signer map (verified-gate) and sends UNSIGNED until the operator
	// publishes the new record and re-verifies — rather than signing with a
	// selector that does not yet resolve. The domain stays active, so inbound
	// is unaffected.
	if d.Active {
		if err := h.registry.PublishReload(r.Context()); err != nil {
			h.logger.Warn("domain reload publish failed", zap.Error(err))
		}
	}
	h.respondJSON(w, http.StatusOK, h.toResponse(d))
}

// load fetches the {domain} path var, writing a 404 and returning nil if absent.
func (h *InternalDomainHandler) load(w http.ResponseWriter, r *http.Request) *storage.Domain {
	name := strings.ToLower(strings.TrimSpace(mux.Vars(r)["domain"]))
	if name == "" {
		errors.BadRequest("VALIDATION_FAILED", "domain is required").WriteResponse(w)
		return nil
	}
	d, err := h.db.GetDomain(r.Context(), name)
	if err != nil {
		errors.InternalError("INTERNAL_ERROR", "failed to load domain").WriteResponse(w)
		return nil
	}
	if d == nil {
		errors.NotFound("DOMAIN_NOT_FOUND", "domain not found").WriteResponse(w)
		return nil
	}
	return d
}

func (h *InternalDomainHandler) respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.logger.Error("failed to encode response", zap.Error(err))
	}
}
