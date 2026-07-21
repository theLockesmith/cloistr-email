// Package domains provides a live, refreshable registry of the served domains
// and their per-domain DKIM signers.
//
// The old model loaded the served-domains table once at boot into an immutable
// map, so any change required rolling the pods. This registry re-reads the
// table on demand and, because cloistr-email runs multiple replicas behind a
// Service, coordinates reloads across every replica via a Dragonfly/Redis
// pub/sub channel: a write on one pod publishes a signal that all pods
// (including the writer) act on, so every replica converges near-instantly
// without a restart.
package domains

import (
	"context"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/identity"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/storage"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/transport"
)

// ReloadChannel is the Dragonfly/Redis pub/sub channel used to broadcast
// "served domains changed, reload now" to every replica.
const ReloadChannel = "cloistr-email:domains:reload"

// DomainLister is the storage capability the registry needs.
type DomainLister interface {
	ListActiveDomains(ctx context.Context) ([]*storage.Domain, error)
}

// Registry holds the current per-domain DKIM signers and keeps them in sync
// with the served-domains table. It implements transport.DKIMProvider.
type Registry struct {
	db     DomainLister
	rdb    *redis.Client // optional; nil disables cross-replica fan-out
	logger *zap.Logger

	mu      sync.RWMutex
	signers map[string]*transport.DKIMSigner
}

// NewRegistry creates a registry. rdb may be nil (single-replica / no pub/sub),
// in which case Reload still works but is local to this process.
func NewRegistry(db DomainLister, rdb *redis.Client, logger *zap.Logger) *Registry {
	return &Registry{
		db:      db,
		rdb:     rdb,
		logger:  logger,
		signers: map[string]*transport.DKIMSigner{},
	}
}

// DKIMSignerFor implements transport.DKIMProvider: returns the signer for the
// domain, or nil if none (the transport then falls back to its legacy
// config-based signer).
func (r *Registry) DKIMSignerFor(domain string) *transport.DKIMSigner {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.signers[domain]
}

// Reload re-reads the served-domains table, rebuilds the DKIM signer map, and
// atomically swaps it in. It also refreshes the identity served-domains set
// (which gates inbound acceptance + internal/external classification). Safe to
// call concurrently.
func (r *Registry) Reload(ctx context.Context) error {
	domainRows, err := r.db.ListActiveDomains(ctx)
	if err != nil {
		return err
	}

	next := make(map[string]*transport.DKIMSigner, len(domainRows))
	names := make([]string, 0, len(domainRows))
	for _, d := range domainRows {
		names = append(names, d.Domain)
		if d.DKIMPrivateKey == nil || *d.DKIMPrivateKey == "" {
			r.logger.Warn("served domain has no DKIM key; outbound will be unsigned",
				zap.String("domain", d.Domain))
			continue
		}
		// Only sign with a key whose DNS record is confirmed published. An
		// active-but-unverified domain (e.g. mid-DKIM-rotation) keeps serving
		// inbound but sends unsigned rather than signing with a selector that
		// does not yet resolve — which would hard-fail DKIM at recipients.
		if !d.Verified {
			r.logger.Warn("served domain not verified; outbound unsigned until DNS is verified",
				zap.String("domain", d.Domain))
			continue
		}
		signer, serr := transport.NewDKIMSigner(&transport.DKIMConfig{
			Domain: d.Domain, Selector: d.DKIMSelector, PrivateKey: *d.DKIMPrivateKey,
		})
		if serr != nil {
			r.logger.Error("failed to build DKIM signer for domain",
				zap.String("domain", d.Domain), zap.Error(serr))
			continue
		}
		next[d.Domain] = signer
	}

	r.mu.Lock()
	r.signers = next
	r.mu.Unlock()

	// Empty input keeps the built-in default domain (see SetServedDomains).
	identity.SetServedDomains(names)

	r.logger.Info("served domains reloaded",
		zap.Strings("domains", names), zap.Int("dkim_signers", len(next)))
	return nil
}

// PublishReload refreshes this replica immediately, then broadcasts a reload
// signal so every OTHER replica converges. Call it after any write to the
// served-domains table.
//
// The local reload is synchronous on purpose: the API response must reflect the
// new state on the replica that served the request (otherwise a caller could
// deactivate a domain, get 200, and this pod keeps signing for it until its own
// pub/sub round-trip lands). Cross-replica fan-out is best-effort.
func (r *Registry) PublishReload(ctx context.Context) error {
	if err := r.Reload(ctx); err != nil {
		return err
	}
	if r.rdb != nil {
		if err := r.rdb.Publish(ctx, ReloadChannel, "reload").Err(); err != nil {
			r.logger.Warn("failed to fan out domain reload to other replicas", zap.Error(err))
		}
	}
	return nil
}

// StartSubscriber runs the pub/sub listener until ctx is cancelled: every
// reload message triggers a Reload on this replica. Run it in a goroutine.
// No-op when no Redis client is configured.
func (r *Registry) StartSubscriber(ctx context.Context) {
	if r.rdb == nil {
		return
	}
	sub := r.rdb.Subscribe(ctx, ReloadChannel)
	defer func() { _ = sub.Close() }()
	ch := sub.Channel()

	r.logger.Info("domain reload subscriber started", zap.String("channel", ReloadChannel))
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ch:
			if !ok {
				return
			}
			rctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := r.Reload(rctx); err != nil {
				r.logger.Error("reload after pub/sub signal failed", zap.Error(err))
			}
			cancel()
		}
	}
}
