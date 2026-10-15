# Protocol

## Framing

One JSON object per line, UTF-8, newline-terminated. No length prefix, no
framing header, no handshake beyond the first message.

```text
{"version":1,"agent_id":"edge-1","hostname":"edge-1.lan","token":"…","apps":["edge-proxy"]}
{"ok":true}
{"source":"file","app":"edge-proxy","category":"access","line":"…","level":"6"}
{"ok":true}
```

The agent encodes with a `json.Encoder`, which appends the newline. The server
reads with a `bufio.Scanner` configured for the frame limit below.

| Constant | Value | Meaning |
|---|---|---|
| `Version` | `1` | wire and handshake version |
| `MaxFrameSize` | 1 MiB (`1 << 20`) | scanner buffer ceiling |
| `MaxLineSize` | 128 KiB (`MaxFrameSize / 8`) | longest `Line` accepted |
| `MaxLevelSize` | 16 | longest `Level` accepted |

A line longer than `MaxLineSize` is rejected by `validateRecord` before it is
stored, so a single oversized line cannot fill a partition. A frame longer than
`MaxFrameSize` fails the scanner, which ends that connection rather than
desynchronising the stream.

## Types

All four are in `internal/protocol`. That package imports nothing internal,
which is what lets the agent and server ship independently.

### `Hello`

```go
type Hello struct {
    Version  int      `json:"version"`
    AgentID  string   `json:"agent_id"`
    Hostname string   `json:"hostname"`
    Token    string   `json:"token"`
    Apps     []string `json:"apps"`
}
```

The only message that carries the shared secret. `Apps` is the set the agent
intends to ship, and the server uses it as the allowlist for `Record.App` — a
record naming an app the agent did not declare is rejected, so an agent cannot
write into an app directory it never claimed.

`AgentID` must satisfy `ValidName`: 1–128 characters of `A-Za-z0-9._-`.

### `Record`

```go
type Record struct {
    Source     string     `json:"source"`
    App        string     `json:"app"`
    Category   string     `json:"category"`
    Line       string     `json:"line"`
    Level      string     `json:"level,omitempty"`
    SourceTime *time.Time `json:"source_time,omitempty"`
}
```

`Source` is the source *type* — `file`, `journald` or `docker` — not a path. The
path lives in the agent's own configuration and never crosses the wire.

### `StoredRecord`

What actually lands in a JSONL file: `Record` plus the server's own stamp.

```go
type StoredRecord struct {
    ReceivedAt time.Time  `json:"received_at"`
    SourceTime *time.Time `json:"source_time,omitempty"`
    AgentID    string     `json:"agent_id"`
    Hostname   string     `json:"hostname"`
    Source     string     `json:"source"`
    App        string     `json:"app"`
    Category   string     `json:"category"`
    Line       string     `json:"line"`
    Level      string     `json:"level,omitempty"`
}
```

`ReceivedAt` is stamped by the server on arrival, in UTC. It is the partition
key and the timestamp the interface displays. `SourceTime` is what the source
claimed, when it claimed anything.

`AgentID` and `Hostname` are filled from the connection's registration, not from
the wire. See [architecture.md](architecture.md#one-connection).

### `Ack`

```go
type Ack struct {
    OK    bool   `json:"ok"`
    Error string `json:"error,omitempty"`
}
```

One per record, plus one for the handshake.

## Handshake

```text
agent                                    server
  │                                          │
  │──── Hello{version, agent_id, ───────────►│  decode
  │     hostname, token, apps}               │
  │                                          │  validate id, version, app set
  │                                          │  sha256(token) == stored digest
  │                                          │  bind conn → agent_id
  │◄─── Ack{ok:true} ────────────────────────│
  │                                          │
  │──── Record ─────────────────────────────►│  validate + append
  │◄─── Ack{ok:true} ────────────────────────│
```

Timeouts: 5 s for the handshake, 15 min idle. Both live on the server side.

### Rejection

Every authentication failure — malformed JSON, an ID not in the allowlist, a
wrong version, a token whose digest does not match — produces the same reply:

```text
Ack{ok:false, error:"authentication failed"}
```

One string for four faults. Splitting it would tell a caller probing the server
which agent IDs exist, which is an enumeration oracle the allowlist otherwise
does not expose. The specific reason belongs in the server's own log, and
`authenticate` currently returns a bare `bool` and does not log one — that is
tracked as an open item, not a design decision.

## Severity on the wire

`Level` is the severity **the source stated**, carried verbatim. It is never
mapped by the agent.

- **journald** emits `PRIORITY` as its syslog number, 0 `emerg` through 7
  `debug`, and that is what is carried.
- **file and Docker** sources have no severity of their own and leave it empty.

The syslog-number-to-badge mapping lives in exactly one place, in the server,
which falls back to a keyword heuristic over the line when nothing is stated.
One keyword table, one owner. An agent that guessed would put an inference on
the wire where it looks like a fact.

### Why `Version` stayed 1

Adding `Level` did not bump the version, and that is deliberate:

- the field is `omitempty`, so a record with no stated level is byte-identical
  to what a pre-`Level` agent sent;
- an absent field means "not stated", which is exactly what every already-stored
  record meant when the field did not exist;
- therefore **no stored file needs migrating**, and an old agent talking to a
  new server is a supported pairing.

If a future change alters the meaning of an existing field rather than adding
one, that is the change that requires a version bump and a migration.

### One decoding trap

`json.Unmarshal` of JSON `null` into an `int` succeeds and leaves the value at
zero — and zero is `emerg`, the most severe level syslog has. `journalPriority`
checks for `null` before decoding the number for exactly that reason. Removing
that check would make `PRIORITY: null` render as an emergency.

## Validation

`validateRecord` runs on every record before it is stored. Anything it rejects
gets an `Ack{ok:false}` and the connection continues.

```text
app        must be in the Hello's app set, and a valid name
category   1–128 characters
line       ≤ 128 KiB
level      ≤ 16 bytes
source     one of file, journald, docker
```

## Compatibility

| Agent | Server | |
|---|---|---|
| pre-`Level` | current | fine — `level` is absent, which means "not stated" |
| current | current | fine |
| current | pre-`Level` | **not supported** — an old server's struct drops `level` on decode, so stated severities are lost silently |

The third row is why `Level` is `omitempty` and why the version did not move:
the only unsupported pairing is a new agent against an old server, which is a
deployment-ordering problem an operator controls.

## Limits at a glance

| | |
|---|---|
| Frame | 1 MiB |
| Line | 128 KiB |
| Level | 16 bytes |
| Category | 128 bytes |
| Agent ID | 128 bytes, `[A-Za-z0-9._-]` |
| Handshake | 5 s |
| Idle | 15 min |
| Connections | `--max-connections`, default 100 |
