# Oka Portfolio API

Standalone Go backend for Sudoku solving, random cat GIFs, and a two-account IM chat demo. The frontend lives in the sibling `../oka-portfolio` repository. This service uses SQLite and WebSockets and needs no external broker.

## Start

Requires Go 1.25+. Dependencies are pinned in `go.mod` and `go.sum`.

```sh
go run ./cmd/api
```

The API listens on `:8081`; the SQLite database defaults to `data/chat.db`. In another terminal, run `npm run dev` in the frontend repository and open http://127.0.0.1:4173/projects.html#chat-section. Both demo accounts sign in automatically.

Configuration is read from environment variables; `.env.example` documents them but is not automatically loaded:

| Variable | Default |
| --- | --- |
| `HTTP_ADDR` | `:8081` |
| `DATABASE_PATH` | `data/chat.db` |
| `CORS_ORIGINS` | localhost and 127.0.0.1 on ports 4173 and 4174 |

Use an explicit loopback address for local-only API access. Add the exact production frontend origin before deployment; WebSocket requests require an allowed Origin header. CORS is not a substitute for chat authentication.

## Structure

```text
cmd/api/                     process configuration and graceful shutdown
internal/httpapi/            shared HTTP routing, limits and CORS
internal/chat/               sessions, transactional commands, WebSocket hub
internal/chat/migrations/    embedded SQLite schema, applied at startup
internal/sudoku/             validated, cancellable Go solver
internal/cats/               bounded CATAAS client
docs/chat-protocol.md         session, command and event contract
```

Chat mutations are committed to SQLite before snapshots or acknowledgments are sent. Internal events are dispatched to room subscribers with viewer-specific stars, deletions, and reply previews. Each socket has a bounded queue; slow or disconnected clients reconnect and obtain an authoritative snapshot. One service process owns the database and in-memory room hub; do not run multiple replicas against this design without a shared event transport and suitable database architecture.

## APIs

- `GET /healthz`: process health, `{"status":"ok"}`.
- `POST /api/v1/sudoku/solve`: `{"board":[[...],...]}` with nine rows of nine integers, 0–9; zero means empty. Returns the first solution as `{"board":[[...],...]}`. Invalid or unsolvable inputs return 422. Requests are limited to 4 KiB and two seconds of solving.
- `GET /api/v1/cats/gif`: random GIF bytes through [CATAAS](https://cataas.com/cat/gif). Provider calls have a 12-second timeout, 10 MiB bound, GIF signature checking, and redirects disabled. Provider failures return 502 and should offer retry.
- `POST /api/v1/chat/demos`: create an isolated room and recovery credential.
- `POST /api/v1/chat/sessions`: authenticate one demo identity using its room recovery credential.
- `GET /api/v1/chat/ws`: WebSocket transport; authenticate in the first frame.

See [the chat protocol](docs/chat-protocol.md) for examples and action semantics. Chat credentials are capabilities granting access to demo data, not production user accounts. The frontend stores the room recovery credential locally and holds separate account session tokens in memory. Tokens and recovery credentials are stored hashed in SQLite and must never be logged.

The two pre-registered identities are Demo A (`a`) and Demo B (`b`). A room's recovery capability permits signing in as either identity for that room only; no public password or registration flow is needed. Never put real private conversations into this public portfolio demo.

## Persistence and limits

Rooms expire after 24 hours without authenticated login or durable message activity. Typing and WebSocket heartbeats do not extend retention. Startup and minute-by-minute cleanup cascade-delete expired rooms, messages, preferences, pins, and sessions. Active sockets stop working on expiry and the frontend offers a new demo.

Defaults: 1,000 rooms; 500 messages per room; 2,000 Unicode characters per message; 100 connections including authentication handshakes; 16 queued events per socket; 40 commands per second per socket; 10 new rooms per minute per direct peer address; 8 retained sessions per account per room. Reverse proxies share the new-room limit unless deployed with a separately designed trusted client-address policy. HTTP solver/provider work has a separate 16-request concurrency bound. Use ingress TLS and rate limiting before public deployment.

## Checks

```sh
go test -race ./...
go vet ./...
CGO_ENABLED=0 go build -o bin/api ./cmd/api
```

Tests cover the original Sudoku puzzle, invalid/unsolvable inputs and cancellation, cat provider errors/size limits, SQLite persistence/expiry, concurrent deduplication, permissions, private visibility, receipts, and two real authenticated WebSocket clients. Network tests need permission to bind ephemeral localhost ports. With both services running, `API_SMOKE_URL=http://127.0.0.1:4173 go test ./integration -v` also checks the frontend HTTP/WebSocket proxy.

## Docker

```sh
docker build -t oka-portfolio-api .
docker volume create oka-chat-data
docker run --rm -p 127.0.0.1:8081:8081 -v oka-chat-data:/data oka-portfolio-api
```

The multi-stage image runs as a non-root user with CA certificates and a writable `/data` directory. The frontend's Compose file builds this sibling repository and provides a named SQLite volume. Back up the database using SQLite-aware tooling; do not copy an active database file without its WAL/checkpoint considerations.

## Provenance

Sudoku adapts the local `../sudoku-solver/main.go` backtracking logic with validation, minimum-candidate selection, and cancellation. That repository is unchanged. CATAAS replaces the lost cat GIF repository; its original implementation was not recovered. Chat is a fresh portfolio implementation and does not copy company application code.
