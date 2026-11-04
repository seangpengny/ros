package game

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"
)

func testServer(t *testing.T, duration time.Duration) (*Server, context.CancelFunc) {
	return testServerWithConfig(t, duration, nil)
}

func testServerWithConfig(t *testing.T, duration time.Duration, configure func(*Config)) (*Server, context.CancelFunc) {
	return testServerWithSink(t, duration, configure, nil)
}

func testServerWithSink(t *testing.T, duration time.Duration, configure func(*Config), sink CompletionSink) (*Server, context.CancelFunc) {
	t.Helper()
	config := DefaultConfig()
	config.ListenAddr = "127.0.0.1:0"
	config.MatchDuration = duration
	if configure != nil {
		configure(&config)
	}
	config.ReconnectWindow = time.Second
	server, err := NewServerWithSink(config, slog.New(slog.NewTextHandler(testWriter{t}, nil)), sink)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Run(ctx)
	}()
	deadline := time.Now().Add(time.Second)
	for !server.Ready() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !server.Ready() {
		t.Fatal("server did not become ready")
	}
	return server, func() {
		cancel()
		_ = server.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("server did not stop")
		}
	}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Helper()
	w.t.Log(string(p))
	return len(p), nil
}

func sendMessage(t *testing.T, conn *net.UDPConn, server net.Addr, message Message) ServerMessage {
	return sendMessageExpect(t, conn, server, message, "")
}

func sendMessageExpect(t *testing.T, conn *net.UDPConn, server net.Addr, message Message, expectedType string) ServerMessage {
	t.Helper()
	data, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.WriteToUDP(data, server.(*net.UDPAddr)); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	buffer := make([]byte, maxDatagramBytes)
	for {
		n, _, err := conn.ReadFromUDP(buffer)
		if err != nil {
			t.Fatal(err)
		}
		var response ServerMessage
		if err := json.Unmarshal(buffer[:n], &response); err != nil {
			continue
		}
		if expectedType == "" || response.Type == expectedType {
			return response
		}
	}
}

func TestJoinMovementAndAttack(t *testing.T) {
	server, stop := testServer(t, 5*time.Second)
	defer stop()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	first := sendMessageExpect(t, conn, server.Addr(), Message{Version: 1, Type: "join", SessionID: "test", PlayerID: "one"}, "welcome")
	if first.Type != "welcome" {
		t.Fatalf("join response type = %q", first.Type)
	}
	second := sendMessageExpect(t, conn, server.Addr(), Message{Version: 1, Type: "join", SessionID: "test", PlayerID: "two"}, "welcome")
	if second.Type != "welcome" {
		t.Fatalf("second join response type = %q", second.Type)
	}

	_ = sendMessage(t, conn, server.Addr(), Message{Version: 1, Type: "input", SessionID: "test", PlayerID: "one", Seq: 1, DX: 1})
	time.Sleep(100 * time.Millisecond)
	_ = sendMessageExpect(t, conn, server.Addr(), Message{Version: 1, Type: "ping", SessionID: "test", PlayerID: "one"}, "pong")
	response := sendMessageExpect(t, conn, server.Addr(), Message{Version: 1, Type: "attack", SessionID: "test", PlayerID: "one", TargetID: "two", Seq: 2}, "snapshot")
	if response.Type != "snapshot" && response.Type != "match_complete" {
		t.Fatalf("attack response type = %q", response.Type)
	}
}

func TestMatchCompletesByDuration(t *testing.T) {
	server, stop := testServer(t, 50*time.Millisecond)
	defer stop()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = sendMessageExpect(t, conn, server.Addr(), Message{Version: 1, Type: "join", SessionID: "duration", PlayerID: "one"}, "welcome")
	_ = sendMessageExpect(t, conn, server.Addr(), Message{Version: 1, Type: "join", SessionID: "duration", PlayerID: "two"}, "welcome")
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		buffer := make([]byte, maxDatagramBytes)
		n, _, readErr := conn.ReadFromUDP(buffer)
		if readErr != nil {
			continue
		}
		var response ServerMessage
		if json.Unmarshal(buffer[:n], &response) == nil && response.Type == "match_complete" {
			return
		}
	}
	t.Fatal("match did not complete within the deadline")
}

func TestKnockdownReviveAndElimination(t *testing.T) {
	server, stop := testServerWithConfig(t, 5*time.Second, func(config *Config) {
		config.AttackCooldown = time.Millisecond
		config.KnockdownWindow = time.Second
	})
	defer stop()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, id := range []string{"one", "two"} {
		_ = sendMessageExpect(t, conn, server.Addr(), Message{Version: 1, Type: "join", SessionID: "combat", PlayerID: id}, "welcome")
	}
	for seq := uint64(1); seq <= 4; seq++ {
		expectedHealth := int(defaultHealth) - int(seq)*server.config.AttackDamage
		if expectedHealth <= 0 {
			expectedHealth = 1
		}
		response := sendMessageUntil(t, conn, server.Addr(), Message{Version: 1, Type: "attack", SessionID: "combat", PlayerID: "one", TargetID: "two", Seq: seq}, func(value ServerMessage) bool {
			player := playerSnapshot(value, "two")
			return player.Health == expectedHealth && (seq < 4 || player.Knocked)
		})
		if seq == 4 && (len(response.Players) != 2 || !playerSnapshot(response, "two").Knocked) {
			t.Fatalf("target was not knocked down: %+v", response.Players)
		}
		time.Sleep(2 * time.Millisecond)
	}
	response := sendMessageUntil(t, conn, server.Addr(), Message{Version: 1, Type: "revive", SessionID: "combat", PlayerID: "one", TargetID: "two"}, func(value ServerMessage) bool {
		player := playerSnapshot(value, "two")
		return !player.Knocked && player.Health == defaultHealth/2
	})
	if revived := playerSnapshot(response, "two"); revived.Knocked || revived.Health != defaultHealth/2 {
		t.Fatalf("target was not revived: %+v", revived)
	}
	for seq := uint64(5); seq <= 7; seq++ {
		expectedHealth := defaultHealth/2 - int(seq-4)*server.config.AttackDamage
		if expectedHealth <= 0 {
			expectedHealth = 1
		}
		response = sendMessageUntil(t, conn, server.Addr(), Message{Version: 1, Type: "attack", SessionID: "combat", PlayerID: "one", TargetID: "two", Seq: seq}, func(value ServerMessage) bool {
			player := playerSnapshot(value, "two")
			return (seq == 7 && !player.Alive) || (seq < 7 && player.Health == expectedHealth)
		})
		time.Sleep(2 * time.Millisecond)
	}
	if target := playerSnapshot(response, "two"); target.Alive || !target.Spectator || target.Eliminations != 0 {
		t.Fatalf("target should be eliminated: %+v", target)
	}
	if attacker := playerSnapshot(response, "one"); attacker.Eliminations != 1 {
		t.Fatalf("attacker eliminations = %d, want 1", attacker.Eliminations)
	}
}

func TestTeamReviveAndWeaponReload(t *testing.T) {
	server, stop := testServerWithConfig(t, 5*time.Second, func(config *Config) {
		config.AttackCooldown = time.Millisecond
		config.MagazineSize = 1
		config.ReloadDuration = 20 * time.Millisecond
	})
	defer stop()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, player := range []struct{ id, team string }{{"red-one", "red"}, {"blue-one", "blue"}, {"blue-two", "blue"}} {
		_ = sendMessageExpect(t, conn, server.Addr(), Message{Version: 1, Type: "join", SessionID: "teams", PlayerID: player.id, TeamID: player.team}, "welcome")
	}
	response := sendMessageUntil(t, conn, server.Addr(), Message{Version: 1, Type: "attack", SessionID: "teams", PlayerID: "red-one", TargetID: "blue-one", Seq: 1}, func(value ServerMessage) bool {
		return playerSnapshot(value, "blue-one").Health == defaultHealth-server.config.AttackDamage
	})
	if player := playerSnapshot(response, "red-one"); player.TeamID != "red" || player.WeaponID != "rifle" || player.Ammo != 0 {
		t.Fatalf("unexpected attacker state: %+v", player)
	}
	_ = sendMessageExpect(t, conn, server.Addr(), Message{Version: 1, Type: "reload", SessionID: "teams", PlayerID: "red-one"}, "reload_started")
	time.Sleep(40 * time.Millisecond)
	response = sendMessageUntil(t, conn, server.Addr(), Message{Version: 1, Type: "attack", SessionID: "teams", PlayerID: "red-one", TargetID: "blue-one", Seq: 2}, func(value ServerMessage) bool {
		return playerSnapshot(value, "blue-one").Health == defaultHealth-2*server.config.AttackDamage
	})
	if player := playerSnapshot(response, "red-one"); player.Ammo != 0 || player.Reloading {
		t.Fatalf("reload did not restore and consume ammo correctly: %+v", player)
	}
	for seq := uint64(3); seq <= 4; seq++ {
		_ = sendMessageUntil(t, conn, server.Addr(), Message{Version: 1, Type: "reload", SessionID: "teams", PlayerID: "red-one"}, func(value ServerMessage) bool {
			return value.Type == "reload_started"
		})
		time.Sleep(25 * time.Millisecond)
		_ = sendMessageUntil(t, conn, server.Addr(), Message{Version: 1, Type: "attack", SessionID: "teams", PlayerID: "red-one", TargetID: "blue-one", Seq: seq}, func(value ServerMessage) bool {
			player := playerSnapshot(value, "blue-one")
			return (seq == 4 && player.Knocked) || (seq < 4 && player.Health == defaultHealth-int(seq)*server.config.AttackDamage)
		})
	}
	// A red player cannot revive the blue player, while a red teammate can.
	_ = sendMessage(t, conn, server.Addr(), Message{Version: 1, Type: "revive", SessionID: "teams", PlayerID: "red-one", TargetID: "blue-one"})
	time.Sleep(20 * time.Millisecond)
	response = sendMessageUntil(t, conn, server.Addr(), Message{Version: 1, Type: "revive", SessionID: "teams", PlayerID: "blue-two", TargetID: "blue-one"}, func(value ServerMessage) bool {
		player := playerSnapshot(value, "blue-one")
		return !player.Knocked && player.Health == defaultHealth/2
	})
	if player := playerSnapshot(response, "blue-one"); player.TeamID != "blue" || player.Knocked {
		t.Fatalf("teammate revive did not restore the target: %+v", player)
	}
}

func TestWeaponDefinitionsAndGrenadeDamage(t *testing.T) {
	server, stop := testServerWithConfig(t, 2*time.Second, func(config *Config) {
		config.AttackCooldown = time.Millisecond
		config.GrenadeDamage = defaultHealth
		config.GrenadeRadius = 1
		config.GrenadeCooldown = time.Millisecond
		config.StagingDuration = 0
		config.AircraftDuration = time.Second
		config.ZoneFinalRadius = 2
		config.ArenaRadius = 10
		config.LootCount = 0
		config.SupplyDropCount = 0
		config.VehicleCount = 0
	})
	defer stop()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = sendMessageExpect(t, conn, server.Addr(), Message{Version: 1, Type: "join", SessionID: "weapons", PlayerID: "one", WeaponID: "pistol"}, "welcome")
	_ = sendMessageExpect(t, conn, server.Addr(), Message{Version: 1, Type: "join", SessionID: "weapons", PlayerID: "two"}, "welcome")
	_ = sendMessageExpect(t, conn, server.Addr(), Message{Version: 1, Type: "join", SessionID: "weapons", PlayerID: "three"}, "welcome")
	attack := sendMessageUntil(t, conn, server.Addr(), Message{Version: 1, Type: "attack", SessionID: "weapons", PlayerID: "one", TargetID: "two", Seq: 1}, func(value ServerMessage) bool {
		return playerSnapshot(value, "two").Health == defaultHealth-15
	})
	if player := playerSnapshot(attack, "one"); player.WeaponID != "pistol" || player.Ammo != 11 {
		t.Fatalf("weapon definition was not applied: %+v", player)
	}
	grenade := sendMessageUntil(t, conn, server.Addr(), Message{Version: 1, Type: "grenade", SessionID: "weapons", PlayerID: "one", TargetID: "two", Seq: 2}, func(value ServerMessage) bool {
		return playerSnapshot(value, "two").Knocked
	})
	if player := playerSnapshot(grenade, "one"); player.Grenades != 1 {
		t.Fatalf("grenade inventory was not consumed: %+v", player)
	}
	time.Sleep(2 * time.Millisecond)
	eliminated := sendMessageUntil(t, conn, server.Addr(), Message{Version: 1, Type: "grenade", SessionID: "weapons", PlayerID: "one", TargetID: "two", Seq: 3}, func(value ServerMessage) bool {
		return !playerSnapshot(value, "two").Alive
	})
	if target := playerSnapshot(eliminated, "two"); !target.Spectator || target.Alive {
		t.Fatalf("grenade did not eliminate the knocked target: %+v", target)
	}
	if player := playerSnapshot(eliminated, "one"); player.Eliminations != 1 || player.Grenades != 0 {
		t.Fatalf("grenade elimination accounting is wrong: %+v", player)
	}
}

func sendMessageUntil(t *testing.T, conn *net.UDPConn, server net.Addr, message Message, predicate func(ServerMessage) bool) ServerMessage {
	t.Helper()
	data, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.WriteToUDP(data, server.(*net.UDPAddr)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		buffer := make([]byte, maxDatagramBytes)
		n, _, err := conn.ReadFromUDP(buffer)
		if err != nil {
			continue
		}
		var response ServerMessage
		if json.Unmarshal(buffer[:n], &response) == nil && predicate(response) {
			return response
		}
	}
	t.Fatal("server response predicate was not observed")
	return ServerMessage{}
}

func TestDeterministicLootPickup(t *testing.T) {
	server, stop := testServerWithConfig(t, 5*time.Second, func(config *Config) {
		config.ArenaRadius = 10
		config.ZoneFinalRadius = 2
		config.ZoneFinalRadius = 2
		config.LootCount = 1
	})
	defer stop()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = sendMessageExpect(t, conn, server.Addr(), Message{Version: 1, Type: "join", SessionID: "loot", PlayerID: "one"}, "welcome")
	pickupResponse := sendMessageExpect(t, conn, server.Addr(), Message{Version: 1, Type: "pickup", SessionID: "loot", PlayerID: "one", ItemID: "loot-001"}, "snapshot")
	if player := playerSnapshot(pickupResponse, "one"); player.Inventory["ammo"] != 1 || len(pickupResponse.Loot) != 0 {
		t.Fatalf("loot was not picked up: player=%+v loot=%+v", player, pickupResponse.Loot)
	}
	response := sendMessageExpect(t, conn, server.Addr(), Message{Version: 1, Type: "ping", SessionID: "loot", PlayerID: "one"}, "pong")
	if len(response.Players) != 0 {
		t.Fatalf("unexpected players in pong response: %+v", response.Players)
	}
}

func TestAttachmentPickupAndEquip(t *testing.T) {
	server, stop := testServerWithConfig(t, 2*time.Second, func(config *Config) {
		config.ArenaRadius = 5
		config.ZoneFinalRadius = 2
		config.LootCount = 4
		config.SupplyDropCount = 0
		config.VehicleCount = 0
	})
	defer stop()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = sendMessageExpect(t, conn, server.Addr(), Message{Version: 1, Type: "join", SessionID: "attachments", PlayerID: "one"}, "welcome")
	picked := sendMessageUntil(t, conn, server.Addr(), Message{Version: 1, Type: "pickup", SessionID: "attachments", PlayerID: "one", ItemID: "loot-004"}, func(value ServerMessage) bool {
		return playerSnapshot(value, "one").Inventory["scope"] == 1
	})
	if player := playerSnapshot(picked, "one"); player.Inventory["scope"] != 1 {
		t.Fatalf("scope attachment was not picked up: %+v", player)
	}
	equipped := sendMessageUntil(t, conn, server.Addr(), Message{Version: 1, Type: "equip_attachment", SessionID: "attachments", PlayerID: "one", AttachmentID: "scope"}, func(value ServerMessage) bool {
		player := playerSnapshot(value, "one")
		return len(player.Attachments) == 1 && player.Attachments[0] == "scope"
	})
	player := playerSnapshot(equipped, "one")
	if player.Inventory["scope"] != 0 || len(player.Attachments) != 1 || player.Attachments[0] != "scope" {
		t.Fatalf("scope attachment was not equipped: %+v", player)
	}
}

func TestSafeZoneShrinksBeforeMatchCompletion(t *testing.T) {
	server, stop := testServerWithConfig(t, 100*time.Millisecond, func(config *Config) {
		config.ArenaRadius = 10
		config.ZoneFinalRadius = 2
	})
	defer stop()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, id := range []string{"one", "two"} {
		_ = sendMessageExpect(t, conn, server.Addr(), Message{Version: 1, Type: "join", SessionID: "zone", PlayerID: id}, "welcome")
	}
	response := sendMessageUntil(t, conn, server.Addr(), Message{Version: 1, Type: "ping", SessionID: "zone", PlayerID: "one"}, func(value ServerMessage) bool {
		return value.Type == "match_complete"
	})
	if response.ZoneRadius > server.config.ZoneFinalRadius+0.01 {
		t.Fatalf("zone radius = %f, want <= %f", response.ZoneRadius, server.config.ZoneFinalRadius+0.01)
	}
}

func TestBattleRoyaleLifecycleSupplyDropAndVehicle(t *testing.T) {
	server, stop := testServerWithConfig(t, 2*time.Second, func(config *Config) {
		config.ArenaRadius = 10
		config.ZoneFinalRadius = 2
		config.StagingDuration = 20 * time.Millisecond
		config.AircraftDuration = 200 * time.Millisecond
		config.ParachuteSpeed = 1000
		config.LootCount = 0
		config.SupplyDropCount = 1
		config.VehicleCount = 1
	})
	defer stop()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	welcome := sendMessageExpect(t, conn, server.Addr(), Message{Version: 1, Type: "join", SessionID: "br", PlayerID: "one"}, "welcome")
	if welcome.Status != "staging" || welcome.Phase != "staging" {
		t.Fatalf("initial match phase = %q/%q, want staging", welcome.Status, welcome.Phase)
	}
	aircraft := sendMessageUntil(t, conn, server.Addr(), Message{Version: 1, Type: "ping", SessionID: "br", PlayerID: "one"}, func(value ServerMessage) bool {
		return value.Type == "snapshot" && value.Phase == "aircraft" && value.AircraftX < server.config.ArenaRadius
	})
	if aircraft.AircraftY != server.config.ArenaRadius {
		t.Fatalf("aircraft y = %f, want %f", aircraft.AircraftY, server.config.ArenaRadius)
	}
	parachuting := sendMessageUntil(t, conn, server.Addr(), Message{Version: 1, Type: "deploy", SessionID: "br", PlayerID: "one"}, func(value ServerMessage) bool {
		return playerSnapshot(value, "one").FlightState == "parachuting"
	})
	if player := playerSnapshot(parachuting, "one"); player.Altitude <= 0 {
		t.Fatalf("parachuting altitude = %f, want positive", player.Altitude)
	}
	running := sendMessageUntil(t, conn, server.Addr(), Message{Version: 1, Type: "ping", SessionID: "br", PlayerID: "one"}, func(value ServerMessage) bool {
		return value.Type == "snapshot" && value.Phase == "running" && playerSnapshot(value, "one").FlightState == "landed"
	})
	if player := playerSnapshot(running, "one"); player.Altitude != 0 {
		t.Fatalf("landed altitude = %f, want 0", player.Altitude)
	}
	supply := sendMessageUntil(t, conn, server.Addr(), Message{Version: 1, Type: "open_supply_drop", SessionID: "br", PlayerID: "one", ItemID: "supply-001"}, func(value ServerMessage) bool {
		return value.Type == "snapshot" && len(value.SupplyDrops) == 1 && value.SupplyDrops[0].Opened
	})
	if player := playerSnapshot(supply, "one"); player.Inventory["supply_crate"] != 1 || len(supply.SupplyDrops) != 1 || !supply.SupplyDrops[0].Opened {
		t.Fatalf("supply drop was not opened authoritatively: player=%+v drops=%+v", player, supply.SupplyDrops)
	}
	entered := sendMessageUntil(t, conn, server.Addr(), Message{Version: 1, Type: "enter_vehicle", SessionID: "br", PlayerID: "one", ItemID: "vehicle-001"}, func(value ServerMessage) bool {
		return value.Type == "snapshot" && playerSnapshot(value, "one").VehicleID == "vehicle-001"
	})
	if player := playerSnapshot(entered, "one"); player.VehicleID != "vehicle-001" || !entered.Vehicles[0].Occupied {
		t.Fatalf("vehicle entry was not replicated: player=%+v vehicles=%+v", player, entered.Vehicles)
	}
	_ = sendMessage(t, conn, server.Addr(), Message{Version: 1, Type: "input", SessionID: "br", PlayerID: "one", Seq: 1, DX: 1})
	moved := sendMessageUntil(t, conn, server.Addr(), Message{Version: 1, Type: "ping", SessionID: "br", PlayerID: "one"}, func(value ServerMessage) bool {
		player := playerSnapshot(value, "one")
		return value.Type == "snapshot" && player.VehicleID == "vehicle-001" && player.X > 0
	})
	if player := playerSnapshot(moved, "one"); player.X <= 0 {
		t.Fatalf("vehicle movement was not authoritative: %+v", player)
	}
	left := sendMessageUntil(t, conn, server.Addr(), Message{Version: 1, Type: "leave_vehicle", SessionID: "br", PlayerID: "one"}, func(value ServerMessage) bool {
		return value.Type == "snapshot" && playerSnapshot(value, "one").VehicleID == "" && len(value.Vehicles) == 1 && !value.Vehicles[0].Occupied
	})
	if player := playerSnapshot(left, "one"); player.VehicleID != "" || left.Vehicles[0].Occupied {
		t.Fatalf("vehicle exit was not replicated: player=%+v vehicles=%+v", player, left.Vehicles)
	}
}

func TestHundredPlayerSnapshotFitsDatagram(t *testing.T) {
	players := make([]PlayerSnapshot, 0, 100)
	for index := 0; index < 100; index++ {
		players = append(players, PlayerSnapshot{
			ID: fmt.Sprintf("player-%03d", index+1), X: float64(index % 10), Y: float64(index / 10),
			Health: defaultHealth, Alive: true, Connected: true, TeamID: fmt.Sprintf("team-%d", index/4+1),
			WeaponID: "rifle", Ammo: 30, Inventory: map[string]int{},
		})
	}
	session := &match{id: "load", status: "running", zoneRadius: 100, tick: 1}
	messages := snapshotMessages(session, players, make([]LootSnapshot, 16), nil, nil, "snapshot", "")
	if len(messages) < 2 {
		t.Fatalf("expected high-density snapshot fragmentation, got %d message", len(messages))
	}
	playerCount := 0
	for _, message := range messages {
		data, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) > maxSnapshotBytes {
			t.Fatalf("snapshot chunk exceeds safe UDP budget: %d bytes", len(data))
		}
		playerCount += len(message.Players)
	}
	if playerCount != len(players) {
		t.Fatalf("fragmented snapshot contains %d/%d players", playerCount, len(players))
	}
}

type completionTestSink struct {
	values chan Completion
}

func (s *completionTestSink) RecordGameCompletion(_ context.Context, value Completion) error {
	s.values <- value
	return nil
}

func TestCompletionSinkReceivesIdempotentEvent(t *testing.T) {
	sink := &completionTestSink{values: make(chan Completion, 1)}
	server, stop := testServerWithSink(t, 50*time.Millisecond, nil, sink)
	defer stop()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, id := range []string{"one", "two"} {
		_ = sendMessageExpect(t, conn, server.Addr(), Message{Version: 1, Type: "join", SessionID: "sink", PlayerID: id}, "welcome")
	}
	select {
	case completion := <-sink.values:
		if completion.SessionID != "sink" || completion.Reason != "duration" || len(completion.Participants) != 2 {
			t.Fatalf("unexpected completion: %+v", completion)
		}
	case <-time.After(time.Second):
		t.Fatal("completion sink did not receive event")
	}
}

func TestMetricsExposeRuntimeMeasurements(t *testing.T) {
	server, stop := testServerWithConfig(t, 500*time.Millisecond, func(config *Config) {
		config.TickRate = 100
	})
	defer stop()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = sendMessageExpect(t, conn, server.Addr(), Message{Version: 1, Type: "join", SessionID: "metrics", PlayerID: "one"}, "welcome")
	deadline := time.Now().Add(time.Second)
	for server.ticks.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	output := server.Metrics()
	for _, expected := range []string{
		"ros_game_tick_duration_seconds_total",
		"ros_game_tick_duration_seconds_max",
		"ros_game_tick_overruns_total",
		"ros_game_active_sessions 1",
		"ros_game_active_players 1",
		"ros_game_snapshot_bytes_total",
		"ros_game_udp_receive_bytes_total",
		"ros_game_udp_send_bytes_total",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("metrics output missing %q: %s", expected, output)
		}
	}
}

func playerSnapshot(response ServerMessage, id string) PlayerSnapshot {
	for _, player := range response.Players {
		if player.ID == id {
			return player
		}
	}
	return PlayerSnapshot{}
}

func TestLegacyBaseAppHandshake(t *testing.T) {
	server, cancel := testServer(t, 2*time.Second)
	defer cancel()

	conn, err := net.Dial("udp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	packet := make([]byte, 24)
	packet[0] = 0x01
	packet[3] = 0x0b
	packet[11] = 0x78
	packet[12] = 0x56
	packet[13] = 0x34
	packet[14] = 0x12 // sessionKey: 0x12345678
	packet[19] = 0x14
	packet[22] = 0x02

	if _, err := conn.Write(packet); err != nil {
		t.Fatalf("failed to send handshake: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	ack := make([]byte, 64)
	n, err := conn.Read(ack)
	if err != nil {
		t.Fatalf("failed to receive handshake ack: %v", err)
	}

	if n != 24 {
		t.Fatalf("expected 24 bytes ack, got %d", n)
	}
	if ack[0] != 0x02 {
		t.Fatalf("expected ack opcode 0x02, got %#02x", ack[0])
	}
	if ack[11] != 0x78 || ack[12] != 0x56 || ack[13] != 0x34 || ack[14] != 0x12 {
		t.Fatalf("session key mismatch in ack: %v", ack[11:15])
	}
}
