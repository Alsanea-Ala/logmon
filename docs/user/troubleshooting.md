# Troubleshooting

Ordered by how often each one actually happens.

## The agent will not authenticate

```
server rejected request: authentication failed
```

One message covers four different faults, and the server does not currently log
which. Work through them in order.

### 1. The digest does not match the token

The most common cause by a wide margin.

```bash
TOKEN="$(grep -oP '(?<=^token: ).*' agent.yaml)"
printf '%s' "$TOKEN" | sha256sum
```

Compare with `token_sha256` in `server.yaml`.

**If you generated the digest with `echo` instead of `printf`, it is wrong.**
`echo` appends a newline, so the digest is of the token plus `\n`. This is the
single most common setup failure.

### 2. The server has not reloaded the allowlist

The allowlist is read **once, at startup**. Adding an agent to `server.yaml`
while the server is running does nothing until it is restarted.

```bash
# what the server actually has, vs what you think
grep -A2 'agents:' server.yaml
ps -o lstart= -p "$(pgrep -f 'logmon.*server' | head -1)"
```

If the file was edited after the process started, restart it. This one is
genuinely confusing to diagnose because nothing in the message hints at time.

### 3. Wrong version

The handshake carries a version and the server rejects a mismatch. Both sides
are built from this repository, so this only appears when an old binary is
still installed somewhere.

### 4. Malformed handshake

Rare, and usually a proxy in the middle. Logmon speaks raw NDJSON over TCP with
no framing header, so anything that expects HTTP will fail confusingly.

## Records arrive but never appear

### The file you are looking at is not the one receiving them

The log panel shows **one partition** — `data/<date>/<app>/<agent>.jsonl` — and
the date is the server's **receipt** time in UTC.

Check the Files panel tree against where records are actually landing:

```bash
find data -name '*.jsonl' -newermt '-10 minutes'
```

Records from an agent with a badly wrong clock still land under the date the
*server* received them, so the partition can look wrong while the data is fine.

### Only the most recent 1,000 are loaded

The panel loads at most the latest 1,000 records from the open partition, and
there is no pagination and no search across files. Older records are still on
disk.

### Loading a partition reads the whole file

Even though only 1,000 are kept, the entire file is read and unmarshalled every
time you open it and on every `r`. A partition with tens of megabytes of history
is slow to open, and gets slower as it grows. `r` walks the whole data
directory, so it gets slower with total history too.

### The time window is filtering

`t` cycles `5m`, `1h`, `24h`, `all`. The default is `all`, but if it has been
cycled the window is a **display** filter — the file is still read in full. The
current window is in the status bar.

## Nothing is being stored at all

### Every record is rejected

An `Ack{ok:false}` is silent from the operator's point of view. A record is
rejected when:

- its `app` is not one the agent declared in the handshake
- `category` is empty or over 128 characters
- the line is over 128 KiB
- `level` is over 16 bytes
- `source` is not `file`, `journald` or `docker`

The most likely one in practice is the app: adding a new `app` to
`agent.yaml` and restarting the agent is required, because the declared set is
sent in the handshake.

### The partition is disabled

If a write ever fails *and* the rollback fails, the server marks that file
disabled and refuses every later append to it, rather than compounding the
damage. Restart the server to clear the condition — and check the disk, because
that is what caused it.

### The data directory is not writable

```bash
ls -ld data data/*/ data/*/*/ 2>/dev/null
```

Directories are `0750` and files `0640`. The server creates them at startup and
fails immediately if it cannot.

## The filter returns nothing

- **Scope.** Inside the filter, `tab` switches between `msg`, `cat` and `agent`.
  Filtering for an agent name while scoped to `msg` matches nothing.
- **Mode.** `ctrl+r` toggles substring and regex. A `.` in substring mode is a
  literal dot, not any character.
- **A pattern that does not compile** is marked `(bad pattern)` and the panel
  keeps its previous contents rather than emptying — so it looks stuck rather
  than empty.
- **Scope of the data.** Filtering only searches the loaded records from the
  open partition. It cannot find anything in another file.

## The layout looks wrong

| Symptom | Cause |
|---|---|
| "Terminal too small" | Below 40x14. |
| Panels stacked | Terminal is narrower than 100 columns. |
| Sidebar a fixed width | Between 100 and 139 columns, where the configured share is not consulted. |
| Large blank area under a short panel | Expected. Below 100 columns the focused panel gets two thirds of the height regardless of its content. |
| A panel's content is cut off | Expected. Panels scroll; content taller than the terminal is not shrunk to fit. |
| A column vanished from the agents panel | Expected. Count drops first, then last-seen, then hostname, as the sidebar narrows. |

## The graph is empty

The ingest graph is **live only**. It counts records the interface received
since the session started and never backfills from storage, so:

- it is flat immediately after starting the server, until traffic arrives;
- it is flat after `r`, because rescanning reads files but does not replay them
  into the graph.

It is also hidden automatically when the log panel has fewer than 8 interior
rows, whatever `g` says.

## Journald and Docker produce nothing

Both shell out to host CLIs and depend on local permissions.

```bash
journalctl --follow -u your.unit          # does this work as this user?
docker logs --follow your-container       # and this?
```

- **Journal group membership varies by distribution** — `systemd-journal` on
  most, `adm` on Debian-family.
- **Docker socket or group access is effectively root.**
- The unit or container name is wrong, or the app/category contains a character
  outside `[A-Za-z0-9._-]`.
- The journal unit does not exist on that host at all — units differ between
  distributions and between machines running the same application.

## The agent shows nothing useful

The startup screen prints resolved configuration. If it is missing fields or
colours are wrong:

- `NO_COLOR` is set, or stdout is not a terminal (a pipe, a systemd unit
  without a TTY). The banner becomes static and uncoloured. That is intended.

If the agent is running but sending nothing:

```bash
# is it connected?
ss -tnp | grep :9000
```

## Data is not being cleaned up

Logmon never deletes anything. There is no retention, rotation or expiry.
Retention is entirely yours:

```bash
find data -name '*.jsonl' -mtime +30 -delete
find data -type d -empty -delete
```

Add it to cron. Note that the interface reads a partition in full on open, so
deleting old partitions is also what keeps `r` fast.

## Getting more detail

The two gaps worth knowing about, because they are the reason several of the
above are guesswork:

- **The server logs nothing at startup.** There is no line saying which agents
  the allowlist contains, so a server silently missing one agent ID looks
  identical to a token problem.
- **All four authentication failures return the same string.** The specific
  reason is not currently recorded anywhere.
