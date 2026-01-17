package config

import (
	"fmt"
	"os"

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

	// Stalwart Mail Server
	StalwartAdminURL   string
	StalwartAdminToken string

	// Nostr
	NSECBunkerRelayURL string
	IdentityServiceURL string

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

		// Stalwart
		StalwartAdminURL:   getEnvRequired("STALWART_ADMIN_URL"),
		StalwartAdminToken: getEnvRequired("STALWART_ADMIN_TOKEN"),

		// Nostr
		NSECBunkerRelayURL: getEnvRequired("NSECBUNKER_RELAY_URL"),
		IdentityServiceURL: getEnv("IDENTITY_SERVICE_URL", "http://localhost:3000"),

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
