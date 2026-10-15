# Storage

## The store

Append-only JSONL on a normal filesystem. No database, no index, no query
language, no compaction job.

```text
data/
└── 2026-10-15/            ← server receipt time, UTC. NOT source time.
    ├── edge-proxy/
    │   ├── edge-1.jsonl  ← one JSON StoredRecord per line
    │   └── edge-2.jsonl
    └── auth-svc/
        └── app-1.jsonl
```

**The partition path is the index.** `date/app/agent.jsonl` is not an
optimisation of some real table; it *is* the table. Listing a day's records for
an agent is reading one file.

The date is `ReceivedAt`, stamped by the server on arrival. A record written by
a machine whose clock is a day fast still lands under the day the server
received it. Source time is carried in the record, where it can be read and
reasoned about, but it never decides where the record goes.

## Write path

One writer at a time, behind `storeMu`:

```text
record
  │
  ├─ directory = <received date>/<app>
  ├─ path      = <directory>/<agent id>.jsonl
  ├─ if run.failed[path] → refuse, do not append
  ├─ MkdirAll(directory, 0750)
  ├─ marshal to JSON, append '\n'
  ├─ OpenFile(path, O_CREATE|O_APPEND|O_WRONLY, 0640)
  ├─ Stat the file first                       ← rollback target
  ├─ Write
  │    └─ short write or error → Truncate back to the size from Stat
  │         └─ if the truncate itself fails, mark the file disabled
  └─ Close
```

Three things in there are not incidental:

- **`O_APPEND`, not seek-and-write.** Concurrent writers cannot interleave
  within a line even if the lock is ever relaxed.
- **The rollback.** A partial write would leave a truncated JSON line at the
  tail of the file, and the next reader would fail on it. Truncating back to the
  size recorded before the write turns a partial line into no line.
- **The disabled set.** If the rollback fails, the file's length is now unknown
  and further appends could compound the damage. `run.failed` records it and
  every later append to that path is refused, so the damage stops growing and
  the condition is visible instead of accumulating.

### Permissions

Directories `0750`, files `0640`. Both are created through an `os.Root` opened
on the data directory, so a crafted `app` or `AgentID` cannot escape it —
containment is enforced by the filesystem handle, not by validating the string.

`os.Root` also means every path operation is rooted: there is no `..` that
reaches outside, whether it arrives from an agent, a partition name, or a
symlink.

## Reading

The interface scans `data/`, then loads one partition at a time.

Two details that are easy to get wrong when reasoning about cost:

- **Loading a partition reads the whole file.** The last 1,000 records are kept
  in a ring buffer, but every line is read and unmarshalled to get them. The cap
  bounds memory, not I/O. A partition with 40 MB of history costs a full read on
  every open and on every `r`.
- **One malformed line fails the whole partition.** `loadRecords` returns the
  error rather than skipping the line, so a truncated record makes the file
  unreadable in the interface — see [failure modes](#failure-modes) below.

What follows from that:

- Records are held in memory, newest last, and the view follows the tail.
- `r` rescans storage, and re-reads the GUI config at the same time.
- Each partition is one line per record, so a bad line costs the partition, not
  the record.

The 1,000-record cap is the reason the interface has no search across stored
data: there is no index to search, and loading a day of logs to filter them
would mean reading files the operator did not ask for.

## What this does not do

| | |
|---|---|
| **Retention** | Nothing is ever deleted. Logmon does not rotate, prune or expire. Retention is entirely the operator's job — `find data -mtime +30 -delete` and a cron entry. |
| **Compaction** | Files only grow. |
| **Indexing** | None. Finding a record means reading files. |
| **Timestamps** | Partition dates are receipt time. Querying by source time means reading every partition. |
| **Concurrency across servers** | One writer per `data/`. Two servers sharing a directory over NFS is not supported and will not be safe. |

## Failure modes

| Situation | What happens |
|---|---|
| `data/` missing at startup | Created with `0750`; the server does not mind. |
| `data/` unwritable | Fails at startup with `create data directory`. |
| Disk full mid-write | The write fails, the file is rolled back, the record is refused. The agent sees `Ack{ok:false}` and retries. |
| Rollback fails | The partition is disabled. Appends to it are refused for the life of the process. |
| A record arrives for an unknown app | Rejected by `validateRecord` before any path is built. |
| A line is truncated by something outside Logmon | The partition still appends, but `loadRecords` fails on it, so the file will not open in the interface. Repair or remove the line; there is no skip-bad-lines path. |
| A partition grows very large | Every open and every `r` reads it end to end. Archive or split it by hand. |
| Clock jumps backwards | Records still land under the date the server computed at receipt. Files are not reordered. |

## Sizing

Roughly, per record:

```text
one line of JSON + newline
  received_at, source_time, agent_id, hostname,
  source, app, category, line, level
```

A 200-byte log line becomes a ~350-byte stored line. At a steady 100 records/s
across 50 agents, that is roughly 3 GB/day before compression, and text JSONL
compresses well if you archive it.

The number worth watching is the file count: `date × app × agent`. A deployment
with many applications and short retention produces many small files rather than
a few large ones, and `r` walks the whole tree.

## Verify

```bash
# the layout on disk
find data -name '*.jsonl' | head

# the append path, including the rollback
sed -n '/func (run \*runtime) append/,/^}/p' internal/server/server.go

# the 1,000-record cap
grep -n 'maxVisibleRecords' internal/server/tui.go
```
