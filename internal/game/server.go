package game

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	maxDatagramBytes = 64 * 1024
	maxSnapshotBytes = 8 * 1024
	defaultHealth    = 100
)

// Config contains the independent game-session protocol settings. It is not a
// ROS Legacy protocol configuration.
type Config struct {
	ListenAddr       string
	TickRate         int
	MaxPlayers       int
	MatchDuration    time.Duration
	ReconnectWindow  time.Duration
	ArenaRadius      float64
	MoveSpeed        float64
	AttackRange      float64
	AttackDamage     int
	AttackCooldown   time.Duration
	MagazineSize     int
	ReloadDuration   time.Duration
	GrenadeDamage    int
	GrenadeRadius    float64
	GrenadeCooldown  time.Duration
	StagingDuration  time.Duration
	AircraftDuration time.Duration
	ParachuteSpeed   float64
	ZoneFinalRadius  float64
	ZoneDamage       float64
	KnockdownWindow  time.Duration
	LootCount        int
	SupplyDropCount  int
	VehicleCount     int
}

func DefaultConfig() Config {
	return Config{
		ListenAddr:       ":9000",
		TickRate:         20,
		MaxPlayers:       100,
		MatchDuration:    30 * time.Second,
		ReconnectWindow:  20 * time.Second,
		ArenaRadius:      100,
		MoveSpeed:        6,
		AttackRange:      8,
		AttackDamage:     25,
		AttackCooldown:   250 * time.Millisecond,
		MagazineSize:     30,
		ReloadDuration:   1 * time.Second,
		GrenadeDamage:    60,
		GrenadeRadius:    6,
		GrenadeCooldown:  1 * time.Second,
		StagingDuration:  2 * time.Second,
		AircraftDuration: 2 * time.Second,
		ParachuteSpeed:   4,
		ZoneFinalRadius:  25,
		ZoneDamage:       10,
		KnockdownWindow:  5 * time.Second,
		LootCount:        16,
		SupplyDropCount:  3,
		VehicleCount:     4,
	}
}

func (c Config) validate() error {
	if c.ListenAddr == "" || c.TickRate <= 0 || c.MaxPlayers <= 0 || c.MatchDuration <= 0 || c.ReconnectWindow <= 0 {
		return errors.New("listen address, tick rate, max players, match duration, and reconnect window must be positive")
	}
	if c.ArenaRadius <= 0 || c.MoveSpeed <= 0 || c.AttackRange <= 0 || c.AttackDamage <= 0 || c.AttackCooldown <= 0 || c.MagazineSize <= 0 || c.ReloadDuration <= 0 || c.GrenadeDamage <= 0 || c.GrenadeRadius <= 0 || c.GrenadeCooldown <= 0 || c.StagingDuration < 0 || c.AircraftDuration <= 0 || c.ParachuteSpeed <= 0 || c.ZoneFinalRadius <= 0 || c.ZoneDamage <= 0 || c.KnockdownWindow <= 0 || c.LootCount < 0 || c.SupplyDropCount < 0 || c.VehicleCount < 0 {
		return errors.New("arena, movement, combat, zone, knockdown, and loot settings must be valid")
	}
	if c.ZoneFinalRadius > c.ArenaRadius {
		return errors.New("final zone radius cannot exceed arena radius")
	}
	return nil
}

// Message is the versioned, independent UDP test protocol. Production clients
// should replace JSON with an explicitly versioned binary schema after the
// contract is finalized; this service never claims ROS wire compatibility.
type Message struct {
	Version      int     `json:"version"`
	Type         string  `json:"type"`
	SessionID    string  `json:"session_id,omitempty"`
	PlayerID     string  `json:"player_id,omitempty"`
	TargetID     string  `json:"target_id,omitempty"`
	ItemID       string  `json:"item_id,omitempty"`
	WeaponID     string  `json:"weapon_id,omitempty"`
	TeamID       string  `json:"team_id,omitempty"`
	AttachmentID string  `json:"attachment_id,omitempty"`
	Seq          uint64  `json:"seq,omitempty"`
	DX           float64 `json:"dx,omitempty"`
	DY           float64 `json:"dy,omitempty"`
}

type PlayerSnapshot struct {
	ID           string         `json:"id"`
	X            float64        `json:"x,omitempty"`
	Y            float64        `json:"y,omitempty"`
	Health       int            `json:"health"`
	Alive        bool           `json:"alive"`
	Connected    bool           `json:"connected"`
	Knocked      bool           `json:"knocked,omitempty"`
	Spectator    bool           `json:"spectator,omitempty"`
	Armor        int            `json:"armor,omitempty"`
	TeamID       string         `json:"team_id,omitempty"`
	WeaponID     string         `json:"weapon_id,omitempty"`
	Attachments  []string       `json:"attachments,omitempty"`
	Ammo         int            `json:"ammo,omitempty"`
	Reloading    bool           `json:"reloading,omitempty"`
	Eliminations int            `json:"eliminations,omitempty"`
	Grenades     int            `json:"grenades,omitempty"`
	FlightState  string         `json:"flight_state,omitempty"`
	VehicleID    string         `json:"vehicle_id,omitempty"`
	Altitude     float64        `json:"altitude,omitempty"`
	Inventory    map[string]int `json:"inventory,omitempty"`
}

type LootSnapshot struct {
	ID   string  `json:"id"`
	Kind string  `json:"kind"`
	X    float64 `json:"x,omitempty"`
	Y    float64 `json:"y,omitempty"`
}

type SupplyDropSnapshot struct {
	ID     string  `json:"id"`
	X      float64 `json:"x,omitempty"`
	Y      float64 `json:"y,omitempty"`
	Opened bool    `json:"opened,omitempty"`
}

type VehicleSnapshot struct {
	ID       string  `json:"id"`
	X        float64 `json:"x,omitempty"`
	Y        float64 `json:"y,omitempty"`
	Occupied bool    `json:"occupied,omitempty"`
}

type ServerMessage struct {
	Version     int                  `json:"version"`
	Type        string               `json:"type"`
	SessionID   string               `json:"session_id,omitempty"`
	PlayerID    string               `json:"player_id,omitempty"`
	TargetID    string               `json:"target_id,omitempty"`
	SnapshotID  string               `json:"snapshot_id,omitempty"`
	ChunkIndex  int                  `json:"chunk_index,omitempty"`
	ChunkCount  int                  `json:"chunk_count,omitempty"`
	Tick        uint64               `json:"tick,omitempty"`
	Status      string               `json:"status,omitempty"`
	Reason      string               `json:"reason,omitempty"`
	WinnerID    string               `json:"winner_id,omitempty"`
	Error       string               `json:"error,omitempty"`
	ZoneRadius  float64              `json:"zone_radius,omitempty"`
	Phase       string               `json:"phase,omitempty"`
	AircraftX   float64              `json:"aircraft_x,omitempty"`
	AircraftY   float64              `json:"aircraft_y,omitempty"`
	Players     []PlayerSnapshot     `json:"players,omitempty"`
	Loot        []LootSnapshot       `json:"loot,omitempty"`
	SupplyDrops []SupplyDropSnapshot `json:"supply_drops,omitempty"`
	Vehicles    []VehicleSnapshot    `json:"vehicles,omitempty"`
}

type inbound struct {
	message Message
	address *net.UDPAddr
}

type player struct {
	id             string
	address        *net.UDPAddr
	x              float64
	y              float64
	health         int
	alive          bool
	connected      bool
	lastSequence   uint64
	pendingDX      float64
	pendingDY      float64
	disconnectedAt time.Time
	knockedDown    bool
	knockedAt      time.Time
	lastAttackAt   time.Time
	lastGrenadeAt  time.Time
	reloadUntil    time.Time
	zoneDamage     float64
	armor          int
	teamID         string
	weaponID       string
	ammo           int
	grenades       int
	spectator      bool
	eliminations   int
	flightState    string
	altitude       float64
	vehicleID      string
	attachments    map[string]bool
	inventory      map[string]int
}

type lootItem struct {
	id   string
	kind string
	x    float64
	y    float64
}

type match struct {
	id             string
	players        map[string]*player
	startedAt      time.Time
	tick           uint64
	snapshotSeq    uint64
	status         string
	completed      bool
	completionSent bool
	zoneRadius     float64
	loot           map[string]*lootItem
	supplyDrops    map[string]*supplyDrop
	vehicles       map[string]*vehicle
	phase          string
	aircraftX      float64
	aircraftY      float64
}

type supplyDrop struct {
	id     string
	x      float64
	y      float64
	opened bool
}

type vehicle struct {
	id         string
	x          float64
	y          float64
	occupantID string
}

// Server is an authoritative UDP game-session server for the independent
// platform. All state transitions occur on the Run event loop.
type Server struct {
	conn                 *net.UDPConn
	config               Config
	logger               *slog.Logger
	inbound              chan inbound
	matches              map[string]*match
	ready                atomic.Bool
	ticks                atomic.Uint64
	tickDurationNS       atomic.Uint64
	maxTickDurationNS    atomic.Uint64
	tickOverruns         atomic.Uint64
	broadcasts           atomic.Uint64
	snapshotBytes        atomic.Uint64
	udpReceiveBytes      atomic.Uint64
	udpSendBytes         atomic.Uint64
	activeSessions       atomic.Int64
	activePlayers        atomic.Int64
	validationViolations atomic.Uint64
	sink                 CompletionSink
	completion           chan Completion
	weapons              map[string]WeaponDefinition
}

type WeaponDefinition struct {
	ID           string
	Damage       int
	Range        float64
	Cooldown     time.Duration
	MagazineSize int
}

func NewServer(config Config, logger *slog.Logger) (*Server, error) {
	return newServer(config, logger, nil)
}

func NewServerWithSink(config Config, logger *slog.Logger, sink CompletionSink) (*Server, error) {
	return newServer(config, logger, sink)
}

func newServer(config Config, logger *slog.Logger, sink CompletionSink) (*Server, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	address, err := net.ResolveUDPAddr("udp", config.ListenAddr)
	if err != nil {
		return nil, fmt.Errorf("resolve UDP address: %w", err)
	}
	conn, err := net.ListenUDP("udp", address)
	if err != nil {
		return nil, fmt.Errorf("listen for UDP: %w", err)
	}
	return &Server{
		conn:       conn,
		config:     config,
		logger:     logger,
		inbound:    make(chan inbound, 1024),
		matches:    make(map[string]*match),
		sink:       sink,
		completion: make(chan Completion, 64),
		weapons:    defaultWeapons(config),
	}, nil
}

func (s *Server) Addr() net.Addr {
	return s.conn.LocalAddr()
}

func (s *Server) Ready() bool {
	return s.ready.Load()
}

func (s *Server) Close() error {
	s.ready.Store(false)
	return s.conn.Close()
}

func (s *Server) Run(ctx context.Context) error {
	go s.readLoop(ctx)
	if s.sink != nil {
		go s.completionLoop(ctx)
	}
	s.ready.Store(true)
	interval := time.Second / time.Duration(s.config.TickRate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	defer s.ready.Store(false)

	for {
		select {
		case <-ctx.Done():
			_ = s.conn.Close()
			return nil
		case packet := <-s.inbound:
			s.handle(packet, time.Now())
		case now := <-ticker.C:
			started := time.Now()
			s.ticks.Add(1)
			s.tick(now)
			s.observeTick(started, interval)
			s.observeActiveState()
		}
	}
}

func (s *Server) observeTick(started time.Time, interval time.Duration) {
	duration := time.Since(started)
	nanoseconds := uint64(duration)
	s.tickDurationNS.Add(nanoseconds)
	for {
		current := s.maxTickDurationNS.Load()
		if nanoseconds <= current || s.maxTickDurationNS.CompareAndSwap(current, nanoseconds) {
			break
		}
	}
	if duration > interval {
		s.tickOverruns.Add(1)
	}
}

func (s *Server) observeActiveState() {
	activeSessions := int64(0)
	activePlayers := int64(0)
	for _, session := range s.matches {
		if session.completed {
			continue
		}
		activeSessions++
		activePlayers += int64(len(session.players))
	}
	s.activeSessions.Store(activeSessions)
	s.activePlayers.Store(activePlayers)
}

func (s *Server) completionLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case completion := <-s.completion:
			if err := s.sink.RecordGameCompletion(ctx, completion); err != nil {
				s.logger.Error("game_completion_persist_failed", "session_id", completion.SessionID, "error", err)
			}
		}
	}
}

func (s *Server) readLoop(ctx context.Context) {
	for {
		buffer := make([]byte, maxDatagramBytes)
		n, address, err := s.conn.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		s.udpReceiveBytes.Add(uint64(n))

		// Detect and respond to legacy RoS BaseApp 24-byte UDP handshake packet
		if n == 24 && buffer[0] == 0x01 && buffer[3] == 0x0b && buffer[19] == 0x14 && buffer[22] == 0x02 {
			sessionKey := binary.LittleEndian.Uint32(buffer[11:15])
			if s.logger != nil {
				s.logger.Info("legacy_baseapp_handshake_received",
					"session_key", fmt.Sprintf("%#08x", sessionKey),
					"addr", address.String())
			}
			ack := make([]byte, 24)
			copy(ack, buffer[:24])
			ack[0] = 0x02
			_, _ = s.conn.WriteToUDP(ack, address)
			continue
		}

		var message Message
		if n == 0 || n > maxDatagramBytes || json.Unmarshal(buffer[:n], &message) != nil {
			continue
		}
		select {
		case s.inbound <- inbound{message: message, address: address}:
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) handle(packet inbound, now time.Time) {
	message := packet.message
	if message.Version != 1 {
		s.validationViolations.Add(1)
		s.sendError(packet.address, message.SessionID, message.PlayerID, "unsupported protocol version")
		return
	}
	if !validID(message.SessionID) || !validID(message.PlayerID) {
		s.validationViolations.Add(1)
		s.sendError(packet.address, message.SessionID, message.PlayerID, "session_id and player_id are required and must be bounded")
		return
	}

	session := s.matches[message.SessionID]
	switch message.Type {
	case "join":
		if session == nil {
			session = &match{
				id:          message.SessionID,
				players:     make(map[string]*player),
				startedAt:   now,
				status:      "staging",
				phase:       "staging",
				zoneRadius:  s.config.ArenaRadius,
				loot:        makeLoot(s.config.LootCount, s.config.ArenaRadius),
				supplyDrops: makeSupplyDrops(s.config.SupplyDropCount, s.config.ArenaRadius),
				vehicles:    makeVehicles(s.config.VehicleCount, s.config.ArenaRadius),
			}
			s.matches[message.SessionID] = session
		}
		if session.completed {
			s.sendError(packet.address, message.SessionID, message.PlayerID, "match is complete")
			return
		}
		current, exists := session.players[message.PlayerID]
		if !exists {
			if len(session.players) >= s.config.MaxPlayers {
				s.sendError(packet.address, message.SessionID, message.PlayerID, "match is full")
				return
			}
			index := len(session.players)
			weaponID := weaponIDForJoin(message.WeaponID)
			weapon := s.weaponForID(weaponID)
			current = &player{
				id:          message.PlayerID,
				address:     packet.address,
				x:           float64(index%10) * 2,
				y:           float64(index/10) * 2,
				health:      defaultHealth,
				alive:       true,
				connected:   true,
				teamID:      teamIDForJoin(message.TeamID, index),
				weaponID:    weapon.ID,
				ammo:        weapon.MagazineSize,
				grenades:    2,
				flightState: "staged",
				attachments: make(map[string]bool),
				inventory:   make(map[string]int),
			}
			session.players[message.PlayerID] = current
		} else {
			current.address = packet.address
			current.connected = true
			current.disconnectedAt = time.Time{}
			if !current.alive {
				current.spectator = true
			}
		}
		status := session.status
		if current.spectator {
			status = "spectating"
		}
		s.send(packet.address, ServerMessage{Version: 1, Type: "welcome", SessionID: message.SessionID, PlayerID: message.PlayerID, Status: status, Phase: session.phase, AircraftX: session.aircraftX, AircraftY: session.aircraftY})
	case "input":
		current := s.playerFor(session, message.PlayerID, packet.address)
		if math.Abs(message.DX) > 1 || math.Abs(message.DY) > 1 {
			s.validationViolations.Add(1)
		}
		if current == nil || !current.alive || current.knockedDown || message.Seq <= current.lastSequence {
			if current != nil && message.Seq <= current.lastSequence {
				s.validationViolations.Add(1)
			}
			return
		}
		current.lastSequence = message.Seq
		current.pendingDX = clamp(message.DX, -1, 1)
		current.pendingDY = clamp(message.DY, -1, 1)
	case "deploy":
		current := s.playerFor(session, message.PlayerID, packet.address)
		if current == nil || current.flightState != "staged" || session.phase != "aircraft" {
			return
		}
		current.flightState = "parachuting"
		current.altitude = 1000
		s.broadcastSnapshot(session)
	case "attack":
		current := s.playerFor(session, message.PlayerID, packet.address)
		if current != nil {
			s.finishReload(current, now)
		}
		if current == nil {
			return
		}
		weapon := s.weaponFor(current)
		if !current.alive || current.spectator || current.knockedDown || message.Seq <= current.lastSequence || !validID(message.TargetID) || (!current.lastAttackAt.IsZero() && now.Sub(current.lastAttackAt) < weapon.Cooldown) || !current.reloadUntil.IsZero() && now.Before(current.reloadUntil) || current.ammo <= 0 {
			if message.Seq <= current.lastSequence {
				s.validationViolations.Add(1)
			}
			return
		}
		current.lastSequence = message.Seq
		current.lastAttackAt = now
		current.ammo--
		target := session.players[message.TargetID]
		if target == nil || !target.alive || distance(current.x, current.y, target.x, target.y) > weapon.Range {
			return
		}
		if s.applyDamage(target, weapon.Damage, now) {
			current.eliminations++
		}
		s.broadcastSnapshot(session)
	case "grenade":
		current := s.playerFor(session, message.PlayerID, packet.address)
		if current == nil || !current.alive || current.spectator || current.knockedDown || message.Seq <= current.lastSequence || current.grenades <= 0 || (!current.lastGrenadeAt.IsZero() && now.Sub(current.lastGrenadeAt) < s.config.GrenadeCooldown) || !validID(message.TargetID) {
			if current != nil && message.Seq <= current.lastSequence {
				s.validationViolations.Add(1)
			}
			return
		}
		target := session.players[message.TargetID]
		if target == nil || !target.alive || distance(current.x, current.y, target.x, target.y) > s.config.AttackRange {
			return
		}
		current.lastSequence = message.Seq
		current.lastGrenadeAt = now
		current.grenades--
		for _, candidate := range session.players {
			if !candidate.alive || distance(target.x, target.y, candidate.x, candidate.y) > s.config.GrenadeRadius {
				continue
			}
			if s.applyDamage(candidate, s.config.GrenadeDamage, now) {
				current.eliminations++
			}
		}
		s.broadcastSnapshot(session)
	case "reload":
		current := s.playerFor(session, message.PlayerID, packet.address)
		if current != nil {
			s.finishReload(current, now)
		}
		if current == nil || !current.alive || current.spectator || current.knockedDown || current.ammo >= s.magazineSize(current) || (!current.reloadUntil.IsZero() && now.Before(current.reloadUntil)) {
			return
		}
		current.reloadUntil = now.Add(s.config.ReloadDuration)
		s.send(packet.address, ServerMessage{Version: 1, Type: "reload_started", SessionID: message.SessionID, PlayerID: message.PlayerID, Status: "reloading"})
	case "revive":
		current := s.playerFor(session, message.PlayerID, packet.address)
		target := session.players[message.TargetID]
		if current == nil || target == nil || !current.alive || current.spectator || current.knockedDown || !target.knockedDown || current.teamID != target.teamID || distance(current.x, current.y, target.x, target.y) > s.config.AttackRange {
			return
		}
		target.knockedDown = false
		target.knockedAt = time.Time{}
		target.health = defaultHealth / 2
		s.broadcastSnapshot(session)
	case "pickup":
		current := s.playerFor(session, message.PlayerID, packet.address)
		item := session.loot[message.ItemID]
		if current == nil || !current.alive || current.knockedDown || item == nil || distance(current.x, current.y, item.x, item.y) > s.config.AttackRange {
			return
		}
		if current.inventory == nil {
			current.inventory = make(map[string]int)
		}
		current.inventory[item.kind]++
		delete(session.loot, item.id)
		s.broadcastSnapshot(session)
	case "equip_attachment":
		current := s.playerFor(session, message.PlayerID, packet.address)
		attachmentID := strings.TrimSpace(message.AttachmentID)
		if current == nil || !current.alive || current.knockedDown || !validAttachment(attachmentID) || current.inventory[attachmentID] <= 0 {
			return
		}
		if current.attachments == nil {
			current.attachments = make(map[string]bool)
		}
		current.inventory[attachmentID]--
		if current.inventory[attachmentID] == 0 {
			delete(current.inventory, attachmentID)
		}
		current.attachments[attachmentID] = true
		s.broadcastSnapshot(session)
	case "open_supply_drop":
		current := s.playerFor(session, message.PlayerID, packet.address)
		drop := session.supplyDrops[message.ItemID]
		if current == nil || !current.alive || current.knockedDown || drop == nil || drop.opened || distance(current.x, current.y, drop.x, drop.y) > s.config.AttackRange {
			return
		}
		drop.opened = true
		if current.inventory == nil {
			current.inventory = make(map[string]int)
		}
		current.inventory["supply_crate"]++
		s.broadcastSnapshot(session)
	case "enter_vehicle":
		current := s.playerFor(session, message.PlayerID, packet.address)
		vehicle := session.vehicles[message.ItemID]
		if current == nil || !current.alive || current.knockedDown || current.vehicleID != "" || vehicle == nil || vehicle.occupantID != "" || distance(current.x, current.y, vehicle.x, vehicle.y) > s.config.AttackRange {
			return
		}
		vehicle.occupantID = current.id
		current.vehicleID = vehicle.id
		current.x, current.y = vehicle.x, vehicle.y
		s.broadcastSnapshot(session)
	case "leave_vehicle":
		current := s.playerFor(session, message.PlayerID, packet.address)
		if current == nil || current.vehicleID == "" {
			return
		}
		vehicle := session.vehicles[current.vehicleID]
		if vehicle != nil && vehicle.occupantID == current.id {
			vehicle.occupantID = ""
		}
		current.vehicleID = ""
		s.broadcastSnapshot(session)
	case "leave":
		current := s.playerFor(session, message.PlayerID, packet.address)
		if current == nil {
			return
		}
		current.connected = false
		current.disconnectedAt = now
		s.broadcastSnapshot(session)
	case "ping":
		s.send(packet.address, ServerMessage{Version: 1, Type: "pong", SessionID: message.SessionID, PlayerID: message.PlayerID})
	default:
		s.sendError(packet.address, message.SessionID, message.PlayerID, "unknown message type")
	}
}

func (s *Server) playerFor(session *match, playerID string, address *net.UDPAddr) *player {
	if session == nil || session.completed {
		return nil
	}
	current := session.players[playerID]
	if current == nil {
		return nil
	}
	if current.address == nil || current.address.String() != address.String() {
		return nil
	}
	current.connected = true
	return current
}

func (s *Server) tick(now time.Time) {
	for _, session := range s.matches {
		if session.completed {
			continue
		}
		session.tick++
		s.updatePhase(session, now)
		for _, current := range session.players {
			s.finishReload(current, now)
			if current.flightState == "parachuting" {
				current.altitude = math.Max(0, current.altitude-s.config.ParachuteSpeed/float64(s.config.TickRate))
				if current.altitude == 0 {
					current.flightState = "landed"
				}
			}
			if current.connected && current.alive && !current.spectator && !current.knockedDown {
				magnitude := math.Hypot(current.pendingDX, current.pendingDY)
				if magnitude > 1 {
					current.pendingDX /= magnitude
					current.pendingDY /= magnitude
				}
				step := s.config.MoveSpeed / float64(s.config.TickRate)
				if current.vehicleID != "" {
					if vehicle := session.vehicles[current.vehicleID]; vehicle != nil && vehicle.occupantID == current.id {
						vehicle.x = clamp(vehicle.x+current.pendingDX*step*2, -s.config.ArenaRadius, s.config.ArenaRadius)
						vehicle.y = clamp(vehicle.y+current.pendingDY*step*2, -s.config.ArenaRadius, s.config.ArenaRadius)
						current.x, current.y = vehicle.x, vehicle.y
					}
				} else {
					current.x = clamp(current.x+current.pendingDX*step, -s.config.ArenaRadius, s.config.ArenaRadius)
					current.y = clamp(current.y+current.pendingDY*step, -s.config.ArenaRadius, s.config.ArenaRadius)
				}
			}
			current.pendingDX = 0
			current.pendingDY = 0
			if !current.connected && !current.disconnectedAt.IsZero() && now.Sub(current.disconnectedAt) > s.config.ReconnectWindow {
				current.alive = false
			}
			if current.knockedDown && !current.knockedAt.IsZero() && now.Sub(current.knockedAt) > s.config.KnockdownWindow {
				current.knockedDown = false
				current.alive = false
				current.spectator = true
				current.health = 0
			}
			if current.alive && !current.knockedDown && distance(0, 0, current.x, current.y) > session.zoneRadius {
				current.zoneDamage += s.config.ZoneDamage / float64(s.config.TickRate)
				if current.zoneDamage >= 1 {
					damage := int(current.zoneDamage)
					current.zoneDamage -= float64(damage)
					s.applyDamage(current, damage, now)
				}
			} else {
				current.zoneDamage = 0
			}
		}

		progress := clamp(now.Sub(session.startedAt).Seconds()/sessionDurationSeconds(s.config.MatchDuration), 0, 1)
		session.zoneRadius = s.config.ArenaRadius - (s.config.ArenaRadius-s.config.ZoneFinalRadius)*progress
		alive := 0
		winner := ""
		for _, current := range session.players {
			if current.alive {
				alive++
				if !current.knockedDown {
					winner = current.id
				}
			}
		}
		if now.Sub(session.startedAt) >= s.config.MatchDuration {
			s.complete(session, "duration", winner)
		} else if len(session.players) >= 2 && alive <= 1 {
			s.complete(session, "last_player_standing", winner)
		}
		if !session.completed {
			s.broadcastSnapshot(session)
		} else if !session.completionSent {
			s.broadcastSnapshot(session)
			session.completionSent = true
		}
	}
}

func (s *Server) updatePhase(session *match, now time.Time) {
	elapsed := now.Sub(session.startedAt)
	previous := session.phase
	switch {
	case elapsed < s.config.StagingDuration:
		session.phase = "staging"
		session.aircraftX = -s.config.ArenaRadius
		session.aircraftY = s.config.ArenaRadius
	case elapsed < s.config.StagingDuration+s.config.AircraftDuration:
		session.phase = "aircraft"
		progress := clamp((elapsed-s.config.StagingDuration).Seconds()/s.config.AircraftDuration.Seconds(), 0, 1)
		session.aircraftX = -s.config.ArenaRadius + 2*s.config.ArenaRadius*progress
		session.aircraftY = s.config.ArenaRadius
	default:
		session.phase = "running"
		session.aircraftX = s.config.ArenaRadius
		session.aircraftY = s.config.ArenaRadius
	}
	if session.phase == "running" {
		for _, current := range session.players {
			if current.flightState != "landed" {
				current.flightState = "landed"
				current.altitude = 0
			}
		}
	}
	if previous != session.phase {
		session.status = session.phase
		s.logger.Info("match_phase_changed", "session_id", session.id, "phase", session.phase)
	}
}

func (s *Server) complete(session *match, reason, winner string) {
	session.completed = true
	session.status = "complete"
	s.logger.Info("match_complete", "session_id", session.id, "reason", reason, "winner_id", winner, "players", len(session.players))
	if s.sink != nil {
		participants := make([]ParticipantResult, 0, len(session.players))
		for _, current := range session.players {
			participants = append(participants, ParticipantResult{PlayerID: current.id, TeamID: current.teamID, Health: current.health, Alive: current.alive, Knocked: current.knockedDown, Spectator: current.spectator, Eliminations: current.eliminations})
		}
		completion := Completion{SessionID: session.id, Reason: reason, WinnerID: winner, CompletedAt: time.Now().UTC(), Participants: participants}
		select {
		case s.completion <- completion:
		default:
			s.logger.Error("game_completion_queue_full", "session_id", session.id)
		}
	}
}

func (s *Server) broadcastSnapshot(session *match) {
	if session == nil {
		return
	}
	players := make([]PlayerSnapshot, 0, len(session.players))
	for _, current := range session.players {
		inventory := make(map[string]int, len(current.inventory))
		for key, value := range current.inventory {
			inventory[key] = value
		}
		attachments := make([]string, 0, len(current.attachments))
		for attachmentID, equipped := range current.attachments {
			if equipped {
				attachments = append(attachments, attachmentID)
			}
		}
		players = append(players, PlayerSnapshot{ID: current.id, X: current.x, Y: current.y, Health: current.health, Alive: current.alive, Connected: current.connected, Knocked: current.knockedDown, Spectator: current.spectator, Armor: current.armor, TeamID: current.teamID, WeaponID: current.weaponID, Attachments: attachments, Ammo: current.ammo, Reloading: !current.reloadUntil.IsZero() && time.Now().Before(current.reloadUntil), Eliminations: current.eliminations, Grenades: current.grenades, FlightState: current.flightState, Altitude: current.altitude, VehicleID: current.vehicleID, Inventory: inventory})
	}
	loot := make([]LootSnapshot, 0, len(session.loot))
	for _, item := range session.loot {
		loot = append(loot, LootSnapshot{ID: item.id, Kind: item.kind, X: item.x, Y: item.y})
	}
	supplyDrops := make([]SupplyDropSnapshot, 0, len(session.supplyDrops))
	for _, drop := range session.supplyDrops {
		supplyDrops = append(supplyDrops, SupplyDropSnapshot{ID: drop.id, X: drop.x, Y: drop.y, Opened: drop.opened})
	}
	vehicles := make([]VehicleSnapshot, 0, len(session.vehicles))
	for _, current := range session.vehicles {
		vehicles = append(vehicles, VehicleSnapshot{ID: current.id, X: current.x, Y: current.y, Occupied: current.occupantID != ""})
	}
	messageType := "snapshot"
	reason := ""
	if session.completed {
		messageType = "match_complete"
		reason = session.status
	}
	destinations := make(map[string]*player, len(session.players))
	for _, current := range session.players {
		if !current.connected || current.address == nil {
			continue
		}
		destinations[current.address.String()] = current
	}
	if session.tick == 1 {
		s.logger.Info("snapshot_broadcast", "session_id", session.id, "players", len(players), "destinations", len(destinations), "loot", len(loot))
	}
	session.snapshotSeq++
	messages := snapshotMessages(session, players, loot, supplyDrops, vehicles, messageType, reason)
	s.broadcasts.Add(uint64(len(destinations) * len(messages)))
	for _, current := range destinations {
		for _, message := range messages {
			message.PlayerID = current.id
			if data, err := json.Marshal(message); err == nil {
				if s.send(current.address, message) {
					s.snapshotBytes.Add(uint64(len(data)))
				}
				continue
			}
			s.send(current.address, message)
		}
	}
}

func snapshotMessages(session *match, players []PlayerSnapshot, loot []LootSnapshot, supplyDrops []SupplyDropSnapshot, vehicles []VehicleSnapshot, messageType, reason string) []ServerMessage {
	base := ServerMessage{Version: 1, Type: messageType, SessionID: session.id, Tick: session.tick, Status: session.status, Reason: reason, ZoneRadius: session.zoneRadius, Phase: session.phase, AircraftX: session.aircraftX, AircraftY: session.aircraftY, Loot: loot, SupplyDrops: supplyDrops, Vehicles: vehicles}
	if messageFitsSnapshot(base, players) {
		base.Players = players
		return []ServerMessage{base}
	}

	snapshotID := fmt.Sprintf("%s-%d-%d", session.id, session.tick, session.snapshotSeq)
	for chunkSize := len(players) - 1; chunkSize > 0; chunkSize-- {
		messages := make([]ServerMessage, 0, (len(players)+chunkSize-1)/chunkSize)
		for start := 0; start < len(players); start += chunkSize {
			end := start + chunkSize
			if end > len(players) {
				end = len(players)
			}
			message := base
			message.SnapshotID = snapshotID
			message.ChunkIndex = len(messages)
			message.Players = players[start:end]
			messages = append(messages, message)
		}
		for index := range messages {
			messages[index].ChunkCount = len(messages)
		}
		fits := true
		for _, message := range messages {
			if !messageFitsSnapshot(message, message.Players) {
				fits = false
				break
			}
		}
		if fits {
			return messages
		}
	}
	// A single-player chunk should always fit because IDs are bounded. Keep
	// the fallback explicit so a future schema change cannot silently drop it.
	base.SnapshotID = snapshotID
	base.ChunkCount = len(players)
	base.Players = players
	return []ServerMessage{base}
}

func messageFitsSnapshot(base ServerMessage, players []PlayerSnapshot) bool {
	base.Players = players
	data, err := json.Marshal(base)
	return err == nil && len(data) <= maxSnapshotBytes
}

func (s *Server) Metrics() string {
	tickDurationSeconds := float64(s.tickDurationNS.Load()) / float64(time.Second)
	maxTickDurationSeconds := float64(s.maxTickDurationNS.Load()) / float64(time.Second)
	return "# HELP ros_game_ticks_total Authoritative simulation ticks.\n# TYPE ros_game_ticks_total counter\nros_game_ticks_total " + strconv.FormatUint(s.ticks.Load(), 10) +
		"\n# HELP ros_game_tick_duration_seconds_total Cumulative authoritative tick execution time.\n# TYPE ros_game_tick_duration_seconds_total counter\nros_game_tick_duration_seconds_total " + strconv.FormatFloat(tickDurationSeconds, 'f', 9, 64) +
		"\n# HELP ros_game_tick_duration_seconds_max Maximum observed authoritative tick execution time.\n# TYPE ros_game_tick_duration_seconds_max gauge\nros_game_tick_duration_seconds_max " + strconv.FormatFloat(maxTickDurationSeconds, 'f', 9, 64) +
		"\n# HELP ros_game_tick_overruns_total Ticks that exceeded the configured tick interval.\n# TYPE ros_game_tick_overruns_total counter\nros_game_tick_overruns_total " + strconv.FormatUint(s.tickOverruns.Load(), 10) +
		"\n# HELP ros_game_active_sessions Current non-completed sessions.\n# TYPE ros_game_active_sessions gauge\nros_game_active_sessions " + strconv.FormatInt(s.activeSessions.Load(), 10) +
		"\n# HELP ros_game_active_players Current players in non-completed sessions.\n# TYPE ros_game_active_players gauge\nros_game_active_players " + strconv.FormatInt(s.activePlayers.Load(), 10) +
		"\n# HELP ros_game_snapshot_broadcasts_total Snapshot datagrams sent to client endpoints.\n# TYPE ros_game_snapshot_broadcasts_total counter\nros_game_snapshot_broadcasts_total " + strconv.FormatUint(s.broadcasts.Load(), 10) +
		"\n# HELP ros_game_snapshot_bytes_total Serialized snapshot bytes sent to client endpoints.\n# TYPE ros_game_snapshot_bytes_total counter\nros_game_snapshot_bytes_total " + strconv.FormatUint(s.snapshotBytes.Load(), 10) +
		"\n# HELP ros_game_udp_receive_bytes_total UDP payload bytes received.\n# TYPE ros_game_udp_receive_bytes_total counter\nros_game_udp_receive_bytes_total " + strconv.FormatUint(s.udpReceiveBytes.Load(), 10) +
		"\n# HELP ros_game_udp_send_bytes_total UDP payload bytes sent.\n# TYPE ros_game_udp_send_bytes_total counter\nros_game_udp_send_bytes_total " + strconv.FormatUint(s.udpSendBytes.Load(), 10) +
		"\n# HELP ros_game_validation_violations_total Rejected or suspicious client protocol actions.\n# TYPE ros_game_validation_violations_total counter\nros_game_validation_violations_total " + strconv.FormatUint(s.validationViolations.Load(), 10) + "\n"
}

func (s *Server) sendError(address *net.UDPAddr, sessionID, playerID, reason string) {
	s.send(address, ServerMessage{Version: 1, Type: "error", SessionID: sessionID, PlayerID: playerID, Error: reason})
}

func (s *Server) send(address *net.UDPAddr, message ServerMessage) bool {
	if address == nil {
		return false
	}
	data, err := json.Marshal(message)
	if err != nil {
		return false
	}
	if _, err := s.conn.WriteToUDP(data, address); err != nil {
		s.logger.Warn("udp_send_failed", "error", err, "address", address.String(), "bytes", len(data))
		return false
	}
	s.udpSendBytes.Add(uint64(len(data)))
	return true
}

func validID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 64
}

func clamp(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func distance(x1, y1, x2, y2 float64) float64 {
	return math.Hypot(x1-x2, y1-y2)
}

func (s *Server) applyDamage(target *player, damage int, now time.Time) bool {
	if target == nil || !target.alive || damage <= 0 {
		return false
	}
	if target.armor > 0 {
		absorbed := damage / 2
		if absorbed > target.armor {
			absorbed = target.armor
		}
		target.armor -= absorbed
		damage -= absorbed
	}
	target.health -= damage
	if target.health > 0 {
		return false
	}
	if !target.knockedDown {
		target.health = 1
		target.knockedDown = true
		target.knockedAt = now
		return false
	}
	target.health = 0
	target.knockedDown = false
	target.alive = false
	target.spectator = true
	return true
}

func (s *Server) finishReload(current *player, now time.Time) {
	if current != nil && !current.reloadUntil.IsZero() && !now.Before(current.reloadUntil) {
		current.reloadUntil = time.Time{}
		current.ammo = s.magazineSize(current)
	}
}

func (s *Server) weaponFor(current *player) WeaponDefinition {
	if current == nil {
		return s.weaponForID("rifle")
	}
	weapon := s.weaponForID(current.weaponID)
	if current.attachments["scope"] {
		weapon.Range *= 1.25
	}
	if current.attachments["muzzle"] {
		weapon.Damage += 5
	}
	if current.attachments["extended_mag"] {
		weapon.MagazineSize += 5
	}
	return weapon
}

func (s *Server) weaponForID(id string) WeaponDefinition {
	if weapon, ok := s.weapons[id]; ok {
		return weapon
	}
	return s.weapons["rifle"]
}

func (s *Server) magazineSize(current *player) int {
	return s.weaponFor(current).MagazineSize
}

func teamIDForJoin(value string, index int) string {
	if validID(value) {
		return strings.TrimSpace(value)
	}
	return fmt.Sprintf("team-%d", index/4+1)
}

func weaponIDForJoin(value string) string {
	if validID(value) {
		return strings.TrimSpace(value)
	}
	return "rifle"
}

func validAttachment(value string) bool {
	switch value {
	case "scope", "muzzle", "extended_mag":
		return true
	default:
		return false
	}
}

func defaultWeapons(config Config) map[string]WeaponDefinition {
	return map[string]WeaponDefinition{
		"rifle":   {ID: "rifle", Damage: config.AttackDamage, Range: config.AttackRange, Cooldown: config.AttackCooldown, MagazineSize: config.MagazineSize},
		"pistol":  {ID: "pistol", Damage: 15, Range: config.AttackRange + 2, Cooldown: 150 * time.Millisecond, MagazineSize: 12},
		"shotgun": {ID: "shotgun", Damage: 45, Range: config.AttackRange - 2, Cooldown: 800 * time.Millisecond, MagazineSize: 8},
	}
}

func makeLoot(count int, radius float64) map[string]*lootItem {
	items := make(map[string]*lootItem, count)
	for index := 0; index < count; index++ {
		fraction := float64(index+1) / float64(count+1)
		id := fmt.Sprintf("loot-%03d", index+1)
		items[id] = &lootItem{id: id, kind: []string{"ammo", "armor", "medkit", "scope"}[index%4], x: -radius + 2*radius*fraction, y: -radius + 2*radius*(1-fraction)}
	}
	return items
}

func makeSupplyDrops(count int, radius float64) map[string]*supplyDrop {
	drops := make(map[string]*supplyDrop, count)
	for index := 0; index < count; index++ {
		fraction := float64(index+1) / float64(count+1)
		id := fmt.Sprintf("supply-%03d", index+1)
		drops[id] = &supplyDrop{id: id, x: -radius + 2*radius*fraction, y: 0}
	}
	return drops
}

func makeVehicles(count int, radius float64) map[string]*vehicle {
	vehicles := make(map[string]*vehicle, count)
	for index := 0; index < count; index++ {
		fraction := float64(index+1) / float64(count+1)
		id := fmt.Sprintf("vehicle-%03d", index+1)
		vehicles[id] = &vehicle{id: id, x: -radius + 2*radius*fraction, y: -radius / 2}
	}
	return vehicles
}

func sessionDurationSeconds(duration time.Duration) float64 {
	return math.Max(duration.Seconds(), 0.001)
}
