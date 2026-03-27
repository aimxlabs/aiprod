# aiprod

**An AI-native productivity suite.** Think Google Workspace, but with no human UI — built from the ground up for AI agents.

aiprod is a self-hosted, single-binary server that gives AI agents a complete operating environment: email, documents, spreadsheets, files, tasks, inter-agent messaging, persistent memory, execution tracing, planning, a knowledge graph, and local LLM inference — all accessible through a REST API and CLI.

Everything runs on SQLite. Everything is inspectable. One binary, zero cloud dependencies.

---

## Why This Exists

AI agents need more than an LLM and a prompt. They need infrastructure:

- **Somewhere to remember things** across sessions and conversations
- **Somewhere to plan** and break work into steps
- **Somewhere to communicate** with other agents
- **Somewhere to store and retrieve** documents, data, and files
- **Somewhere to observe** what happened and learn from failures
- **Budgets and guardrails** so agents don't run unchecked

Most productivity tools are designed for humans clicking buttons. aiprod is designed for agents making API calls. There's no web dashboard, no drag-and-drop, no WYSIWYG editor. Just clean JSON APIs that an agent can reason about.

---

## What's Included

### Core Suite

| Module | What It Does |
|--------|-------------|
| **Email** | Full email with threading, labels, full-text search. Standalone SMTP or [mailr](https://github.com/aimxlabs/mailr) relay for production |
| **Documents** | Markdown docs with version history, stored on the filesystem |
| **Tables** | Dynamic SQLite tables — create schemas on the fly, import CSV, run SQL |
| **Files** | Content-addressed storage with SHA-256 deduplication |
| **Tasks** | State machine (open/in_progress/review/done/blocked/cancelled), dependencies, events |
| **Auth** | Agent identities, scoped API keys, audit logging |
| **Search** | Unified full-text search across docs and email |

### Cognitive Layer

Infrastructure for agents to think, plan, coordinate, and learn.

| Module | What It Does |
|--------|-------------|
| **Memory** | Persistent long-term memory with importance scoring, namespaces, FTS. Ephemeral scratchpad with TTL. Context compression tracking. |
| **Observe** | Execution traces with steps, timing, token counts, and costs. Replay snapshots. Failure pattern detection. |
| **Tools** | Registry of available tools with schemas. Execution logging. Dry-run simulations with approval workflows. |
| **Governor** | Per-agent resource budgets with spend tracking and alerts. Versioned prompt library. Configurable strategies. |
| **Planner** | Hierarchical plans with steps and dependencies. Post-mortem reflections with lessons learned. |
| **Task Graph** | DAG execution engine. Define nodes and edges, query for ready-to-execute nodes. |
| **Agents** | Inter-agent messaging with inbox/channels. Coordination protocols. Behavior profiles. |
| **Knowledge** | Fact store (subject-predicate-object triples) with confidence scores. Entity graph with typed relations. Schema inference. |

### Local LLM Integration

Five LLM-powered features via [Ollama](https://ollama.com), with per-feature model configuration:

| Feature | What It Does |
|---------|-------------|
| **Compress** | Summarize text while preserving key information |
| **Extract Facts** | Pull structured triples from unstructured text |
| **Infer Schema** | Analyze data samples and produce a JSON schema |
| **Reflect** | Generate post-mortem analysis of execution traces |
| **Analyze Failure** | Diagnose errors and suggest fixes |

Each feature can use a different model. Configure via API or override per-request.

---

## Quick Start

### From Source

```bash
# Prerequisites: Go 1.22+, Ollama (optional, for LLM features)

git clone https://github.com/garett/aiprod.git
cd aiprod

# One-command setup: build, init, create admin agent + API key
./scripts/bootstrap.sh

# Start the server
./aiprod serve
```

### With Docker

```bash
docker compose -f docker/docker-compose.yml up --build
```

### Verify It's Running

```bash
curl http://localhost:8600/api/v1/search?q=hello
```

---

## Configuration

All configuration is via environment variables.

| Variable | Default | Description |
|----------|---------|-------------|
| `AIPROD_DATA_DIR` | `./data` | Where databases and files are stored |
| `AIPROD_HTTP_ADDR` | `:8600` | HTTP API listen address |
| `AIPROD_SMTP_ADDR` | `:2525` | SMTP server listen address |
| `AIPROD_DOMAIN` | `localhost` | Email domain |
| `AIPROD_NO_AUTH` | `0` | Set to `1` to disable auth (dev mode) |
| `AIPROD_ADMIN_KEY` | — | Admin API key override |
| `AIPROD_TLS_CERT` | — | Path to TLS certificate |
| `AIPROD_TLS_KEY` | — | Path to TLS private key |
| `AIPROD_OLLAMA_URL` | `http://localhost:11434` | Ollama API URL |
| `AIPROD_OLLAMA_MODEL` | `qwen2:7b` | Default LLM model |
| `AIPROD_MAILR_URL` | — | mailr relay URL (enables relay mode) |
| `AIPROD_MAILR_DOMAIN_ID` | — | mailr domain ID |
| `AIPROD_MAILR_AUTH_TOKEN` | — | mailr domain auth token |

---

## Authentication

Every request requires a bearer token (unless `AIPROD_NO_AUTH=1`).

```bash
# Create an agent identity
./aiprod auth create-agent --name my-agent --description "My AI agent"

# Create an API key with specific scopes
./aiprod auth create-key --agent my-agent --scopes "docs:*,tasks:*,memory:*" --name "agent-key"

# Use it
curl -H "Authorization: Bearer aiprod_live_..." http://localhost:8600/api/v1/docs
```

Keys are SHA-256 hashed at rest. Scopes support wildcards (`docs:*`, `*`). Every API call is logged in the audit trail.

---

## How an Agent Uses aiprod

### Remember things across sessions

```bash
# Store a memory
curl -X POST http://localhost:8600/api/v1/memory \
  -d '{"key": "user_preference", "content": "Prefers concise responses", "namespace": "user:alice", "importance": 0.9}'

# Recall later (also increments access_count)
curl "http://localhost:8600/api/v1/memory?agent_id=agent:my-bot&namespace=user:alice"

# Ephemeral scratchpad (auto-expires)
curl -X POST http://localhost:8600/api/v1/scratchpad \
  -d '{"key": "current_task", "value": "processing invoice batch", "ttl_seconds": 300}'
```

### Plan and execute work

```bash
# Create a plan
curl -X POST http://localhost:8600/api/v1/plans \
  -d '{"name": "Migrate database", "goal": "Move from v1 to v2 schema"}'

# Add steps
curl -X POST http://localhost:8600/api/v1/plans/plan_abc123/steps \
  -d '{"name": "Backup current DB", "action": "pg_dump"}'
curl -X POST http://localhost:8600/api/v1/plans/plan_abc123/steps \
  -d '{"name": "Run migrations", "action": "migrate up", "depends_on": ["pstep_first"]}'

# Or build a full DAG for complex workflows
curl -X POST http://localhost:8600/api/v1/graphs \
  -d '{"name": "deploy-pipeline"}'
# Add nodes, edges, then query ready nodes:
curl http://localhost:8600/api/v1/graphs/dag_xyz/ready
```

### Track what happened

```bash
# Start a trace
curl -X POST http://localhost:8600/api/v1/traces \
  -d '{"name": "process-invoice", "trace_type": "task"}'

# Log steps as you go
curl -X POST http://localhost:8600/api/v1/traces/trace_abc/steps \
  -d '{"step_type": "llm_call", "name": "extract fields", "status": "ok"}'

# End it
curl -X POST http://localhost:8600/api/v1/traces/trace_abc/end \
  -d '{"status": "completed", "token_count": 1500, "cost": 0.02}'

# Ask the LLM to reflect on it
curl -X POST http://localhost:8600/api/v1/llm/reflect/trace_abc
```

### Coordinate with other agents

```bash
# Send a message
curl -X POST http://localhost:8600/api/v1/agent-messages \
  -d '{"from_agent": "agent:researcher", "to_agent": "agent:writer", "body": "Research complete, 12 sources found"}'

# Check inbox
curl "http://localhost:8600/api/v1/agent-messages/inbox?agent_id=agent:writer"
```

### Build knowledge over time

```bash
# Extract facts from text using the local LLM
curl -X POST http://localhost:8600/api/v1/llm/extract-facts \
  -d '{"text": "PostgreSQL was created by Michael Stonebraker at UC Berkeley in 1986.", "store": true}'

# Query the knowledge graph
curl "http://localhost:8600/api/v1/facts?subject=PostgreSQL"

# Build an entity graph
curl -X POST http://localhost:8600/api/v1/entities \
  -d '{"name": "PostgreSQL", "entity_type": "database"}'
curl -X POST http://localhost:8600/api/v1/entities/ent_abc/relations \
  -d '{"to_entity": "ent_xyz", "relation_type": "created_by"}'
```

### Stay within budget

```bash
# Set a daily token budget
curl -X POST http://localhost:8600/api/v1/budgets \
  -d '{"agent_id": "agent:my-bot", "resource_type": "tokens", "budget_limit": 100000, "period": "daily", "period_start": "2026-03-26T00:00:00Z", "period_end": "2026-03-27T00:00:00Z"}'

# Record spending (returns remaining balance + alert flag)
curl -X POST http://localhost:8600/api/v1/budgets/spend \
  -d '{"agent_id": "agent:my-bot", "resource_type": "tokens", "amount": 1500, "description": "invoice processing"}'
```

### Use the right model for the job

```bash
# Set a bigger model for fact extraction, keep the small one for compression
curl -X PUT http://localhost:8600/api/v1/llm/config/extract_facts \
  -d '{"model": "llama3:70b", "temperature": 0.1}'

curl -X PUT http://localhost:8600/api/v1/llm/config/compress \
  -d '{"model": "qwen2:7b"}'

# Or override per-request
curl -X POST http://localhost:8600/api/v1/llm/compress \
  -d '{"text": "...", "model": "mistral:latest"}'

# Check what's configured
curl http://localhost:8600/api/v1/llm/config
```

---

## Data Storage

Four SQLite databases, all in `AIPROD_DATA_DIR`:

| Database | Purpose | Why Separate |
|----------|---------|-------------|
| `core.db` | Auth, docs, tasks, files, memory, planner, tools, governor, agents, knowledge, LLM config | Main system state |
| `email.db` | Email messages, threads, labels | High volume, independent lifecycle |
| `tables.db` | User-created dynamic tables | Arbitrary schemas, isolated from core |
| `observe.db` | Traces, steps, snapshots, failure patterns | High write volume, can be rotated |

All databases use WAL mode for concurrent reads. Documents are stored as Markdown files on disk with version history. Email raw messages are stored as `.eml` files. Files use content-addressed storage with SHA-256.

---

## CLI Reference

```
aiprod init                         Initialize databases and directories
aiprod serve                        Start HTTP + SMTP servers

aiprod auth create-agent            Create an agent identity
aiprod auth create-key              Create a scoped API key
aiprod auth list-agents             List agents
aiprod auth list-keys               List API keys
aiprod auth revoke-key              Revoke an API key

aiprod docs create                  Create document (stdin for content)
aiprod docs read <id>               Print document markdown
aiprod docs write <id>              Update document (stdin for content)
aiprod docs list                    List documents
aiprod docs history <id>            Show version history
aiprod docs search <query>          Full-text search

aiprod email send                   Send an email
aiprod email list                   List messages
aiprod email get <id>               Get message details
aiprod email search <query>         Search messages
aiprod email label <id>             Add/remove labels

aiprod tables create <name>         Create a table
aiprod tables list                  List tables
aiprod tables insert <table>        Insert row (--data JSON)
aiprod tables query <table>         Query rows
aiprod tables sql <table> <query>   Execute SELECT

aiprod files upload <path>          Upload a file
aiprod files list                   List files
aiprod files download <id>          Download to stdout

aiprod tasks create                 Create a task
aiprod tasks list                   List tasks
aiprod tasks get <id>               Get task details
aiprod tasks transition <id> <st>   Change status
aiprod tasks comment <id> <msg>     Add comment
```

---

## API Overview

All endpoints are under `/api/v1/`. Responses use the envelope `{"ok": true, "data": ...}` or `{"ok": false, "error": {...}}`.

<details>
<summary><strong>Core Suite — 45 endpoints</strong></summary>

| Method | Path | Description |
|--------|------|-------------|
| POST | `/auth/agents` | Create agent |
| GET | `/auth/agents` | List agents |
| POST | `/auth/keys` | Create API key |
| GET | `/auth/keys` | List API keys |
| DELETE | `/auth/keys/{id}` | Revoke key |
| GET | `/auth/audit` | Query audit log |
| POST | `/docs` | Create document |
| GET | `/docs` | List documents |
| GET | `/docs/search` | Search documents |
| GET | `/docs/{id}` | Read document |
| PUT | `/docs/{id}` | Update document |
| GET | `/docs/{id}/versions` | Version history |
| GET | `/docs/{id}/versions/{v}` | Read version |
| DELETE | `/docs/{id}` | Delete document |
| POST | `/email/send` | Send email |
| GET | `/email/messages` | List messages |
| GET | `/email/messages/{id}` | Get message |
| PATCH | `/email/messages/{id}` | Update labels |
| DELETE | `/email/messages/{id}` | Delete message |
| GET | `/email/threads/{id}` | Get thread |
| GET | `/email/search` | Search email |
| POST | `/files` | Upload file |
| GET | `/files` | List files |
| GET | `/files/{id}` | Download file |
| GET | `/files/{id}/meta` | File metadata |
| PATCH | `/files/{id}/meta` | Update metadata |
| DELETE | `/files/{id}` | Delete file |
| POST | `/tasks` | Create task |
| GET | `/tasks` | List tasks |
| GET | `/tasks/{id}` | Get task |
| PATCH | `/tasks/{id}` | Update task |
| POST | `/tasks/{id}/transition` | Change status |
| POST | `/tasks/{id}/comment` | Add comment |
| GET | `/tasks/{id}/events` | Event history |
| POST | `/tasks/{id}/dependencies` | Add dependency |
| GET | `/tasks/{id}/dependencies` | Get dependencies |
| POST | `/tables` | Create table |
| GET | `/tables` | List tables |
| GET | `/tables/{name}` | Get schema |
| DELETE | `/tables/{name}` | Drop table |
| POST | `/tables/{name}/rows` | Insert row |
| GET | `/tables/{name}/rows` | Query rows |
| PATCH | `/tables/{name}/rows/{id}` | Update row |
| DELETE | `/tables/{name}/rows/{id}` | Delete row |
| POST | `/tables/{name}/query` | Run SQL SELECT |
| POST | `/tables/{name}/import` | Import CSV |
| GET | `/tables/{name}/export` | Export data |
| GET | `/search` | Unified search |

</details>

<details>
<summary><strong>Cognitive Layer — 65+ endpoints</strong></summary>

| Method | Path | Description |
|--------|------|-------------|
| POST | `/memory` | Create memory |
| GET | `/memory` | List/search memories |
| GET | `/memory/{id}` | Get memory |
| PATCH | `/memory/{id}` | Update memory |
| DELETE | `/memory/{id}` | Delete memory |
| POST | `/scratchpad` | Set scratch value |
| GET | `/scratchpad` | List scratch entries |
| GET | `/scratchpad/{id}` | Get scratch entry |
| DELETE | `/scratchpad/{id}` | Delete scratch entry |
| POST | `/scratchpad/cleanup` | Purge expired |
| POST | `/compressions` | Record compression |
| GET | `/compressions` | List compressions |
| POST | `/traces` | Start trace |
| GET | `/traces` | List traces |
| GET | `/traces/{id}` | Get trace |
| POST | `/traces/{id}/end` | End trace |
| POST | `/traces/{id}/steps` | Add step |
| GET | `/traces/{id}/steps` | List steps |
| POST | `/traces/{id}/snapshots` | Save snapshot |
| GET | `/traces/{id}/snapshots` | List snapshots |
| GET | `/traces/{id}/stats` | Agent stats |
| POST | `/failures` | Record failure pattern |
| GET | `/failures` | List failure patterns |
| POST | `/tools` | Register tool |
| GET | `/tools` | List tools |
| GET | `/tools/{id}` | Get tool |
| PATCH | `/tools/{id}` | Update tool |
| DELETE | `/tools/{id}` | Delete tool |
| POST | `/tools/{id}/execute` | Record execution |
| GET | `/tools/{id}/executions` | List executions |
| POST | `/tools/{id}/simulate` | Create simulation |
| GET | `/tools/{id}/simulations` | List simulations |
| POST | `/tools/simulations/{id}/approve` | Approve simulation |
| POST | `/budgets` | Create budget |
| GET | `/budgets` | List budgets |
| GET | `/budgets/{id}` | Get budget |
| POST | `/budgets/spend` | Record spending |
| GET | `/budgets/{id}/events` | Spending history |
| POST | `/prompts` | Create prompt version |
| GET | `/prompts` | List prompts |
| GET | `/prompts/{name}/active` | Get active version |
| GET | `/prompts/{name}/versions/{v}` | Get specific version |
| POST | `/prompts/{name}/activate/{v}` | Activate version |
| POST | `/strategies` | Create strategy |
| GET | `/strategies` | List strategies |
| GET | `/strategies/{id}` | Get strategy |
| PATCH | `/strategies/{id}` | Update strategy |
| POST | `/plans` | Create plan |
| GET | `/plans` | List plans |
| GET | `/plans/{id}` | Get plan with steps |
| PATCH | `/plans/{id}` | Update plan |
| DELETE | `/plans/{id}` | Delete plan |
| POST | `/plans/{id}/steps` | Add step |
| GET | `/plans/{id}/steps` | List steps |
| PATCH | `/plans/steps/{id}` | Update step |
| POST | `/reflections` | Create reflection |
| GET | `/reflections` | List reflections |
| GET | `/reflections/{id}` | Get reflection |
| POST | `/graphs` | Create DAG |
| GET | `/graphs` | List DAGs |
| GET | `/graphs/{id}` | Get DAG with nodes |
| PATCH | `/graphs/{id}` | Update DAG |
| DELETE | `/graphs/{id}` | Delete DAG |
| POST | `/graphs/{id}/nodes` | Add node |
| GET | `/graphs/{id}/nodes` | List nodes |
| PATCH | `/graphs/nodes/{id}` | Update node |
| POST | `/graphs/{id}/edges` | Add edge |
| GET | `/graphs/{id}/edges` | List edges |
| DELETE | `/graphs/edges/{id}` | Remove edge |
| GET | `/graphs/{id}/ready` | Ready nodes |
| POST | `/agent-messages` | Send message |
| GET | `/agent-messages` | List messages |
| GET | `/agent-messages/inbox` | Agent inbox |
| GET | `/agent-messages/{id}` | Get message |
| POST | `/agent-messages/{id}/read` | Mark read |
| POST | `/channels` | Create channel |
| GET | `/channels` | List channels |
| GET | `/channels/{id}` | Get channel |
| POST | `/protocols` | Create protocol |
| GET | `/protocols` | List protocols |
| POST | `/profiles` | Create behavior profile |
| GET | `/profiles` | List profiles |
| GET | `/profiles/active` | Active profile |
| POST | `/profiles/{id}/activate` | Activate profile |
| POST | `/facts` | Create fact |
| GET | `/facts` | List/search facts |
| GET | `/facts/{id}` | Get fact |
| PATCH | `/facts/{id}` | Update fact |
| DELETE | `/facts/{id}` | Delete fact |
| POST | `/entities` | Create entity |
| GET | `/entities` | List entities |
| GET | `/entities/{id}` | Get entity |
| PATCH | `/entities/{id}` | Update entity |
| DELETE | `/entities/{id}` | Delete entity |
| POST | `/entities/{id}/relations` | Add relation |
| GET | `/entities/{id}/relations` | List relations |
| DELETE | `/entities/relations/{id}` | Remove relation |
| POST | `/schemas` | Save schema inference |
| GET | `/schemas` | List inferences |
| GET | `/schemas/{type}/{id}` | Get inference |
| GET | `/llm/status` | LLM availability + config |
| GET | `/llm/config` | List feature configs |
| PUT | `/llm/config/{feature}` | Set model override |
| DELETE | `/llm/config/{feature}` | Remove override |
| POST | `/llm/compress` | Compress text |
| POST | `/llm/extract-facts` | Extract facts |
| POST | `/llm/infer-schema` | Infer schema |
| POST | `/llm/reflect/{traceId}` | Reflect on trace |
| POST | `/llm/analyze-failure` | Analyze failure |

</details>

---

## Email: Standalone vs Relay Mode

aiprod's email system runs in one of two modes:

### Standalone (default)

aiprod runs its own SMTP server and delivers outbound email directly via MX lookup. Good for local development and testing, but **not production-ready** — no DKIM signing, no SPF, no sender reputation.

```bash
./aiprod serve   # SMTP on :2525, direct MX delivery
```

### Relay Mode (production)

Set `AIPROD_MAILR_URL` to route email through a [mailr](https://github.com/aimxlabs/mailr) relay server. mailr handles the hard parts — inbound SMTP on port 25, outbound MX delivery with DKIM signing, retry queues, and DNS configuration. aiprod polls mailr for inbound messages and sends outbound via mailr's API.

```bash
AIPROD_MAILR_URL=https://mail.example.com \
AIPROD_MAILR_DOMAIN_ID=dom_abc123 \
AIPROD_MAILR_AUTH_TOKEN=tok_xyz789 \
./aiprod serve
```

In relay mode:
- aiprod's local SMTP server is disabled
- Outbound email is submitted to mailr's `/api/domains/:id/send` endpoint
- Inbound email is polled from mailr every 15 seconds
- Messages are stored locally in aiprod's `email.db` for threading, search, and labels
- The existing `/api/v1/email/*` endpoints work identically in both modes

Deploy mailr with one command using the `/deploy-mailr` skill in the [mailr repo](https://github.com/aimxlabs/mailr).

---

## Architecture

```
                    +-----------+
                    |  Agents   |
                    |  (Claude, |
                    |  GPT, etc)|
                    +-----+-----+
                          |
                    REST API / CLI
                          |
              +-----------+-----------+
              |       aiprod          |
              |   (single Go binary)  |
              +-----------+-----------+
              |                       |
    +---------+---------+   +---------+---------+
    |   Core Suite      |   |  Cognitive Layer  |
    |                   |   |                   |
    | Email (SMTP/mailr)|   | Memory            |
    | Documents (MD)    |   | Observe (traces)  |
    | Tables (SQL)      |   | Tools (registry)  |
    | Files (SHA-256)   |   | Governor (budgets)|
    | Tasks (FSM)       |   | Planner (plans)   |
    | Auth (API keys)   |   | Task Graph (DAG)  |
    | Search (FTS5)     |   | Agents (messages) |
    | Webhooks (hookd)  |   | Knowledge (graph) |
    +---------+---------+   | LLM (Ollama)      |
              |             +---------+---------+
    +---------+---+---------+---------+
    |             |         |         |
 core.db     email.db  tables.db  observe.db
 (SQLite)    (SQLite)  (SQLite)   (SQLite)
              |                       |
         [optional]              [optional]
          mailr                    hookd
       (mail relay)          (webhook relay)
```

**Tech stack:** Go, SQLite (pure Go via modernc.org), chi router, cobra CLI, go-smtp, Ollama.

**Optional relay services:** [mailr](https://github.com/aimxlabs/mailr) for production email (DKIM, MX delivery), [hookd](https://github.com/aimxlabs/hookd) for webhook reception.

---

## License

MIT
