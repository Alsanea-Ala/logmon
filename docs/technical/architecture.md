# Architecture

## What this is

A push-based log shipping system. Agents tail local sources and push NDJSON
records over plain TCP to a single-writer server, which authenticates them,
timestamps each record on receipt, and appends it to partitioned JSONL. A
terminal UI on the server reads that storage and streams it live.

Two binaries, one dependency-free shared contract.

It is not a log aggregator with a query language, a message broker, or a
database. The last section of this document exists because getting that wrong is
the expensive mistake.

## Package layering

Neither binary imports the other. `internal/protocol` imports nothing internal,
so the wire format is the only thing the two sides share — and it can change
without either side depending on the other.

```text
   cmd/agent                          cmd/server
      │                                  │
      ▼                                  ▼
 internal/agent                     internal/server
      │                                  │
      │                                  │
      ▼                                  │
 internal/protocol  ◄─────────────────────┘
      │
      └── imports nothing internal.
          Standard library only.
```

Verify it:

```bash
go list -f '{{.ImportPath}} -> {{join .Imports " "}}' ./...
```

## End to end

Three kinds of source converge on one fan-in, then one socket per agent.

```text
 /var/log/nginx/*.log     journald          docker logs
          │                   │                   │
     tail-follow         journalctl          docker logs --follow
          │                   │                   │
          └───────────────────┼───────────────────┘
                              ▼
                      ┌────────────────┐
                      │ internal/agent │
                      └────────────────┘
         goroutine/source ··· goroutine/source
                              │
                              ▼  records chan (cap 256)
                              │
                             sender
                              │  NDJSON over TCP
                              ▼
                      ┌────────────────┐
                      │internal/server │
                      └────────────────┘
```

## One connection

One goroutine per connection. The order matters: authentication completes
before a single record is accepted, and the agent identity used for storage
comes from the connection rather than from anything on the wire.

```text
  listener.Accept
        │
        ▼
  handle(conn)              one goroutine, per connection
        │
        ├─ decode Hello ────────────────┐
        │                               │  malformed JSON, unknown ID,
        │                               │  wrong version, wrong digest —
        │                               │  all four produce one reply
        │                               ▼
        │ Ack{OK:false, "authentication failed"}
        │                               │
        ├─ authenticate ◄───────────────┘   sha256(token) == stored digest
        │
        ├─ register →  agents[id] = {Connected, LastSeen, conn}
        │
        ├─ Ack{OK:true}
        │
        └─ loop, per record
             decode
             validateRecord        app, category, line size, level, source
             append                ──► storeMu ──► JSONL
             seen(id, now)         ──► LastSeen
             notify                ──► recordMsg ──► TUI
             Ack{OK:true}
```

Two details in that loop are load-bearing:

- **`register` binds the connection to the ID that authenticated it.** A record's
  `AgentID` and `Hostname` are taken from that binding, not from the record.
  An agent therefore cannot write into another agent's partition even if it
  sends records claiming to be that agent.
- **A short write is rolled back.** `append` truncates the file to its previous
  size if the write does not complete, so a torn write cannot leave a partial
  JSON line at the tail of a partition. If the rollback itself fails the file is
  marked disabled in `run.failed` and refused rather than appended to again.

## One process

Ingest and the terminal UI are the same process. That is a consequence of the
storage design, not a plan, and it is why `--no-tui` exists for unattended
service.

```text
╔════════════════════ server process ═════════════════════╗
║                                                          ║
║   ┌──────────────┐        ┌────────────────────────┐     ║
║   │ TCP accept   │        │  tea.Program(model)    │     ║
║   │  + handle()  │───────►│  Msg ─► Update ─► Model │     ║
║   └──────┬───────┘ recordMsg└──────────┬─────────────┘     ║
║          │                           │ tea.Cmd           ║
║          ▼                           ▼                   ║
║   append to JSONL            View() ──► string ──► stdout ║
║                                                          ║
╚══════════════════════════════════════════════════════════╝
```

What that costs, precisely:

- **Headless mode is not paying for the UI.** `run.program` is never set when
  `--no-tui` is passed, so `notify` hits its nil guard and sends nothing.
- **In TUI mode the send is synchronous.** `notify` hands the record to the
  program's message channel and waits for room. A UI that cannot keep up applies
  backpressure to ingest rather than silently dropping records — the failure
  mode is slower writes, not lost ones.
- **A crash in the UI is a crash in the collector.** There is no supervisor
  between them.

Shutdown is ordered: `cancel()` stops the accept loop, `listener.Close()` wakes
it, `closeConnections()` drops live agents, and `workers.Wait()` drains the
connection goroutines before `Run` returns.

## Concurrency

| Primitive | Guards | Scope |
|---|---|---|
| `sync.RWMutex` (`run.mu`) | the agent registry | reads are frequent, writes are per connect and per record |
| `sync.Mutex` (`run.storeMu`) | the storage append path | one writer at a time |
| `chan struct{}` (`run.slots`) | the connection limit | `--max-connections`, default 100 |
| `sync.WaitGroup` (`run.workers`) | connection goroutines | shutdown drain |
| `sync.WaitGroup` (agent side) | source collectors | one per source, plus the sender |

This is sized for the stated target of fewer than 50 agents on a trusted LAN.
The two places that say so in the source carry `ponytail:` comments marking them
as the first things to profile if that target moves: the single storage lock,
and opening the file per record rather than caching handles.

## The seven decisions

| # | Decision | Why |
|---|---|---|
| 1 | **Push, not pull** — agents dial out | No inbound firewall rule on every host, and the server never calls anything. |
| 2 | **Two binaries, one contract** | Agent and server deploy to different machines, and are upgraded at different times. |
| 3 | **Append-only JSONL as the system of record** | No database, no index, no query language. The partition path *is* the index. See [storage.md](storage.md). |
| 4 | **At-least-once, cursors in memory only** | A lost acknowledgement duplicates a record; an agent restart loses unsent ones. See [agent.md](agent.md). |
| 5 | **Fan-in worker pipeline in the agent** | One goroutine per source, one sender, a 256-record channel between them. One source failing cancels the agent. |
| 6 | **Single-writer server** | One accept loop, one storage lock. Scoped to the stated target rather than pretended to be general. |
| 7 | **Elm-style event loop for the UI** | Model/Update/View. Sharing the process with ingest is what forces `--no-tui`. |

## What this is not

Written out because each of these is a reasonable assumption that is false here,
and designing against one produces work that has to be thrown away.

```text
not microservices        two processes, and that is all
not event streaming      no broker; agents speak TCP straight to one server
not a log index          no database, no query language, no search across files
not TLS-terminated       plaintext, deliberately, on a trusted LAN or VPN
not horizontally scalable  one writer, one node
not a metrics pipeline    records are log lines; there are no counters to query
not remote               viewing requires a terminal on the server host
```

## How to check this document

Every claim above is settled by one of these:

```bash
# layering
go list -f '{{.ImportPath}} -> {{join .Imports " "}}' ./...

# no HTTP surface, no database, no broker
grep -rn 'http\.\|database/sql' --include='*.go' internal/ cmd/ | grep -v _test

# the storage append path
sed -n '/func (run \*runtime) append/,/^}/p' internal/server/server.go

# the connection lifecycle
sed -n '/func (run \*runtime) handle/,/^}/p' internal/server/server.go

# the agent pipeline
sed -n '/^func Run/,/^}/p' internal/agent/agent.go
```

## Where to go next

| Question | File |
|---|---|
| What exactly goes on the wire | [protocol.md](protocol.md) |
| Where records are written and what breaks | [storage.md](storage.md) |
| How sources are collected and what delivery costs | [agent.md](agent.md) |
| How the interface computes what it draws | [tui.md](tui.md) |
