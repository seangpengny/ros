package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"roslegacy/internal/game"
	gamesession "roslegacy/internal/session"
	"roslegacy/internal/storage"
)

func main() {
	var listenAddr string
	flag.StringVar(&listenAddr, "listen", envString("ROS_GAME_LISTEN_ADDR", ":9000"), "UDP listen address")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	config := game.DefaultConfig()
	config.ListenAddr = listenAddr
	config.TickRate = envInt("ROS_GAME_TICK_RATE", config.TickRate)
	config.MaxPlayers = envInt("ROS_GAME_MAX_PLAYERS", config.MaxPlayers)
	config.MatchDuration = envDuration("ROS_GAME_MATCH_DURATION", config.MatchDuration)
	config.ReconnectWindow = envDuration("ROS_GAME_RECONNECT_WINDOW", config.ReconnectWindow)
	config.ArenaRadius = envFloat("ROS_GAME_ARENA_RADIUS", config.ArenaRadius)
	config.MoveSpeed = envFloat("ROS_GAME_MOVE_SPEED", config.MoveSpeed)
	config.AttackRange = envFloat("ROS_GAME_ATTACK_RANGE", config.AttackRange)
	config.AttackDamage = envInt("ROS_GAME_ATTACK_DAMAGE", config.AttackDamage)
	config.AttackCooldown = envDuration("ROS_GAME_ATTACK_COOLDOWN", config.AttackCooldown)
	config.MagazineSize = envInt("ROS_GAME_MAGAZINE_SIZE", config.MagazineSize)
	config.ReloadDuration = envDuration("ROS_GAME_RELOAD_DURATION", config.ReloadDuration)
	config.GrenadeDamage = envInt("ROS_GAME_GRENADE_DAMAGE", config.GrenadeDamage)
	config.GrenadeRadius = envFloat("ROS_GAME_GRENADE_RADIUS", config.GrenadeRadius)
	config.GrenadeCooldown = envDuration("ROS_GAME_GRENADE_COOLDOWN", config.GrenadeCooldown)
	config.StagingDuration = envDurationAllowZero("ROS_GAME_STAGING_DURATION", config.StagingDuration)
	config.AircraftDuration = envDuration("ROS_GAME_AIRCRAFT_DURATION", config.AircraftDuration)
	config.ParachuteSpeed = envFloat("ROS_GAME_PARACHUTE_SPEED", config.ParachuteSpeed)
	config.ZoneFinalRadius = envFloat("ROS_GAME_ZONE_FINAL_RADIUS", config.ZoneFinalRadius)
	config.ZoneDamage = envFloat("ROS_GAME_ZONE_DAMAGE", config.ZoneDamage)
	config.KnockdownWindow = envDuration("ROS_GAME_KNOCKDOWN_WINDOW", config.KnockdownWindow)
	config.LootCount = envInt("ROS_GAME_LOOT_COUNT", config.LootCount)
	config.SupplyDropCount = envInt("ROS_GAME_SUPPLY_DROP_COUNT", config.SupplyDropCount)
	config.VehicleCount = envInt("ROS_GAME_VEHICLE_COUNT", config.VehicleCount)

	var completionSink game.CompletionSink
	if databaseURL := os.Getenv("DATABASE_URL"); databaseURL != "" {
		database, databaseErr := storage.NewPostgres(context.Background(), storage.PostgresConfig{URL: databaseURL, MinConns: 1, MaxConns: 4})
		if databaseErr != nil {
			logger.Error("game_database_init_failed", "error", databaseErr)
			os.Exit(1)
		}
		defer database.Close()
		domainStore, storeErr := storage.NewDomainStore(database)
		if storeErr != nil {
			logger.Error("game_domain_store_init_failed", "error", storeErr)
			os.Exit(1)
		}
		completionSink = domainStore
	}
	server, err := game.NewServerWithSink(config, logger, completionSink)
	if err != nil {
		logger.Error("game_server_init_failed", "error", err)
		os.Exit(1)
	}
	defer server.Close()

	healthAddr := envString("ROS_GAME_HEALTH_ADDR", ":8080")
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writer.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = writer.Write([]byte(server.Metrics()))
	})
	mux.HandleFunc("/healthz", func(writer http.ResponseWriter, request *http.Request) {
		ready := server.Ready()
		writer.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(writer).Encode(map[string]any{"status": map[bool]string{true: "ok", false: "starting"}[ready]})
	})
	mux.HandleFunc("/readyz", func(writer http.ResponseWriter, request *http.Request) {
		ready := server.Ready()
		if !ready {
			writer.WriteHeader(http.StatusServiceUnavailable)
		} else {
			writer.WriteHeader(http.StatusOK)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"status": map[bool]string{true: "ok", false: "starting"}[ready]})
	})
	health := &http.Server{Addr: healthAddr, Handler: mux}
	go func() {
		if err := health.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("health_server_failed", "error", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	logger.Info("game_server_started", "udp_addr", server.Addr().String(), "health_addr", healthAddr, "tick_rate", config.TickRate, "max_players", config.MaxPlayers)

	var lifecycle *gamesession.LifecycleClient
	var heartbeatCancel context.CancelFunc
	var heartbeatDone <-chan error
	apiBaseURL := os.Getenv("ROS_API_BASE_URL")
	sessionID := os.Getenv("ROS_GAME_SESSION_ID")
	serverToken := os.Getenv("ROS_GAME_SERVER_TOKEN")
	if apiBaseURL != "" || sessionID != "" || serverToken != "" {
		if apiBaseURL == "" || sessionID == "" || serverToken == "" {
			logger.Error("game_session_lifecycle_config_invalid", "error", "ROS_API_BASE_URL, ROS_GAME_SESSION_ID, and ROS_GAME_SERVER_TOKEN must be set together")
			os.Exit(1)
		}
		lifecycle, err = gamesession.NewLifecycleClient(apiBaseURL, sessionID, envString("ROS_GAME_SERVER_ID", "game-server-local"), serverToken, nil)
		if err != nil {
			logger.Error("game_session_lifecycle_init_failed", "error", err)
			os.Exit(1)
		}
		if _, err := lifecycle.Start(ctx); err != nil {
			logger.Error("game_session_start_failed", "error", err, "session_id", sessionID)
			os.Exit(1)
		}
		lifecycleCtx, cancel := context.WithCancel(ctx)
		heartbeatCancel = cancel
		done := make(chan error, 1)
		heartbeatDone = done
		go func() {
			done <- lifecycle.HeartbeatLoop(lifecycleCtx, envDuration("ROS_GAME_HEARTBEAT_INTERVAL", gamesession.DefaultHeartbeatInterval))
		}()
		logger.Info("game_session_lifecycle_started", "session_id", sessionID, "server_id", envString("ROS_GAME_SERVER_ID", "game-server-local"))
	}

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	runDone := make(chan error, 1)
	go func() {
		runDone <- server.Run(runCtx)
	}()

	var runErr error
	var heartbeatErr error
	heartbeatObserved := false
	if heartbeatDone != nil {
		select {
		case runErr = <-runDone:
		case heartbeatErr = <-heartbeatDone:
			heartbeatObserved = true
			if heartbeatErr != nil {
				cancelRun()
			}
			runErr = <-runDone
		}
	} else {
		runErr = <-runDone
	}
	cancelRun()
	if heartbeatCancel != nil {
		heartbeatCancel()
		if !heartbeatObserved {
			heartbeatErr = <-heartbeatDone
		}
	}
	if heartbeatErr != nil {
		logger.Error("game_session_heartbeat_failed", "error", heartbeatErr, "session_id", sessionID)
	}
	if lifecycle != nil {
		completeCtx, completeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		if _, completeErr := lifecycle.Complete(completeCtx); completeErr != nil {
			logger.Error("game_session_complete_failed", "error", completeErr, "session_id", sessionID)
		}
		completeCancel()
	}
	if runErr != nil {
		logger.Error("game_server_failed", "error", runErr)
		os.Exit(1)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = health.Shutdown(shutdownCtx)
}

func envString(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(key))
	if err != nil || value == 0 {
		return fallback
	}
	return value
}

func envFloat(key string, fallback float64) float64 {
	value, err := strconv.ParseFloat(os.Getenv(key), 64)
	if err != nil || value == 0 {
		return fallback
	}
	return value
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value, err := time.ParseDuration(os.Getenv(key))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func envDurationAllowZero(key string, fallback time.Duration) time.Duration {
	value, err := time.ParseDuration(os.Getenv(key))
	if err != nil || value < 0 {
		return fallback
	}
	return value
}
