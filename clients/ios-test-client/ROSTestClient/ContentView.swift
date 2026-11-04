import Foundation
import Network
import SwiftUI

private struct APIClient {
    let baseURL: URL

    enum APIError: Error, CustomStringConvertible {
        case invalidResponse
        case status(Int, String)
        case missingField(String)

        var description: String {
            switch self {
            case .invalidResponse:
                return "invalid HTTP response"
            case let .status(code, body):
                return "HTTP \(code): \(body)"
            case let .missingField(field):
                return "missing response field: \(field)"
            }
        }
    }

    struct Session: Decodable {
        let sessionID: String
        let accountID: String
        let accessToken: String
        let refreshToken: String

        enum CodingKeys: String, CodingKey {
            case sessionID = "session_id"
            case accountID = "account_id"
            case accessToken = "access_token"
            case refreshToken = "refresh_token"
        }
    }

    struct CatalogItem: Decodable, Identifiable {
        let itemID: String
        let displayName: String
        let description: String
        let price: Int
        let gameplayItem: Bool

        var id: String { itemID }

        enum CodingKeys: String, CodingKey {
            case itemID = "item_id"
            case displayName = "display_name"
            case description
            case price
            case gameplayItem = "gameplay_item"
        }
    }

    struct Catalog: Decodable {
        var coins: Int
        let items: [CatalogItem]
    }

    struct Receipt: Decodable {
        let itemID: String
        let quantity: Int
        let coins: Int
        let inventory: [String: Int]

        enum CodingKeys: String, CodingKey {
            case itemID = "item_id"
            case quantity
            case coins
            case inventory
        }
    }

    func register(email: String, password: String, displayName: String, deviceID: String) async throws -> Session {
        try await post("v1/auth/register", body: [
            "email": email,
            "password": password,
            "display_name": displayName,
            "device_id": deviceID
        ])
    }

    func login(email: String, password: String, deviceID: String) async throws -> Session {
        try await post("v1/auth/login", body: [
            "email": email,
            "password": password,
            "device_id": deviceID
        ])
    }

    func catalog(token: String) async throws -> Catalog {
        try await request("v1/shop/catalog", method: "GET", token: token)
    }

    func purchase(token: String, itemID: String, quantity: Int = 1) async throws -> Receipt {
        try await post("v1/shop/purchase", body: ["item_id": itemID, "quantity": quantity], token: token)
    }

    func post<T: Decodable>(_ path: String, body: [String: Any], token: String? = nil) async throws -> T {
        try await request(path, method: "POST", body: body, token: token)
    }

    private func request<T: Decodable>(_ path: String, method: String, body: [String: Any]? = nil, token: String? = nil) async throws -> T {
        var request = URLRequest(url: baseURL.appendingPathComponent(path))
        request.httpMethod = method
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        if let token {
            request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }
        if let body {
            request.httpBody = try JSONSerialization.data(withJSONObject: body)
        }
        let (data, response) = try await URLSession.shared.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse else {
            throw APIError.invalidResponse
        }
        guard (200..<300).contains(httpResponse.statusCode) else {
            throw APIError.status(httpResponse.statusCode, String(data: data, encoding: .utf8) ?? "")
        }
        do {
            return try JSONDecoder().decode(T.self, from: data)
        } catch {
            throw APIError.status(httpResponse.statusCode, "decode failed: \(error)")
        }
    }
}

private struct GameMessage: Encodable {
    let version: Int
    let type: String
    let sessionID: String
    let playerID: String
    var targetID: String?
    var itemID: String?
    var attachmentID: String?
    var weaponID: String?
    var teamID: String?
    var seq: UInt64?
    var dx: Double?
    var dy: Double?

    enum CodingKeys: String, CodingKey {
        case version, type
        case sessionID = "session_id"
        case playerID = "player_id"
        case targetID = "target_id"
        case itemID = "item_id"
        case attachmentID = "attachment_id"
        case weaponID = "weapon_id"
        case teamID = "team_id"
        case seq, dx, dy
    }
}

private struct GamePlayer: Decodable, Identifiable {
    let id: String
    let x: Double
    let y: Double
    let health: Int
    let alive: Bool
    let connected: Bool
    let knocked: Bool
    let spectator: Bool
    let armor: Int
    let teamID: String
    let weaponID: String
    let attachments: [String]
    let ammo: Int
    let reloading: Bool
    let eliminations: Int
    let grenades: Int
    let flightState: String
    let altitude: Double
    let vehicleID: String
    let inventory: [String: Int]

    enum CodingKeys: String, CodingKey {
        case id, x, y, health, alive, connected, knocked, spectator, armor
        case teamID = "team_id"
        case weaponID = "weapon_id"
        case attachments, ammo, reloading, eliminations, grenades
        case flightState = "flight_state"
        case altitude
        case vehicleID = "vehicle_id"
        case inventory
    }
}

private struct GameLoot: Decodable, Identifiable {
    let id: String
    let kind: String
    let x: Double
    let y: Double
}

private struct GameSupplyDrop: Decodable, Identifiable {
    let id: String
    let x: Double
    let y: Double
    let opened: Bool
}

private struct GameVehicle: Decodable, Identifiable {
    let id: String
    let x: Double
    let y: Double
    let occupied: Bool
}

private struct GameServerMessage: Decodable {
    let version: Int
    let type: String
    let sessionID: String?
    let playerID: String?
    let tick: UInt64?
    let status: String?
    let reason: String?
    let winnerID: String?
    let error: String?
    let zoneRadius: Double?
    let phase: String?
    let aircraftX: Double?
    let aircraftY: Double?
    let players: [GamePlayer]?
    let loot: [GameLoot]?
    let supplyDrops: [GameSupplyDrop]?
    let vehicles: [GameVehicle]?

    enum CodingKeys: String, CodingKey {
        case version, type
        case sessionID = "session_id"
        case playerID = "player_id"
        case tick, status, reason
        case winnerID = "winner_id"
        case error
        case zoneRadius = "zone_radius"
        case phase
        case aircraftX = "aircraft_x"
        case aircraftY = "aircraft_y"
        case players, loot
        case supplyDrops = "supply_drops"
        case vehicles
    }
}

private final class UDPGameClient {
    var onReady: (() -> Void)?
    var onMessage: ((GameServerMessage) -> Void)?
    var onFailure: ((String) -> Void)?

    private let queue = DispatchQueue(label: "net.roslegacy.testclient.udp")
    private var connection: NWConnection?

    func connect(host: String, port: UInt16) {
        guard let gamePort = NWEndpoint.Port(rawValue: port) else {
            onFailure?("invalid game port")
            return
        }
        let connection = NWConnection(host: NWEndpoint.Host(host), port: gamePort, using: .udp)
        self.connection = connection
        connection.stateUpdateHandler = { [weak self] state in
            switch state {
            case .ready:
                self?.receive()
                self?.onReady?()
            case let .failed(error):
                self?.onFailure?(error.localizedDescription)
            case .cancelled:
                break
            default:
                break
            }
        }
        connection.start(queue: queue)
    }

    func send(_ message: GameMessage) {
        guard let data = try? JSONEncoder().encode(message) else { return }
        connection?.send(content: data, completion: .contentProcessed { [weak self] error in
            if let error {
                self?.onFailure?(error.localizedDescription)
            }
        })
    }

    func close() {
        connection?.cancel()
        connection = nil
    }

    private func receive() {
        connection?.receiveMessage { [weak self] data, _, _, error in
            if let error {
                self?.onFailure?(error.localizedDescription)
            } else if let data, let message = try? JSONDecoder().decode(GameServerMessage.self, from: data) {
                DispatchQueue.main.async {
                    self?.onMessage?(message)
                }
            }
            if error == nil {
                self?.receive()
            }
        }
    }
}

@MainActor
private final class GameModel: ObservableObject {
    @Published var status = "Ready"
    @Published var gameStatus = "Not connected"
    @Published var serverHost: String
    @Published var apiPortText: String
    @Published var gamePortText: String
    @Published var serverSettingsStatus: String
    @Published var shop: APIClient.Catalog?
    @Published var inventory: [String: Int] = [:]
    @Published var gameMessage: GameServerMessage?
    @Published var isPlaying = false
    @Published var busy = false

    private var api: APIClient
    private var gameHost: String
    private var gamePort: UInt16
    private var session: APIClient.Session?
    private var udp: UDPGameClient?
    private var sequence: UInt64 = 0

    init() {
        let arguments = CommandLine.arguments
        let storedHost = UserDefaults.standard.string(forKey: "ros.serverHost") ?? "127.0.0.1"
        let storedAPIPort = UserDefaults.standard.integer(forKey: "ros.apiPort")
        let storedGamePort = UserDefaults.standard.integer(forKey: "ros.gamePort")
        let commandLineAPIURL = Self.commandLineValue("-apiBaseURL", arguments: arguments).flatMap(URL.init(string:))
        let configuredHost = Self.commandLineValue("-gameHost", arguments: arguments) ?? commandLineAPIURL?.host ?? storedHost
        let configuredAPIPort = commandLineAPIURL?.port ?? (storedAPIPort > 0 ? storedAPIPort : 18081)
        let rawGamePort = Self.commandLineValue("-gamePort", arguments: arguments).flatMap(Int.init) ?? (storedGamePort > 0 ? storedGamePort : 9000)
        let configuredGamePort = (1...65535).contains(rawGamePort) ? UInt16(rawGamePort) : 9000

        serverHost = configuredHost
        apiPortText = String(configuredAPIPort)
        gamePortText = String(configuredGamePort)
        serverSettingsStatus = "For an iPhone, use your Mac's LAN IP address."
        api = APIClient(baseURL: commandLineAPIURL ?? Self.makeAPIURL(host: configuredHost, port: configuredAPIPort))
        gameHost = configuredHost
        gamePort = configuredGamePort
    }

    func saveServerSettings() {
        let host = serverHost.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !host.isEmpty, !host.contains("://"), !host.contains("/") else {
            serverSettingsStatus = "Enter a host or IP address, not a URL."
            return
        }
        guard let apiPort = Self.validPort(apiPortText), let gamePort = Self.validPort(gamePortText) else {
            serverSettingsStatus = "Ports must be numbers from 1 to 65535."
            return
        }

        self.serverHost = host
        self.apiPortText = String(apiPort)
        self.gamePortText = String(gamePort)
        self.api = APIClient(baseURL: Self.makeAPIURL(host: host, port: apiPort))
        self.gameHost = host
        self.gamePort = UInt16(gamePort)
        UserDefaults.standard.set(host, forKey: "ros.serverHost")
        UserDefaults.standard.set(apiPort, forKey: "ros.apiPort")
        UserDefaults.standard.set(gamePort, forKey: "ros.gamePort")
        serverSettingsStatus = "Saved. HTTP (host):(apiPort) • UDP (host):(gamePort)"
        status = "Server configured: (host)"
    }

    private static func commandLineValue(_ name: String, arguments: [String]) -> String? {
        guard let index = arguments.firstIndex(of: name), arguments.indices.contains(index + 1) else { return nil }
        return arguments[index + 1]
    }

    private static func validPort(_ text: String) -> Int? {
        guard let port = Int(text), (1...65535).contains(port) else { return nil }
        return port
    }

    private static func makeAPIURL(host: String, port: Int) -> URL {
        var components = URLComponents()
        components.scheme = "http"
        components.host = host
        components.port = port
        return components.url ?? URL(string: "http://127.0.0.1:18081")!
    }

    func prepareAccount() async {
        do {
            if session == nil {
                let suffix = UUID().uuidString.lowercased()
                session = try await api.register(email: "player-\(suffix)@example.com", password: "simulator-password-123!", displayName: "Simulator Player", deviceID: "ios-simulator-playable")
            }
            guard let token = session?.accessToken else { throw APIClient.APIError.missingField("access_token") }
            shop = try await api.catalog(token: token)
            status = "Ready: \(shop?.coins ?? 0) coins"
        } catch {
            status = "FAIL: \(error)"
        }
    }

    func buy(itemID: String) async {
        busy = true
        defer { busy = false }
        do {
            if session == nil { await prepareAccount() }
            guard let token = session?.accessToken else { throw APIClient.APIError.missingField("access_token") }
            let receipt = try await api.purchase(token: token, itemID: itemID)
            inventory.merge(receipt.inventory) { _, new in new }
            shop?.coins = receipt.coins
            status = "Bought \(itemID) x\(receipt.quantity); \(receipt.coins) coins left"
        } catch {
            status = "FAIL: purchase \(error)"
        }
    }

    func runAPIFlow() async {
        busy = true
        defer { busy = false }
        do {
            let suffix = UUID().uuidString.lowercased()
            let email = "simulator-\(suffix)@example.com"
            let password = "simulator-password-123!"
            let registered = try await api.register(email: email, password: password, displayName: "Simulator Player", deviceID: "ios-simulator")
            let login = try await api.login(email: email, password: password, deviceID: "ios-simulator-login")
            guard registered.accessToken != login.accessToken else { throw APIClient.APIError.invalidResponse }
            let _: [String: Any] = try await rawGet("v1/profile", token: login.accessToken)
            let _: [String: Any] = try await rawGet("v1/stats", token: login.accessToken)
            let _: [String: Any] = try await rawPost("v1/lobbies", body: ["mode": "duo", "region": "simulator"], token: login.accessToken)
            let member = try await api.register(email: "simulator-member-\(UUID().uuidString.lowercased())@example.com", password: password, displayName: "Simulator Member", deviceID: "ios-simulator-member")
            let _: [String: Any] = try await rawPost("v1/matchmaking/queue", body: ["mode": "duo", "region": "simulator"], token: login.accessToken)
            let _: [String: Any] = try await rawPost("v1/matchmaking/queue", body: ["mode": "duo", "region": "simulator"], token: member.accessToken)
            let _: [String: Any] = try await rawPost("v1/matchmaking/match", body: ["mode": "duo", "region": "simulator", "size": 2], token: login.accessToken)
            status = "PASS: registration/login/profile/lobby/matchmaking"
        } catch {
            status = "FAIL: \(error)"
        }
    }

    func enterGame() async {
        busy = true
        defer { busy = false }
        do {
            if session == nil { await prepareAccount() }
            guard let session else { throw APIClient.APIError.missingField("session") }
            let sessionID = "ios-sim-\(UUID().uuidString.lowercased())"
            let client = UDPGameClient()
            udp = client
            client.onReady = { [weak self] in
                self?.send(type: "join", sessionID: sessionID, playerID: session.accountID, weaponID: "rifle", teamID: "simulator")
            }
            client.onMessage = { [weak self] message in
                Task { @MainActor in self?.handle(message) }
            }
            client.onFailure = { [weak self] error in
                Task { @MainActor in
                    self?.gameStatus = "ERROR: \(error)"
                    self?.isPlaying = false
                }
            }
            gameStatus = "CONNECTING to \(gameHost):\(gamePort)…"
            client.connect(host: gameHost, port: gamePort)
        } catch {
            gameStatus = "ERROR: \(error)"
        }
    }

    func move(dx: Double, dy: Double) {
        guard let session else { return }
        for step in 0..<8 {
            DispatchQueue.main.asyncAfter(deadline: .now() + Double(step) * 0.08) { [weak self] in
                guard let self else { return }
                self.send(type: "input", sessionID: self.gameMessage?.sessionID ?? "", playerID: session.accountID, dx: dx, dy: dy)
            }
        }
    }

    func collectNearestLoot() {
        guard let session, let player = localPlayer, let loot = nearestLoot(for: player) else { return }
        Task { @MainActor in
            for _ in 0..<45 {
                guard let current = self.localPlayer else { return }
                let distance = hypot(loot.x - current.x, loot.y - current.y)
                if distance <= 7.5 {
                    self.send(type: "pickup", sessionID: self.gameMessage?.sessionID ?? "", playerID: session.accountID, itemID: loot.id)
                    self.status = "Picked up \(loot.kind)"
                    return
                }
                let length = max(distance, 0.001)
                self.send(type: "input", sessionID: self.gameMessage?.sessionID ?? "", playerID: session.accountID, dx: (loot.x - current.x) / length, dy: (loot.y - current.y) / length)
                try? await Task.sleep(nanoseconds: 120_000_000)
            }
            self.status = "Loot was outside the collection window"
        }
    }

    func reload() {
        guard let session else { return }
        send(type: "reload", sessionID: gameMessage?.sessionID ?? "", playerID: session.accountID)
    }

    func leaveGame() {
        guard let session else { return }
        send(type: "leave", sessionID: gameMessage?.sessionID ?? "", playerID: session.accountID)
        udp?.close()
        udp = nil
        isPlaying = false
        gameStatus = "Disconnected"
    }

    private var localPlayer: GamePlayer? {
        guard let session else { return nil }
        return gameMessage?.players?.first(where: { $0.id == session.accountID })
    }

    private func nearestLoot(for player: GamePlayer) -> GameLoot? {
        gameMessage?.loot?.min { lhs, rhs in
            hypot(lhs.x - player.x, lhs.y - player.y) < hypot(rhs.x - player.x, rhs.y - player.y)
        }
    }

    private func handle(_ message: GameServerMessage) {
        gameMessage = message
        switch message.type {
        case "welcome":
            isPlaying = true
            gameStatus = "LIVE: \(message.phase ?? message.status ?? "staging")"
        case "snapshot":
            isPlaying = true
            gameStatus = "LIVE: \(message.phase ?? message.status ?? "running") tick \(message.tick ?? 0)"
            if let player = localPlayer { inventory = player.inventory }
        case "match_complete":
            isPlaying = false
            gameStatus = "COMPLETE: \(message.reason ?? "finished")"
            udp?.close()
            udp = nil
        case "error":
            gameStatus = "ERROR: \(message.error ?? "server rejected request")"
        default:
            break
        }
    }

    private func send(type: String, sessionID: String, playerID: String, targetID: String? = nil, itemID: String? = nil, weaponID: String? = nil, teamID: String? = nil, dx: Double? = nil, dy: Double? = nil) {
        sequence += 1
        udp?.send(GameMessage(version: 1, type: type, sessionID: sessionID, playerID: playerID, targetID: targetID, itemID: itemID, attachmentID: nil, weaponID: weaponID, teamID: teamID, seq: sequence, dx: dx, dy: dy))
    }

    private func rawGet(_ path: String, token: String) async throws -> [String: Any] {
        var request = URLRequest(url: api.baseURL.appendingPathComponent(path))
        request.httpMethod = "GET"
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        let (data, response) = try await URLSession.shared.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse, (200..<300).contains(httpResponse.statusCode) else { throw APIClient.APIError.invalidResponse }
        guard let object = try JSONSerialization.jsonObject(with: data) as? [String: Any] else { throw APIClient.APIError.invalidResponse }
        return object
    }

    private func rawPost(_ path: String, body: [String: Any], token: String) async throws -> [String: Any] {
        var request = URLRequest(url: api.baseURL.appendingPathComponent(path))
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        request.httpBody = try JSONSerialization.data(withJSONObject: body)
        let (data, response) = try await URLSession.shared.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse, (200..<300).contains(httpResponse.statusCode) else { throw APIClient.APIError.invalidResponse }
        guard let object = try JSONSerialization.jsonObject(with: data) as? [String: Any] else { throw APIClient.APIError.invalidResponse }
        return object
    }
}

struct ContentView: View {
    @StateObject private var model = GameModel()

    var body: some View {
        ScrollView {
            VStack(spacing: 14) {
                Text("ROS-style independent game")
                    .font(.headline)
                    .accessibilityIdentifier("appTitle")
                Text(model.status)
                    .multilineTextAlignment(.center)
                    .accessibilityIdentifier("statusLabel")

                GroupBox("Local server") {
                    VStack(alignment: .leading, spacing: 8) {
                        Text("Connect the iPhone to the same Wi-Fi as your Mac, then enter the Mac's LAN IP.")
                            .font(.caption)
                        TextField("Mac IP or hostname", text: $model.serverHost)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                            .textFieldStyle(.roundedBorder)
                            .accessibilityIdentifier("serverHostField")
                        HStack {
                            TextField("API port", text: $model.apiPortText)
                                .keyboardType(.numberPad)
                                .textFieldStyle(.roundedBorder)
                                .accessibilityIdentifier("apiPortField")
                            TextField("Game UDP port", text: $model.gamePortText)
                                .keyboardType(.numberPad)
                                .textFieldStyle(.roundedBorder)
                                .accessibilityIdentifier("gamePortField")
                        }
                        Button("Save server settings") {
                            model.saveServerSettings()
                        }
                        .accessibilityIdentifier("saveServerSettingsButton")
                        Text(model.serverSettingsStatus)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .accessibilityIdentifier("serverSettingsStatusLabel")
                    }
                }

                Button(model.busy ? "Working…" : "Run API flow") {
                    Task { await model.runAPIFlow() }
                }
                .disabled(model.busy)
                .accessibilityIdentifier("runFlowButton")

                Divider()
                Text("Store")
                    .font(.title3)
                if let shop = model.shop {
                    Text("Coins: \(shop.coins)")
                        .accessibilityIdentifier("coinsLabel")
                    ForEach(shop.items) { item in
                        VStack(spacing: 4) {
                            Text("\(item.displayName) — \(item.price) coins")
                            Text(item.description)
                                .font(.caption)
                                .multilineTextAlignment(.center)
                            Button("Buy \(item.displayName)") {
                                Task { await model.buy(itemID: item.itemID) }
                            }
                            .disabled(model.busy || shop.coins < item.price)
                            .accessibilityIdentifier(item.itemID == "starter_medkit" ? "buyStarterItemButton" : "buy_\(item.itemID)")
                        }
                        .padding(8)
                        .background(.thinMaterial)
                        .cornerRadius(8)
                    }
                } else {
                    Button("Connect account and load store") {
                        Task { await model.prepareAccount() }
                    }
                    .accessibilityIdentifier("loadStoreButton")
                }

                Button("Enter match") {
                    Task { await model.enterGame() }
                }
                .disabled(model.busy || model.isPlaying)
                .accessibilityIdentifier("enterGameButton")

                Text(model.gameStatus)
                    .multilineTextAlignment(.center)
                    .accessibilityIdentifier("gameStatusLabel")

                if model.isPlaying, let message = model.gameMessage {
                    GameBoard(message: message, model: model)
                }
            }
            .padding(20)
        }
    }
}

private struct GameBoard: View {
    let message: GameServerMessage
    @ObservedObject var model: GameModel

    var body: some View {
        VStack(spacing: 10) {
            Text("Match: \(message.phase ?? message.status ?? "running")")
                .font(.headline)
            Canvas { context, size in
                context.fill(Path(CGRect(origin: .zero, size: size)), with: .color(.green.opacity(0.15)))
                for player in message.players ?? [] {
                    let point = map(x: player.x, y: player.y, size: size)
                    context.fill(Path(ellipseIn: CGRect(x: point.x - 7, y: point.y - 7, width: 14, height: 14)), with: .color(player.id == message.playerID ? .blue : .red))
                }
                for loot in message.loot ?? [] {
                    let point = map(x: loot.x, y: loot.y, size: size)
                    context.fill(Path(CGRect(x: point.x - 4, y: point.y - 4, width: 8, height: 8)), with: .color(.yellow))
                }
            }
            .frame(height: 220)
            .border(.gray)
            .accessibilityIdentifier("gameBoard")

            HStack {
                Text("HP \(model.gameMessage?.players?.first(where: { $0.id == model.gameMessage?.playerID })?.health ?? 0)")
                Text("Loot \(message.loot?.count ?? 0)")
                Text("Zone \(Int(message.zoneRadius ?? 0))")
            }
            HStack {
                Button("↑") { model.move(dx: 0, dy: 1) }.accessibilityIdentifier("moveUpButton")
                Button("↓") { model.move(dx: 0, dy: -1) }.accessibilityIdentifier("moveDownButton")
                Button("←") { model.move(dx: -1, dy: 0) }.accessibilityIdentifier("moveLeftButton")
                Button("→") { model.move(dx: 1, dy: 0) }.accessibilityIdentifier("moveRightButton")
            }
            HStack {
                Button("Collect nearest loot") { model.collectNearestLoot() }.accessibilityIdentifier("pickupButton")
                Button("Reload") { model.reload() }.accessibilityIdentifier("reloadButton")
                Button("Leave") { model.leaveGame() }.accessibilityIdentifier("disconnectGameButton")
            }
            if !model.inventory.isEmpty {
                Text("Inventory: \(model.inventory.map { "\($0.key)=\($0.value)" }.sorted().joined(separator: ", "))")
                    .font(.caption)
                    .accessibilityIdentifier("inventoryLabel")
            }
        }
    }

    private func map(x: Double, y: Double, size: CGSize) -> CGPoint {
        let normalizedX = max(0, min(1, (x + 100) / 200))
        let normalizedY = max(0, min(1, (100 - y) / 200))
        return CGPoint(x: normalizedX * size.width, y: normalizedY * size.height)
    }
}
