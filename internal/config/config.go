package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds all configuration for the service
type Config struct {
	// Server
	ListenAddr  string
	MetricsAddr string

	// Database
	DatabaseURL string

	// Cache
	RedisURL string

	// SMTP Outbound (for email delivery)
	SMTPHost         string
	SMTPPort         int
	SMTPUsername     string
	SMTPPassword     string
	SMTPDeliveryMode string // "relay", "direct", or "hybrid"
	SMTPLocalDomains []string

	// SMTP Inbound (for receiving email)
	SMTPInboundEnabled bool
	SMTPInboundAddr    string   // Address to listen on (e.g., ":25")
	SMTPInboundDomain  string   // Server hostname for HELO
	SMTPInboundDomains []string // Domains we accept mail for
	SMTPInboundTLSCert string   // Path to TLS cert (optional)
	SMTPInboundTLSKey  string   // Path to TLS key (optional)

	// DKIM signing configuration
	DKIMDomain     string
	DKIMSelector   string
	DKIMPrivateKey string

	// Blossom storage (attachment offload). Empty BlossomServers disables it.
	BlossomServers    []string
	BlossomRedundancy int

	// Nostr
	NSECBunkerRelayURL string
	IdentityServiceURL string

	// cloistr-me integration (address verification)
	CloistrMeURL    string // Base URL for cloistr-me internal API
	CloistrMeSecret string // Shared secret for internal API auth

	// Unified-auth: Cloistr signer session validation (slice 3 — auth only).
	// Empty = disabled (skip signer fallback in ValidateSession).
	SignerURL string

	// NostrConnectRelay is the relay used for the signer-as-bunker bootstrap
	// (Option D).  The server posts a nostrconnect:// URI to the signer, the
	// signer publishes a kind-24133 ACK, and the server upgrades the session
	// to a live bunker connection.  Defaults to wss://relay.cloistr.xyz.
	NostrConnectRelay string

	// Logging
	LogLevel string

	// Environment
	Environment string
}

// Load loads configuration from environment variables
func Load() (*Config, error) {
	// Load .env file if it exists (for local development)
	_ = godotenv.Load()

	return &Config{
		// Server
		ListenAddr:  getEnv("LISTEN_ADDR", "0.0.0.0:8080"),
		MetricsAddr: getEnv("METRICS_ADDR", "0.0.0.0:9090"),

		// Database
		DatabaseURL: getEnvRequired("DATABASE_URL"),

		// Cache
		RedisURL: getEnvRequired("REDIS_URL"),

		// SMTP Outbound (for email delivery)
		SMTPHost:         getEnv("SMTP_HOST", "localhost"),
		SMTPPort:         getEnvInt("SMTP_PORT", 587),
		SMTPUsername:     getEnv("SMTP_USERNAME", ""),
		SMTPPassword:     getEnv("SMTP_PASSWORD", ""),
		SMTPDeliveryMode: getEnv("SMTP_DELIVERY_MODE", "relay"),
		SMTPLocalDomains: getEnvList("SMTP_LOCAL_DOMAINS", []string{}),

		// SMTP Inbound (for receiving email)
		SMTPInboundEnabled: getEnvBool("SMTP_INBOUND_ENABLED", false),
		SMTPInboundAddr:    getEnv("SMTP_INBOUND_ADDR", ":25"),
		SMTPInboundDomain:  getEnv("SMTP_INBOUND_DOMAIN", "localhost"),
		SMTPInboundDomains: getEnvList("SMTP_INBOUND_DOMAINS", []string{}),
		SMTPInboundTLSCert: getEnv("SMTP_INBOUND_TLS_CERT", ""),
		SMTPInboundTLSKey:  getEnv("SMTP_INBOUND_TLS_KEY", ""),

		// DKIM (optional - for signing outbound email)
		DKIMDomain:     getEnv("DKIM_DOMAIN", ""),
		DKIMSelector:   getEnv("DKIM_SELECTOR", "mail"),
		DKIMPrivateKey: getEnv("DKIM_PRIVATE_KEY", ""),

		// Blossom (optional - for attachment offload)
		BlossomServers:    getEnvList("BLOSSOM_SERVERS", []string{}),
		BlossomRedundancy: getEnvInt("BLOSSOM_REDUNDANCY", 2),

		// Nostr
		NSECBunkerRelayURL: getEnvRequired("NSECBUNKER_RELAY_URL"),
		IdentityServiceURL: getEnv("IDENTITY_SERVICE_URL", "http://localhost:3000"),

		// cloistr-me integration (address verification)
		CloistrMeURL:    getEnv("CLOISTR_ME_URL", "http://cloistr-me.cloistr.svc.cluster.local:8080"),
		CloistrMeSecret: getEnv("CLOISTR_ME_SECRET", ""),

		// Unified-auth signer URL (empty = disabled)
		SignerURL: getEnv("MAIL_SIGNER_URL", "http://cloistr-signer.cloistr.svc.cluster.local:7777"),

		// NostrConnect relay for signer-as-bunker bootstrap (Option D)
		NostrConnectRelay: getEnv("MAIL_NOSTRCONNECT_RELAY", "wss://relay.cloistr.xyz"),

		// Logging
		LogLevel: getEnv("LOG_LEVEL", "info"),

		// Environment
		Environment: getEnv("ENVIRONMENT", "development"),
	}, nil
}

// getEnv gets an environment variable with a default value
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvRequired gets a required environment variable
func getEnvRequired(key string) string {
	value := os.Getenv(key)
	if value == "" {
		panic(fmt.Sprintf("Required environment variable not set: %s", key))
	}
	return value
}

// getEnvInt gets an environment variable as an integer with a default value
func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

// getEnvBool gets an environment variable as a boolean with a default value
func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolVal, err := strconv.ParseBool(value); err == nil {
			return boolVal
		}
	}
	return defaultValue
}

// getEnvList gets an environment variable as a comma-separated list
func getEnvList(key string, defaultValue []string) []string {
	if value := os.Getenv(key); value != "" {
		var result []string
		for _, item := range splitString(value, ',') {
			item = strings.TrimSpace(item)
			if item != "" {
				result = append(result, item)
			}
		}
		return result
	}
	return defaultValue
}

// splitString splits a string by a separator
func splitString(s string, sep rune) []string {
	var result []string
	current := ""
	for _, c := range s {
		if c == sep {
			result = append(result, current)
			current = ""
		} else {
			current += string(c)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}
