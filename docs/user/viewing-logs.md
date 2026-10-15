# Viewing logs

The server's terminal interface. Three panels: **agents** and **files** in the
sidebar, **logs** beside them.

![The Logmon server interface](../images/tui-overview.png)

Every image on this page is a real capture of the real binaries against a
generated dataset. The log content in it is synthetic.

## Starting it

```bash
server --config server.yaml              # with the interface
server --config server.yaml --no-tui      # headless, for a service
```

The interface runs on the **server host**, in that process. There is no remote
viewer and no HTTP endpoint — if you are not on the box, you are not looking at
it. `--no-tui` is required for unattended service because the interface takes
over the terminal.

The terminal must be at least **40x14**. Smaller than that and the interface
says so rather than drawing something broken.

## The three panels

### Agents

Which agents are connected, when they were last seen, and how many records they
contributed to what is on screen.

![The agents panel, with one agent disconnected](../images/tui-agents-panel.png)

Columns, in order: a **state dot**, the **agent ID**, the **hostname**, the
**last-seen** time, and a **record count**.

| Column | Meaning |
|---|---|
| state dot | `●` green connected, `○` hollow disconnected |
| agent ID | as configured in `agent.yaml`; this is the partition name on disk |
| hostname | the label the agent reports, which need not match the ID |
| last-seen | relative — `now`, `12s ago`, `4m ago`, `2h ago`, `never` |
| record count | records from this agent **in the currently open file and filter** |

**The count is not a lifetime total.** It is derived from whatever the log panel
is showing, so it changes as you move between files and as you type a filter. The
filter screenshot below shows it dropping from 1,000 to 207 under `/error`.

That is deliberate: a stored total would have to be updated on every record and
would disagree with the panel above it. A number that disagrees with what you
can see is worse than no number.

**It can only be non-zero for one agent at a time.** The log panel shows a single
`date/app/agent.jsonl` partition, so only that agent's records are in scope and
every other agent counts zero. Read the column as "how much of *this* is from
that agent", not "how busy is that agent".

**Columns are dropped as the panel narrows**, in order of least usefulness:
first the count, then last-seen, then the hostname. The state dot and the agent
ID are never dropped. Fitting all four needs 43 columns, so on the 30-column
minimum sidebar you get the dot and the ID.

### Files

A tree of everything in storage:

```text
logs
└── 2026-10-15                 ← the day the server received them
    ├── auth-svc
    │   ├── app-1
    │   └── app-2
    └── edge-proxy
        ├── edge-1
        └── edge-2
```

`←`/`→` (or `h`/`l`) collapse and expand. `Enter` on a leaf opens it in the log
panel; `Enter` on a branch toggles it.

Dates are **server receipt time in UTC**, not the time in the log line. A record
from a machine with a wrong clock still lands under the day the server received
it.

### Logs

The open partition, newest at the bottom, following new arrivals.

Each row is a marker for the selected line, the receipt time, a severity badge,
the category, and the message.

| Badge | Colour | Source of truth |
|---|---|---|
| `ERR` | red | syslog 0–3, or an error keyword in the line |
| `WRN` | orange | syslog 4, or a warning keyword |
| `INF` | green | syslog 5–6, or an info keyword |
| `DBG` | grey | syslog 7, or a debug keyword |
| *(none)* | default | nothing matched |

**What the badge actually means.** If the source stated a severity, that is used
verbatim — journald's `PRIORITY`. Only when nothing is stated does the interface
guess from words in the line. That guess is a heuristic and it misfires:
`no errors in the last hour` is badged as an error, and so is a line containing
the path `/var/log/errors/`.

Records arriving while the panel is open appear immediately.

## Keys

`?` shows this list inside the interface, which is always accurate.

| Key | Action |
|---|---|
| `Tab` / `shift+tab` | Focus the next or previous panel |
| `1` `2` `3` | Focus agents, files or logs |
| `↑` `↓` or `k` `j` | Move the selection |
| `←` `→` or `h` `l` | Collapse or expand a tree node |
| `Enter` | Open the selected file — **or, on the agents panel, filter the logs to that agent** |
| `[` `]` | Previous or next category |
| `/` | Filter (see below) |
| `esc` | Clear the filter, or close the help overlay |
| `w` | Toggle line wrapping |
| `g` | Toggle the ingest graph |
| `t` | Cycle the log time window: `5m`, `1h`, `24h`, `all` |
| `r` | Rescan storage, and re-read the appearance config |
| `?` | Toggle the keybinding overlay |
| `q` `ctrl+c` | Quit the server |

![The keybinding overlay](../images/tui-help.png)

The time window is a **display filter**. The file is still read in full, so a
window reduces what you read, not what the server reads. The default is `all`.

### Filtering

`/` narrows **as you type** — there is no enter to confirm.

![Filtering for "error"](../images/tui-filter.png)

Inside the prompt:

| Key | Action |
|---|---|
| `tab` | Switch scope: `msg` → `cat` → `agent` |
| `ctrl+r` | Switch between substring (`sub`) and regex (`re`) |
| `esc` | Discard and close |

The status bar shows the active scope and match mode: `Filter [msg|sub] error_`.

A pattern that does not compile is marked `(bad pattern)` and the panel keeps
its previous contents rather than emptying.

Filtering applies to the records **loaded from the open partition** — at most the
most recent 1,000. There is no search across stored data and no pagination.

## The ingest graph

One row per series, one bucket per second, **60 seconds of history**. The top
row is every agent combined; the second is the agent whose records are on
screen.

It is **live only**. It counts what the interface has received since the session
started and never backfills from storage, so it is flat until traffic arrives
and it says nothing about the past. That is the honest reading for a live view.

`g` hides it. It is also hidden automatically when the log panel has fewer than
8 interior rows, whatever `g` says — the graph is the first thing sacrificed to
a short panel, the same way the severity badge goes first on a narrow one.

## Layout

| Terminal width | Layout |
|---|---|
| 140+ | Sidebar at the configured share, logs beside it |
| 100–139 | Fixed 32-column sidebar, logs beside it |
| under 100 | Stacked: agents, files, logs, one above the other |
| under 40 wide, or 14 tall | Refuses to draw |

![The stacked layout at 90x32](../images/tui-narrow.png)

Below 100 columns the stacked layout gives the **focused panel two thirds** of
the height. Focusing the agents panel therefore leaves a lot of blank space
below three agents. That is the trade for the log panel not being squeezed out
when you focus something else.

The sidebar share is configurable — see [configuration.md](configuration.md).

## Line wrapping

Off by default, because a log tail exists to be scanned and one very long line
pushes everything else off screen. `w` turns it on for when the whole line is
the point.

A wrapped record is cut at 20 rows and marked. The selection highlights only the
record's first row, so one record does not read as several.

## Appearance

Everything visual is configurable in `~/.config/logmon/config.yml`. The file is
optional and a partial file is valid — absent settings keep their defaults.

```yaml
gui:
  border: rounded          # rounded | single | double | hidden
  sidePanelWidth: 0.33     # share of the width, clamped to 0.20–0.60
  graph: true
  wrap: false
  mouse: false
  theme:
    borderActive: "10"
    selectedBg: "236"
    selectedFg: "255"
```

`r` re-reads the file. That also resets `g` and `w` to whatever it says — the
file wins, always.

Parsing is strict. An unknown key, border or colour is an error: the interface
falls back to defaults and prints a `GUI config` line rather than silently
ignoring it.

### Colours used as backgrounds must be 16–255

0–15 are ANSI colours that your terminal theme remaps, so contrast against one
is not knowable in advance. A selection background of `4` (ANSI blue) with a
foreground of `230` measured 2.67:1 on a theme that maps blue to a light blue —
illegible. The default is `236` with `255`, which is fixed RGB everywhere and
measures 11.38:1.

Foregrounds may use 0–255 freely. A border that comes out a slightly different
green than expected still reads as a border.

Colours accept a name (`brightblue`), an index (`12`), or `#rrggbb`.

## Mouse

**Off by default**, and there is no key to turn it on.

```yaml
gui:
  mouse: true
```

Terminal mouse reporting takes over text selection — with it enabled, dragging
selects nothing and you cannot copy a log line, which is the most common thing
anyone does with one. The reporting mode is negotiated when the program starts
and cannot be renegotiated from inside it, so a runtime toggle would leave the
terminal in a state the program cannot exit. Restart the server to change it.

## What it will not do

- Load more than the most recent **1,000 records** from one partition.
- Search across stored files, or paginate.
- Show anything from before the partition was opened, beyond those 1,000.
- Backfill the graph from storage.
- Delete, rotate or expire anything.
- Work from anywhere but the server host.
