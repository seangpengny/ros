# Independent backend API implementation

The independent API is implemented in `cmd/api-server`, `internal/api`, and
`internal/auth`. It is a new contract and is not inferred from the ROS Legacy
IPA.

## Endpoints exercised by the test client

- `GET /healthz`, `GET /readyz`
- `GET /metrics`
- `POST /v1/auth/register`
- `POST /v1/auth/login`
- `POST /v1/auth/refresh`
- `POST /v1/auth/logout`
- `POST /v1/auth/recovery/request`
- `POST /v1/auth/recovery/complete`
- `GET /v1/auth/sessions`
- `DELETE /v1/auth/sessions/{session_id}`
- `GET /v1/profile`
- `PATCH /v1/profile`
- `GET /v1/stats`
- `GET /v1/stats/history`
- `GET /v1/leaderboards`
- `POST /v1/lobbies`
- `GET /v1/lobbies/{id}`
- `POST /v1/lobbies/{id}/join`
- `POST /v1/lobbies/{id}/ready`
- `POST /v1/lobbies/{id}/invites`
- `GET /v1/lobby-invites`
- `POST /v1/lobby-invites/{id}/accept`
- `POST /v1/lobby-invites/{id}/decline`
- `POST /v1/presence`
- `GET /v1/presence/{account_id}`
- `POST /v1/sessions`
- `GET /v1/sessions/{id}`
- `POST /v1/sessions/{id}/admit`
- Internal `POST /v1/sessions/{id}/start`
- Internal `POST /v1/sessions/{id}/heartbeat`
- Internal `POST /v1/sessions/{id}/complete`
- `POST /v1/lobbies/{id}/allocate`
- `POST /v1/matchmaking/queue`
- `DELETE /v1/matchmaking/queue/{ticket_id}`
- `POST /v1/matchmaking/match`

All protected profile, statistics, lobby, and queue endpoints require a signed
access token. Request bodies are size limited and decoded with unknown-field
rejection. PostgreSQL stores accounts, profiles, initial statistics rows, and
rotated refresh-token hashes. Production API wiring uses Redis-backed lobby and
queue adapters with bounded TTLs and an allocation lock; deterministic
in-process services remain available for unit tests. Durable ticket and party
tables exist, and the current match endpoint persists each allocation and marks
its tickets matched in one PostgreSQL transaction before returning admission
data. A separate allocation worker is not part of this synchronous baseline.

The live Phase 11 run verified this API against the Docker PostgreSQL/Redis
services and passed registration/login, lobby lifecycle, party invitations,
presence, game-session allocation/admission/heartbeat/completion, and matchmaking.
The dedicated game-server executable also contains an optional lifecycle client
for the internal start/heartbeat/complete endpoints; it is enabled only when a
session launcher supplies all four control-plane environment values documented
in the deployment guide. The API can assign sessions round-robin across the
validated ROS_GAME_SERVER_IDS pool.

The versioned HTTP contract is also published as
[`docs/openapi.yaml`](openapi.yaml); it describes the implemented `/v1` routes
and bearer-token requirements without implying ROS Legacy compatibility.
