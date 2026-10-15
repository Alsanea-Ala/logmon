# Documentation

Ten files, split by who is reading them.

## Start here

| If you want to | Read |
|---|---|
| Get it running | [../README.md](../README.md) — install, quickstart, build |
| Configure an agent or a server | [user/configuration.md](user/configuration.md) |
| Know what every key does | [reference/configuration-reference.md](reference/configuration-reference.md) |
| Know what every flag does | [reference/cli.md](reference/cli.md) |
| Use the terminal interface | [user/viewing-logs.md](user/viewing-logs.md) |
| Fix something that is broken | [user/troubleshooting.md](user/troubleshooting.md) |
| Understand how it works | [technical/architecture.md](technical/architecture.md) |
| Work on the code | [technical/*.md](technical/) and `AGENTS.md` |

## The three tracks

```text
user/        you run it        prose, task-shaped, no internals assumed
reference/   you need a value  flat tables, every key and flag, nothing else
technical/   you read Go      how and why, including the decisions that
                               look arbitrary until you know the reason
```

A fact lives in exactly one file. The README links here; it does not repeat.

## user/

| File | Covers |
|---|---|
| [configuration.md](user/configuration.md) | Precedence, tokens, path resolution, `server.yaml`, `agent.yaml`, sources, the appearance file, a worked pair that runs |
| [deployment.md](user/deployment.md) | Preflight, acceptance, upgrades and the one real hazard in them, failure behaviour, capacity, constraints, security checklist |
| [viewing-logs.md](user/viewing-logs.md) | The three panels, the agents panel's columns, every key, filtering, the graph, layout, wrapping, appearance, mouse, and what it will not do |
| [troubleshooting.md](user/troubleshooting.md) | Ordered by how often it happens: authentication first, then missing records, then layout and source problems |

## reference/

| File | Covers |
|---|---|
| [cli.md](reference/cli.md) | Every flag on both binaries, defaults, and which flags override YAML |
| [configuration-reference.md](reference/configuration-reference.md) | Every key in all three files, every default, every validation error message, and the colour range rule |

## technical/

| File | Covers |
|---|---|
| [architecture.md](technical/architecture.md) | What the architecture is, package layering, data flow, the connection lifecycle, concurrency, seven decisions with reasons, and what it is **not** |
| [protocol.md](technical/protocol.md) | NDJSON framing, the four types, the handshake, severity on the wire, why `Version` stayed 1, validation, compatibility |
| [storage.md](technical/storage.md) | The partition layout, the append path and its rollback, what reading costs, failure modes, sizing, retention |
| [agent.md](technical/agent.md) | The fan-in pipeline, the three source types, cursors, at-least-once delivery, retry, path resolution |
| [tui.md](technical/tui.md) | The event loop, the layout algorithm and its invariants, geometry, filtering, severity, colour rules, the graph, mouse, configuration |

## Images

The screenshots throughout these docs are real captures of the real binaries.
They are not mockups and they are not hand-drawn: the server and agents were
driven inside a terminal multiplexer against a generated dataset, and the
captured output rendered with the xterm 256-colour palette, so the colours in an
image are the colours a terminal shows. Only the log content is synthetic.

They are committed as-is. The capture tooling is not part of this repository, so
these images are regenerated out of band and replaced by hand — nothing in the
tree will warn you when one has gone stale.

Images live in `images/`.

## Conventions used here

- **Defaults and limits are quoted from the source**, not from memory, and each
  technical document ends with the commands that verify its claims.
- **Tradeoffs are stated, not hidden.** Where a choice has a cost — the focused
  panel getting two thirds of a short stacked layout, the severity heuristic
  misfiring on `no errors in the last hour` — the document says so.
- **Known gaps are named.** Where the code does something that makes
  troubleshooting harder, the document says that too, rather than writing
  around it.
