package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/coldforge/coldforge-email/internal/api"
	"github.com/coldforge/coldforge-email/internal/auth"
	"github.com/coldforge/coldforge-email/internal/config"
	"github.com/coldforge/coldforge-email/internal/storage"
	"github.com/gorilla/mux"
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

	// Initialize Stalwart client
	stalwartClient, err := auth.NewStalwartClient(
		cfg.StalwartAdminURL,
		cfg.StalwartAdminToken,
		logger,
	)
	if err != nil {
		logger.Fatal("Failed to initialize Stalwart client", zap.Error(err))
	}

	// Initialize NIP-46 auth handler
	authHandler, err := auth.NewNIP46Handler(
		cfg.NSECBunkerRelayURL,
		sessionStore,
		stalwartClient,
		logger,
	)
	if err != nil {
		logger.Fatal("Failed to initialize auth handler", zap.Error(err))
	}

	// Initialize API handler
	apiHandler := api.NewHandler(
		db,
		authHandler,
		stalwartClient,
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

	metricsServer := &http.Server{
		Addr:    cfg.MetricsAddr,
		Handler: metricsRouter,
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

	logger.Info("Shutdown complete")
}

// loggingMiddleware logs HTTP requests
func loggingMiddleware(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Create a response writer wrapper to capture status code
			wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			next.ServeHTTP(wrapped, r)

			duration := time.Since(start)
			logger.Info("HTTP request",
				zap.String("method", r.Method),
				zap.String("path", r.RequestURI),
				zap.Int("status", wrapped.statusCode),
				zap.Duration("duration", duration),
				zap.String("remote_addr", r.RemoteAddr),
			)
		})
	}
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
