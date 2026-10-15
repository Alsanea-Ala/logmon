# CLI reference

Every flag on both binaries, with its default and whether it overrides YAML.

## Precedence

A flag overrides the config file **only when explicitly passed**. Cobra's
`Changed`, not the flag's value, is what decides — so `--server` with no
argument does not clear an address that was in the file.

## server

```
server [flags]
```

| Flag | Type | Default | Overrides YAML | Meaning |
|---|---|---|---|---|
| `--config` | string | none | — | Path to the server YAML. Without it, the allowlist is empty and validation fails. |
| `--listen` | string | `127.0.0.1:9000` | yes | TCP bind address for agent connections. |
| `--data-dir` | string | `data` | yes | Storage root. A relative YAML value resolves from the config file's directory. |
| `--max-connections` | int | `100` | yes | Simultaneous agent connections. Connections beyond the limit are refused. |
| `--no-tui` | bool | `false` | no | Run headless. Required for unattended service. |
| `--gui-config` | string | `~/.config/logmon/config.yml` | no | Appearance config. An empty string disables appearance configuration. |

`--no-tui` and `--gui-config` have no YAML equivalent — the first because it
decides whether the process is interactive at all, the second because it names
a file rather than a setting.

### Examples

```bash
# normal
server --config server.yaml

# headless service
server --config server.yaml --no-tui

# override the bind address and storage location
server --config server.yaml --listen 10.0.0.5:9000 --data-dir /var/lib/logmon

# ignore any appearance config
server --config server.yaml --gui-config ''
```

## agent

```
agent [flags]
```

| Flag | Type | Default | Overrides YAML | Meaning |
|---|---|---|---|---|
| `--config` | string | none | — | Path to the agent YAML. Optional; without it only defaults and the environment apply. |
| `--id` | string | none | yes | Agent ID. Must be in the server allowlist. |
| `--hostname` | string | none | yes | Host label stored with every record. **Required**; there is no `os.Hostname()` fallback. |
| `--server` | string | `127.0.0.1:9000` | yes | Server `host:port`. |
| `--retry` | bool | `true` | yes | Retry failed sources and connections instead of exiting. |
| `--retry-delay` | duration | `2s` | yes | Delay before a retry. Flat, not exponential. |
| `--file` | stringArray | none | **replaces** | `path,app,category,from_beginning` |
| `--journal` | stringArray | none | **replaces** | `unit,app,category,from_beginning` |
| `--docker` | stringArray | none | **replaces** | `container,app,category,from_beginning` |

The three source flags are repeatable:

```bash
agent --config agent.yaml \
  --file /var/log/a.log,a,access,false \
  --file /var/log/b.log,b,access,false
```

**They replace the YAML `sources:` list entirely** — not merge with it. Passing
any of the three clears the list first. Using any of `--file`, `--journal` or
`--docker` triggers that, even if the list you pass is empty.

### Source spec format

Four comma-separated fields, no quoting, no embedded commas:

```text
file      path,app,category,from_beginning
journal   unit,app,category,from_beginning
docker    container,app,category,from_beginning
```

`from_beginning` is a Go boolean: `true` or `false`. Anything else is an error.

### Environment

| Variable | Used when |
|---|---|
| `LOGMON_AGENT_TOKEN` | The agent YAML omits `token:`, or no `--config` is given. |
| `NO_COLOR` | Set to anything — the startup banner is static and uncoloured. |

The agent does not need a config file at all:

```bash
LOGMON_AGENT_TOKEN='...' agent \
  --id prod-1 --hostname api-01 --server logmon.internal:9000 \
  --file /var/log/app.log,app,access,false
```

## Exit behaviour

| Situation | Server | Agent |
|---|---|---|
| Invalid config | exits non-zero with the reason | exits non-zero with the reason |
| Authentication rejected | closes the connection | logs `server rejected request: authentication failed` and, with `--retry`, keeps trying |
| Source fails | — | with `--retry`, retries that source; without it, exits non-zero |
| `SIGINT` / `SIGTERM` | drains connections and exits | stops collecting and exits |

The agent prints errors to **stderr**; the startup banner goes to **stdout**.

## Version

```bash
server --help
```

There is no `--version` flag. The interface header shows `vdev` on a binary built
without version stamping, which is the current state of the release build.
