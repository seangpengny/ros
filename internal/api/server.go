package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"roslegacy/internal/auth"
	"roslegacy/internal/lobby"
	"roslegacy/internal/matchmaking"
	"roslegacy/internal/metrics"
	"roslegacy/internal/observability"
	"roslegacy/internal/profile"
	"roslegacy/internal/security"
	gamesession "roslegacy/internal/session"
	"roslegacy/internal/shop"
	"roslegacy/internal/statistics"
	"roslegacy/internal/storage"
)

type Server struct {
	auth            *auth.Service
	lobbies         lobbyService
	queues          matchmakingService
	profiles        *profile.Service
	stats           *statistics.Service
	domain          *storage.DomainStore
	gameSessions    *gamesession.Service
	gameServerToken string
	shop            *shop.Service
	metrics         *metrics.Registry
	logger          *slog.Logger
}

type lobbyService interface {
	Create(string, string, string) (lobby.Lobby, error)
	Join(string, string) (lobby.Lobby, error)
	SetReady(string, string, bool) (lobby.Lobby, error)
	Invite(string, string, string) (lobby.Invitation, error)
	ListInvitations(string) ([]lobby.Invitation, error)
	GetInvitation(string) (lobby.Invitation, error)
	AcceptInvitation(string, string) (lobby.Lobby, error)
	DeclineInvitation(string, string) (lobby.Invitation, error)
	SetPresence(string, bool) (lobby.Presence, error)
	GetPresence(string) (lobby.Presence, error)
	Allocate(string) (lobby.Lobby, error)
	Close(string) error
	Get(string) (lobby.Lobby, error)
}

type matchmakingService interface {
	Enqueue(string, string, string, time.Time) (matchmaking.Ticket, error)
	Cancel(string) bool
	CancelFor(string, string) bool
	Match(string, string, int) (matchmaking.Allocation, bool)
	MatchFor(string, string, string, int) (matchmaking.Allocation, bool)
	Len() int
}

func NewServer(authService *auth.Service, lobbyService lobbyService, queue matchmakingService, logger *slog.Logger) (*Server, error) {
	return NewServerWithPersistence(authService, lobbyService, queue, nil, nil, nil, logger)
}

func NewServerWithDomains(authService *auth.Service, lobbyService lobbyService, queue matchmakingService, profileService *profile.Service, statisticsService *statistics.Service, logger *slog.Logger) (*Server, error) {
	return NewServerWithPersistence(authService, lobbyService, queue, profileService, statisticsService, nil, logger)
}

func NewServerWithPersistence(authService *auth.Service, lobbyService lobbyService, queue matchmakingService, profileService *profile.Service, statisticsService *statistics.Service, domainStore *storage.DomainStore, logger *slog.Logger) (*Server, error) {
	return NewServerWithGameSessions(authService, lobbyService, queue, profileService, statisticsService, domainStore, nil, "", logger)
}

func NewServerWithGameSessions(authService *auth.Service, lobbyService lobbyService, queue matchmakingService, profileService *profile.Service, statisticsService *statistics.Service, domainStore *storage.DomainStore, gameSessionService *gamesession.Service, gameServerToken string, logger *slog.Logger) (*Server, error) {
	return NewServerWithGameSessionsAndShop(authService, lobbyService, queue, profileService, statisticsService, domainStore, gameSessionService, nil, gameServerToken, logger)
}

func NewServerWithGameSessionsAndShop(authService *auth.Service, lobbyService lobbyService, queue matchmakingService, profileService *profile.Service, statisticsService *statistics.Service, domainStore *storage.DomainStore, gameSessionService *gamesession.Service, shopService *shop.Service, gameServerToken string, logger *slog.Logger) (*Server, error) {
	if authService == nil || lobbyService == nil || queue == nil {
		return nil, errors.New("auth, lobby, and matchmaking services are required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{auth: authService, lobbies: lobbyService, queues: queue, profiles: profileService, stats: statisticsService, domain: domainStore, gameSessions: gameSessionService, shop: shopService, gameServerToken: gameServerToken, metrics: metrics.NewRegistry(), logger: logger}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/readyz", s.health)
	mux.HandleFunc("/metrics", s.metricsHandler)
	mux.HandleFunc("/v1/auth/register", s.register)
	mux.HandleFunc("/v1/auth/login", s.login)
	mux.HandleFunc("/v1/auth/refresh", s.refresh)
	mux.HandleFunc("/v1/auth/logout", s.logout)
	mux.HandleFunc("/v1/auth/recovery/request", s.recoveryRequest)
	mux.HandleFunc("/v1/auth/recovery/complete", s.recoveryComplete)
	mux.HandleFunc("/v1/auth/sessions", s.sessions)
	mux.HandleFunc("/v1/auth/sessions/", s.sessionRoute)
	mux.HandleFunc("/v1/profile", s.profile)
	mux.HandleFunc("/v1/shop/catalog", s.shopCatalog)
	mux.HandleFunc("/v1/shop/purchase", s.shopPurchase)
	mux.HandleFunc("/v1/stats", s.statsRoute)
	mux.HandleFunc("/v1/stats/history", s.statsHistory)
	mux.HandleFunc("/v1/leaderboards", s.leaderboards)
	mux.HandleFunc("/v1/lobbies", s.createLobby)
	mux.HandleFunc("/v1/lobbies/", s.lobbyRoute)
	mux.HandleFunc("/v1/lobby-invites", s.invitations)
	mux.HandleFunc("/v1/lobby-invites/", s.invitationRoute)
	mux.HandleFunc("/v1/presence", s.setPresence)
	mux.HandleFunc("/v1/presence/", s.getPresence)
	mux.HandleFunc("/v1/matchmaking/queue", s.enqueue)
	mux.HandleFunc("/v1/matchmaking/queue/", s.cancelQueue)
	mux.HandleFunc("/v1/matchmaking/match", s.match)
	mux.HandleFunc("/v1/sessions", s.createGameSession)
	mux.HandleFunc("/v1/sessions/", s.gameSessionRoute)
	mux.HandleFunc("/v2/ros/login_mobile_ios_v2", s.legacyLogin)
	mux.HandleFunc("/v2/ros/login", s.legacyLogin)
	mux.HandleFunc("/v2/ros/relaunch_mobile_ios_v2", s.legacyRelaunch)
	mux.HandleFunc("/v2/ros/relaunch", s.legacyRelaunch)
	mux.HandleFunc("/v2/account/rosl/exchangeTicket", s.legacyExchangeTicket)
	mux.HandleFunc("/v2/account/exchange", s.legacyExchangeTicket)
	mux.HandleFunc("/v2/ros/online-count", s.legacyOnlineCount)
	mux.HandleFunc("/v2/ros/online", s.legacyOnlineCount)
	return requestContextMiddleware(s.metricsMiddleware(mux))
}

type credentialsRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DeviceID    string `json:"device_id"`
	DisplayName string `json:"display_name"`
}

func (s *Server) register(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var input credentialsRequest
	if !decodeJSON(writer, request, &input) {
		return
	}
	result, err := s.auth.Register(request.Context(), input.Email, input.Password, input.DisplayName, input.DeviceID)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(writer, http.StatusCreated, result)
}

func (s *Server) login(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var input credentialsRequest
	if !decodeJSON(writer, request, &input) {
		return
	}
	result, err := s.auth.Login(request.Context(), input.Email, input.Password, input.DeviceID)
	if err != nil {
		writeError(writer, http.StatusUnauthorized, "invalid credentials")
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (s *Server) refresh(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var input struct {
		RefreshToken string `json:"refresh_token"`
		DeviceID     string `json:"device_id"`
	}
	if !decodeJSON(writer, request, &input) {
		return
	}
	result, err := s.auth.Refresh(request.Context(), input.RefreshToken, input.DeviceID)
	if err != nil {
		writeError(writer, http.StatusUnauthorized, "invalid refresh token")
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (s *Server) logout(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var input struct {
		RefreshToken string `json:"refresh_token"`
	}
	if !decodeJSON(writer, request, &input) {
		return
	}
	if err := s.auth.Logout(request.Context(), input.RefreshToken); err != nil {
		writeError(writer, http.StatusUnauthorized, "invalid refresh token")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"revoked": true})
}

func (s *Server) recoveryRequest(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var input struct {
		Email string `json:"email"`
	}
	if !decodeJSON(writer, request, &input) {
		return
	}
	if err := s.auth.RequestRecovery(request.Context(), input.Email); err != nil {
		if security.ValidateEmail(strings.TrimSpace(input.Email)) != nil {
			writeError(writer, http.StatusBadRequest, "invalid email")
			return
		}
		s.logger.Error("recovery_request_failed", "error", err)
	}
	// Always return the same response for a syntactically valid address so the
	// endpoint cannot be used to enumerate accounts or delivery failures.
	writeJSON(writer, http.StatusAccepted, map[string]bool{"accepted": true})
}

func (s *Server) recoveryComplete(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var input struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !decodeJSON(writer, request, &input) {
		return
	}
	if err := s.auth.CompleteRecovery(request.Context(), input.Token, input.Password); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid or expired recovery token")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"reset": true})
}

func (s *Server) sessions(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	claims, ok := s.claims(writer, request)
	if !ok {
		return
	}
	result, err := s.auth.ListSessions(request.Context(), claims.Subject)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "session listing unavailable")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"sessions": result})
}

func (s *Server) sessionRoute(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodDelete {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	claims, ok := s.claims(writer, request)
	if !ok {
		return
	}
	sessionID := strings.TrimPrefix(request.URL.Path, "/v1/auth/sessions/")
	if err := s.auth.RevokeSession(request.Context(), claims.Subject, sessionID); err != nil {
		if errors.Is(err, auth.ErrSessionRevoked) {
			writeError(writer, http.StatusNotFound, "session not found")
			return
		}
		writeError(writer, http.StatusServiceUnavailable, "session revocation unavailable")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"revoked": true})
}

func (s *Server) profile(writer http.ResponseWriter, request *http.Request) {
	if (request.Method != http.MethodGet && request.Method != http.MethodPatch) || s.profiles == nil {
		if s.profiles == nil {
			writeError(writer, http.StatusNotImplemented, "profile service is not configured")
		} else {
			writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	claims, ok := s.claims(writer, request)
	if !ok {
		return
	}
	if request.Method == http.MethodPatch {
		var input struct {
			DisplayName *string        `json:"display_name"`
			Preferences map[string]any `json:"preferences"`
		}
		if !decodeJSON(writer, request, &input) {
			return
		}
		result, err := s.profiles.Update(request.Context(), claims.Subject, input.DisplayName, input.Preferences)
		if err != nil {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(writer, http.StatusOK, result)
		return
	}
	result, err := s.profiles.Get(request.Context(), claims.Subject)
	if err != nil {
		writeError(writer, http.StatusNotFound, "profile not found")
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (s *Server) shopCatalog(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet || s.shop == nil {
		if s.shop == nil {
			writeError(writer, http.StatusNotImplemented, "shop service is not configured")
		} else {
			writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	claims, ok := s.claims(writer, request)
	if !ok {
		return
	}
	result, err := s.shop.Catalog(request.Context(), claims.Subject)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "shop catalog unavailable")
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (s *Server) shopPurchase(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || s.shop == nil {
		if s.shop == nil {
			writeError(writer, http.StatusNotImplemented, "shop service is not configured")
		} else {
			writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	claims, ok := s.claims(writer, request)
	if !ok {
		return
	}
	var input struct {
		ItemID   string `json:"item_id"`
		Quantity int    `json:"quantity"`
	}
	if !decodeJSON(writer, request, &input) {
		return
	}
	result, err := s.shop.Purchase(request.Context(), claims.Subject, input.ItemID, input.Quantity)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(writer, http.StatusCreated, result)
}

func (s *Server) statsRoute(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet || s.stats == nil {
		if s.stats == nil {
			writeError(writer, http.StatusNotImplemented, "statistics service is not configured")
		} else {
			writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	claims, ok := s.claims(writer, request)
	if !ok {
		return
	}
	result, err := s.stats.Get(request.Context(), claims.Subject)
	if err != nil {
		writeError(writer, http.StatusNotFound, "statistics not found")
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (s *Server) statsHistory(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet || s.stats == nil {
		if s.stats == nil {
			writeError(writer, http.StatusNotImplemented, "statistics service is not configured")
		} else {
			writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	claims, ok := s.claims(writer, request)
	if !ok {
		return
	}
	limit := 20
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "invalid history limit")
			return
		}
		limit = parsed
	}
	result, err := s.stats.History(request.Context(), claims.Subject, limit)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"matches": result})
}

func (s *Server) leaderboards(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet || s.stats == nil {
		if s.stats == nil {
			writeError(writer, http.StatusNotImplemented, "statistics service is not configured")
		} else {
			writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	if _, ok := s.claims(writer, request); !ok {
		return
	}
	query := request.URL.Query()
	season, mode, region := query.Get("season"), query.Get("mode"), query.Get("region")
	limit := 50
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "invalid leaderboard limit")
			return
		}
		limit = parsed
	}
	result, err := s.stats.Leaderboard(request.Context(), season, mode, region, limit)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"entries": result})
}

func (s *Server) createLobby(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	claims, ok := s.claims(writer, request)
	if !ok {
		return
	}
	var input struct {
		Mode   string `json:"mode"`
		Region string `json:"region"`
	}
	if !decodeJSON(writer, request, &input) {
		return
	}
	result, err := s.lobbies.Create(claims.Subject, input.Mode, input.Region)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	if s.domain != nil {
		if err := s.domain.UpsertLobby(request.Context(), result); err != nil {
			writeError(writer, http.StatusServiceUnavailable, "lobby persistence unavailable")
			return
		}
	}
	writeJSON(writer, http.StatusCreated, result)
}

func (s *Server) lobbyRoute(writer http.ResponseWriter, request *http.Request) {
	claims, ok := s.claims(writer, request)
	if !ok {
		return
	}
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) < 3 {
		writeError(writer, http.StatusNotFound, "lobby route not found")
		return
	}
	lobbyID := parts[2]
	if len(parts) == 3 && request.Method == http.MethodGet {
		result, err := s.lobbies.Get(lobbyID)
		if err != nil {
			writeError(writer, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(writer, http.StatusOK, result)
		return
	}
	if len(parts) != 4 || request.Method != http.MethodPost {
		writeError(writer, http.StatusNotFound, "lobby route not found")
		return
	}
	switch parts[3] {
	case "join":
		result, err := s.lobbies.Join(lobbyID, claims.Subject)
		if err != nil {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		}
		if s.domain != nil {
			if err := s.domain.UpsertLobby(request.Context(), result); err != nil {
				writeError(writer, http.StatusServiceUnavailable, "lobby persistence unavailable")
				return
			}
		}
		writeJSON(writer, http.StatusOK, result)
	case "ready":
		var input struct {
			Ready bool `json:"ready"`
		}
		if !decodeJSON(writer, request, &input) {
			return
		}
		result, err := s.lobbies.SetReady(lobbyID, claims.Subject, input.Ready)
		if err != nil {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		}
		if s.domain != nil {
			if err := s.domain.UpsertLobby(request.Context(), result); err != nil {
				writeError(writer, http.StatusServiceUnavailable, "lobby persistence unavailable")
				return
			}
		}
		writeJSON(writer, http.StatusOK, result)
	case "invites":
		var input struct {
			AccountID string `json:"account_id"`
		}
		if !decodeJSON(writer, request, &input) {
			return
		}
		result, err := s.lobbies.Invite(lobbyID, claims.Subject, input.AccountID)
		if err != nil {
			if strings.Contains(err.Error(), "owner required") {
				writeError(writer, http.StatusForbidden, err.Error())
			} else {
				writeError(writer, http.StatusBadRequest, err.Error())
			}
			return
		}
		if s.domain != nil {
			if err := s.domain.UpsertLobbyInvitation(request.Context(), result); err != nil {
				writeError(writer, http.StatusServiceUnavailable, "lobby invitation persistence unavailable")
				return
			}
		}
		writeJSON(writer, http.StatusCreated, result)
	case "allocate":
		current, err := s.lobbies.Get(lobbyID)
		if err != nil {
			writeError(writer, http.StatusNotFound, err.Error())
			return
		}
		if current.OwnerID != claims.Subject {
			writeError(writer, http.StatusForbidden, "lobby owner required")
			return
		}
		result, err := s.lobbies.Allocate(lobbyID)
		if err != nil {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		}
		if s.domain != nil {
			if err := s.domain.UpsertLobby(request.Context(), result); err != nil {
				writeError(writer, http.StatusServiceUnavailable, "lobby persistence unavailable")
				return
			}
		}
		writeJSON(writer, http.StatusOK, result)
	default:
		writeError(writer, http.StatusNotFound, "lobby route not found")
	}
}

func (s *Server) invitations(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	claims, ok := s.claims(writer, request)
	if !ok {
		return
	}
	result, err := s.lobbies.ListInvitations(claims.Subject)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "lobby invitations unavailable")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"invitations": result})
}

func (s *Server) invitationRoute(writer http.ResponseWriter, request *http.Request) {
	claims, ok := s.claims(writer, request)
	if !ok {
		return
	}
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) != 4 || request.Method != http.MethodPost {
		writeError(writer, http.StatusNotFound, "invitation route not found")
		return
	}
	invitationID := parts[2]
	switch parts[3] {
	case "accept":
		result, err := s.lobbies.AcceptInvitation(invitationID, claims.Subject)
		if err != nil {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		}
		if s.domain != nil {
			if err := s.domain.UpsertLobby(request.Context(), result); err != nil {
				writeError(writer, http.StatusServiceUnavailable, "lobby persistence unavailable")
				return
			}
			invitation, invitationErr := s.lobbies.GetInvitation(invitationID)
			if invitationErr != nil {
				writeError(writer, http.StatusServiceUnavailable, "lobby invitation persistence unavailable")
				return
			}
			if err := s.domain.UpsertLobbyInvitation(request.Context(), invitation); err != nil {
				writeError(writer, http.StatusServiceUnavailable, "lobby invitation persistence unavailable")
				return
			}
		}
		writeJSON(writer, http.StatusOK, result)
	case "decline":
		result, err := s.lobbies.DeclineInvitation(invitationID, claims.Subject)
		if err != nil {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		}
		if s.domain != nil {
			if err := s.domain.UpsertLobbyInvitation(request.Context(), result); err != nil {
				writeError(writer, http.StatusServiceUnavailable, "lobby invitation persistence unavailable")
				return
			}
		}
		writeJSON(writer, http.StatusOK, result)
	default:
		writeError(writer, http.StatusNotFound, "invitation route not found")
	}
}

func (s *Server) createGameSession(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.gameSessions == nil {
		writeError(writer, http.StatusNotImplemented, "game session service is not configured")
		return
	}
	claims, ok := s.claims(writer, request)
	if !ok {
		return
	}
	var input struct {
		LobbyID    string `json:"lobby_id"`
		MaxPlayers int    `json:"max_players"`
	}
	if !decodeJSON(writer, request, &input) {
		return
	}
	current, err := s.lobbies.Get(input.LobbyID)
	if err != nil {
		writeError(writer, http.StatusNotFound, err.Error())
		return
	}
	if current.OwnerID != claims.Subject {
		writeError(writer, http.StatusForbidden, "lobby owner required")
		return
	}
	if current.Status != lobby.StatusReady {
		writeError(writer, http.StatusBadRequest, "lobby is not ready")
		return
	}
	if input.MaxPlayers == 0 {
		input.MaxPlayers = len(current.Members)
	}
	result, err := s.gameSessions.Create(request.Context(), current.ID, current.Mode, current.Region, input.MaxPlayers)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	allocated, err := s.lobbies.Allocate(current.ID)
	if err != nil {
		_ = s.gameSessions.Fail(request.Context(), result.ID, result.AssignedServer)
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	if s.domain != nil {
		if err := s.domain.UpsertLobby(request.Context(), allocated); err != nil {
			_ = s.gameSessions.Fail(request.Context(), result.ID, result.AssignedServer)
			writeError(writer, http.StatusServiceUnavailable, "lobby persistence unavailable")
			return
		}
	}
	writeJSON(writer, http.StatusCreated, result)
}

func (s *Server) gameSessionRoute(writer http.ResponseWriter, request *http.Request) {
	if s.gameSessions == nil {
		writeError(writer, http.StatusNotImplemented, "game session service is not configured")
		return
	}
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) < 3 {
		writeError(writer, http.StatusNotFound, "session route not found")
		return
	}
	sessionID := parts[2]
	if len(parts) == 3 && request.Method == http.MethodGet {
		claims, ok := s.claims(writer, request)
		if !ok {
			return
		}
		result, err := s.gameSessions.Get(request.Context(), sessionID)
		if err != nil {
			writeError(writer, http.StatusNotFound, err.Error())
			return
		}
		if !s.sessionMember(request, claims.Subject, result) {
			writeError(writer, http.StatusForbidden, "session member required")
			return
		}
		writeJSON(writer, http.StatusOK, result)
		return
	}
	if len(parts) != 4 || request.Method != http.MethodPost {
		writeError(writer, http.StatusNotFound, "session route not found")
		return
	}
	switch parts[3] {
	case "admit":
		claims, ok := s.claims(writer, request)
		if !ok {
			return
		}
		result, err := s.gameSessions.Get(request.Context(), sessionID)
		if err != nil {
			writeError(writer, http.StatusNotFound, err.Error())
			return
		}
		if !s.sessionMember(request, claims.Subject, result) {
			writeError(writer, http.StatusForbidden, "session member required")
			return
		}
		var input struct {
			TeamID string `json:"team_id"`
		}
		if !decodeJSON(writer, request, &input) {
			return
		}
		if err := s.gameSessions.Admit(request.Context(), sessionID, claims.Subject, input.TeamID); err != nil {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		}
		result, err = s.gameSessions.Get(request.Context(), sessionID)
		if err != nil {
			writeError(writer, http.StatusServiceUnavailable, "session read unavailable")
			return
		}
		writeJSON(writer, http.StatusOK, result)
	case "start":
		serverID, ok := s.gameServerAuthorization(writer, request)
		if !ok {
			return
		}
		result, err := s.gameSessions.Start(request.Context(), sessionID, serverID)
		if err != nil {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(writer, http.StatusOK, result)
	case "heartbeat":
		serverID, ok := s.gameServerAuthorization(writer, request)
		if !ok {
			return
		}
		result, err := s.gameSessions.Heartbeat(request.Context(), sessionID, serverID)
		if err != nil {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(writer, http.StatusOK, result)
	case "complete":
		serverID, ok := s.gameServerAuthorization(writer, request)
		if !ok {
			return
		}
		result, err := s.gameSessions.Complete(request.Context(), sessionID, serverID)
		if err != nil {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		}
		if result.LobbyID != "" {
			if err := s.lobbies.Close(result.LobbyID); err == nil && s.domain != nil {
				if closed, closeErr := s.lobbies.Get(result.LobbyID); closeErr == nil {
					if persistErr := s.domain.UpsertLobby(request.Context(), closed); persistErr != nil {
						writeError(writer, http.StatusServiceUnavailable, "lobby cleanup unavailable")
						return
					}
				}
			}
		}
		writeJSON(writer, http.StatusOK, result)
	default:
		writeError(writer, http.StatusNotFound, "session route not found")
	}
}

func (s *Server) sessionMember(request *http.Request, accountID string, value gamesession.Session) bool {
	if value.LobbyID == "" {
		return false
	}
	current, err := s.lobbies.Get(value.LobbyID)
	if err != nil {
		return false
	}
	if current.OwnerID == accountID {
		return true
	}
	for _, member := range current.Members {
		if member.AccountID == accountID {
			return true
		}
	}
	return false
}

func (s *Server) gameServerAuthorization(writer http.ResponseWriter, request *http.Request) (string, bool) {
	if s.gameServerToken == "" || subtle.ConstantTimeCompare([]byte(s.gameServerToken), []byte(request.Header.Get("X-Game-Server-Token"))) != 1 {
		writeError(writer, http.StatusForbidden, "game server authorization required")
		return "", false
	}
	serverID := strings.TrimSpace(request.Header.Get("X-Game-Server-ID"))
	if serverID == "" {
		writeError(writer, http.StatusBadRequest, "game server id is required")
		return "", false
	}
	return serverID, true
}

func (s *Server) setPresence(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	claims, ok := s.claims(writer, request)
	if !ok {
		return
	}
	var input struct {
		Online bool `json:"online"`
	}
	if !decodeJSON(writer, request, &input) {
		return
	}
	result, err := s.lobbies.SetPresence(claims.Subject, input.Online)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (s *Server) getPresence(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if _, ok := s.claims(writer, request); !ok {
		return
	}
	accountID := strings.TrimPrefix(request.URL.Path, "/v1/presence/")
	result, err := s.lobbies.GetPresence(accountID)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (s *Server) enqueue(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	claims, ok := s.claims(writer, request)
	if !ok {
		return
	}
	var input struct{ Mode, Region string }
	if !decodeJSON(writer, request, &input) {
		return
	}
	ticket, err := s.queues.Enqueue(claims.Subject, input.Mode, input.Region, time.Now().UTC())
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	if s.domain != nil {
		if err := s.domain.UpsertTicket(request.Context(), ticket); err != nil {
			writeError(writer, http.StatusServiceUnavailable, "matchmaking persistence unavailable")
			return
		}
	}
	writeJSON(writer, http.StatusCreated, ticket)
}

func (s *Server) cancelQueue(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodDelete {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	claims, ok := s.claims(writer, request)
	if !ok {
		return
	}
	ticketID := strings.TrimPrefix(request.URL.Path, "/v1/matchmaking/queue/")
	if !s.queues.CancelFor(ticketID, claims.Subject) {
		writeError(writer, http.StatusNotFound, "ticket not found or not owned by caller")
		return
	}
	if s.domain != nil {
		if err := s.domain.CancelTicket(request.Context(), ticketID); err != nil {
			writeError(writer, http.StatusServiceUnavailable, "matchmaking persistence unavailable")
			return
		}
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"cancelled": true})
}

func (s *Server) match(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	claims, ok := s.claims(writer, request)
	if !ok {
		return
	}
	var input struct {
		Mode   string `json:"mode"`
		Region string `json:"region"`
		Size   int    `json:"size"`
	}
	if !decodeJSON(writer, request, &input) {
		return
	}
	if input.Size < 2 || input.Size > 100 || matchmaking.ValidateMode(input.Mode) != nil || matchmaking.ValidateRegion(input.Region) != nil {
		writeError(writer, http.StatusBadRequest, "invalid match mode, region, or size")
		return
	}
	allocation, ok := s.queues.MatchFor(claims.Subject, input.Mode, input.Region, input.Size)
	if !ok {
		writeError(writer, http.StatusAccepted, "not enough players")
		return
	}
	if s.domain != nil {
		if err := s.domain.PersistAllocation(request.Context(), allocation); err != nil {
			writeError(writer, http.StatusServiceUnavailable, "match allocation persistence unavailable")
			return
		}
	}
	writeJSON(writer, http.StatusOK, allocation)
}

func (s *Server) claims(writer http.ResponseWriter, request *http.Request) (value security.Claims, ok bool) {
	header := strings.TrimSpace(request.Header.Get("Authorization"))
	if !strings.HasPrefix(header, "Bearer ") {
		writeError(writer, http.StatusUnauthorized, "unauthorized")
		return security.Claims{}, false
	}
	claims, err := s.auth.VerifyAccess(strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")), time.Now().UTC())
	if err != nil {
		writeError(writer, http.StatusUnauthorized, "unauthorized")
		return security.Claims{}, false
	}
	return claims, true
}

func (s *Server) authenticated(writer http.ResponseWriter, request *http.Request) bool {
	_, ok := s.claims(writer, request)
	return ok
}

func (s *Server) health(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) metricsHandler(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writer.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = writer.Write([]byte(s.metrics.Prometheus()))
}

func (s *Server) metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		instrumented := &statusWriter{ResponseWriter: writer, status: http.StatusOK}
		next.ServeHTTP(instrumented, request)
		duration := time.Since(started)
		s.metrics.ObserveRequestDuration(instrumented.status, duration)
		observability.AnnotateHTTPSpan(request.Context(), request.Method, request.URL.Path, instrumented.status, duration)
		if span, ok := observability.FromContext(request.Context()); ok {
			s.logger.Info("http_request_completed", "method", request.Method, "path", request.URL.Path, "status", instrumented.status, "duration_ms", float64(duration.Microseconds())/1000, "request_id", request.Header.Get("X-Request-ID"), "trace_id", span.TraceID, "span_id", span.SpanID, "parent_span_id", span.ParentSpanID)
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(value []byte) (int, error) {
	return w.ResponseWriter.Write(value)
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, value any) bool {
	request.Body = http.MaxBytesReader(writer, request.Body, 1<<20)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeError(writer http.ResponseWriter, status int, message string) {
	writeJSON(writer, status, map[string]string{"error": message})
}

func requestContextMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestID := request.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = strconv.FormatInt(time.Now().UnixNano(), 10)
		}
		otelContext := observability.Extract(request.Context(), request.Header)
		otelContext, otelSpan := observability.StartSpan(otelContext, request.Method+" "+request.URL.Path)
		defer otelSpan.End()
		ctx, span := observability.Start(otelContext, request.Header.Get("traceparent"), observability.SpanContextFor(otelSpan))
		request = request.WithContext(ctx)
		writer.Header().Set("X-Request-ID", requestID)
		writer.Header().Set("traceparent", span.Traceparent())
		writer.Header().Set("X-Trace-ID", span.TraceID)
		next.ServeHTTP(writer, request)
	})
}

func (s *Server) String() string { return fmt.Sprintf("api-server") }

type legacyLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type legacySessionRequest struct {
	Session string `json:"session"`
}

type legacyTicketRequest struct {
	Ticket string `json:"ticket"`
}

func (s *Server) legacyLogin(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var input legacyLoginRequest
	if !decodeJSON(writer, request, &input) {
		return
	}
	username := strings.TrimSpace(input.Username)
	if username == "" {
		writeJSON(writer, http.StatusOK, map[string]any{"success": false, "message": "username is required"})
		return
	}
	email := username
	if !strings.Contains(email, "@") {
		email = strings.ToLower(username) + "@roslegacy.local"
	}

	password := input.Password
	if len(password) < 12 {
		password = password + "roslegacy1234"[:12-len(password)]
	}

	session, err := s.auth.Login(request.Context(), email, password, "ios-legacy")
	if err != nil {
		// Attempt auto-registration for local development
		session, err = s.auth.Register(request.Context(), email, password, username, "ios-legacy")
		if err != nil {
			s.logger.Warn("legacy_login_failed", "username", username, "error", err)
			writeJSON(writer, http.StatusOK, map[string]any{"success": false, "message": "Invalid username or password"})
			return
		}
	}

	writeJSON(writer, http.StatusOK, map[string]any{
		"success":  true,
		"session":  session.AccessToken,
		"username": username,
	})
}

func (s *Server) legacyRelaunch(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var input legacySessionRequest
	if !decodeJSON(writer, request, &input) {
		return
	}
	claims, err := s.auth.VerifyAccess(input.Session, time.Now().UTC())
	if err != nil {
		writeJSON(writer, http.StatusOK, map[string]any{"success": false, "message": "session expired"})
		return
	}
	username := claims.Subject
	if s.profiles != nil {
		if p, err := s.profiles.Get(request.Context(), claims.Subject); err == nil && p.DisplayName != "" {
			username = p.DisplayName
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"success":  true,
		"session":  input.Session,
		"username": username,
	})
}

func (s *Server) legacyExchangeTicket(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var input legacyTicketRequest
	if !decodeJSON(writer, request, &input) {
		return
	}
	claims, err := s.auth.VerifyAccess(input.Ticket, time.Now().UTC())
	if err != nil {
		writeJSON(writer, http.StatusOK, map[string]any{"success": false, "message": "invalid ticket"})
		return
	}
	username := claims.Subject
	if s.profiles != nil {
		if p, err := s.profiles.Get(request.Context(), claims.Subject); err == nil && p.DisplayName != "" {
			username = p.DisplayName
		}
	}
	rosToken, err := s.auth.IssueToken(claims.Subject, 24*time.Hour)
	if err != nil {
		writeJSON(writer, http.StatusOK, map[string]any{"success": false, "message": "failed to issue launch token"})
		return
	}

	writeJSON(writer, http.StatusOK, map[string]any{
		"success":   true,
		"username":  username,
		"ros_token": rosToken,
	})
}

func (s *Server) legacyOnlineCount(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{
		"total_online": 1,
		"captured_at":  time.Now().UTC().Format("2006-01-02T15:04:05"),
	})
}
