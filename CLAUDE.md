# aiprod — Claude Code project context

AI-native productivity suite. Self-hosted, single Go binary with email, docs, tables, files, tasks, inter-agent messaging, memory, planning, knowledge graph, and local LLM inference — all via REST API.

## Quick reference

- **Language**: Go 1.26
- **Build**: `go build ./cmd/aiprod`
- **Run**: `./aiprod serve`
- **Test**: `go test ./...`

## Architecture

```
cmd/aiprod/main.go                  CLI entrypoint
internal/
  cli/                              Cobra CLI (serve, auth, docs, email, tables, files, tasks)
  api/                              HTTP server (chi router) + all handler files
  auth/                             Agent identities, scoped API keys, audit logging
  db/                               SQLite open + migration utilities (shared by all modules)
  email/                            Email (standalone SMTP or mailr relay)
    store.go                        Message persistence, threading, FTS, labels
    smtp_server.go                  Inbound SMTP (go-smtp) — standalone mode
    smtp_client.go                  Outbound MX delivery — standalone mode
    mailr_client.go                 mailr relay client — relay mode (send via API, poll inbound)
    service.go                      Wiring factory
  webhooks/                         hookd client (WebSocket + polling for webhook events)
  docs/                             Markdown documents with versioning
  storage/                          Content-addressed file storage (SHA-256)
  tasks/                            Task state machine with dependencies
  tables/                           Dynamic SQLite tables, CSV import
  memory/                           Long-term memory, scratchpad, compression tracking
  observe/                          Execution traces, failure patterns
  tools/                            Tool registry, execution logging, simulations
  governor/                         Per-agent budgets, prompt library, strategies
  planner/                          Hierarchical plans with steps
  taskgraph/                        DAG execution engine
  agents/                           Inter-agent messaging, channels, protocols, profiles
  knowledge/                        Fact triples, entity graph, schema inference
  llm/                              Ollama integration for compression, extraction, reflection
  search/                           Unified FTS across docs + email
config/                             Config struct (env vars)
```

## Module patterns

All modules follow the same structure:

- **Store**: `type Store struct{ db *sql.DB }` with `NewStore(db) (*Store, error)`
- **Migrations**: `var migrations = []string{...}` applied via `db.Migrate(db, namespace, migrations)`
- **IDs**: `newID(prefix)` generates `prefix + 24 hex chars` (12 random bytes)
- **Timestamps**: `time.Now().UTC().Format(time.RFC3339)`
- **SQL**: Raw queries with `?` params, no ORM
- **JSON columns**: `json.Marshal` to string on write, `json.Unmarshal` on read
- **API handlers**: `func (s *Server) handleXxx(store *Module.Store) http.HandlerFunc`
- **Route registration**: `func (s *Server) RegisterXxxRoutes(r chi.Router, store *Module.Store)`

## Email modes

Email runs in one of two modes, controlled by environment variables:

- **Standalone** (default): aiprod runs its own SMTP server + direct MX delivery. No DKIM, not production-ready.
- **Relay** (`AIPROD_MAILR_URL` set): Routes through a [mailr](https://github.com/aimxlabs/mailr) server. Production-ready with DKIM signing. Polls for inbound, sends via API.

## External services

- **hookd** — webhook relay. aiprod connects as a client via `internal/webhooks/store.go`.
- **mailr** — mail relay. aiprod connects as a client via `internal/email/mailr_client.go`.
- **Ollama** — local LLM inference. Optional, used by `/llm/*` endpoints.

## Databases

Four SQLite databases in `AIPROD_DATA_DIR`:

| Database | Modules | Why separate |
|----------|---------|-------------|
| `core.db` | auth, docs, tasks, files, memory, planner, tools, governor, agents, knowledge, webhooks | Main system state |
| `email.db` | email | High volume, independent lifecycle |
| `tables.db` | tables | Arbitrary user schemas |
| `observe.db` | observe | High write volume, can be rotated |
