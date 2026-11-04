package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"roslegacy/internal/api"
	"roslegacy/internal/auth"
	"roslegacy/internal/lobby"
	"roslegacy/internal/matchmaking"
	"roslegacy/internal/observability"
	"roslegacy/internal/profile"
	"roslegacy/internal/security"
	gamesession "roslegacy/internal/session"
	"roslegacy/internal/shop"
	"roslegacy/internal/statistics"
	"roslegacy/internal/storage"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	traceShutdown, traceExportEnabled, err := observability.Configure(context.Background(), "ros-api-server")
	if err != nil {
		logger.Error("api_tracing_init_failed", "error", err)
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := traceShutdown(shutdownCtx); err != nil {
			logger.Error("api_tracing_shutdown_failed", "error", err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	database, err := storage.NewPostgres(ctx, storage.PostgresConfig{URL: requiredEnv("DATABASE_URL"), MinConns: 1, MaxConns: 8})
	if err != nil {
		logger.Error("api_database_init_failed", "error", err)
		os.Exit(1)
	}
	defer database.Close()
	cache, err := storage.NewRedis(ctx, storage.RedisConfig{Addr: requiredEnv("REDIS_ADDR"), Password: os.Getenv("REDIS_PASSWORD")})
	if err != nil {
		logger.Error("api_redis_init_failed", "error", err)
		os.Exit(1)
	}
	defer cache.Close()

	signerSecret := os.Getenv("TOKEN_SIGNING_SECRET")
	if signerSecret == "" {
		logger.Error("api_config_invalid", "error", "TOKEN_SIGNING_SECRET is required")
		os.Exit(1)
	}
	var signer *security.TokenSigner
	previousSignerSecret := os.Getenv("TOKEN_SIGNING_PREVIOUS_SECRET")
	if previousSignerSecret == "" {
		signer, err = security.NewTokenSigner([]byte(signerSecret))
	} else {
		signer, err = security.NewRotatingTokenSigner([]byte(signerSecret), []byte(previousSignerSecret))
	}
	if err != nil {
		logger.Error("api_token_signer_init_failed", "error", err)
		os.Exit(1)
	}
	var recoverySender auth.RecoverySender
	recoveryWebhookURL := os.Getenv("ROS_RECOVERY_WEBHOOK_URL")
	if recoveryWebhookURL != "" {
		recoverySender, err = auth.NewWebhookRecoverySender(recoveryWebhookURL, os.Getenv("ROS_RECOVERY_WEBHOOK_SECRET"))
		if err != nil {
			logger.Error("api_recovery_delivery_init_failed", "error", err)
			os.Exit(1)
		}
	}
	authService, err := auth.NewServiceWithRecoverySender(database, signer,
		security.NewRedisRateLimiter(cache, "ros:rate:login", 10, time.Minute),
		security.NewRedisRateLimiter(cache, "ros:rate:register", 5, time.Hour), recoverySender, logger)
	if err != nil {
		logger.Error("api_auth_init_failed", "error", err)
		os.Exit(1)
	}
	lobbyService, err := lobby.NewRedisService(cache, 4)
	if err != nil {
		logger.Error("api_lobby_init_failed", "error", err)
		os.Exit(1)
	}
	profileService, err := profile.NewService(database)
	if err != nil {
		logger.Error("api_profile_init_failed", "error", err)
		os.Exit(1)
	}
	statisticsService, err := statistics.NewService(database)
	if err != nil {
		logger.Error("api_statistics_init_failed", "error", err)
		os.Exit(1)
	}
	queue, err := matchmaking.NewRedisQueue(cache)
	if err != nil {
		logger.Error("api_matchmaking_init_failed", "error", err)
		os.Exit(1)
	}
	domainStore, err := storage.NewDomainStore(database)
	if err != nil {
		logger.Error("api_domain_store_init_failed", "error", err)
		os.Exit(1)
	}
	gameSessionService, err := gamesession.NewServiceWithServers(domainStore, cache, envList("ROS_GAME_SERVER_IDS", envString("ROS_GAME_SERVER_ID", "game-server-local")))
	if err != nil {
		logger.Error("api_game_session_init_failed", "error", err)
		os.Exit(1)
	}
	shopService, err := shop.NewService(database)
	if err != nil {
		logger.Error("api_shop_init_failed", "error", err)
		os.Exit(1)
	}
	apiServer, err := api.NewServerWithGameSessionsAndShop(authService, lobbyService, queue, profileService, statisticsService, domainStore, gameSessionService, shopService, os.Getenv("ROS_GAME_SERVER_TOKEN"), logger)
	if err != nil {
		logger.Error("api_init_failed", "error", err)
		os.Exit(1)
	}
	httpServer := &http.Server{Addr: envString("ROS_API_HTTP_ADDR", ":8081"), Handler: apiServer.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	tlsCertFile := os.Getenv("ROS_API_TLS_CERT_FILE")
	tlsKeyFile := os.Getenv("ROS_API_TLS_KEY_FILE")
	if (tlsCertFile == "") != (tlsKeyFile == "") {
		logger.Error("api_tls_config_invalid", "error", "ROS_API_TLS_CERT_FILE and ROS_API_TLS_KEY_FILE must be set together")
		os.Exit(1)
	}
	var tlsConfig *tls.Config
	if tlsCertFile != "" {
		tlsConfig, err = security.LoadServerTLSConfig(tlsCertFile, tlsKeyFile)
		if err != nil {
			logger.Error("api_tls_init_failed", "error", err)
			os.Exit(1)
		}
	}
	go func() {
		var serveErr error
		if tlsConfig == nil {
			serveErr = httpServer.ListenAndServe()
		} else {
			listener, listenErr := tls.Listen("tcp", httpServer.Addr, tlsConfig)
			if listenErr != nil {
				serveErr = listenErr
			} else {
				serveErr = httpServer.Serve(listener)
			}
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logger.Error("api_server_failed", "error", serveErr)
		}
	}()

	stopCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	logger.Info("api_server_started", "addr", httpServer.Addr, "tls", tlsConfig != nil, "token_key_rotation", previousSignerSecret != "", "otel_export_enabled", traceExportEnabled)
	<-stopCtx.Done()
	shutdownCtx, shutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdown()
	_ = httpServer.Shutdown(shutdownCtx)
}

func requiredEnv(key string) string {
	value := os.Getenv(key)
	if value == "" {
		panic(key + " is required")
	}
	return value
}

func envString(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envList(key, fallback string) []string {
	raw := envString(key, fallback)
	values := strings.Split(raw, ",")
	result := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
