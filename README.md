# Logmon

Logmon collects Linux application logs through small agents, sends them to one central server over raw TCP, stores them as dated JSONL files, and exposes them in a Bubble Tea terminal UI.

The current MVP targets fewer than 50 agents over a trusted private LAN or VPN.

## Features

- Collect regular files, systemd journal units, and explicit Docker containers.
- Run multiple configured sources concurrently on each agent.
- Preserve journald and Docker source timestamps when available.
- Authenticate agents against a server-side YAML allowlist.
- Keep one persistent TCP connection per agent with ACK and retry behavior.
- Stamp records on server receipt and append them to UTC-dated JSONL files.
- Show connected/seen agents and browse stored logs by date, app, agent, and category in a live TUI.
- Override YAML configuration with explicit Cobra flags.
- Reject unknown YAML fields and bound protocol frames and displayed records.

## Architecture

```text
file tail ---------\
journalctl ----------> bounded channel -> TCP sender -> Logmon server
docker logs --------/                         |              |
                                              | ACK          +-> dated JSONL
                                              +--------------+-> Bubble Tea TUI
```

### Design Pattern

Logmon uses a deliberately concrete pipeline rather than a framework of collector interfaces:

- **Producer-consumer:** one goroutine per configured source produces records into one bounded channel; one sender consumes them in order.
- **Fan-in:** file, journald, and Docker sources converge on the same protocol record stream.
- **Connection-per-agent:** each authenticated agent owns one persistent TCP connection; the server handles connections concurrently.
- **Request/acknowledgement:** the agent keeps one pending record until the server confirms its append.
- **Append-only storage:** accepted records become immutable JSONL entries partitioned by server receipt date.
- **Model-update-view:** Bubble Tea owns TUI state; server goroutines deliver agent and record events through `Program.Send`.
- **Layered configuration:** defaults load first, YAML loads second, and explicitly changed flags win last.

There are no collector interfaces, factories, database repositories, or service layers. Add those only when a second concrete implementation makes them useful.

### Runtime Flow

1. Agent loads defaults, optional YAML, environment token, and flag overrides.
2. Agent validates identity and sources, then starts one goroutine per source.
3. Agent connects and sends a `Hello` frame containing protocol version, identity, token, and declared apps.
4. Server hashes the supplied token and compares it with the configured allowlist entry.
5. Agent sends one NDJSON `Record` and waits for an `Ack`.
6. Server validates the record, adds trusted connection identity and UTC receipt time, then appends it.
7. Server ACKs only after the append succeeds and sends the stored record to the TUI.
8. A failed connection retains the current record in memory and reconnects after `retry_delay`.

## Project Structure

```text
cmd/
  agent/main.go             Cobra agent command and flag precedence
  server/main.go            Cobra server command and flag precedence
internal/
  agent/
    config.go               Agent YAML, source CSV parsing, validation
    agent.go                Source collection, cursors, TCP sender
  protocol/
    message.go              Bounded NDJSON wire and stored record types
  server/
    server.go               Allowlist auth, registry, TCP ingest, storage
    tui.go                  Bubble Tea model, storage scan, navigation
tmp/notes/                  Local plan and session history; Git-ignored
```

Tests live beside their packages. Runtime binaries and data are written to `bin/` and `data/`, both ignored by Git.

## Dependencies

### Runtime Libraries

| Library | Purpose |
|---|---|
| [`github.com/charmbracelet/bubbletea`](https://github.com/charmbracelet/bubbletea) | Server terminal UI |
| [`github.com/nxadm/tail`](https://github.com/nxadm/tail) | Follow regular files and rotation |
| [`github.com/spf13/cobra`](https://github.com/spf13/cobra) | Agent and server flags/help |
| [`gopkg.in/yaml.v3`](https://pkg.go.dev/gopkg.in/yaml.v3) | Strict YAML configuration decoding |

TCP, JSON, process execution, hashing, signals, concurrency, and rooted filesystem access use the Go standard library. Exact direct and transitive versions are pinned by `go.mod` and `go.sum`.

### Host Tools

| Tool | Required when |
|---|---|
| Go `1.25.7` | Building or developing |
| `journalctl` | A journald source is configured |
| Docker CLI | A Docker source is configured |
| `golangci-lint` | Running `make lint` or pre-commit hooks |
| Lefthook | Installing/running repository pre-commit hooks |
| OpenSSL or another secure generator | Creating agent tokens |
| `tmux` or equivalent PTY supervisor | Running the current always-interactive server unattended |

## Configuration

Unknown YAML keys fail startup. Agent IDs and app names accept letters, numbers, `.`, `_`, and `-`; tokens must contain at least 32 characters.

### Authentication Token

Generate a high-entropy token and its server-side SHA-256 hash:

**Bash:**
```bash
TOKEN="$(openssl rand -hex 32)"
echo "TOKEN: $TOKEN"
echo "SHA256: $(printf '%s' "$TOKEN" | sha256sum | cut -d' ' -f1)"
```

**Fish:**
```fish
set -x TOKEN (openssl rand -hex 32)
echo "TOKEN: $TOKEN"
echo "SHA256: "(printf '%s' "$TOKEN" | sha256sum | cut -d' ' -f1)
```

Put the 64-character hash in `server.yaml`. Put the original token in protected agent YAML or `LOGMON_AGENT_TOKEN`. The environment variable is used only when YAML does not contain `token`.

Do not pass secrets as command-line flags; command lines may be visible to other users.

### Server YAML

```yaml
listen: 10.0.0.5:9000
data_dir: /var/lib/logmon
max_connections: 100
agents:
  prod-1:
    token_sha256: "<64-character-sha256>"
  prod-2:
    token_sha256: "<64-character-sha256>"
```

| Field | Required | Default | Meaning |
|---|---|---|---|
| `listen` | No | `127.0.0.1:9000` | TCP bind address |
| `data_dir` | No | `data` | JSONL storage root |
| `max_connections` | No | `100` | Maximum simultaneous accepted connections |
| `agents` | Yes | None | Allowed agent IDs and token hashes |

Relative `data_dir` values resolve from the server config file directory. The server is not useful without an allowlist, so production runs should always pass `--config`.

### Agent YAML

```yaml
id: prod-1
hostname: api-01
server: 10.0.0.5:9000
token: "<original-random-token>"
retry: true
retry_delay: 2s
sources:
  - type: file
    path: /var/log/nginx/access.log
    app: nginx
    category: access
    from_beginning: false

  - type: journald
    unit: billing.service
    app: billing
    category: service
    from_beginning: false

  - type: docker
    container: billing-api
    app: billing
    category: container
    from_beginning: false
```

| Field | Required | Default | Meaning |
|---|---|---|---|
| `id` | Yes | None | Stable allowlisted agent ID |
| `hostname` | No | `os.Hostname()` | Host label stored with records |
| `server` | No | `127.0.0.1:9000` | Server TCP address |
| `token` | Yes unless environment is set | `LOGMON_AGENT_TOKEN` | Authentication secret |
| `retry` | No | `true` | Restart failed sources and reconnect TCP |
| `retry_delay` | No | `2s` | Delay between attempts |
| `sources` | Yes | None | One or more source definitions |

Relative file paths resolve from the agent config file directory. CLI file paths resolve from the process working directory.

### Source Fields

| Type | Required target | Collector | `from_beginning: false` |
|---|---|---|---|
| `file` | `path` | `github.com/nxadm/tail` | Seek to current file end |
| `journald` | `unit` | `journalctl --follow --output=json` | Start with `--lines=0` |
| `docker` | `container` | `docker logs --follow --timestamps` | Start with `--tail=0` |

Every source also requires `app` and `category`. `from_beginning` defaults to `false`.

The file collector follows rename-based rotation. Journald retries from its latest in-memory cursor. Docker retries from its latest in-memory timestamp. These cursors do not survive agent restart.

### Flags and Precedence

Show all flags:

**Bash:**
```bash
go run ./cmd/agent --help
go run ./cmd/server --help
```

**Fish:**
```fish
go run ./cmd/agent --help
go run ./cmd/server --help
```

Agent source flags are repeatable CSV records:

**Bash:**
```bash
LOGMON_AGENT_TOKEN="$TOKEN" go run ./cmd/agent \
  --id prod-1 \
  --server 10.0.0.5:9000 \
  --file '/var/log/nginx/access.log,nginx,access,false' \
  --journal 'billing.service,billing,service,false' \
  --docker 'billing-api,billing,container,false'
```

**Fish:**
```fish
set -x LOGMON_AGENT_TOKEN "$TOKEN"
go run ./cmd/agent \
  --id prod-1 \
  --server 10.0.0.5:9000 \
  --file '/var/log/nginx/access.log,nginx,access,false' \
  --journal 'billing.service,billing,service,false' \
  --docker 'billing-api,billing,container,false'
```

If any `--file`, `--journal`, or `--docker` flag is supplied, the complete YAML source list is replaced. Other flags override only their corresponding YAML field. Server allowlist entries have no CLI flag.

## Development

### Prerequisites

- Go `1.25.7`, matching `go.mod`.
- `golangci-lint` on `PATH` for linting.
- `journalctl` and Docker only when manually exercising those collectors.

Bare `make` prints available commands and does not install or modify tools.

### Run Locally

Start the server in one terminal:

**Bash:**
```bash
go run ./cmd/server --config server.yaml
```

**Fish:**
```fish
go run ./cmd/server --config server.yaml
```

Start an agent in another terminal:

**Bash:**
```bash
LOGMON_AGENT_TOKEN="$TOKEN" go run ./cmd/agent --config agent.yaml
```

**Fish:**
```fish
set -x LOGMON_AGENT_TOKEN "$TOKEN"
go run ./cmd/agent --config agent.yaml
```

For a file source with `from_beginning: false`, start the agent first and append a line afterward:

**Bash:**
```bash
printf '%s\n' 'test log record' >> app.log
```

**Fish:**
```fish
printf '%s\n' 'test log record' >> app.log
```

### Verify

**Bash:**
```bash
make fmt
make vet
make lint
go test ./...
go test -race ./...
make build
```

**Fish:**
```fish
make fmt
make vet
make lint
go test ./...
go test -race ./...
make build
```

Run one package or test:

**Bash:**
```bash
go test ./internal/server
go test ./internal/server -run '^TestAuthenticatedRecordIsStoredBeforeAck$'
```

**Fish:**
```fish
go test ./internal/server
go test ./internal/server -run '^TestAuthenticatedRecordIsStoredBeforeAck$'
```

Lefthook runs formatting, vet, lint, and tests serially. Formatting may modify and stage files.

## Build

Build both host-platform binaries:

**Bash:**
```bash
make build
```

**Fish:**
```fish
make build
```

Outputs:

```text
bin/agent
bin/server
```

Build Linux release binaries explicitly:

**Bash:**
```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o bin/logmon-agent ./cmd/agent
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o bin/logmon-server ./cmd/server
sha256sum bin/logmon-agent bin/logmon-server
```

**Fish:**
```fish
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o bin/logmon-agent ./cmd/agent
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o bin/logmon-server ./cmd/server
sha256sum bin/logmon-agent bin/logmon-server
```

Change `GOARCH` to `arm64` for 64-bit ARM Linux. The repository has no release automation, package builder, container image, or generated artifacts.

## Production Deployment

1. Build for the target Linux architecture and copy `logmon-agent` to each source host and `logmon-server` to the central host.
2. Install binaries under `/usr/local/bin/` and configs under `/etc/logmon/`.
3. Store server data under `/var/lib/logmon/` and set `data_dir` accordingly.
4. Protect agent config/environment files with mode `0600` and use a distinct token per agent.
5. Bind the server to its VPN/private address and firewall the port to agent addresses only.
6. Run agents as a dedicated `logmon` user with only source-specific read permissions.
7. Monitor free disk space and manage retention externally; Logmon never deletes stored files.

### Agent systemd Service

`/etc/systemd/system/logmon-agent.service`:

```ini
[Unit]
Description=Logmon agent
Wants=network-online.target
After=network-online.target

[Service]
User=logmon
SupplementaryGroups=systemd-journal docker
EnvironmentFile=/etc/logmon/agent.env
ExecStart=/usr/local/bin/logmon-agent --config /etc/logmon/agent.yaml
Restart=always
RestartSec=2
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
```

`/etc/logmon/agent.env`:

**Bash:**
```bash
LOGMON_AGENT_TOKEN=<original-random-token>
```

**Fish:**
```fish
set -x LOGMON_AGENT_TOKEN <original-random-token>
```

Use `systemd-journal` or distribution-specific `adm` membership for journal access. Add `docker` only when Docker sources are configured: Docker socket/group access is effectively root. Prefer Docker's journald logging driver when possible, then remove `docker` from `SupplementaryGroups`.

Enable the agent:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now logmon-agent
sudo systemctl status logmon-agent
```

### Server Process

The server currently always starts Bubble Tea and therefore requires a terminal. It is not yet suitable as a normal headless systemd service. For the current small trusted deployment, run it in a protected foreground terminal or PTY supervisor such as `tmux`:

**Bash:**
```bash
tmux new-session -d -s logmon-server '/usr/local/bin/logmon-server --config /etc/logmon/server.yaml'
tmux attach -t logmon-server
```

**Fish:**
```fish
tmux new-session -d -s logmon-server '/usr/local/bin/logmon-server --config /etc/logmon/server.yaml'
tmux attach -t logmon-server
```

Add a headless server mode before requiring unattended service supervision, automatic restart, or container deployment.

## Storage and TUI

The server stamps accepted records in UTC and stores them under:

```text
/var/lib/logmon/YYYY-MM-DD/<app>/<agent-id>.jsonl
```

Each line contains receipt time, optional source time, authenticated agent ID and hostname, source type, app, category, and raw log text.

| Key | Action |
|---|---|
| `left` / `right` or `h` / `l` | Select stored date/app/agent file |
| `[` / `]` | Select category |
| `up` / `down` or `k` / `j` | Select log line |
| `r` | Rescan storage |
| `q` | Quit server |

The TUI keeps at most the latest 1,000 records from the selected file in memory. It receives newly appended records live and rescans the storage tree only on startup or `r`.

## Limitations

- Raw TCP is plaintext; security currently depends on VPN encryption, firewall rules, and token authentication.
- Delivery is at-least-once. A lost ACK can duplicate a stored record.
- Pending records and source cursors live only in agent memory; agent restart can lose unsent records or alter replay behavior.
- Server agent presence lives only in memory and resets on server restart.
- There is no heartbeat. A silent connection expires after 15 minutes and reconnects when the agent next sends a record.
- Storage has no retention, compression, indexing, full-text search, or disk quota.
- TUI history is capped at 1,000 records per selected file and has no search or pagination.
- The server combines ingestion and an interactive TUI; there is no headless mode or separate viewer.
- Journald and Docker collectors depend on installed host CLIs and local permissions.
- Docker selection is explicit; there is no label discovery or container event watcher.
- No TLS, durable spool, deduplication, database, message broker, clustering, multi-server failover, Kubernetes collector, plugin system, metrics endpoint, or health endpoint exists yet.
