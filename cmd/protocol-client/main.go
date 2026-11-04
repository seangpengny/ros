package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"roslegacy/internal/game"
)

type client struct {
	conn            *net.UDPConn
	server          *net.UDPAddr
	sessionID       string
	players         int
	latency         time.Duration
	packetLoss      float64
	seq             uint64
	rng             *rand.Rand
	partials        map[string]*snapshotAssembly
	sentBytes       uint64
	sentPackets     uint64
	receivedBytes   uint64
	receivedPackets uint64
	sendDrops       uint64
	receiveDrops    uint64
	rtt             time.Duration
}

type snapshotAssembly struct {
	base       game.ServerMessage
	chunkCount int
	chunks     map[int][]game.PlayerSnapshot
}

func main() {
	suite := envString("ROS_TEST_SUITE", "synchronization")
	if suite == "registration_login" || suite == "lobby_creation" || suite == "party_invitations" || suite == "session_lifecycle" || suite == "matchmaking" {
		if err := runAPISuite(suite); err != nil {
			fatal(err)
		}
		fmt.Printf("protocol-client: suite=%s passed\n", suite)
		return
	}

	players := envInt("ROS_TEST_PLAYERS", 2)
	if players < 2 || players > 100 {
		fmt.Fprintln(os.Stderr, "protocol-client: ROS_TEST_PLAYERS must be between 2 and 100")
		os.Exit(2)
	}
	serverAddr, err := net.ResolveUDPAddr("udp", envString("ROS_GAME_SERVER_ADDR", "127.0.0.1:9000"))
	if err != nil {
		fatal(err)
	}
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		fatal(err)
	}
	defer conn.Close()

	testClient := &client{
		conn:       conn,
		server:     serverAddr,
		sessionID:  envString("ROS_TEST_SESSION_ID", fmt.Sprintf("phase3-%s-%d-%d", suite, players, time.Now().UnixNano())),
		players:    players,
		latency:    time.Duration(envInt("ROS_TEST_LATENCY_MS", 0)) * time.Millisecond,
		packetLoss: float64(envInt("ROS_TEST_PACKET_LOSS_PCT", 0)) / 100,
		rng:        rand.New(rand.NewSource(1)),
		partials:   make(map[string]*snapshotAssembly),
	}
	started := time.Now()
	if err := testClient.run(context.Background(), suite); err != nil {
		fatal(err)
	}
	fmt.Printf("protocol-client: suite=%s players=%d passed elapsed_ms=%d sent_packets=%d sent_bytes=%d received_packets=%d received_bytes=%d send_drops=%d receive_drops=%d rtt_ms=%.3f\n", suite, players, time.Since(started).Milliseconds(), testClient.sentPackets, testClient.sentBytes, testClient.receivedPackets, testClient.receivedBytes, testClient.sendDrops, testClient.receiveDrops, float64(testClient.rtt.Microseconds())/1000)
}

func (c *client) run(ctx context.Context, suite string) error {
	for index := 0; index < c.players; index++ {
		if err := c.send(MessageForJoin(c.sessionID, playerID(index))); err != nil {
			return err
		}
	}
	welcomeCount := 0
	deadline := time.Now().Add(3 * time.Second)
	for welcomeCount < c.players && time.Now().Before(deadline) {
		response, err := c.receive(deadline)
		if err != nil {
			return fmt.Errorf("waiting for welcome: %w", err)
		}
		if response.Type == "welcome" {
			welcomeCount++
		}
	}
	if welcomeCount != c.players {
		return fmt.Errorf("received %d/%d welcome messages", welcomeCount, c.players)
	}
	if err := c.measureRTT(); err != nil {
		return err
	}

	if suite == "join_disconnect" {
		last := playerID(c.players - 1)
		if err := c.send(game.Message{Version: 1, Type: "leave", SessionID: c.sessionID, PlayerID: last}); err != nil {
			return err
		}
		if err := c.send(MessageForJoin(c.sessionID, last)); err != nil {
			return err
		}
	}

	if suite == "synchronization" || suite == "network_impairment" || suite == "load" {
		for index := 0; index < c.players; index++ {
			if err := c.send(game.Message{Version: 1, Type: "input", SessionID: c.sessionID, PlayerID: playerID(index), Seq: c.nextSequence(), DX: 1, DY: 0}); err != nil {
				return err
			}
		}
	}

	wait := 3 * time.Second
	if suite == "match_completion" || suite == "network_impairment" || suite == "load" {
		wait = time.Duration(envInt("ROS_TEST_WAIT_MS", 6000)) * time.Millisecond
	}
	if suite == "join_disconnect" {
		wait = time.Second
	}
	if suite == "network_impairment" {
		disconnectAfter := time.Duration(envInt("ROS_TEST_DISCONNECT_AFTER_MS", 5000)) * time.Millisecond
		if disconnectAfter > 0 {
			time.Sleep(disconnectAfter)
			if envInt("ROS_TEST_RECONNECT", 0) == 1 {
				_ = c.send(MessageForJoin(c.sessionID, playerID(0)))
			}
		}
	}

	seenSnapshot := false
	seenComplete := false
	responseTypes := make(map[string]int)
	deadline = time.Now().Add(wait)
	for time.Now().Before(deadline) {
		response, err := c.receive(deadline)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return err
			}
			var networkError net.Error
			if errors.As(err, &networkError) && networkError.Timeout() {
				break
			}
			if strings.Contains(err.Error(), "simulated packet loss") {
				continue
			}
			return fmt.Errorf("receiving authoritative packet: %w", err)
		}
		responseTypes[response.Type]++
		if response.Type == "snapshot" {
			seenSnapshot = true
			if len(response.Players) < c.players && suite != "network_impairment" {
				return fmt.Errorf("snapshot contains %d/%d players", len(response.Players), c.players)
			}
		}
		if response.Type == "match_complete" {
			seenComplete = true
			break
		}
	}

	if !seenSnapshot && suite != "match_completion" {
		return fmt.Errorf("no authoritative snapshot received; response types=%v", responseTypes)
	}
	if suite == "match_completion" && !seenComplete {
		return errors.New("match did not complete before test deadline")
	}
	return nil
}

func (c *client) send(message game.Message) error {
	if c.packetLoss > 0 && c.rng.Float64() < c.packetLoss {
		c.sendDrops++
		return nil
	}
	if c.latency > 0 {
		time.Sleep(c.latency)
	}
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	_, err = c.conn.WriteToUDP(data, c.server)
	if err == nil {
		c.sentPackets++
		c.sentBytes += uint64(len(data))
	}
	return err
}

func (c *client) measureRTT() error {
	for attempt := 0; attempt < 3; attempt++ {
		started := time.Now()
		if err := c.send(game.Message{Version: 1, Type: "ping", SessionID: c.sessionID, PlayerID: playerID(0)}); err != nil {
			return fmt.Errorf("send RTT probe: %w", err)
		}
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			response, err := c.receive(deadline)
			if err != nil {
				if strings.Contains(err.Error(), "simulated packet loss") {
					continue
				}
				var networkError net.Error
				if errors.As(err, &networkError) && networkError.Timeout() {
					break
				}
				return fmt.Errorf("receive RTT probe: %w", err)
			}
			if response.Type == "pong" {
				c.rtt = time.Since(started)
				return nil
			}
		}
	}
	return errors.New("RTT probe did not receive pong before deadline")
}

func (c *client) receive(deadline time.Time) (game.ServerMessage, error) {
	for {
		_ = c.conn.SetReadDeadline(deadline)
		buffer := make([]byte, 64*1024)
		n, _, err := c.conn.ReadFromUDP(buffer)
		if err != nil {
			return game.ServerMessage{}, err
		}
		c.receivedPackets++
		c.receivedBytes += uint64(n)
		var response game.ServerMessage
		if err := json.Unmarshal(buffer[:n], &response); err != nil {
			return game.ServerMessage{}, fmt.Errorf("decode server packet of %d bytes: %w", n, err)
		}
		if c.packetLoss > 0 && c.rng.Float64() < c.packetLoss {
			c.receiveDrops++
			return game.ServerMessage{}, errors.New("simulated packet loss")
		}
		if response.ChunkCount <= 1 || response.SnapshotID == "" {
			return response, nil
		}
		if response.ChunkIndex < 0 || response.ChunkIndex >= response.ChunkCount {
			return game.ServerMessage{}, fmt.Errorf("invalid snapshot chunk %d/%d", response.ChunkIndex, response.ChunkCount)
		}
		assembly := c.partials[response.SnapshotID]
		if assembly == nil || assembly.chunkCount != response.ChunkCount {
			assembly = &snapshotAssembly{base: response, chunkCount: response.ChunkCount, chunks: make(map[int][]game.PlayerSnapshot, response.ChunkCount)}
			assembly.base.Players = nil
			assembly.base.ChunkIndex = 0
			assembly.base.ChunkCount = 0
			assembly.base.SnapshotID = ""
			c.partials[response.SnapshotID] = assembly
		}
		assembly.chunks[response.ChunkIndex] = response.Players
		if len(assembly.chunks) != response.ChunkCount {
			continue
		}
		players := make([]game.PlayerSnapshot, 0)
		for index := 0; index < response.ChunkCount; index++ {
			chunk, ok := assembly.chunks[index]
			if !ok {
				players = nil
				break
			}
			players = append(players, chunk...)
		}
		delete(c.partials, response.SnapshotID)
		if players == nil {
			continue
		}
		assembly.base.Players = players
		return assembly.base, nil
	}
}

func (c *client) nextSequence() uint64 {
	c.seq++
	return c.seq
}

func MessageForJoin(sessionID, playerID string) game.Message {
	return game.Message{Version: 1, Type: "join", SessionID: sessionID, PlayerID: playerID}
}

func playerID(index int) string { return fmt.Sprintf("player-%03d", index+1) }

func envString(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(key))
	if err != nil {
		return fallback
	}
	return value
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "protocol-client:", err)
	os.Exit(1)
}

type apiClient struct {
	baseURL string
	client  *http.Client
}

type apiSession struct {
	SessionID    string `json:"session_id"`
	AccountID    string `json:"account_id"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

type apiLobby struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type apiProfile struct {
	AccountID   string `json:"account_id"`
	DisplayName string `json:"display_name"`
}

type apiStats struct {
	AccountID     string `json:"account_id"`
	MatchesPlayed int    `json:"matches_played"`
	Rating        int    `json:"rating"`
}

func runAPISuite(suite string) error {
	client := &apiClient{baseURL: strings.TrimRight(envString("ROS_API_BASE_URL", "http://127.0.0.1:8081"), "/"), client: &http.Client{Timeout: 5 * time.Second}}
	owner, ownerEmail, err := client.register("owner")
	if err != nil {
		return err
	}
	if suite == "registration_login" {
		var profile apiProfile
		status, body, err := client.do(http.MethodGet, "/v1/profile", owner.AccessToken, nil, &profile)
		if err != nil || status != http.StatusOK || profile.AccountID != owner.AccountID {
			return fmt.Errorf("profile returned status %d: %s", status, string(body))
		}
		var updatedProfile apiProfile
		status, body, err = client.do(http.MethodPatch, "/v1/profile", owner.AccessToken, map[string]any{
			"display_name": "updated-owner",
			"preferences":  map[string]any{"language": "en", "theme": "dark"},
		}, &updatedProfile)
		if err != nil || status != http.StatusOK || updatedProfile.DisplayName != "updated-owner" {
			return fmt.Errorf("profile update returned status %d: %s", status, string(body))
		}
		var stats apiStats
		status, body, err = client.do(http.MethodGet, "/v1/stats", owner.AccessToken, nil, &stats)
		if err != nil || status != http.StatusOK || stats.AccountID != owner.AccountID || stats.Rating <= 0 {
			return fmt.Errorf("stats returned status %d: %s", status, string(body))
		}
		status, body, err = client.do(http.MethodPost, "/v1/auth/recovery/request", owner.AccessToken, map[string]string{"email": ownerEmail}, &map[string]any{})
		if err != nil || status != http.StatusAccepted {
			return fmt.Errorf("recovery request returned status %d: %s", status, string(body))
		}
		status, body, err = client.do(http.MethodPost, "/v1/auth/recovery/request", owner.AccessToken, map[string]string{"email": "unknown@example.com"}, &map[string]any{})
		if err != nil || status != http.StatusAccepted {
			return fmt.Errorf("unknown recovery request returned status %d: %s", status, string(body))
		}
		status, body, err = client.do(http.MethodPost, "/v1/auth/recovery/complete", owner.AccessToken, map[string]string{"token": "invalid-token", "password": "correct-horse-battery-staple"}, &map[string]any{})
		if err != nil || status != http.StatusBadRequest {
			return fmt.Errorf("invalid recovery completion returned status %d: %s", status, string(body))
		}
		var history map[string]any
		status, body, err = client.do(http.MethodGet, "/v1/stats/history?limit=20", owner.AccessToken, nil, &history)
		if err != nil || status != http.StatusOK {
			return fmt.Errorf("history returned status %d: %s", status, string(body))
		}
		var leaderboard map[string]any
		status, body, err = client.do(http.MethodGet, "/v1/leaderboards?season=season-1&mode=duo&region=ap-southeast&limit=20", owner.AccessToken, nil, &leaderboard)
		if err != nil || status != http.StatusOK {
			return fmt.Errorf("leaderboard returned status %d: %s", status, string(body))
		}
		status, body, err = client.do(http.MethodGet, "/metrics", "", nil, nil)
		if err != nil || status != http.StatusOK || !strings.Contains(string(body), "ros_http_requests_total") {
			return fmt.Errorf("metrics returned status %d: %s", status, string(body))
		}
		login, err := client.login(ownerEmail)
		if err != nil {
			return err
		}
		if login.AccessToken == "" || login.RefreshToken == "" {
			return errors.New("login returned incomplete session")
		}
		var sessions struct {
			Sessions []struct {
				ID string `json:"id"`
			} `json:"sessions"`
		}
		status, body, err = client.do(http.MethodGet, "/v1/auth/sessions", login.AccessToken, nil, &sessions)
		if err != nil || status != http.StatusOK || login.SessionID == "" {
			return fmt.Errorf("device session listing returned status %d: %s", status, string(body))
		}
		found := false
		for _, session := range sessions.Sessions {
			if session.ID == login.SessionID {
				found = true
				break
			}
		}
		if !found {
			return errors.New("login device session was not listed")
		}
		oldRefreshToken := login.RefreshToken
		refreshed, err := client.refresh(oldRefreshToken)
		if err != nil || refreshed.AccessToken == "" || refreshed.RefreshToken == "" {
			if err != nil {
				return fmt.Errorf("refresh failed: %w", err)
			}
			return errors.New("refresh returned incomplete session")
		}
		status, body, err = client.do(http.MethodPost, "/v1/auth/refresh", "", map[string]string{"refresh_token": oldRefreshToken, "device_id": "replay-device"}, nil)
		if err != nil || status != http.StatusUnauthorized {
			return fmt.Errorf("rotated refresh token replay returned status %d: %s", status, string(body))
		}
		secondary, err := client.login(ownerEmail)
		if err != nil {
			return fmt.Errorf("secondary device login failed: %w", err)
		}
		if secondary.SessionID == "" {
			return errors.New("secondary device login returned incomplete session")
		}
		status, body, err = client.do(http.MethodDelete, "/v1/auth/sessions/"+secondary.SessionID, refreshed.AccessToken, nil, &map[string]any{})
		if err != nil || status != http.StatusOK {
			return fmt.Errorf("device session revocation returned status %d: %s", status, string(body))
		}
		if err := client.logout(refreshed.RefreshToken); err != nil {
			return err
		}
		status, body, err = client.do(http.MethodPost, "/v1/auth/refresh", "", map[string]string{"refresh_token": refreshed.RefreshToken, "device_id": "revoked-device"}, nil)
		if err != nil || status != http.StatusUnauthorized {
			return fmt.Errorf("revoked refresh token returned status %d: %s", status, string(body))
		}
		return nil
	}
	member, _, err := client.register("member")
	if err != nil {
		return err
	}
	if suite == "lobby_creation" {
		return client.runLobby(owner, member)
	}
	if suite == "party_invitations" {
		return client.runPartyInvitations(owner, member)
	}
	if suite == "session_lifecycle" {
		return client.runSessionLifecycle(owner, member)
	}
	if suite == "matchmaking" {
		return client.runMatchmaking(owner, member)
	}
	return fmt.Errorf("unsupported API suite %s", suite)
}

func (a *apiClient) register(label string) (apiSession, string, error) {
	email := fmt.Sprintf("phase7-%s-%d@example.com", label, time.Now().UnixNano())
	var session apiSession
	status, body, err := a.do(http.MethodPost, "/v1/auth/register", "", map[string]string{
		"email": email, "password": "correct-horse-battery-staple", "device_id": label, "display_name": label,
	}, &session)
	if err != nil {
		return apiSession{}, "", err
	}
	if status != http.StatusCreated || session.AccessToken == "" {
		return apiSession{}, "", fmt.Errorf("register returned status %d: %s", status, string(body))
	}
	return session, email, nil
}

func (a *apiClient) login(email string) (apiSession, error) {
	var session apiSession
	status, body, err := a.do(http.MethodPost, "/v1/auth/login", "", map[string]string{
		"email": email, "password": "correct-horse-battery-staple", "device_id": "login-device",
	}, &session)
	if err != nil {
		return apiSession{}, err
	}
	if status != http.StatusOK {
		return apiSession{}, fmt.Errorf("login returned status %d: %s", status, string(body))
	}
	return session, nil
}

func (a *apiClient) refresh(refreshToken string) (apiSession, error) {
	var session apiSession
	status, body, err := a.do(http.MethodPost, "/v1/auth/refresh", "", map[string]string{"refresh_token": refreshToken, "device_id": "refresh-device"}, &session)
	if err != nil {
		return apiSession{}, err
	}
	if status != http.StatusOK {
		return apiSession{}, fmt.Errorf("refresh returned status %d: %s", status, string(body))
	}
	return session, nil
}

func (a *apiClient) logout(refreshToken string) error {
	status, body, err := a.do(http.MethodPost, "/v1/auth/logout", "", map[string]string{"refresh_token": refreshToken}, &map[string]any{})
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("logout returned status %d: %s", status, string(body))
	}
	return nil
}

func (a *apiClient) runLobby(owner, member apiSession) error {
	var lobby apiLobby
	status, body, err := a.do(http.MethodPost, "/v1/lobbies", owner.AccessToken, map[string]string{"mode": "duo", "region": "ap-southeast"}, &lobby)
	if err != nil {
		return err
	}
	if status != http.StatusCreated || lobby.ID == "" {
		return fmt.Errorf("lobby creation returned status %d: %s", status, string(body))
	}
	status, body, err = a.do(http.MethodPost, "/v1/lobbies/"+lobby.ID+"/join", member.AccessToken, nil, &lobby)
	if err != nil || status != http.StatusOK {
		return fmt.Errorf("lobby join returned status %d: %s", status, string(body))
	}
	for _, session := range []apiSession{owner, member} {
		status, body, err = a.do(http.MethodPost, "/v1/lobbies/"+lobby.ID+"/ready", session.AccessToken, map[string]bool{"ready": true}, &lobby)
		if err != nil || status != http.StatusOK {
			return fmt.Errorf("lobby ready returned status %d: %s", status, string(body))
		}
	}
	if lobby.Status != "ready" {
		return fmt.Errorf("lobby status = %s, want ready", lobby.Status)
	}
	status, body, err = a.do(http.MethodPost, "/v1/lobbies/"+lobby.ID+"/allocate", member.AccessToken, nil, &map[string]any{})
	if err != nil || status != http.StatusForbidden {
		return fmt.Errorf("unauthorized lobby allocation returned status %d body=%s", status, string(body))
	}
	status, body, err = a.do(http.MethodPost, "/v1/lobbies/"+lobby.ID+"/allocate", owner.AccessToken, nil, &lobby)
	if err != nil || status != http.StatusOK || lobby.Status != "allocated" {
		return fmt.Errorf("lobby allocation returned status %d body=%s state=%+v", status, string(body), lobby)
	}
	return nil
}

func (a *apiClient) runPartyInvitations(owner, member apiSession) error {
	status, body, err := a.do(http.MethodPost, "/v1/presence", owner.AccessToken, map[string]bool{"online": true}, &map[string]any{})
	if err != nil || status != http.StatusOK {
		return fmt.Errorf("presence update returned status %d: %s", status, string(body))
	}
	var presence struct {
		AccountID string `json:"account_id"`
		Online    bool   `json:"online"`
	}
	status, body, err = a.do(http.MethodGet, "/v1/presence/"+owner.AccountID, member.AccessToken, nil, &presence)
	if err != nil || status != http.StatusOK || presence.AccountID != owner.AccountID || !presence.Online {
		return fmt.Errorf("presence read returned status %d: %s", status, string(body))
	}
	var value apiLobby
	status, body, err = a.do(http.MethodPost, "/v1/lobbies", owner.AccessToken, map[string]string{"mode": "duo", "region": "ap-southeast"}, &value)
	if err != nil || status != http.StatusCreated || value.ID == "" {
		return fmt.Errorf("party lobby creation returned status %d: %s", status, string(body))
	}
	var invitation struct {
		ID        string `json:"id"`
		InviteeID string `json:"invitee_id"`
		Status    string `json:"status"`
	}
	status, body, err = a.do(http.MethodPost, "/v1/lobbies/"+value.ID+"/invites", owner.AccessToken, map[string]string{"account_id": member.AccountID}, &invitation)
	if err != nil || status != http.StatusCreated || invitation.ID == "" || invitation.InviteeID != member.AccountID || invitation.Status != "pending" {
		return fmt.Errorf("party invitation returned status %d: %s", status, string(body))
	}
	var inbox struct {
		Invitations []struct {
			ID string `json:"id"`
		} `json:"invitations"`
	}
	status, body, err = a.do(http.MethodGet, "/v1/lobby-invites", member.AccessToken, nil, &inbox)
	if err != nil || status != http.StatusOK || len(inbox.Invitations) != 1 || inbox.Invitations[0].ID != invitation.ID {
		return fmt.Errorf("party invitation inbox returned status %d: %s", status, string(body))
	}
	status, body, err = a.do(http.MethodPost, "/v1/lobby-invites/"+invitation.ID+"/accept", member.AccessToken, &map[string]any{}, &value)
	if err != nil || status != http.StatusOK {
		return fmt.Errorf("party invitation accept returned status %d: %s", status, string(body))
	}
	var lobbyState struct {
		Members []struct {
			AccountID string `json:"account_id"`
		} `json:"members"`
	}
	status, body, err = a.do(http.MethodGet, "/v1/lobbies/"+value.ID, member.AccessToken, nil, &lobbyState)
	if err != nil || status != http.StatusOK || len(lobbyState.Members) != 2 {
		return fmt.Errorf("party membership after accept returned status %d: %s", status, string(body))
	}
	return nil
}

func (a *apiClient) runSessionLifecycle(owner, member apiSession) error {
	var lobbyValue struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		Members []struct {
			AccountID string `json:"account_id"`
		} `json:"members"`
	}
	status, body, err := a.do(http.MethodPost, "/v1/lobbies", owner.AccessToken, map[string]string{"mode": "duo", "region": "ap-southeast"}, &lobbyValue)
	if err != nil || status != http.StatusCreated {
		return fmt.Errorf("session lobby creation returned status %d: %s", status, string(body))
	}
	status, body, err = a.do(http.MethodPost, "/v1/lobbies/"+lobbyValue.ID+"/join", member.AccessToken, nil, &lobbyValue)
	if err != nil || status != http.StatusOK {
		return fmt.Errorf("session lobby join returned status %d: %s", status, string(body))
	}
	for _, value := range []apiSession{owner, member} {
		status, body, err = a.do(http.MethodPost, "/v1/lobbies/"+lobbyValue.ID+"/ready", value.AccessToken, map[string]bool{"ready": true}, &lobbyValue)
		if err != nil || status != http.StatusOK {
			return fmt.Errorf("session lobby ready returned status %d: %s", status, string(body))
		}
	}
	var gameSession struct {
		ID             string `json:"id"`
		Status         string `json:"status"`
		AssignedServer string `json:"assigned_server"`
		Healthy        bool   `json:"healthy"`
	}
	status, body, err = a.do(http.MethodPost, "/v1/sessions", owner.AccessToken, map[string]any{"lobby_id": lobbyValue.ID, "max_players": 2}, &gameSession)
	if err != nil || status != http.StatusCreated || gameSession.ID == "" || gameSession.Status != "allocating" || gameSession.AssignedServer == "" {
		return fmt.Errorf("session allocation returned status %d: %s", status, string(body))
	}
	for _, value := range []struct {
		session apiSession
		team    string
	}{{owner, "team-1"}, {member, "team-1"}} {
		status, body, err = a.do(http.MethodPost, "/v1/sessions/"+gameSession.ID+"/admit", value.session.AccessToken, map[string]string{"team_id": value.team}, &gameSession)
		if err != nil || status != http.StatusOK {
			return fmt.Errorf("session admission returned status %d: %s", status, string(body))
		}
	}
	var admitted struct {
		Participants []struct {
			AccountID string `json:"account_id"`
		} `json:"participants"`
	}
	status, body, err = a.do(http.MethodGet, "/v1/sessions/"+gameSession.ID, member.AccessToken, nil, &admitted)
	if err != nil || status != http.StatusOK || len(admitted.Participants) != 2 {
		return fmt.Errorf("session read returned status %d: %s", status, string(body))
	}
	internalToken := envString("ROS_GAME_SERVER_TOKEN", "")
	if internalToken == "" {
		return errors.New("ROS_GAME_SERVER_TOKEN is required for session lifecycle test")
	}
	serverID := envString("ROS_GAME_SERVER_ID", "game-server-local")
	status, body, err = a.doInternal(http.MethodPost, "/v1/sessions/"+gameSession.ID+"/start", "wrong-session-token", serverID, map[string]any{}, &gameSession)
	if err != nil || status != http.StatusForbidden {
		return fmt.Errorf("unauthorized session start returned status %d: %s", status, string(body))
	}
	status, body, err = a.doInternal(http.MethodPost, "/v1/sessions/"+gameSession.ID+"/start", internalToken, serverID, map[string]any{}, &gameSession)
	if err != nil || status != http.StatusOK || gameSession.Status != "running" {
		return fmt.Errorf("session start returned status %d: %s", status, string(body))
	}
	status, body, err = a.doInternal(http.MethodPost, "/v1/sessions/"+gameSession.ID+"/heartbeat", internalToken, serverID, map[string]any{}, &gameSession)
	if err != nil || status != http.StatusOK || !gameSession.Healthy {
		return fmt.Errorf("session heartbeat returned status %d: %s", status, string(body))
	}
	status, body, err = a.doInternal(http.MethodPost, "/v1/sessions/"+gameSession.ID+"/complete", internalToken, serverID, map[string]any{}, &gameSession)
	if err != nil || status != http.StatusOK || gameSession.Status != "complete" {
		return fmt.Errorf("session completion returned status %d: %s", status, string(body))
	}
	return nil
}

func (a *apiClient) runMatchmaking(owner, member apiSession) error {
	var ownerTicket, memberTicket struct {
		ID string `json:"id"`
	}
	for index, session := range []apiSession{owner, member} {
		var ticket *struct {
			ID string `json:"id"`
		}
		if index == 0 {
			ticket = &ownerTicket
		} else {
			ticket = &memberTicket
		}
		status, body, err := a.do(http.MethodPost, "/v1/matchmaking/queue", session.AccessToken, map[string]string{"mode": "duo", "region": "ap-southeast"}, ticket)
		if err != nil || status != http.StatusCreated {
			return fmt.Errorf("queue returned status %d: %s", status, string(body))
		}
	}
	status, body, err := a.do(http.MethodDelete, "/v1/matchmaking/queue/"+memberTicket.ID, owner.AccessToken, nil, &map[string]any{})
	if err != nil || status != http.StatusNotFound {
		return fmt.Errorf("unauthorized queue cancellation returned status %d: %s", status, string(body))
	}
	var allocation map[string]any
	status, body, err = a.do(http.MethodPost, "/v1/matchmaking/match", owner.AccessToken, map[string]any{"mode": "duo", "region": "ap-southeast", "size": 2}, &allocation)
	if err != nil || status != http.StatusOK {
		return fmt.Errorf("matchmaking returned status %d: %s", status, string(body))
	}
	if allocation["id"] == nil {
		return errors.New("matchmaking allocation has no id")
	}
	return nil
}

func (a *apiClient) do(method, path, token string, input, output any) (int, []byte, error) {
	return a.doWithHeaders(method, path, token, input, output, nil)
}

func (a *apiClient) doInternal(method, path, internalToken, serverID string, input, output any) (int, []byte, error) {
	return a.doWithHeaders(method, path, "", input, output, map[string]string{
		"X-Game-Server-Token": internalToken,
		"X-Game-Server-ID":    serverID,
	})
}

func (a *apiClient) doWithHeaders(method, path, token string, input, output any, headers map[string]string) (int, []byte, error) {
	var body bytes.Buffer
	if input != nil {
		if err := json.NewEncoder(&body).Encode(input); err != nil {
			return 0, nil, err
		}
	}
	request, err := http.NewRequest(method, a.baseURL+path, &body)
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := a.client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return response.StatusCode, nil, err
	}
	if output != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, output); err != nil {
			return response.StatusCode, raw, err
		}
	}
	return response.StatusCode, raw, nil
}
