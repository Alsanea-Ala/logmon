# The terminal interface

## Event loop

Plain Model/Update/View. No Bubbles widgets, no component library — the whole
interface is one model and a `View` that returns a string.

```text
  Msg ──► Update ──► Model ──► View ──► string
   ▲                                    │
   └────────────── tea.Cmd ─────────────┘

  Msg sources: tea.KeyMsg, tea.WindowSizeMsg, tea.MouseMsg
              recordMsg, agentMsg, scanMsg, loadMsg, tickMsg
```

`Init` returns a batch of two commands: the initial storage scan, and the first
graph tick. Every later tick is scheduled *by* a tick, so exactly one is ever
outstanding — a model that re-armed a timer on every update would compound into
a tick storm, and nothing else about this model would reveal that.

Ingest reaches the interface as `recordMsg`, pushed by `run.notify` from the
connection goroutine. See [architecture.md](architecture.md#one-process) for
what sharing a process with ingest costs.

## State that matters

| Field | Notes |
|---|---|
| `focus` | one of agents / files / logs; starts on agents |
| `agentIndex`, `treeIndex`, `lineIndex` | three independent cursors, one per panel |
| `filter`, `filterBuffer` | the committed pattern and what has been typed |
| `windowIndex` | `5m`, `1h`, `24h`, `all` — **the default is `all`** |
| `wrap` | off by default |
| `graphEnabled` | on by default |
| `records` | at most 1,000, newest last |
| `m.now` | a clock seam, so time-dependent output is testable |

`windowIndex`'s zero value is the *first* option, `5m`, not `all`. It is set
explicitly in `newModel` because leaving it unset means every session opens in a
window the operator never asked for.

## Layout

```text
width >= 140   sidebar at the configured ratio, logs beside it
width >= 100   32-column sidebar, logs beside it
width <  100   stacked: agents, files, logs, one above the other
below 40x14    "Terminal too small"
```

### The height invariant

`computeLayout` guarantees `agentsH + filesH + mainH == height - 2` at every
size and every focus. Two layers enforce it:

- `shareHeight` returns parts **plus a surplus**, which the caller routes.
- An overflow guard in `computeLayout` absorbs whatever the first layer could
  not fit.

Both layers are needed. Removing the cap in `shareHeight` alone still passes the
layout tests, because the guard quietly absorbs the difference; only
`TestShareHeightAlwaysFillsTheSpace` catches that one.

### A requirement is a floor, never a demand

Panels are sized from a content *minimum*, not a content demand. Treating the
content as a demand and focusing a 200-agent panel at 80x80 produced **negative
panel heights** — the bug that made the content-aware layout worth writing.

Panels with more content than the terminal scroll. That is the intended
behaviour, not a truncation bug.

### Focus gets two thirds

In the stacked layout, the focused panel takes two thirds of the contested
height at or above `rebalanceMin = 30` rows. Below that there is no surplus and
rebalancing would resize panels on every `tab` to reach a worse result.

**The documented cost:** focusing a sidebar panel gives it two thirds even when
its content is three rows. Those rows are blank. This is deliberate and is
asserted by `TestFocusedPanelSizeIsAShareNotAnUnboundedWindfall`, so nobody
"fixes" it later without noticing the trade.

### Surplus goes to the logs

The surplus from `shareHeight` is routed to the log panel, **not** to the
focused panel. Only logs have unbounded content; sending the surplus to the
focused panel handed 64 rows to a three-agent list, which is the dead space the
layout exists to remove, relocated.

### Two row counters, opposite directions

```text
m.panelTopBorder()   rows ABOVE the first body row
m.panelChromeV()     total border cost, top AND bottom
```

They are used in opposite directions, and confusing them puts a click or a
budget one row off.

## Geometry is measured once

`hitTest` returns the `panelGeometry` it measured against, and `selectRow` uses
that return value rather than recomputing.

This is not tidiness. Focus changes the layout: at 80x80, focusing the logs moves
the log panel from row 61 to row 17. A `selectRow` that recomputed the layout
*after* the focus change would measure the row against a panel that has already
moved, and the click would land on the wrong line. `TestMouseHitTestAgreesWithTheRenderer`
is the test that caught it.

## Filtering

`/` narrows on **every keystroke**, not on enter. So `filter` tracks
`filterBuffer` while the prompt is open. Both must be set together; a mismatch
filters on something the operator did not type.

Filter mode runs before focus cycling in `handleKey`, which is why `tab` and
`ctrl+r` mean something different inside it: `tab` switches scope
(`msg` → `cat` → `agent`), `ctrl+r` switches between substring (`sub`) and
regex (`re`) matching.

The status bar shows the active pair while the prompt is open:
`Filter [msg|sub] error_`.

### The cache

`filtered()` is cached, keyed on:

```text
filter | scope | match | category | agent | len(records)
```

`len(records)` changes on every append and drops to zero on load, which is what
makes it a sufficient key on its own — the length is a proxy for "the contents
changed".

## Severity

`recordSeverity` is the only place a record becomes a badge:

```text
1. record.Level stated and recognised  →  that level
2. otherwise                           →  detectSeverity(line), a keyword guess
```

`detectSeverity` splits the line on non-alphanumerics and matches whole words,
so `error` matches but `errors` does not, and `/var/log/errors/` does not badge.

**The heuristic's limit, stated plainly:** it is a guess. `no errors in the last
hour` badges as an error, and so does a path that happens to contain one. A
stated `Level` is the only reliable signal, and only journald provides one.

## Colour

Palette values are xterm 256-colour indices. Foregrounds may use 0–255.

**Backgrounds must be 16–255.** The first 16 are ANSI colours that a terminal
theme remaps, so contrast against one is not knowable at build time. The
selection bar's background was `"4"` — ANSI blue — with foreground `"230"`, and
on a theme mapping blue to a light blue that is 2.67:1, below WCAG AA for normal
text. It is now `"236"` with foreground `"255"`, 11.38:1, which is fixed RGB on
every terminal.

`TestEveryBackgroundColourIsThemeIndependent` scans the source for
`.Background(` calls and enforces the range, so the mistake cannot come back.

## Wrapping

**Off by default** (`w`). Truncation is the default because a log tail exists
to be scanned, and one wrapped 128 KiB line pushes every other record off
screen. Wrapping is opt-in for the case where the whole line is the point.

A wrapped record is cut at 20 rows and marked, so a single enormous line cannot
take the panel.

The cursor highlights a wrapped record's **first row only**. Highlighting every
row makes one record read as several that happen to share a timestamp.

Continuation lines align under the message column where there is room for at
least 20 columns of text, and fall back to a small indent otherwise, so a narrow
panel does not push the text off the edge.

## The ingest graph

60 one-second buckets — one minute — drawn as block glyphs, one row per series.

- **Live only.** It counts records the interface receives and never backfills
  from storage. A freshly started server shows a flat graph until traffic
  arrives. That is the honest reading for a live view; backfilling would mean
  re-reading every partition on a timer.
- **Counts above the selection gate.** The global rate includes every agent
  being ingested, not only the agent whose records happen to be on screen.
- **Hidden below 8 interior rows** in the log panel regardless of `g`. The graph
  is the first thing sacrificed, the same way the severity badge goes first on
  width.
- **Fixed width, not panel-derived.** Two terminals watching the same server show
  the same shape rather than the same pixels.

## Mouse

Off by default, config-only (`gui.mouse`), and there is deliberately **no runtime
toggle**.

Terminal mouse reporting takes over text selection: with it enabled, dragging
does not select, and copying a log line is the most common thing anyone does
with one. Bubble Tea negotiates the reporting mode with the terminal at startup
and cannot renegotiate from inside the program, so a key that turned it on
mid-session would leave the terminal in a mode the program cannot exit.

## Configuration

Read at startup and re-read on `r`. Everything has a default, so the file is
optional and a partial file is valid.

Parsing is **strict**: an unknown key, border or colour is an error that falls
back to defaults and prints a `GUI config` line. Never a silent substitution.

`r` re-reading the config also resets `g` and `w` to what the file says. That is
the price of one rule instead of two — the file wins, always — and the operator
who wants the graph back presses the key again.

See [../reference/configuration-reference.md](../reference/configuration-reference.md)
for every key.

## Verify

```bash
# the layout invariant and the surplus
grep -n 'func (m \*model) computeLayout\|func shareHeight' internal/server/tui.go

# geometry measured once
sed -n '/func (m \*model) hitTest/,/^}/p' internal/server/tui.go

# the cache key
sed -n '/func (m \*model) filtered/,/^}/p' internal/server/tui.go

# the severity mapping
sed -n '/^func recordSeverity/,/^}/p' internal/server/tui.go
```
