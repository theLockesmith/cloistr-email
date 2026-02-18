package config

import (
	"fmt"
	"os"
	"strconv"

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

	// SMTP (for outbound email delivery)
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string

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

		// SMTP (optional - for outbound email delivery)
		SMTPHost:     getEnv("SMTP_HOST", "localhost"),
		SMTPPort:     getEnvInt("SMTP_PORT", 587),
		SMTPUsername: getEnv("SMTP_USERNAME", ""),
		SMTPPassword: getEnv("SMTP_PASSWORD", ""),

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

// getEnvInt gets an environment variable as an integer with a default value
func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}
