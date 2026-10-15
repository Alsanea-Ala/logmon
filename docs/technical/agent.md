# Agent

## What it does

Tails a set of configured sources on one machine and pushes each new line to
the server as a `Record`. No local buffer, no disk queue, no index. If it is
stopped, unsent lines are gone.

## The pipeline

One goroutine per source, one sender, a bounded channel between them.

```text
  source goroutine ─┐
  source goroutine ─┤
  source goroutine ─┼──► records chan (cap 256) ──► sender ──► TCP
  source goroutine ─┘                                   │
                                                         ▼
                                                    server
```

```go
for _, source := range cfg.Sources {
    workers.Go(func() { collect(ctx, cfg, source, records) })
}
workers.Go(func() { send(ctx, cfg, hostname, records) })
```

The channel is capped at 256 records. When the sender cannot keep up — a
flapping server, a slow disk — collectors block on send rather than buffering
without bound. Tailing libraries have their own read buffers behind that, so
the practical backpressure is a little softer than 256 lines.

### One failing source stops the agent

`collect` returns an error, the error is pushed to a shared channel, and `Run`
cancels the context, which stops the *other* sources too.

That is deliberate: an agent that silently drops one source while reporting
itself healthy is worse than one that stops and says why. With `retry: true`
(the default) the failing source is retried after `retry_delay` instead, and the
agent keeps running.

With `retry: false` the first error ends the process.

## Sources

Three types. Two of them shell out to a host CLI.

| Type | Reads | Needs |
|---|---|---|
| `file` | a path, tailed | read access to the file |
| `journald` | `journalctl --follow` | membership of the right journal group — varies by distribution |
| `docker` | `docker logs --follow` | docker group or socket access, effectively root |

`Source` on the wire is the *type*, not the path: `file`, `journald`, `docker`.
Paths and container names stay in the agent's own configuration.

### from_beginning

```text
true   seek to the start and read everything already in the file
false  seek to the end and follow only new lines
```

`false` also maps to `journalctl --lines=0` and `docker logs --tail=0`.

It is per source, so an agent can ship an existing file in full while following
a container from now on.

### Cursors

Retry cursors live **in memory only**. A file that is rotated or truncated under
the tailer is detected by comparing the `os.FileInfo` and the offset, and the
tailer restarts from the beginning of the new file.

On restart the agent re-reads its configuration and seeks according to
`from_beginning` again. It has no memory of what it already sent.

## Delivery

**At-least-once.** There is no deduplication on the wire and no ordering
guarantee beyond TCP's.

The duplicate case is a lost acknowledgement:

```text
  agent                                server
    │                                      │
    │──── Record ─────────────────────────►│  append to JSONL
    │                                      │
    │            ✗ ACK lost                 │  socket dies here
    │◄────────────────────────────────────-│
    │                                      │
    │  reconnect                           │
    │──── the same Record ─────────────────►│  appended AGAIN
    │                                      │
    │──── Ack ─────────────────────────────►│
```

The record is stored twice. There is no sequence number to deduplicate on and
no server-side dedup pass, because adding one would mean the agent had to be
the source of truth for identity across a connection that can drop.

The loss case is the mirror: an agent stopped between collecting a line and
receiving its acknowledgement loses that line, and nothing replays it.

Both are accepted rather than engineered away, and both are listed in the
README's limitations. If either matters for a deployment, the fix is a local
queue in front of the agent, not a change here.

### Retry

`retry: true` with `retry_delay` (default 2s) retries both a failed connection
and a failed source. There is no exponential backoff and no jitter — the delay
is flat. Against a server that is down, every agent on the network reconnects
on the same 2-second cadence.

## Configuration resolution

`--config` is optional. With no file, the agent uses defaults plus
`LOGMON_AGENT_TOKEN`, which is how a token is kept out of YAML.

Flags override YAML **only when explicitly set** — Cobra's `Changed`, not the
flag value. Passing `--retry=false` when the default is already `true` turns it
off; passing `--server` with no value does not silently reset an address that
was in the file.

The three source flags replace the YAML source list wholesale, so mixing
`--config` with `--file` means the file's `sources:` is ignored:

```bash
agent --config agent.yaml --file /var/log/app.log,app,category,false
```

Each is CSV: `path,app,category,from_beginning`.

## Relative paths

A relative `path:` in YAML resolves against **the directory containing the
config file**, not the working directory. So `--config /etc/logmon/agent.yaml`
with `path: logs/app.log` reads `/etc/logmon/logs/app.log`.

This is the single most surprising thing about the agent, and it is deliberate:
a config and the files it names should travel together. Equivalent paths given
on the command line resolve against the working directory, because there is no
config file to be relative to.

## The startup screen

Before collecting, the agent draws a block logo and its resolved configuration:

```text
id, hostname, server, retry, token status, and each source
```

Fields that are individually wrong print in red — a malformed agent ID, an
empty `server`, a missing token — rather than aborting the screen, so a
misconfigured agent shows which field is wrong before the process exits with the
error on stderr. `hostname` is the exception: it is always printed plain.

`NO_COLOR` or a non-terminal stdout disables colour and prints a static banner
instead of animating.

## Verify

```bash
# the fan-in
sed -n '/^func Run/,/^}/p' internal/agent/agent.go

# the three collectors
grep -n 'func collectFile\|func collectJournal\|func collectDocker' internal/agent/agent.go

# path resolution against the config directory
sed -n '/^func LoadConfig/,/^}/p' internal/agent/config.go

# validation rules
sed -n '/^func (cfg Config) Validate/,/^}/p' internal/agent/config.go
```
