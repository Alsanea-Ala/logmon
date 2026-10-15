# Configuration reference

Every key in every file, with its default and its validation rule.

All three files are **strict-parsed**: an unknown key is an error, not something
ignored. A partial file is valid — absent keys keep their defaults.

## server.yaml

Top level. No wrapper key.

| Key | Type | Default | Rules |
|---|---|---|---|
| `listen` | string | `127.0.0.1:9000` | Required and non-empty. `host:port`. |
| `data_dir` | string | `data` | Required and non-empty. Relative values resolve from **this file's** directory. |
| `max_connections` | int | `100` | Must be ≥ 1. |
| `agents` | map | — | **Required**, at least one entry. |

### `agents`

```yaml
agents:
  prod-1:
    token_sha256: "<64 hex characters>"
  prod-2:
    token_sha256: "<64 hex characters>"
```

| Key | Rules |
|---|---|
| the map key | A valid name: 1–128 chars of `A-Za-z0-9._-`. |
| `token_sha256` | Exactly 64 hex characters — a SHA-256 digest. The **original token goes to the agent, never here**. |

The server stores only digests. Generate one with:

```bash
printf '%s' "$TOKEN" | sha256sum
```

`printf`, not `echo`. `echo` appends a newline and produces a different digest.

### Server validation errors

```text
listen address is required
data directory is required
max connections must be positive
at least one allowed agent is required
invalid allowed agent id "..."
agent "..." token_sha256 must be a SHA-256 hex digest
```

## agent.yaml

Top level. No wrapper key. Optional — without it, only defaults and
`LOGMON_AGENT_TOKEN` apply.

| Key | Type | Default | Rules |
|---|---|---|---|
| `id` | string | none | **Required.** Valid name: 1–128 chars of `A-Za-z0-9._-`. Must be in the server allowlist. |
| `hostname` | string | none | **Required**, 1–255 chars. No `os.Hostname()` fallback. |
| `server` | string | `127.0.0.1:9000` | **Required** and non-empty. `host:port`. |
| `token` | string | none | **At least 32 characters.** Falls back to `LOGMON_AGENT_TOKEN` when omitted. |
| `retry` | bool | `true` | — |
| `retry_delay` | duration | `2s` | Must be > 0. Go duration syntax: `500ms`, `2s`, `1m`. |
| `sources` | list | none | **Required**, at least one. |

### `sources[]`

| Key | Type | Required | Applies to | Rules |
|---|---|---|---|---|
| `type` | string | yes | all | `file`, `journald` or `docker`. Anything else is an error. |
| `path` | string | if `file` | file | Non-empty. Relative resolves from this file's directory. |
| `unit` | string | if `journald` | journald | Non-empty. |
| `container` | string | if `docker` | docker | Non-empty. |
| `app` | string | yes | all | Valid name, `[A-Za-z0-9._-]`. Also the storage directory name. |
| `category` | string | yes | all | 1–128 characters. |
| `from_beginning` | bool | yes | all | `false` follows only new lines. |

`app` is not free text. It becomes a directory name under `data/`, and it is the
key the server checks a record's app against — so a typo here produces records
rejected as `invalid app`.

### Agent validation errors

```text
agent id must contain only letters, numbers, dot, dash, or underscore
agent hostname is required
agent hostname must be at most 255 characters
server address is required
agent token must contain at least 32 characters
retry delay must be positive
at least one source is required
source N app must contain only letters, numbers, dot, dash, or underscore
source N category must contain 1-128 characters
source N file path is required
source N journal unit is required
source N Docker container is required
source N has unknown type "..."
```

## config.yml — appearance

Default location `~/.config/logmon/config.yml`. Override with `--gui-config`;
pass an empty string to disable it entirely.

Everything sits under a `gui` key, so the file can grow alongside `server.yaml`
rather than being a flat grab bag.

```yaml
gui:
  border: rounded
  sidePanelWidth: 0.33
  graph: true
  wrap: false
  mouse: false
  theme:
    borderActive: "10"
    borderInactive: "240"
    selectedBg: "236"
    optionsText: "4"
    title: "15"
    muted: "244"
    accent: "39"
    ok: "46"
    warn: "214"
    err: "196"
    selectedFg: "255"
```

| Key | Type | Default | Rules |
|---|---|---|---|
| `border` | string | `rounded` | One of `rounded`, `single`, `double`, `hidden`. `normal` and `none` are accepted aliases. |
| `sidePanelWidth` | float | `0.333…` | **Clamped** to 0.20–0.60, not rejected. Only consulted at 140 columns or wider. |
| `graph` | bool | `true` | Ingest graph on or off. |
| `wrap` | bool | `false` | Line wrapping on or off. |
| `mouse` | bool | `false` | Mouse support. **No runtime key** — see below. |

`sidePanelWidth` is clamped rather than rejected because a layout number is not a
correctness question the way a colour name is. Every other key is rejected when
wrong.

### `theme.*`

Every key is optional and falls back to the default, so a theme that sets two
colours is valid.

| Key | Default | Used for |
|---|---|---|
| `borderActive` | `10` | Focused panel border — the only cue for which panel has focus |
| `borderInactive` | `240` | Unfocused panel border |
| `selectedBg` | `236` | **Background.** Selection highlight. |
| `selectedFg` | `255` | **Foreground.** Text on that background. |
| `optionsText` | `4` | Status bar |
| `title` | `15` | Panel titles |
| `muted` | `244` | Timestamps, dimmed metadata |
| `accent` | `39` | Categories, section headings |
| `ok` | `46` | Connected agent |
| `warn` | `214` | Degraded state |
| `err` | `196` | Errors, disconnected agent |

### Colour formats

Three accepted forms:

```yaml
selectedBg: "236"          # 256-colour index, 0-255
selectedBg: brightblue     # one of the sixteen ANSI names
selectedBg: "#1e90ff"      # #rrggbb
```

Names: `black` `red` `green` `yellow` `blue` `magenta` `cyan` `white`, and the
same eight prefixed `bright`.

Anything else is an error naming the three valid forms.

### Backgrounds must be 16–255

**This is a rule, not a preference.** Indices 0–15 are ANSI colours that a
terminal theme remaps, so contrast against one cannot be known in advance.

The selection background was `4` — ANSI blue — with foreground `230`. On a theme
mapping blue to a light blue that is **2.67:1**, below WCAG AA for normal text.
The default is now `236` with `255`: **11.38:1**, and fixed RGB on every
terminal that speaks xterm.

Foregrounds may use 0–255 freely. A border that renders a slightly different
green than expected still reads as a border.

### Why mouse has no runtime toggle

Terminal mouse reporting takes over text selection: with it enabled, dragging
does not select, and copying a log line is the most common thing anyone does
with one. The reporting mode is negotiated when the program starts and cannot be
renegotiated from inside it, so a key that enabled it mid-session would leave
the terminal in a mode the program cannot exit.

Change it here and restart the server.

### Re-reading the file

`r` re-reads this file, and that **resets `g` and `w` to what the file says**.

That is the price of having one rule rather than two: the file wins, always. The
operator who wants the graph back presses the key again.

An unreadable or invalid file leaves the current settings alone rather than
blanking the panel, and prints a `GUI config` line explaining why.

### Errors

```text
gui config <path>: gui config: unknown border "...": want rounded, single, double or hidden
gui config <path>: gui config: theme.selectedBg: unknown colour "...": want a name, 0-255, or #rrggbb
gui config <path>: gui config: theme.err: colour index 300 out of range 0-255
```

A missing file is not an error — no file at all is the common case.

## Environment

| Variable | Binary | Effect |
|---|---|---|
| `LOGMON_AGENT_TOKEN` | agent | Used when the YAML omits `token:`, or when no config is given. YAML wins if both are present. |
| `NO_COLOR` | agent | Static, uncoloured startup banner. |

## Quick reference

| File | Required? | Wrapper key |
|---|---|---|
| `server.yaml` | effectively yes — the allowlist is mandatory | none |
| `agent.yaml` | no | none |
| `config.yml` | no | `gui:` |
