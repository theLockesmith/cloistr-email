package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"git.coldforge.xyz/coldforge/cloistr-email/internal/api"
	"git.coldforge.xyz/coldforge/cloistr-email/internal/auth"
	"git.coldforge.xyz/coldforge/cloistr-email/internal/config"
	"git.coldforge.xyz/coldforge/cloistr-email/internal/email"
	"git.coldforge.xyz/coldforge/cloistr-email/internal/encryption"
	"git.coldforge.xyz/coldforge/cloistr-email/internal/metrics"
	"git.coldforge.xyz/coldforge/cloistr-email/internal/storage"
	"git.coldforge.xyz/coldforge/cloistr-email/internal/transport"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

func main() {
	// Initialize logger
	logger, err := zap.NewDevelopment()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		logger.Fatal("Failed to load configuration", zap.Error(err))
	}

	logger.Info("Starting coldforge-email service",
		zap.String("version", "0.1.0"),
		zap.String("listen_addr", cfg.ListenAddr),
		zap.String("metrics_addr", cfg.MetricsAddr),
	)

	// Initialize storage
	db, err := storage.NewPostgres(cfg.DatabaseURL, logger)
	if err != nil {
		logger.Fatal("Failed to initialize database", zap.Error(err))
	}
	defer db.Close()

	// Run migrations
	if err := db.Migrate(context.Background()); err != nil {
		logger.Fatal("Failed to run migrations", zap.Error(err))
	}

	// Initialize Redis for session management
	sessionStore, err := storage.NewRedisSessionStore(cfg.RedisURL, logger)
	if err != nil {
		logger.Fatal("Failed to initialize session store", zap.Error(err))
	}

	// Initialize NIP-46 auth handler
	authHandler, err := auth.NewNIP46Handler(
		cfg.NSECBunkerRelayURL,
		sessionStore,
		logger,
	)
	if err != nil {
		logger.Fatal("Failed to initialize auth handler", zap.Error(err))
	}

	// Initialize API handler
	apiHandler := api.NewHandler(
		db,
		authHandler,
		sessionStore,
		cfg,
		logger,
	)

	// Setup routes
	router := mux.NewRouter()

	// Health check
	router.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"ok","service":"coldforge-email","timestamp":"%s"}`, time.Now().UTC().Format(time.RFC3339))
	}).Methods("GET")

	// Ready check
	router.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		if err := db.Ping(context.Background()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, `{"status":"not_ready","reason":"database"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"ready"}`)
	}).Methods("GET")

	// API v1 routes
	v1 := router.PathPrefix("/api/v1").Subrouter()

	// Authentication endpoints
	authRoutes := v1.PathPrefix("/auth").Subrouter()
	authRoutes.HandleFunc("/nip46/challenge", apiHandler.StartNIP46Auth).Methods("POST")
	authRoutes.HandleFunc("/nip46/verify", apiHandler.VerifyNIP46Auth).Methods("POST")
	authRoutes.HandleFunc("/logout", apiHandler.Logout).Methods("POST")

	// Email endpoints
	emailRoutes := v1.PathPrefix("/emails").Subrouter()
	emailRoutes.Use(apiHandler.AuthMiddleware)
	emailRoutes.HandleFunc("", apiHandler.ListEmails).Methods("GET")
	emailRoutes.HandleFunc("", apiHandler.SendEmail).Methods("POST")
	emailRoutes.HandleFunc("/{id}", apiHandler.GetEmail).Methods("GET")
	emailRoutes.HandleFunc("/{id}/reply", apiHandler.ReplyEmail).Methods("POST")
	emailRoutes.HandleFunc("/{id}", apiHandler.DeleteEmail).Methods("DELETE")

	// Keys & discovery endpoints
	keyRoutes := v1.PathPrefix("/keys").Subrouter()
	keyRoutes.Use(apiHandler.AuthMiddleware)
	keyRoutes.HandleFunc("/discover", apiHandler.DiscoverKey).Methods("GET")
	keyRoutes.HandleFunc("/import", apiHandler.ImportKey).Methods("POST")
	keyRoutes.HandleFunc("/mine", apiHandler.GetMyKey).Methods("GET")

	// Contacts endpoints
	contactRoutes := v1.PathPrefix("/contacts").Subrouter()
	contactRoutes.Use(apiHandler.AuthMiddleware)
	contactRoutes.HandleFunc("", apiHandler.ListContacts).Methods("GET")
	contactRoutes.HandleFunc("", apiHandler.AddContact).Methods("POST")
	contactRoutes.HandleFunc("/{id}", apiHandler.GetContact).Methods("GET")
	contactRoutes.HandleFunc("/{id}", apiHandler.DeleteContact).Methods("DELETE")

	// Middleware
	router.Use(loggingMiddleware(logger))
	router.Use(corsMiddleware())

	// Start API server
	apiServer := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start metrics server
	metricsRouter := mux.NewRouter()
	metricsRouter.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}).Methods("GET")
	metricsRouter.Handle("/metrics", promhttp.Handler())

	metricsServer := &http.Server{
		Addr:    cfg.MetricsAddr,
		Handler: metricsRouter,
	}

	// Initialize inbound SMTP server if enabled
	var smtpServer *transport.SMTPServer
	if cfg.SMTPInboundEnabled {
		// Create NIP-05 resolver for signature verification
		nip05Resolver := encryption.NewNIP05Resolver(logger)

		// Create inbound processor
		inboundProcessor := email.NewInboundProcessor(db, nip05Resolver, logger)

		// Create SMTP server config
		smtpConfig := &transport.SMTPServerConfig{
			ListenAddr:     cfg.SMTPInboundAddr,
			Domain:         cfg.SMTPInboundDomain,
			AllowedDomains: cfg.SMTPInboundDomains,
			TLSCertFile:    cfg.SMTPInboundTLSCert,
			TLSKeyFile:     cfg.SMTPInboundTLSKey,
		}

		// Create SMTP server
		smtpServer = transport.NewSMTPServer(smtpConfig, inboundProcessor, inboundProcessor, logger)
	}

	// Start servers in goroutines
	go func() {
		logger.Info("Starting API server", zap.String("addr", cfg.ListenAddr))
		if err := apiServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("API server error", zap.Error(err))
		}
	}()

	go func() {
		logger.Info("Starting metrics server", zap.String("addr", cfg.MetricsAddr))
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("Metrics server error", zap.Error(err))
		}
	}()

	// Start SMTP server if enabled
	if smtpServer != nil {
		go func() {
			logger.Info("Starting inbound SMTP server", zap.String("addr", cfg.SMTPInboundAddr))
			if err := smtpServer.Start(); err != nil {
				logger.Error("SMTP server error", zap.Error(err))
			}
		}()
	}

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	logger.Info("Shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := apiServer.Shutdown(ctx); err != nil {
		logger.Error("API server shutdown error", zap.Error(err))
	}

	if err := metricsServer.Shutdown(ctx); err != nil {
		logger.Error("Metrics server shutdown error", zap.Error(err))
	}

	if smtpServer != nil {
		if err := smtpServer.Stop(); err != nil {
			logger.Error("SMTP server shutdown error", zap.Error(err))
		}
	}

	logger.Info("Shutdown complete")
}

// loggingMiddleware logs HTTP requests and records metrics
func loggingMiddleware(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Create a response writer wrapper to capture status code
			wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			next.ServeHTTP(wrapped, r)

			duration := time.Since(start)

			// Log the request
			logger.Info("HTTP request",
				zap.String("method", r.Method),
				zap.String("path", r.RequestURI),
				zap.Int("status", wrapped.statusCode),
				zap.Duration("duration", duration),
				zap.String("remote_addr", r.RemoteAddr),
			)

			// Record metrics
			// Normalize path to avoid high cardinality (remove IDs)
			path := normalizePath(r.URL.Path)
			metrics.HTTPRequestsTotal.WithLabelValues(
				r.Method,
				path,
				strconv.Itoa(wrapped.statusCode),
			).Inc()
			metrics.HTTPRequestDuration.WithLabelValues(r.Method, path).Observe(duration.Seconds())
		})
	}
}

// normalizePath normalizes URL paths to avoid high cardinality metrics
// by replacing dynamic segments like UUIDs and IDs with placeholders
func normalizePath(path string) string {
	// Common patterns to normalize
	// /api/v1/emails/123e4567-e89b-12d3-a456-426614174000 -> /api/v1/emails/:id
	// /api/v1/contacts/abc123 -> /api/v1/contacts/:id
	segments := make([]string, 0)
	for _, seg := range splitPath(path) {
		if isIDSegment(seg) {
			segments = append(segments, ":id")
		} else {
			segments = append(segments, seg)
		}
	}
	if len(segments) == 0 {
		return "/"
	}
	return "/" + joinPath(segments)
}

func splitPath(path string) []string {
	result := make([]string, 0)
	for _, seg := range splitString(path, '/') {
		if seg != "" {
			result = append(result, seg)
		}
	}
	return result
}

func splitString(s string, sep rune) []string {
	var result []string
	current := ""
	for _, c := range s {
		if c == sep {
			if current != "" {
				result = append(result, current)
				current = ""
			}
		} else {
			current += string(c)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}

func joinPath(segments []string) string {
	result := ""
	for i, seg := range segments {
		if i > 0 {
			result += "/"
		}
		result += seg
	}
	return result
}

func isIDSegment(seg string) bool {
	// UUID pattern (8-4-4-4-12 hex chars)
	if len(seg) == 36 && seg[8] == '-' && seg[13] == '-' && seg[18] == '-' && seg[23] == '-' {
		return true
	}
	// Numeric ID
	if len(seg) > 0 && len(seg) < 20 {
		allDigits := true
		for _, c := range seg {
			if c < '0' || c > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			return true
		}
	}
	// Hex string (common for various IDs)
	if len(seg) >= 16 && len(seg) <= 64 {
		allHex := true
		for _, c := range seg {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				allHex = false
				break
			}
		}
		if allHex {
			return true
		}
	}
	return false
}

// corsMiddleware adds CORS headers
func corsMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusOK)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}
