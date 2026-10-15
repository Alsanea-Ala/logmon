# Deployment

Operating Logmon once it is installed. For the install layout, the systemd
units, the journal-group table and the retention cron, see
[README § Production Deployment](../../README.md#production-deployment) — this file
does not repeat them.

What follows is what matters after the service is running: how to verify it
works, how to upgrade it, and what it does when something breaks.

## Preflight

Worth checking before the first install rather than after the first incident.

### Server

```bash
# is the port free
ss -lntp | grep :9000

# clock — partition dates are server receipt time in UTC, so a skewed
# clock files records under the wrong day
timedatectl status

# space, and inodes. File count is date x app x agent, so a deployment with
# many short-lived apps costs inodes long before it costs bytes.
df -h /var/lib/logmon
df -i /var/lib/logmon
```

Decide the **bind address** deliberately. The default `127.0.0.1` is loopback,
so no agent can reach it — binding a routable address is a decision, and it
should be a VPN or private interface address rather than `0.0.0.0`. Transport
is plaintext and the token crosses the wire in cleartext during the handshake.
See [SECURITY.md](../../SECURITY.md).

### Agents

```bash
# can the service user read each source, and nothing more
sudo -u logmon test -r /var/log/app.log

# journal access, where it is needed — the group name varies by distribution
sudo -u logmon journalctl --list-boots >/dev/null && echo readable

# docker is effectively root; only needed for Docker sources
sudo -u logmon docker logs --tail 1 your-container
```

Generate the token with `printf`, not `echo`:

```bash
TOKEN="$(openssl rand -hex 32)"
printf '%s' "$TOKEN" | sha256sum
```

`echo` appends a newline and produces a different digest. That single character
is the most common setup failure and it presents as `authentication failed` with
no other clue. See [configuration.md](configuration.md#generating-a-token).

## First run and acceptance

A pass/fail list, so "is this actually working" has an answer rather than a
feeling.

```text
[ ] agent prints its resolved config, with no field red
[ ] server starts and creates data_dir with mode 0750
[ ] a record arrives: a new partition appears under data/<today>/
[ ] the log panel shows it after r
[ ] the ingest graph moves within about a minute
[ ] stopping an agent shows a hollow dot and an elapsed last-seen
[ ] the retention timer deletes an old partition, and r still works
```

The last one matters: retention is the only thing bounding disk use, and a
retention job that deletes a partition the interface has open is safe, but one
that leaves the tree growing is not. `r` walks the whole data directory, so
retention is also what keeps rescanning fast.

## Upgrade

### No migration is needed

The wire version stayed at `1` when severity was added, because `Level` is
`omitempty` — a record with no stated severity is byte-identical to what an
older agent sent, and an absent field means exactly what it always meant. There
is no data migration and no schema step. See
[protocol.md](../technical/protocol.md#why-version-stayed-1).

### Order: server first

```text
supported   old agent -> new server
supported   new agent -> old server, but stated severity is dropped silently
```

The second pairing loses information without complaining. Upgrade the server
first, then the agents, and the exposure is nil.

### Restarting the server is safe

- Stored records persist — storage is the source of truth, not memory.
- The interface rescans storage on startup and repopulates.
- `run.failed` is per-process, so a **disabled partition gets another chance**
  after a restart. See [failure behaviour](#failure-behaviour).
- Connections are drained on shutdown: the accept loop is cancelled, the
  listener closed, live agents disconnected, connection goroutines waited on.

### Restarting an agent loses unsent records

This is the one worth planning around.

Cursors live **in memory only**. An agent that is stopped between collecting a
line and receiving its acknowledgement for it loses that line, and nothing
replays it. On restart it re-reads its configuration and seeks according to
`from_beginning` again — it has no memory of what it already sent.

The loss window is therefore:

```text
agent last acknowledged  ──────────────►  agent restarted
                        everything in between is gone
```

Rolling an agent restart is only safe if you accept that window, or if the
source is one you can re-read from the beginning afterwards. For a `file` source
with `from_beginning: true`, restarting is harmless: the whole file is shipped
again, at the cost of duplicates. See
[agent.md](../technical/agent.md#delivery).

## Failure behaviour

| Event | What happens |
|---|---|
| Disk full mid-write | The write fails, the file is rolled back to the size it had before, the record is refused, and the agent retries |
| Rollback also fails | The partition is **disabled**: every later append to it is refused for the life of the process, so damage stops compounding. Cleared by restarting the server |
| Server unreachable | The agent retries every `retry_delay` — **flat, no exponential backoff and no jitter**. Every agent on the network reconnects on the same cadence, default 2s |
| Handshake stalls | Dropped by the server at 5s |
| Record fails validation | `Ack{ok:false}`, the connection continues |
| A partition contains a malformed line | The whole partition fails to load. There is no skip-bad-lines path |
| Storage directory unwritable at startup | The server exits with `create data directory` |

### The 15-minute idle timeout

The server sets a **15-minute read deadline on every record read**. An agent that
sends nothing for fifteen minutes is disconnected, and reconnects on its next
attempt.

```go
for {
    conn.SetReadDeadline(time.Now().Add(idleTimeout))  // 15 * time.Minute
    ...
}
```

This is not a fault, but it looks like one if you are watching connection churn
on a quiet source: an agent on a log that only writes hourly will reconnect every
quarter hour, indefinitely. The alternative — a keepalive or a longer deadline —
is not implemented, so the correct response to seeing it is to recognise it
rather than to investigate it.

## Capacity

The arithmetic is in [storage.md § Sizing](../technical/storage.md#sizing). Two
deployment-specific consequences:

**File count is `date × app × agent`.** A deployment with many applications and
short retention produces many small files rather than a few large ones. That is
an inode cost, and `find`-based retention over a deep tree gets slower as it
grows.

**`r` walks the entire data directory.** So does startup. Retention is what
keeps both fast — not as an optimisation, but because nothing else bounds the
tree.

## Constraints

Four limits that are design decisions rather than bugs. Each is argued where it
is made:

| Constraint | Why |
|---|---|
| **One writer per `data/` directory.** Two servers sharing one over NFS is not safe — the storage lock is per-process, not a file lock | [architecture.md](../technical/architecture.md#concurrency) |
| **Plaintext, no TLS.** The token is sent in cleartext during the handshake | [SECURITY.md](../../SECURITY.md) |
| **No high availability or failover.** One server, one writer, no clustering | [architecture.md](../technical/architecture.md#what-this-is-not) |
| **No remote viewer.** Viewing requires a terminal on the server host | [viewing-logs.md](viewing-logs.md) |

Running two servers against one `data/` will corrupt files. It is the one
constraint here that fails loudly and irrecoverably rather than degrading.

## Security posture

Consolidated, because the decisions are spread across three README sections and
`SECURITY.md`.

```text
[ ] server bound to a VPN or private address, not 0.0.0.0
[ ] port firewalled to known agent addresses
[ ] config and environment files mode 0600, owned by the service user
[ ] a distinct token per agent, so one compromised host is not one
    compromised deployment
[ ] agents run as a dedicated user with read access to sources only
[ ] docker group membership only where Docker sources exist — it is
    effectively root; prefer the journald logging driver instead
[ ] the transport is understood to be plaintext, and the network is
    trusted or the traffic is tunnelled
```

[SECURITY.md](../../SECURITY.md) has the threat model. Reporting a vulnerability
goes through it, not a public issue.

## Verify

Every claim above, from the source:

```bash
# 15-minute idle deadline, 5s handshake
sed -n '22,26p' internal/server/server.go
sed -n '/func (run \*runtime) handle/,/^}/p' internal/server/server.go

# rollback and the disabled set
sed -n '/func (run \*runtime) append/,/^}/p' internal/server/server.go

# shutdown ordering
sed -n '/^func Run/,/^}/p' internal/server/server.go

# why an upgrade needs no migration
grep -n 'Version\s*=\|omitempty' internal/protocol/message.go

# why a restart loses records: in-memory cursors
grep -n 'SeekInfo\|FromBeginning' internal/agent/agent.go
```
