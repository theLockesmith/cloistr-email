// Package metrics provides Prometheus metrics for coldforge-email
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const namespace = "coldforge_email"

// Email metrics
var (
	// EmailsSentTotal counts emails sent by transport and status
	EmailsSentTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "emails_sent_total",
			Help:      "Total number of emails sent",
		},
		[]string{"transport", "encrypted", "status"},
	)

	// EmailsReceivedTotal counts emails received by transport and verification status
	EmailsReceivedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "emails_received_total",
			Help:      "Total number of emails received",
		},
		[]string{"transport", "verified"},
	)

	// EmailSendDuration tracks email send latency
	EmailSendDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "email_send_duration_seconds",
			Help:      "Time spent sending emails",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"transport"},
	)
)

// Nostr signature metrics
var (
	// NostrSignaturesTotal counts signature operations
	NostrSignaturesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "nostr_signatures_total",
			Help:      "Total number of Nostr signature operations",
		},
		[]string{"operation", "result"}, // operation: sign|verify, result: success|failure
	)

	// NostrVerificationsTotal counts email verification results
	NostrVerificationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "nostr_verifications_total",
			Help:      "Total number of Nostr email verifications",
		},
		[]string{"result"}, // valid|invalid|missing|error
	)
)

// Encryption metrics
var (
	// EncryptionOperationsTotal counts encrypt/decrypt operations
	EncryptionOperationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "encryption_operations_total",
			Help:      "Total number of encryption operations",
		},
		[]string{"operation", "mode", "result"}, // operation: encrypt|decrypt, mode: nip46|nip07, result: success|failure
	)

	// EncryptionDuration tracks encryption latency
	EncryptionDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "encryption_duration_seconds",
			Help:      "Time spent on encryption operations",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"operation", "mode"},
	)
)

// NIP-05 metrics
var (
	// NIP05LookupsTotal counts NIP-05 lookups by result
	NIP05LookupsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "nip05_lookups_total",
			Help:      "Total number of NIP-05 lookups",
		},
		[]string{"result"}, // success|failure|cached
	)

	// NIP05LookupDuration tracks NIP-05 lookup latency
	NIP05LookupDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "nip05_lookup_duration_seconds",
			Help:      "Time spent on NIP-05 lookups (non-cached)",
			Buckets:   prometheus.DefBuckets,
		},
	)

	// NIP05CacheSize tracks current cache size
	NIP05CacheSize = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "nip05_cache_size",
			Help:      "Current number of entries in NIP-05 cache",
		},
	)
)

// Authentication metrics
var (
	// AuthAttemptsTotal counts authentication attempts
	AuthAttemptsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "auth_attempts_total",
			Help:      "Total number of authentication attempts",
		},
		[]string{"method", "result"}, // method: nip46|nip07, result: success|failure
	)

	// ActiveSessions tracks current active sessions
	ActiveSessions = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "active_sessions",
			Help:      "Current number of active sessions",
		},
	)
)

// HTTP metrics
var (
	// HTTPRequestsTotal counts HTTP requests
	HTTPRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "http_requests_total",
			Help:      "Total number of HTTP requests",
		},
		[]string{"method", "path", "status"},
	)

	// HTTPRequestDuration tracks HTTP request latency
	HTTPRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "http_request_duration_seconds",
			Help:      "HTTP request latency",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)
)

// Database metrics
var (
	// DBQueryDuration tracks database query latency
	DBQueryDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "db_query_duration_seconds",
			Help:      "Database query latency",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"operation"}, // select|insert|update|delete
	)

	// DBConnectionsActive tracks active database connections
	DBConnectionsActive = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "db_connections_active",
			Help:      "Number of active database connections",
		},
	)
)

// Lightning/payment metrics (for future spam control)
var (
	// LightningPaymentsTotal counts Lightning payments for email
	LightningPaymentsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "lightning_payments_total",
			Help:      "Total number of Lightning payments for email access",
		},
		[]string{"result"}, // success|failure|expired
	)

	// LightningPaymentAmount tracks payment amounts
	LightningPaymentAmount = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "lightning_payment_sats",
			Help:      "Lightning payment amounts in satoshis",
			Buckets:   []float64{1, 10, 50, 100, 500, 1000, 5000, 10000},
		},
	)
)

// Transport metrics
var (
	// SMTPConnectionsTotal counts SMTP connection attempts
	SMTPConnectionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "smtp_connections_total",
			Help:      "Total number of SMTP connection attempts",
		},
		[]string{"direction", "result"}, // direction: inbound|outbound, result: success|failure
	)

	// SMTPMessageSize tracks email sizes
	SMTPMessageSize = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "smtp_message_size_bytes",
			Help:      "SMTP message sizes in bytes",
			Buckets:   []float64{1024, 10240, 102400, 1048576, 10485760}, // 1KB, 10KB, 100KB, 1MB, 10MB
		},
	)
)

// Helper functions for common metric patterns

// RecordEmailSent records a sent email metric
func RecordEmailSent(transport string, encrypted bool, success bool) {
	encStr := "false"
	if encrypted {
		encStr = "true"
	}
	statusStr := "failure"
	if success {
		statusStr = "success"
	}
	EmailsSentTotal.WithLabelValues(transport, encStr, statusStr).Inc()
}

// RecordEmailReceived records a received email metric
func RecordEmailReceived(transport string, verified bool) {
	verStr := "false"
	if verified {
		verStr = "true"
	}
	EmailsReceivedTotal.WithLabelValues(transport, verStr).Inc()
}

// RecordNIP05Lookup records a NIP-05 lookup result
func RecordNIP05Lookup(cached bool, success bool) {
	if cached {
		NIP05LookupsTotal.WithLabelValues("cached").Inc()
	} else if success {
		NIP05LookupsTotal.WithLabelValues("success").Inc()
	} else {
		NIP05LookupsTotal.WithLabelValues("failure").Inc()
	}
}
