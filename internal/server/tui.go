package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Alsanea-Ala/logmon/internal/protocol"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Version set at build time via -ldflags="-X github.com/Alsanea-Ala/logmon/internal/server.Version=..."
var Version = "dev"

// Panel chrome. lipgloss Width(n) includes padding and only adds the border
// on top (wrapAt = width - padding), so only the border is subtracted here.
// Panel padding is Padding(0, 1), so the text column is width - 4.
const (
	chromeWidth  = 2 // border only; padding is already inside Width(n)
	chromeHeight = 2 // border only; panels pad vertically by 0
	textWidth    = 4 // border (2) + horizontal padding (2)
)

const maxVisibleRecords = 1000

// Ingest graph.
//
// The buckets count records the TUI receives, not records on disk: there is no
// backfill, so a graph is flat until traffic arrives and it says nothing about
// what happened before the session started. That is the honest reading for a
// live view, and backfilling would mean re-reading every JSONL file on a timer.
//
// The window is fixed at one minute of one-second buckets rather than being
// derived from the panel width, so two terminals watching the same server show
// the same shape instead of the same pixels.
const (
	graphBuckets = 60 // one bucket per second, one minute of history
	graphTick    = time.Second

	// Index 0 is a blank so a zero bucket reads as no traffic rather than as a
	// low bar. Seven steps of block glyph is enough for a single row.
	graphLevels = 8

	// Interior rows below which the graph is dropped regardless of the toggle.
	// At minTTYHeight the log panel has four interior rows; spending three of
	// them on the graph would leave a single log line. The graph is the first
	// thing sacrificed, the same way the severity badge goes first on width.
	graphMinRows = 8

	// Column reserved for the series label. Wide enough for the agent IDs the
	// sidebar already renders, and fixed so the bars start at a consistent x.
	graphLabelWidth = 12
)

// Responsive layout breakpoints (terminal columns/rows).
const (
	breakpointWide   = 140
	breakpointMedium = 100
	minTTYWidth      = 40
	minTTYHeight     = 14
	sidebarMinWidth  = 30
	sidebarMinHeight = 4
	// The narrow layout stacks three bordered panels -- agents, files, logs --
	// plus the header and the status bar. Each panel costs two border rows and
	// wants at least one of content, so the floor is 3*3 + 2 = 11. Fourteen
	// leaves the log panel four usable rows at the smallest supported size.
	mainMinWidth   = 20
	separatorWidth = 2
	// Width below which the severity badge is dropped from the gutter.
	minWidthForBadge = 46

	// statusBarStyle pads one column each side.
	statusBarPadding = 2
)

// panelMinRows is the smallest a bordered panel can be and still show a
// title: two border rows and one of content.
const panelMinRows = 3

// layout describes panel geometry for one frame.
//
// The sidebar is two panels, not one: agents on top, the file tree below. They
// scroll independently and take focus independently, so they get their own row
// budgets rather than sharing one.
type layout struct {
	stacked  bool
	sidebarW int
	agentsH  int
	filesH   int
	mainW    int
	mainH    int
}

// splitSidebar divides the sidebar height between the agents panel and the file
// tree.
//
// The agents panel grows to fit its own rows, capped at half the sidebar so the
// file tree is never squeezed out by a long agent list. Both keep the minimum
// rows needed to render a title at all.
func splitSidebar(sidebarH, agentCount int) (agentsH, filesH int) {
	if sidebarH < panelMinRows*2 {
		sidebarH = panelMinRows * 2
	}
	agentsH = min(2+agentCount+1, max(panelMinRows, sidebarH/2))
	if agentsH < panelMinRows {
		agentsH = panelMinRows
	}
	filesH = sidebarH - agentsH
	if filesH < panelMinRows {
		filesH = panelMinRows
	}
	return agentsH, filesH
}

func (m *model) computeLayout() layout {
	w, h := m.width, m.height
	if w < minTTYWidth {
		w = minTTYWidth
	}
	if h < minTTYHeight {
		h = minTTYHeight
	}

	// Rows available to the panels: total - header(1) - status bar(1).
	availH := h - 2
	if availH < 3 {
		availH = 3
	}

	// Side-by-side: sidebar + 2 column separator + main fills the width.
	side := func(sw int) layout {
		if sw < sidebarMinWidth {
			sw = sidebarMinWidth
		}
		if maxSide := w - separatorWidth - mainMinWidth; sw > maxSide {
			sw = maxSide
		}
		if sw < 1 {
			sw = 1
		}
		agentsH, filesH := splitSidebar(availH, len(m.agents))
		return layout{
			stacked:  false,
			sidebarW: sw,
			agentsH:  agentsH,
			filesH:   filesH,
			mainW:    w - sw - separatorWidth,
			mainH:    availH,
		}
	}

	switch {
	case w >= breakpointWide:
		return side(w / 3)
	case w >= breakpointMedium:
		return side(32)
	}

	// Narrow: all three panels stack, and the logs get at least half the
	// available height.
	//
	// Reserving for the logs first, rather than splitting the sidebar and
	// handing the remainder down, is what keeps the log panel readable. At
	// 80x18 the sidebar-first split left the logs three rows: two of border
	// and one holding the file header, so no log line was visible at all.
	mainH := max(panelMinRows*2, availH/2)
	agentsH, filesH := splitSidebar(availH-mainH, len(m.agents))
	if agentsH+filesH+mainH > availH {
		// splitSidebar enforces a floor on both sidebar panels, which can
		// exceed the space available. The logs give up the difference.
		overflow := agentsH + filesH + mainH - availH
		if overflow < mainH {
			mainH -= overflow
		}
	}
	return layout{stacked: true, sidebarW: w, agentsH: agentsH, filesH: filesH, mainW: w, mainH: mainH}
}

type logFile struct {
	Date  string
	App   string
	Agent string
	Path  string
}

type fileNode struct {
	Name     string
	Path     string
	Children []*fileNode
	Expanded bool
	IsFile   bool
	Date     string
	App      string
	Agent    string
	Depth    int
}

type scanMsg struct {
	version int
	files   []logFile
	err     error
}

type loadMsg struct {
	version int
	path    string
	records []protocol.StoredRecord
	err     error
}

type recordMsg protocol.StoredRecord
type agentMsg agentStatus

// tickMsg advances the ingest graph. The next tick is scheduled when one
// arrives rather than on a timer, so exactly one is ever outstanding: a
// scheduler that re-armed itself on every Update would compound into a tick
// storm, and nothing else in this model would reveal that.
type tickMsg time.Time

type focusPane int

// filterScope is which field of a record the text filter is matched against.
type filterScope int

const (
	filterMessage filterScope = iota
	filterCategory
	filterAgent
	filterScopeCount
)

// filterMatch is how the text filter is matched.
type filterMatch int

const (
	matchSubstring filterMatch = iota
	matchRegex
)

func (s filterScope) String() string {
	switch s {
	case filterCategory:
		return "cat"
	case filterAgent:
		return "agent"
	default:
		return "msg"
	}
}

func (m filterMatch) String() string {
	if m == matchRegex {
		return "re"
	}
	return "sub"
}

// Line wrapping.
//
// Truncation is the default. A log tail exists to be scanned, and one wrapped
// 128 KiB line pushes every other record off screen. Wrapping is opt-in for
// the case where the whole line is the point.
const (
	// Ceiling on the rows one record may occupy. A single log line can be
	// `MaxLineSize`, which at any plausible panel width is thousands of rows:
	// uncapped, one record would be the entire panel.
	wrapMaxRows = 20

	// Rows a wrapped record must leave for text before continuation lines align
	// under the message column rather than taking a fixed small indent.
	wrapMinText = 20

	// Continuation indent when aligning under the message column would leave
	// less than wrapMinText. The gutter is about 28 columns, so on a narrow
	// panel full alignment leaves nothing readable on the second row.
	wrapNarrowIndent = 2

	// Appended to the last visible row of a record cut at wrapMaxRows.
	wrapMarker = " +more"
)

// Three panes, because the sidebar is two panels. Tab advances modulo the
// count; `1 - m.focus` only ever worked with two.
const (
	focusAgents focusPane = iota
	focusFiles
	focusMain
	paneCount
)

type model struct {
	dataDir       string
	files         []logFile
	fileIndex     int
	categories    []string
	categoryIndex int
	records       []protocol.StoredRecord
	lineIndex     int
	agents        map[string]agentStatus
	width         int
	height        int
	scanVersion   int
	loadVersion   int
	loadingPath   string
	pending       []protocol.StoredRecord
	err           error

	// new TUI state
	focus focusPane
	// One cursor per sidebar panel. They were a single unified index once, with
	// agents occupying the first len(agents) slots, which meant the two panels
	// could never scroll independently.
	agentIndex    int
	treeIndex     int
	fileTree      *fileNode
	treeFlattened []*fileNode
	filter        string
	filtering     bool
	filterBuffer  string
	agentFilter   string // filter logs by agent ID

	// Text filter. filter is the committed pattern, which while filtering is
	// kept equal to filterBuffer so the panel narrows on every keystroke
	// rather than on enter.
	filterScope filterScope
	filterMatch filterMatch
	filterRe    *regexp.Regexp // compiled once, reused across frames
	filterBad   bool           // last pattern did not compile

	// wrap is off by default; see the wrapping constants.
	wrap bool

	// filtered() runs on every View() to produce about twenty rows out of up
	// to a thousand records, so its result is cached and invalidated by key.
	// len(m.records) changes on every append and is nil after a load, which is
	// what makes it sufficient to key on.
	filteredCache []protocol.StoredRecord
	filteredKey   string

	// Palette and chrome. p is the single source of colour truth; every style
	// below is built from it rather than carrying its own literals.
	p           palette
	borderStyle lipgloss.Border
	showHelp    bool

	// Ingest graph. graphPending counts records since the last tick and is
	// folded into the ring on the next tickMsg.
	//
	// Two series rather than one: the global rate is what an operator watching
	// the server wants, and the selected agent's rate is what explains the lines
	// directly below it. Both are countable here because server.go notifies
	// every stored record to the TUI, before the selection gate in the
	// recordMsg case drops the ones that are not on screen.
	graphEnabled     bool
	graphCounts      []int // all agents, records per second
	graphPending     int
	fileGraphCounts  []int // selected agent only, records per second
	fileGraphPending int
}

func newModel(dataDir string) *model {
	return &model{
		dataDir: dataDir,
		// The graph starts on because it is the panel's reason to exist and the
		// row budget is what hides it, not the operator opting in. `g` turns it
		// off on a terminal where it is not wanted.
		categories:   []string{"all"},
		agents:       make(map[string]agentStatus),
		focus:        focusAgents,
		p:            defaultPalette,
		borderStyle:  lipgloss.RoundedBorder(),
		graphEnabled: true,
	}
}

// tick schedules the next graph bucket. Called from Init and from the tickMsg
// case, never on a timer.
func (m *model) tick() tea.Cmd {
	return tea.Tick(graphTick, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// palette holds every colour the TUI uses.
//
// Values are 256-colour indices rather than names so they render identically
// on any terminal that speaks the xterm palette. The bright variants are used
// where a dim one would disappear against a light background: a focused border
// has to be unmistakable, and a border is the only cue for which panel has
// keyboard focus.
//
// There is no config file for this yet. The struct is the seam one would attach
// to, so adding a reader later means changing where defaultPalette comes from
// rather than hunting for literals.
type palette struct {
	BorderActive   lipgloss.TerminalColor // focused panel edge
	BorderInactive lipgloss.TerminalColor // unfocused panel edge
	SelectedBg     lipgloss.TerminalColor // highlighted row inside a panel
	OptionsText    lipgloss.TerminalColor // bottom bar
	Title          lipgloss.TerminalColor // panel titles, headings
	Muted          lipgloss.TerminalColor // timestamps, dimmed metadata
	Accent         lipgloss.TerminalColor // section headings, categories
	Ok             lipgloss.TerminalColor // connected agent
	Warn           lipgloss.TerminalColor // degraded state
	Err            lipgloss.TerminalColor // errors, disconnected agent
	SelectedFg     lipgloss.TerminalColor // text on SelectedBg
}

var defaultPalette = palette{
	BorderActive:   lipgloss.Color("10"),
	BorderInactive: lipgloss.Color("240"),
	SelectedBg:     lipgloss.Color("4"),
	OptionsText:    lipgloss.Color("4"),
	Title:          lipgloss.Color("15"),
	Muted:          lipgloss.Color("244"),
	Accent:         lipgloss.Color("39"),
	Ok:             lipgloss.Color("46"),
	Warn:           lipgloss.Color("214"),
	Err:            lipgloss.Color("196"),
	SelectedFg:     lipgloss.Color("230"),
}

// Styles derived from the palette. Built on demand rather than stored, so a
// model carrying a different palette renders consistently.
func (p palette) styles() styles {
	return styles{
		sectionTitle: lipgloss.NewStyle().Bold(true).Foreground(p.Accent),
		selected:     lipgloss.NewStyle().Foreground(p.SelectedFg).Background(p.SelectedBg),
		connected:    lipgloss.NewStyle().Foreground(p.Ok),
		offline:      lipgloss.NewStyle().Foreground(p.BorderInactive),
		error:        lipgloss.NewStyle().Foreground(p.Err),
		statusBar:    lipgloss.NewStyle().Foreground(p.OptionsText),
		category:     lipgloss.NewStyle().Foreground(p.Accent),
		time:         lipgloss.NewStyle().Foreground(p.Muted),
		divider:      lipgloss.NewStyle().Foreground(p.BorderInactive),
		header:       lipgloss.NewStyle().Bold(true).Foreground(p.Title),
		helpKey:      lipgloss.NewStyle().Bold(true).Foreground(p.Accent),
	}
}

type styles struct {
	sectionTitle lipgloss.Style
	selected     lipgloss.Style
	connected    lipgloss.Style
	offline      lipgloss.Style
	error        lipgloss.Style
	statusBar    lipgloss.Style
	category     lipgloss.Style
	time         lipgloss.Style
	divider      lipgloss.Style
	header       lipgloss.Style
	helpKey      lipgloss.Style

	// One style per severity level, built on demand by severityStyles.
	warn  lipgloss.Style
	info  lipgloss.Style
	debug lipgloss.Style
	plain lipgloss.Style
}

// severityStyles returns the text styles for the severity levels. Separate
// from the other styles because they are keyed by level rather than by role.
func (p palette) severityStyles() styles {
	s := p.styles()
	s.warn = lipgloss.NewStyle().Foreground(p.Warn)
	s.info = lipgloss.NewStyle().Foreground(p.Ok)
	s.debug = lipgloss.NewStyle().Foreground(p.Muted)
	s.plain = lipgloss.NewStyle().Foreground(p.Title)
	return s
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.scan(), m.tick())
}

func (m *model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case scanMsg:
		if msg.version != m.scanVersion {
			break
		}
		m.err = msg.err
		m.files = msg.files
		m.buildFileTree()
		if len(m.files) == 0 {
			m.records = nil
			m.categories = []string{"all"}
			m.fileIndex = 0
			m.lineIndex = 0
			break
		}
		if m.fileIndex >= len(m.files) {
			m.fileIndex = max(0, len(m.files)-1)
		}
		return m, m.loadSelected()

	case loadMsg:
		if msg.version != m.loadVersion || len(m.files) == 0 || msg.path != m.files[m.fileIndex].Path {
			break
		}
		m.err = msg.err
		m.records = msg.records
		for _, record := range m.pending {
			m.records = appendRecord(m.records, record)
		}
		m.loadingPath = ""
		m.pending = nil
		m.categories = recordCategories(m.records)
		m.categoryIndex = 0
		m.lineIndex = max(0, len(m.filtered())-1)
		return m, nil

	case tickMsg:
		// Only the graph is touched. A tick that landed while a scan or load was
		// in flight must not clear the pending load, reset the cursor, or drop
		// records, so nothing here reads or writes m.records, m.files or the
		// version counters.
		m.graphCounts = pushBucket(m.graphCounts, m.graphPending)
		m.graphPending = 0
		m.fileGraphCounts = pushBucket(m.fileGraphCounts, m.fileGraphPending)
		m.fileGraphPending = 0
		return m, m.tick()

	case recordMsg:
		m.scanVersion++
		record := protocol.StoredRecord(message.(recordMsg))
		// Counted above the selection gate: the global rate covers every agent
		// the server is ingesting, including the ones whose records are not on
		// screen. Counting it below the gate would have made the graph
		// describe only whatever file happened to be open.
		m.graphPending++
		file := logFile{
			Date:  record.ReceivedAt.Format("2006-01-02"),
			App:   record.App,
			Agent: record.AgentID,
			Path:  filepath.Join(m.dataDir, record.ReceivedAt.Format("2006-01-02"), record.App, record.AgentID+".jsonl"),
		}
		index := m.ensureFile(file)
		if index == m.fileIndex {
			// Below the gate, so this counts only the agent whose records the
			// log panel is about to show.
			m.fileGraphPending++
			m.records = appendRecord(m.records, record)
			if m.loadingPath == file.Path {
				m.pending = appendRecord(m.pending, record)
			}
			if len(m.records) > maxVisibleRecords {
				m.records = m.records[len(m.records)-maxVisibleRecords:]
			}
			selectedCategory := m.categories[m.categoryIndex]
			m.categories = recordCategories(m.records)
			m.categoryIndex = categoryIndex(m.categories, selectedCategory)
			m.lineIndex = max(0, len(m.filtered())-1)
		}
		m.buildFileTree()
		return m, nil

	case agentMsg:
		status := agentStatus(message.(agentMsg))
		m.agents[status.ID] = status
		return m, nil
	}
	return m, nil
}

func (m *model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.filtering {
		return m.handleFilterInput(msg)
	}

	// The overlay swallows everything except the keys that dismiss it, so a
	// stray "j" cannot move the cursor behind it.
	if m.showHelp {
		switch msg.String() {
		case "?", "esc", "q", "ctrl+c":
			m.showHelp = false
		}
		if msg.String() == "q" || msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "?":
		m.showHelp = true
		return m, nil

	case "tab":
		// Modulo the pane count, not `1 - m.focus`: that arithmetic only ever worked
		// with two panes and would never reach the logs from the agents panel.
		m.focus = (m.focus + 1) % paneCount
		return m, nil

	case "up", "k":
		return m.moveUp()

	case "down", "j":
		return m.moveDown()

	case "left", "h":
		return m.moveLeft()

	case "right", "l":
		return m.moveRight()

	case "enter":
		return m.handleEnter()

	case "r":
		return m, m.scan()

	case "g":
		// Only the preference. Whether the graph actually appears is decided
		// against the row budget at render time, so toggling this on a short
		// terminal looks like nothing happened. That is intentional: the
		// preference persists in meaning, the row budget is what hides it.
		m.graphEnabled = !m.graphEnabled
		return m, nil

	case "w":
		m.wrap = !m.wrap
		return m, nil

	case "/":
		m.filtering = true
		m.filterBuffer = ""
		return m, nil

	case "esc":
		if m.filter != "" {
			m.filter = ""
			m.applyFilter()
		}
		return m, nil

	case "[":
		if m.categoryIndex > 0 {
			m.categoryIndex--
			m.lineIndex = 0
		}
		return m, nil

	case "]":
		if m.categoryIndex+1 < len(m.categories) {
			m.categoryIndex++
			m.lineIndex = 0
		}
		return m, nil
	}
	return m, nil
}

// handleFilterInput owns the keyboard while a filter is open.
//
// Filter mode runs before focus cycling and before the help overlay, so `tab`
// and `ctrl+r` mean something different here than they do outside it. That is
// the whole reason those keys were free to be claimed.
func (m *model) handleFilterInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// Cancel undoes the filtering it just did. Leaving m.filter set would
		// mean esc appeared to do nothing.
		m.filtering = false
		m.filterBuffer = ""
		m.setFilter("")
		return m, nil

	case "enter":
		// Stop editing, keep what was typed.
		m.filtering = false
		m.filterBuffer = ""
		return m, nil

	case "tab":
		m.filterScope = (m.filterScope + 1) % filterScopeCount
		m.clampLine()
		return m, nil

	case "ctrl+r":
		if m.filterMatch == matchRegex {
			m.filterMatch = matchSubstring
		} else {
			m.filterMatch = matchRegex
		}
		m.recompile()
		return m, nil

	case "backspace":
		// By rune, not by byte: a filter holding a multi-byte character would
		// otherwise lose its last byte and leave invalid UTF-8 in the buffer.
		buffer := []rune(m.filterBuffer)
		if len(buffer) > 0 {
			m.filterBuffer = string(buffer[:len(buffer)-1])
		}
		m.setFilter(m.filterBuffer)
		return m, nil

	default:
		// Type, not length. msg.String() is a byte count, so "ü" is two bytes
		// long and a length check drops it: typing any non-ASCII character into
		// a filter would silently do nothing at all.
		if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 {
			m.filterBuffer += string(msg.Runes[0])
			m.setFilter(m.filterBuffer)
		}
		return m, nil
	}
}

// setFilter replaces the committed pattern and recompiles.
func (m *model) setFilter(pattern string) {
	m.filter = pattern
	m.recompile()
}

// recompile rebuilds the matcher and clamps the cursor.
//
// A pattern that does not compile leaves filterRe holding the last good one and
// raises filterBad, so a half-typed regex narrows nothing away and then
// recovers as it becomes valid. Compiling per keystroke instead of caching is
// the alternative, and this runs on a path that also runs on every frame.
//
// Regex matching is case-insensitive to match substring matching. A filter
// whose case sensitivity changes when you toggle a mode is a filter you have to
// think about.
func (m *model) recompile() {
	switch {
	case m.filter == "":
		m.filterRe = nil
		m.filterBad = false
	case m.filterMatch == matchRegex:
		re, err := regexp.Compile("(?i)" + m.filter)
		if err != nil {
			m.filterBad = true
		} else {
			m.filterRe = re
			m.filterBad = false
		}
	default:
		m.filterRe = nil
		m.filterBad = false
	}
	m.clampLine()
}

// clampLine keeps the cursor inside the filtered set.
//
// The cursor survives across filter changes rather than being reset to the top,
// so narrowing the filter while scrolled down leaves the operator where they
// were reading. Resetting to zero on every keystroke of live filtering would
// move the panel out from under them.
func (m *model) clampLine() {
	last := len(m.filtered()) - 1
	if last < 0 {
		last = 0
	}
	m.lineIndex = min(max(0, m.lineIndex), last)
}

// applyFilter is called when a filter is committed or cleared.
func (m *model) applyFilter() {
	m.clampLine()
}

func (m *model) moveUp() (tea.Model, tea.Cmd) {
	switch m.focus {
	case focusAgents:
		if m.agentIndex > 0 {
			m.agentIndex--
		}
	case focusFiles:
		if m.treeIndex > 0 {
			m.treeIndex--
		}
	case focusMain:
		if m.lineIndex > 0 {
			m.lineIndex--
		}
	}
	return m, nil
}

func (m *model) moveDown() (tea.Model, tea.Cmd) {
	switch m.focus {
	case focusAgents:
		if m.agentIndex < m.getAgentMaxIndex() {
			m.agentIndex++
		}
	case focusFiles:
		if m.treeIndex < m.getFileMaxIndex() {
			m.treeIndex++
		}
	case focusMain:
		if m.lineIndex+1 < len(m.filtered()) {
			m.lineIndex++
		}
	}
	return m, nil
}

func (m *model) moveLeft() (tea.Model, tea.Cmd) {
	// From the logs, step back to the file tree rather than jumping past it.
	if m.focus == focusMain {
		m.focus = focusFiles
		return m, nil
	}
	// Only the tree has expandable nodes. On the agents panel this is a no-op
	// rather than a focus change, so left and right stay predictable.
	if m.focus != focusFiles || len(m.treeFlattened) == 0 {
		return m, nil
	}
	if m.treeIndex < len(m.treeFlattened) {
		node := m.treeFlattened[m.treeIndex]
		if node.Expanded && len(node.Children) > 0 {
			node.Expanded = false
			m.flattenTree()
			// clamp the cursor: collapsing can pull the node under it away
			if m.treeIndex > m.getFileMaxIndex() {
				m.treeIndex = m.getFileMaxIndex()
			}
		}
	}
	return m, nil
}

func (m *model) moveRight() (tea.Model, tea.Cmd) {
	if m.focus == focusAgents {
		m.focus = focusFiles
		return m, nil
	}
	if m.focus != focusFiles || len(m.treeFlattened) == 0 {
		return m, nil
	}
	if m.treeIndex < len(m.treeFlattened) {
		node := m.treeFlattened[m.treeIndex]
		if !node.Expanded && len(node.Children) > 0 {
			node.Expanded = true
			m.flattenTree()
		}
	}
	return m, nil
}

func (m *model) handleEnter() (tea.Model, tea.Cmd) {
	switch m.focus {
	case focusAgents:
		agentIDs := m.sortedAgentIDs()
		if m.agentIndex < len(agentIDs) {
			m.agentFilter = agentIDs[m.agentIndex]
			m.lineIndex = 0
		}
		return m, nil

	case focusFiles:
		// handled below
	default:
		return m, nil
	}

	if m.treeIndex < len(m.treeFlattened) {
		node := m.treeFlattened[m.treeIndex]
		if node.IsFile {
			for i, f := range m.files {
				if f.Path == node.Path {
					m.fileIndex = i
					return m, m.loadSelected()
				}
			}
		} else if len(node.Children) > 0 {
			node.Expanded = !node.Expanded
			m.flattenTree()
		}
	}
	return m, nil
}

// sortedAgentIDs lists agent IDs in a stable order. The agents map has no
// order of its own, and both the cursor and the row rendering need the same
// one or the highlight drifts between frames.
func (m *model) sortedAgentIDs() []string {
	ids := make([]string, 0, len(m.agents))
	for id := range m.agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (m *model) getAgentMaxIndex() int {
	return max(0, len(m.agents)-1)
}

func (m *model) getFileMaxIndex() int {
	return max(0, len(m.treeFlattened)-1)
}

func (m *model) buildFileTree() {
	root := &fileNode{Name: "logs", Expanded: true, Depth: 0}
	dateMap := make(map[string]*fileNode)

	for _, f := range m.files {
		dateNode, ok := dateMap[f.Date]
		if !ok {
			dateNode = &fileNode{Name: f.Date, Expanded: true, Date: f.Date, Depth: 1}
			dateMap[f.Date] = dateNode
			root.Children = append(root.Children, dateNode)
		}

		appNode := findChild(dateNode, f.App)
		if appNode == nil {
			appNode = &fileNode{Name: f.App, Expanded: true, App: f.App, Depth: 2}
			dateNode.Children = append(dateNode.Children, appNode)
		}

		agentNode := &fileNode{
			Name:   f.Agent,
			Path:   f.Path,
			IsFile: true,
			Agent:  f.Agent,
			Date:   f.Date,
			App:    f.App,
			Depth:  3,
		}
		appNode.Children = append(appNode.Children, agentNode)
	}

	// sort children
	for _, dateNode := range root.Children {
		sort.Slice(dateNode.Children, func(i, j int) bool {
			return dateNode.Children[i].Name < dateNode.Children[j].Name
		})
		for _, appNode := range dateNode.Children {
			sort.Slice(appNode.Children, func(i, j int) bool {
				return appNode.Children[i].Name < appNode.Children[j].Name
			})
		}
	}

	m.fileTree = root
	m.flattenTree()
}

func findChild(parent *fileNode, name string) *fileNode {
	for _, child := range parent.Children {
		if child.Name == name {
			return child
		}
	}
	return nil
}

func (m *model) flattenTree() {
	m.treeFlattened = nil
	if m.fileTree != nil {
		m.flattenNode(m.fileTree)
	}
}

func (m *model) flattenNode(node *fileNode) {
	m.treeFlattened = append(m.treeFlattened, node)
	if node.Expanded {
		for _, child := range node.Children {
			m.flattenNode(child)
		}
	}
}

func (m *model) View() string {
	h := m.height
	if h < 1 {
		h = 24
	}
	if m.width < minTTYWidth || m.height < minTTYHeight {
		return fmt.Sprintf(" LOGMON DASHBOARD  |  v%s \n\nTerminal too small (min %dx%d)\n",
			Version, minTTYWidth, minTTYHeight)
	}

	lay := m.computeLayout()

	// Flat header bar: no forced width, so no trailing background padding.
	headerText := fmt.Sprintf(" LOGMON DASHBOARD  |  v%s ", Version)
	header := m.p.styles().header.Render(headerText)

	// Status bar (minimal on narrow terminals)
	statusBar := m.renderStatusBar(lay.stacked)

	// The sidebar is two panels stacked against each other, borders touching,
	// which is how the reference layout reads: the gap between two panels
	// costs rows that the log panel needs more.
	agents := m.renderAgents(lay.sidebarW, lay.agentsH)
	files := m.renderFiles(lay.sidebarW, lay.filesH)
	sidebar := lipgloss.JoinVertical(lipgloss.Left, agents, files)
	main := m.renderMain(lay.mainW, lay.mainH)

	var content string
	if lay.stacked {
		content = lipgloss.JoinVertical(lipgloss.Left, agents, files, main)
	} else {
		content = lipgloss.JoinHorizontal(lipgloss.Top, sidebar, "  ", main)
	}

	// Tight assembly: header sits flush above the panels.
	frame := header + "\n" + content + "\n" + statusBar

	// The overlay replaces the panels rather than drawing over them: a
	// half-visible frame behind a help screen reads as a rendering bug, and
	// the header and status bar are still worth keeping for context.
	if m.showHelp {
		frame = header + "\n" + m.renderHelp(m.width, availHeight(lay)) + "\n" + statusBar
	}

	// Never emit more rows than the terminal has: an overflowing frame
	// scrolls the header off the top.
	return clampHeight(frame, h)
}

// availHeight is the row budget the panels share: the terminal minus the
// header and the status bar.
func availHeight(lay layout) int {
	if lay.mainH < 1 {
		return 1
	}
	return lay.mainH
}

// clampHeight clips s to at most rows lines while preserving the first row
// (the header) and the last row (the status bar). Clipping the middle keeps
// both pieces of chrome on screen even if the panels overflow.
func clampHeight(s string, rows int) string {
	if rows <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= rows {
		return s
	}
	if rows == 1 {
		return lines[0]
	}
	// Keep the header and the status bar; drop rows from the middle.
	keep := rows - 2 // one row for the header, one for the status bar
	if keep < 0 {
		keep = 0
	}
	middle := lines[1 : len(lines)-1]
	if len(middle) > keep {
		middle = middle[len(middle)-keep:]
	}
	out := make([]string, 0, rows)
	out = append(out, lines[0])
	out = append(out, middle...)
	out = append(out, lines[len(lines)-1])
	return strings.Join(out, "\n")
}

// fitLine clips s to w display cells and pads it out to exactly w.
//
// ANSI-safe, unlike truncateText: rows arriving here are already styled, and
// slicing by rune would cut an escape sequence in half and leak the remainder
// as visible garbage. MaxWidth does the clipping with escape sequences intact.
func fitLine(s string, w int) string {
	if w <= 0 {
		return ""
	}
	got := lipgloss.Width(s)
	if got > w {
		return lipgloss.NewStyle().MaxWidth(w).Render(s)
	}
	if pad := w - got; pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

// panel draws a titled box around body and returns exactly w by h cells.
//
// The frame is built here rather than by lipgloss because lipgloss v1 has no
// border title: BorderTop only turns the edge on or off. Splicing a title into
// a rendered border is not an option either, since the border is coloured and
// the ANSI escapes make byte offsets into that line meaningless.
//
// The body keeps using lipgloss upstream, which still owns wrapping; this only
// adds the frame and guarantees the row count, so a panel can never come out
// taller than the terminal and push the status bar out of view.
func panel(title, body string, w, h int, b lipgloss.Border, active bool, p palette) string {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}

	// A border with no corner characters is the hidden style: no frame, no
	// padding, the body is the whole panel.
	framed := b.TopLeft != "" || b.Left != ""
	const padding = 2 // one space of padding each side

	frameW, innerW := 0, w
	innerH := h
	if framed {
		frameW = 2
		innerW = w - frameW - padding
		if innerH = h - 2; innerH < 1 {
			innerH = 1
		}
	}
	if innerW < 1 {
		innerW = 1
	}

	edge := p.BorderInactive
	if active {
		edge = p.BorderActive
	}
	edgeStyle := lipgloss.NewStyle().Foreground(edge)
	if active {
		edgeStyle = edgeStyle.Bold(true)
	}

	top, bottom, left, right := b.Top, b.Bottom, b.Left, b.Right
	if top == "" {
		top = b.Middle
	}
	if bottom == "" {
		bottom = top
	}

	rows := strings.Split(body, "\n")
	out := make([]string, 0, h)

	if framed {
		// The top and bottom edges span the full interior, padding included:
		// two border cells plus w-2. Filling them with innerW instead leaves
		// both edges two cells short of the body rows between them, and the
		// panel comes out visibly misaligned.
		span := w - frameW
		if span < 1 {
			span = 1
		}

		var head strings.Builder
		head.WriteString(edgeStyle.Render(corner(b.TopLeft)))
		if title != "" {
			label := " " + title + " "
			if lipgloss.Width(label) > span {
				label = fitLine(label, span)
			}
			head.WriteString(lipgloss.NewStyle().Foreground(p.Title).Render(label))
			head.WriteString(edgeStyle.Render(strings.Repeat(top, max(span-lipgloss.Width(label), 0))))
		} else {
			head.WriteString(edgeStyle.Render(strings.Repeat(top, span)))
		}
		head.WriteString(edgeStyle.Render(corner(b.TopRight)))
		out = append(out, head.String())
	}

	for i := 0; i < innerH; i++ {
		content := ""
		if i < len(rows) {
			content = rows[i]
		}
		content = fitLine(content, innerW)
		if !framed {
			out = append(out, content)
			continue
		}
		var row strings.Builder
		row.WriteString(edgeStyle.Render(corner(left)))
		row.WriteString(" ")
		row.WriteString(content)
		row.WriteString(" ")
		row.WriteString(edgeStyle.Render(corner(right)))
		out = append(out, row.String())
	}

	if framed {
		var foot strings.Builder
		foot.WriteString(edgeStyle.Render(corner(b.BottomLeft)))
		foot.WriteString(edgeStyle.Render(strings.Repeat(bottom, max(w-frameW, 1))))
		foot.WriteString(edgeStyle.Render(corner(b.BottomRight)))
		out = append(out, foot.String())
	}

	return strings.Join(out, "\n")
}

// corner returns the first rune of a border edge, or a space when the border
// leaves it blank.
func corner(s string) string {
	if s == "" {
		return " "
	}
	for _, r := range s {
		return string(r)
	}
	return " "
}

// windowAround returns the half-open slice of rows to draw so the cursor stays
// visible in a panel shorter than its contents.
func windowAround(rows int, cursor, budget int) (int, int) {
	if budget < 1 {
		budget = 1
	}
	if rows <= budget {
		return 0, rows
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= rows {
		cursor = rows - 1
	}
	start := cursor - budget/2
	start = max(start, 0)
	start = min(start, rows-budget)
	return start, start + budget
}

// renderAgents draws the agents panel: one row per agent, its connection state
// and hostname.
func (m *model) renderAgents(width, height int) string {
	innerWidth := max(1, width-textWidth)
	rowBudget := max(1, height-chromeHeight)

	ids := m.sortedAgentIDs()
	start, end := windowAround(len(ids), m.agentIndex, rowBudget)

	rows := m.agentRows(ids, innerWidth)
	lines := make([]string, 0, rowBudget)
	if len(ids) == 0 {
		lines = append(lines, "  (none \u2014 start an agent)")
	}
	for i := start; i < end; i++ {
		if i < len(rows) {
			lines = append(lines, rows[i])
		}
	}

	return panel("Agents", strings.Join(lines, "\n"), width, height, m.borderStyle, m.focus == focusAgents, m.p)
}

// agentRows lays out one row per agent, dropping optional columns until the
// row fits.
//
// Columns are given up in order of how little they carry. The record count goes
// first because it is the easiest to recompute; last-seen goes next; the
// hostname goes last, because a hostname is how an operator recognises their
// own machine. The ID is never dropped, because it is the identity and on a
// panel narrowed to the sidebar minimum there has to be something left that
// says which agent this is.
//
// Relative times are rendered here rather than stored. LastSeen updates on
// every record, so a stored duration is stale the moment it is computed, and
// the graph of ages only gets more wrong the longer the session runs.
func (m *model) agentRows(ids []string, innerWidth int) []string {
	now := time.Now()
	counts := m.agentRecordCounts()

	// Widest value in each column, so the rows line up rather than ragged.
	idW, hostW, seenW, countW := 1, 1, 1, 1
	seen := make(map[string]string, len(ids))
	for _, id := range ids {
		agent := m.agents[id]
		seen[id] = relativeTime(agent.LastSeen, now)
		idW = max(idW, lipgloss.Width(agent.ID))
		hostW = max(hostW, lipgloss.Width(safeText(agent.Hostname)))
		seenW = max(seenW, lipgloss.Width(seen[id]))
		countW = max(countW, lipgloss.Width(formatCount(counts[id])))
	}

	// The state dot, a space, and the agent ID are never dropped.
	fixed := 2 + idW
	keepHost, keepSeen, keepCount := true, true, true
	for {
		total := fixed
		if keepHost {
			total += 1 + hostW
		}
		if keepSeen {
			total += 1 + seenW
		}
		if keepCount {
			total += 1 + countW
		}
		if total <= innerWidth {
			break
		}
		// Give up the least informative column still present. Only the host is
		// ever the last one standing, so the loop terminates on it.
		switch {
		case keepCount:
			keepCount = false
		case keepSeen:
			keepSeen = false
		case keepHost:
			keepHost = false
		default:
			keepHost = false
		}
		if !keepCount && !keepSeen && !keepHost {
			break
		}
	}

	styles := m.p.styles()
	rows := make([]string, 0, len(ids))
	for i, id := range ids {
		agent := m.agents[id]
		dot, dotStyle := "\u25cf", styles.connected
		if !agent.Connected {
			dot, dotStyle = "\u25cb", styles.offline
		}

		rest := padRight(agent.ID, idW)
		if keepHost {
			rest += " " + padRight(safeText(agent.Hostname), hostW)
		}
		if keepSeen {
			rest += " " + padRight(seen[id], seenW)
		}
		if keepCount {
			rest += " " + padRight(formatCount(counts[id]), countW)
		}
		// Two columns are the state dot and the space after it. Truncating to
		// the remaining width rather than to innerWidth keeps the row inside the
		// panel once the dot is put back.
		rest = truncateText(rest, max(1, innerWidth-2))

		if m.focus == focusAgents && i == m.agentIndex {
			rows = append(rows, styles.selected.Width(innerWidth).Render(dot+" "+rest))
			continue
		}
		// Only the dot is coloured. The rest of the row is data, and a row that
		// is mostly foreground colour is harder to scan, not easier.
		rows = append(rows, dotStyle.Render(dot)+" "+rest)
	}
	return rows
}

// agentRecordCounts is how many records each agent has in the filtered set.
//
// Counted from filtered() rather than kept as a running total, so the column
// agrees with the panel above it. A stored total would have to be updated on
// every record and would disagree with whatever filter is active, which is
// worse than being a little expensive.
func (m *model) agentRecordCounts() map[string]int {
	counts := make(map[string]int, len(m.agents))
	for _, record := range m.filtered() {
		counts[record.AgentID]++
	}
	return counts
}

// relativeTime renders how long ago t was, at a resolution a human reads.
//
// A zero time is "never" rather than a large number of days. agentStatus is
// only populated once an agent has been seen, and rendering the zero time as a
// duration produces an absurd 20000-day age for an agent that has simply never
// connected.
func relativeTime(t, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	since := now.Sub(t)
	if since < 0 {
		since = 0
	}
	switch {
	case since < time.Second:
		return "now"
	case since < time.Minute:
		return strconv.Itoa(int(since/time.Second)) + "s ago"
	case since < time.Hour:
		return strconv.Itoa(int(since/time.Minute)) + "m ago"
	case since < 24*time.Hour:
		return strconv.Itoa(int(since/time.Hour)) + "h ago"
	default:
		return strconv.Itoa(int(since/(24*time.Hour))) + "d ago"
	}
}

// formatCount is thousands-separated so a column of counts lines up by eye.
func formatCount(n int) string {
	digits := strconv.Itoa(n)
	if len(digits) <= 3 {
		return digits
	}
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func padRight(s string, width int) string {
	if gap := width - lipgloss.Width(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

// renderFiles draws the file tree panel: dates, apps and agents, as stored on
// disk.
func (m *model) renderFiles(width, height int) string {
	st := m.p.styles()
	innerWidth := max(1, width-textWidth)
	rowBudget := max(1, height-chromeHeight)

	start, end := windowAround(len(m.treeFlattened), m.treeIndex, rowBudget)

	lines := make([]string, 0, rowBudget)
	if len(m.treeFlattened) == 0 {
		lines = append(lines, "  (no logs)")
	}
	for i := start; i < end; i++ {
		node := m.treeFlattened[i]
		indent := strings.Repeat("  ", nodeDepth(node))
		prefix := "  "
		if len(node.Children) > 0 {
			prefix = "\u25bc " // down triangle
			if !node.Expanded {
				prefix = "\u25b6 " // right triangle
			}
		}
		name := node.Name
		if node.IsFile {
			name = "\u25cf " + name
		}
		line := indent + prefix + name
		if m.focus == focusFiles && i == m.treeIndex {
			line = st.selected.Width(innerWidth).Render(truncateText(line, innerWidth))
		} else {
			line = truncateText(line, innerWidth)
		}
		lines = append(lines, line)
	}

	return panel("Files", strings.Join(lines, "\n"), width, height, m.borderStyle, m.focus == focusFiles, m.p)
}

func nodeDepth(node *fileNode) int {
	return node.Depth
}

// truncateText shortens s to width display cells, marking the cut with an
// ellipsis instead of slicing mid-glyph.
func truncateText(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}

// severity is the level a log line is drawn at.
//
// Derived from the text of the line, because nothing on the wire says what
// level a record is: protocol.StoredRecord carries a Category string that the
// agent configured, and that is a user-chosen label rather than a level.
type severity int

const (
	severityPlain severity = iota
	severityDebug
	severityInfo
	severityWarn
	severityError
)

func (s severity) String() string {
	switch s {
	case severityError:
		return "ERR"
	case severityWarn:
		return "WRN"
	case severityInfo:
		return "INF"
	case severityDebug:
		return "DBG"
	default:
		return ""
	}
}

// severityKeywords maps the marker words to a level, highest first.
//
// Word matching, not substring: "error" appears inside "errors" and inside
// paths like /var/log/errors/, and a substring match paints half a log red.
// Splitting on non-alphanumerics keeps "errors" matching but leaves
// "erroneous_value" alone.
var severityKeywords = []struct {
	words []string
	level severity
}{
	{[]string{"error", "err", "fatal", "panic", "exception", "critical", "failed", "failure"}, severityError},
	{[]string{"warn", "warning", "deprecated"}, severityWarn},
	{[]string{"info", "notice"}, severityInfo},
	{[]string{"debug", "trace", "verbose"}, severityDebug},
}

// detectSeverity scans a log line for a level marker and returns the highest
// one found.
//
// Order matters and it is not alphabetical: a line reading "error: failed to
// connect" must be an error, not a warning, so the highest level has to win
// regardless of where in the line it appears.
func detectSeverity(line string) severity {
	fields := strings.FieldsFunc(line, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
	if len(fields) == 0 {
		return severityPlain
	}

	for _, entry := range severityKeywords {
		for _, field := range fields {
			word := strings.ToLower(field)
			for _, want := range entry.words {
				if word == want {
					return entry.level
				}
			}
		}
	}
	return severityPlain
}

// graphGlyphs maps a bucket onto one column, indexed by level.
//
// A []string rather than a string constant on purpose. Indexing a string by
// level yields a byte, and slicing it at that level yields half a block
// character: the glyphs are multi-byte, so the level is a rune offset and not a
// byte offset. Either mistake renders a corrupt graph rather than failing to
// compile, so the type makes it unrepresentable.
//
// TestGraphGlyphCountMatchesLevels keeps this and graphLevels in step.
var graphGlyphs = []string{" ", "▁", "▂", "▃", "▄", "▅", "▆", "█"}

// graphVisible reports whether the graph should be drawn in a panel of the
// given height.
//
// The toggle is a preference and the row budget is the veto. At minTTYHeight
// the log panel has four interior rows, so drawing the graph unconditionally
// would leave the log panel with a single line and no way to see why.
func (m *model) graphVisible(height int) bool {
	return m.graphEnabled && height-chromeHeight >= graphMinRows
}

// graphRows renders the series, one per line, each a label, a bar and the peak
// the bar is scaled against.
//
// The peak is declared on every row rather than implied by the bar height. A
// sparkline scaled to its own maximum looks identical for a quiet hour and a
// busy one, and a chart whose axis moves without saying so cannot be read.
// Printing the maximum makes every bar convertible back to a rate.
func (m *model) graphRows(width int) []string {
	st := m.p.severityStyles()

	rows := []string{graphRow("all agents", m.graphCounts, width, st.time)}
	// The per-agent series only means something when a file is open. Without
	// one there is no agent to name, and an empty second row is a mystery.
	if len(m.files) > 0 {
		rows = append(rows, graphRow(m.files[m.fileIndex].Agent, m.fileGraphCounts, width, st.category))
	}
	return rows
}

// graphRow lays out one series: label, bar, peak. Falls back to label and peak
// alone when the panel is too narrow for a bar to mean anything.
func graphRow(label string, counts []int, width int, style lipgloss.Style) string {
	name := truncateText(safeText(label), graphLabelWidth)
	right := fmt.Sprintf("peak %d/s", graphPeak(counts))

	barWidth := width - graphLabelWidth - lipgloss.Width(right) - 2
	if barWidth < 1 {
		return truncateText(name+" "+right, max(1, width))
	}

	return style.Render(fmt.Sprintf("%-*s", graphLabelWidth, name)) + " " +
		style.Render(sparkline(counts, barWidth)) + " " +
		style.Render(right)
}

// graphPeak is the largest bucket in the series, which is the value the top of
// the bar represents.
func graphPeak(counts []int) int {
	peak := 0
	for _, v := range counts {
		if v > peak {
			peak = v
		}
	}
	return peak
}

// sparkline renders counts as bar glyphs, right-aligned to the most recent
// bucket.
//
// Only the newest width buckets are drawn when there are more of them, and the
// left is padded when there are fewer. Buckets are dropped whole rather than
// averaged: a one-second spike averaged into a wider column stops being a
// spike, which is the one thing an ingest graph exists to show.
func sparkline(counts []int, width int) string {
	if width < 1 || len(counts) == 0 {
		return ""
	}

	cols := counts
	if len(cols) > width {
		cols = cols[len(cols)-width:]
	}

	var b strings.Builder
	if pad := width - len(cols); pad > 0 {
		b.WriteString(strings.Repeat(" ", pad))
	}
	peak := graphPeak(cols)
	for _, v := range cols {
		b.WriteString(graphGlyph(v, peak))
	}
	return b.String()
}

// graphGlyph maps one bucket onto the block glyphs, given the peak of the
// visible range.
//
// Index 0 of graphGlyphs is a blank, so a zero bucket reads as no traffic
// rather than as the lowest possible bar. The remaining seven steps span 1 to
// peak, which is why peak is a divisor below and not a multiplier: an integer
// division that maps peak onto the top glyph exactly, with no off-by-one that
// leaves the tallest bar one step short.
func graphGlyph(v, peak int) string {
	if v <= 0 || peak <= 0 {
		return " "
	}
	if peak <= 1 {
		return graphGlyphs[graphLevels-1]
	}
	// Rounded, not truncated. Truncating sends the midpoint of the range one
	// step low, so a steady rate of exactly half the peak draws noticeably
	// below half the height and the graph reads low across its whole range.
	idx := 1 + ((v-1)*(graphLevels-2)+(peak-1)/2)/(peak-1)
	if idx < 1 {
		idx = 1
	}
	if idx >= graphLevels {
		idx = graphLevels - 1
	}
	return graphGlyphs[idx]
}

// recordRows splits one record's message into the rows it will occupy in the
// log panel.
//
// With wrapping off this is always one truncated row, which is what the panel
// has always done and why the cursor is a record index rather than a row index.
func (m *model) recordRows(record protocol.StoredRecord, bodyWidth int) []string {
	if bodyWidth < 1 {
		bodyWidth = 1
	}
	text := safeText(record.Line)
	if !m.wrap {
		return []string{truncateText(text, bodyWidth)}
	}
	return wrapText(text, bodyWidth, wrapMaxRows)
}

// wrapText breaks a line into rows of at most width columns, stopping after
// maxRows and marking the cut.
//
// The cap is not cosmetic. `MaxLineSize` permits a 128 KiB line, which at a
// 76-column panel is over a thousand rows: uncapped, a single record is the
// entire panel and the other twenty visible rows are its continuation.
func wrapText(s string, width, maxRows int) []string {
	if width < 1 || maxRows < 1 {
		return nil
	}
	if lipgloss.Width(s) <= width {
		return []string{s}
	}

	rows := make([]string, 0, maxRows)
	rest := s
	for len(rows) < maxRows {
		chunk, remainder := breakLine(rest, width)
		rows = append(rows, chunk)
		if remainder == "" {
			return rows
		}
		rest = remainder
	}

	// Cut at the cap with text still to come. Say so: a log line that stops
	// mid-sentence with no marker reads as the whole line, and the operator
	// cannot tell that from the panel.
	last := len(rows) - 1
	rows[last] = truncateText(rows[last], max(1, width-lipgloss.Width(wrapMarker))) + wrapMarker
	return rows
}

// breakLine takes as much of s as fits in width columns and returns it with
// the remainder.
//
// The break prefers the last space in the row, and falls back to a hard cut.
// A URL, a hash, or a base64 blob has no spaces to break on, and treating those
// as unbreakable would drop the row on the floor.
func breakLine(s string, width int) (string, string) {
	if width < 1 {
		return "", s
	}

	runes := []rune(s)
	fit, used := 0, 0
	for fit < len(runes) {
		w := lipgloss.Width(string(runes[fit]))
		if used+w > width {
			break
		}
		used += w
		fit++
	}
	// Width is measured in cells and a double-width rune can exceed it on its
	// own. Consume one regardless so the loop always terminates.
	if fit == 0 {
		fit = 1
	}
	if fit >= len(runes) {
		return s, ""
	}

	head := runes[:fit]
	if at := lastSpace(head); at > 0 {
		return string(head[:at]), strings.TrimLeft(string(runes[at:]), " ")
	}
	return string(head), string(runes[fit:])
}

func lastSpace(runes []rune) int {
	for i := len(runes) - 1; i >= 0; i-- {
		if unicode.IsSpace(runes[i]) {
			return i
		}
	}
	return -1
}

// wrapIndent is the left padding for continuation rows.
//
// Aligning under the message column is the readable choice, and the gutter is
// about 28 columns wide, so on a narrow panel that alignment leaves a
// continuation row with almost nothing to show. Below wrapMinText of room the
// indent drops to two instead, which still reads as a continuation and still
// leaves the panel usable.
func wrapIndent(gutterWidth, innerWidth int) int {
	if innerWidth-gutterWidth < wrapMinText {
		return wrapNarrowIndent
	}
	return gutterWidth
}

// windowRecords picks the records to draw, measured in rows rather than
// records.
//
// The window is filled both ways from the cursor: backwards so the record under
// the cursor stays on screen, and forwards into whatever budget is left, which
// is what fills the panel when the cursor is near the top. Counting records
// against the budget drew too many once one record could occupy several rows.
//
// Walking backwards costs one step per row consumed, so it is bounded by the
// budget rather than by the record count. A thousand records stay cheap.
func (m *model) windowRecords(records []protocol.StoredRecord, budget, innerWidth int, gutterOf func(int) (string, int)) (int, int) {
	if budget < 1 {
		budget = 1
	}
	cursor := min(max(0, m.lineIndex), len(records)-1)

	used := 0
	start := cursor
	for i := cursor; i >= 0; i-- {
		_, bodyWidth := gutterOf(i)
		size := len(m.recordRows(records[i], bodyWidth))
		if used > 0 && used+size > budget {
			break
		}
		used += size
		start = i
		if used >= budget {
			break
		}
	}

	end := cursor + 1
	for i := cursor + 1; i < len(records); i++ {
		_, bodyWidth := gutterOf(i)
		size := len(m.recordRows(records[i], bodyWidth))
		if used+size > budget {
			break
		}
		used += size
		end = i + 1
		if used >= budget {
			break
		}
	}

	return start, end
}

// pushBucket appends a completed bucket and drops the oldest, keeping the ring
// bounded. A session left open for days would otherwise grow one int per
// second forever.
func pushBucket(counts []int, value int) []int {
	counts = append(counts, value)
	if len(counts) > graphBuckets {
		counts = counts[len(counts)-graphBuckets:]
	}
	return counts
}

func (m *model) renderMain(width, height int) string {
	st := m.p.severityStyles()

	innerWidth := width - textWidth
	if innerWidth < 1 {
		innerWidth = 1
	}

	// Header block: path, optional category, divider. Rows are accumulated in
	// a slice and joined without a trailing newline, which would otherwise
	// add a phantom row that Height() cannot shrink.
	chrome := 0
	rows := make([]string, 0, height)

	// Graph block, above the file header. It goes first so the newest activity
	// sits directly above the newest records, which is the pair an operator
	// reads together when correlating a spike with a line.
	if m.graphVisible(height) {
		for _, line := range m.graphRows(innerWidth) {
			rows = append(rows, line)
			chrome++
		}
	}

	if len(m.files) > 0 {
		file := m.files[m.fileIndex]
		rows = append(rows, truncateText(
			st.sectionTitle.Render(fmt.Sprintf(" %s / %s / %s ", file.Date, file.App, file.Agent)), innerWidth))
		chrome++
		if cat := m.categories[m.categoryIndex]; cat != "all" {
			rows = append(rows, st.category.Render(" Category: "+cat))
			chrome++
		}
		rows = append(rows, st.divider.Render(strings.Repeat("─", innerWidth)))
		chrome++
	}

	// Rows left for records once the border and header block are accounted for.
	textRows := height - chromeHeight - chrome

	records := m.filtered()
	if len(records) == 0 {
		if textRows > 0 {
			rows = append(rows, "  "+m.emptyState())
		}
	} else {
		// The gutter depends on the record, so the body width does too, and the
		// window has to be measured against the same widths the rows will use.
		gutterOf := func(i int) (string, int) {
			gutter := m.recordGutter(records[i], i == m.lineIndex, innerWidth)
			return gutter, max(1, innerWidth-lipgloss.Width(gutter))
		}

		start, end := m.windowRecords(records, max(1, textRows), innerWidth, gutterOf)

		for i := start; i < end; i++ {
			rec := records[i]
			selected := i == m.lineIndex
			gutter, bodyWidth := gutterOf(i)
			level := detectSeverity(rec.Line)
			chunks := m.recordRows(rec, bodyWidth)

			// The cursor highlights the record's first row only. Highlighting
			// every row of one wrapped record makes it read as several records
			// that happen to share a timestamp.
			if selected {
				rows = append(rows, st.selected.Width(innerWidth).Render(gutter+chunks[0]))
			} else {
				var badgeStyle lipgloss.Style
				switch level {
				case severityError:
					badgeStyle = st.error
				case severityWarn:
					badgeStyle = st.warn
				case severityInfo:
					badgeStyle = st.info
				case severityDebug:
					badgeStyle = st.debug
				default:
					badgeStyle = st.plain
				}
				bodyStyle := badgeStyle
				if level == severityPlain {
					bodyStyle = lipgloss.NewStyle()
				}

				stamp := rec.ReceivedAt.Format("15:04:05")
				badge, category := gutterParts(rec, innerWidth)
				rows = append(rows, " "+
					st.time.Render(stamp)+" "+
					badgeStyle.Render(badge)+
					st.category.Render(fmt.Sprintf("%-12s ", category))+
					bodyStyle.Render(chunks[0]))
			}

			if len(chunks) < 2 {
				continue
			}
			indent := strings.Repeat(" ", wrapIndent(lipgloss.Width(gutter), innerWidth))
			for _, chunk := range chunks[1:] {
				rows = append(rows, st.plain.Render(
					truncateText(indent+chunk, innerWidth)))
			}
		}
	}

	if m.err != nil {
		rows = append(rows, st.error.Render("Error: "+m.err.Error()))
	}

	return panel("Logs", strings.Join(rows, "\n"), width, height, m.borderStyle, m.focus == focusMain, m.p)
}

// recordGutter is the fixed column a record's message starts after: cursor
// marker, receipt time, severity badge, category.
//
// The badge is dropped first on a narrow panel: it is three characters of
// decoration, and the colour still carries the level even once the text is
// gone. What is not dropped is the message itself, which is the reason the
// panel exists.
func (m *model) recordGutter(record protocol.StoredRecord, selected bool, innerWidth int) string {
	badge, category := gutterParts(record, innerWidth)
	marker := " "
	if selected {
		marker = "►"
	}
	return fmt.Sprintf("%s %s %s%-12s ", marker, record.ReceivedAt.Format("15:04:05"), badge, category)
}

// gutterParts is the badge and category, on their own, for the unselected row
// where each piece is styled separately.
func gutterParts(record protocol.StoredRecord, innerWidth int) (badge, category string) {
	category = safeText(record.Category)
	if innerWidth >= minWidthForBadge {
		if tag := detectSeverity(record.Line).String(); tag != "" {
			badge = tag + " "
		}
	}
	return badge, category
}

// emptyState distinguishes an empty file from everything filtered away.
//
// "(no logs)" and "(no records match ...)" answer different questions, and
// collapsing them into one string is how a working filter looks like data loss.
func (m *model) emptyState() string {
	switch {
	case m.filter != "":
		if m.filterBad {
			return fmt.Sprintf("(bad pattern %q)", m.filter)
		}
		return fmt.Sprintf("(no records match %q)", m.filter)
	case m.agentFilter != "":
		return fmt.Sprintf("(no records from %s)", m.agentFilter)
	case len(m.categories) > 0 && m.categoryIndex > 0:
		return fmt.Sprintf("(no records in %s)", m.categories[m.categoryIndex])
	default:
		return "(no logs)"
	}
}

// keybinding is one row of the help overlay.
//
// Declared as data rather than written into the renderer so the overlay, the
// status bar and the test all read the same list. The status bar once
// advertised "?: help" with nothing bound to "?", and no test compared the two;
// TestHelpOverlayListsBindings now does.
type keybinding struct {
	keys   string
	action string
}

var keybindings = []keybinding{
	{"q, ctrl+c", "quit"},
	{"?", "toggle this help"},
	{"tab", "cycle focus: agents, files, logs"},
	{"up, k", "move up"},
	{"down, j", "move down"},
	{"left, h", "collapse a node, or step back to the files"},
	{"right, l", "expand a node, or step on to the logs"},
	{"enter", "open a node, or filter by the selected agent"},
	{"r", "rescan storage"},
	{"g", "toggle the ingest graph"},
	{"/", "filter records: tab scope, ctrl+r regex"},
	{"esc", "clear the filter, or close this"},
	{"[", "previous category"},
	{"]", "next category"},
	{"w", "toggle line wrapping"},
}

// renderHelp draws the keybinding overlay at exactly w by h.
func (m *model) renderHelp(w, h int) string {
	st := m.p.styles()

	// Two columns when there is room, otherwise one. The overlay is a fixed
	// list, so the column split only has to be legible, never interactive.
	rows := make([]string, 0, len(keybindings))
	perColumn := len(keybindings)
	if w >= 72 {
		perColumn = (len(keybindings) + 1) / 2
	}
	keyWidth := 0
	for _, b := range keybindings {
		if n := lipgloss.Width(b.keys); n > keyWidth {
			keyWidth = n
		}
	}
	for i := 0; i < perColumn; i++ {
		left := helpRow(keybindings[i], keyWidth, st)
		if i+perColumn < len(keybindings) {
			left += "    " + helpRow(keybindings[i+perColumn], keyWidth, st)
		}
		rows = append(rows, left)
	}

	body := strings.Join(rows, "\n")
	return panel("Keybindings", body, w, h, m.borderStyle, true, m.p)
}

func helpRow(b keybinding, keyWidth int, st styles) string {
	return st.helpKey.Render(fitLine(b.keys, keyWidth)) + "  " + b.action
}

func (m *model) renderStatusBar(minimal bool) string {
	st := m.p.styles()
	connected := connectedCount(m.agents)
	focusStr := map[focusPane]string{focusAgents: "Agents", focusFiles: "Files", focusMain: "Logs"}[m.focus]

	// Short hints on narrow terminals, minimal when stacked. Hints are
	// dropped in order until the bar fits so it never wraps to a second
	// line (a wrapped status bar makes the frame taller than the terminal
	// and scrolls the header out of view).
	var hints []string
	switch {
	case minimal:
		hints = []string{"[Tab] switch", "[q] quit", "[?] help"}
	case m.width < 100:
		hints = []string{"[Tab] switch", "[↑↓/jk] nav", "[←→] expand", "[Enter] open", "[q] quit", "[?] help"}
	default:
		// `g` is last so it is the first hint dropped. It was the ninth and
		// the bar runs out of room long before that, and the two hints that
		// disappeared along with it were how you quit and how you find out
		// about the key that hides the graph in the first place.
		hints = []string{"[Tab] switch", "[↑↓/jk] nav", "[←→/hl] expand", "[Enter] open",
			"[/] filter", "[r] refresh", "[?] help", "[q] quit", "[g] graph"}
	}

	counters := fmt.Sprintf("Agents: %d/%d   Files: %d   Focus: %s",
		connected, len(m.agents), len(m.files), focusStr)

	var bar string
	switch {
	case m.filtering:
		bar = fmt.Sprintf(" Filter [%s|%s] %s_", m.filterScope, m.filterMatch, m.filterBuffer)
		if m.filterBad {
			bar += "  (bad pattern)"
		}
	case m.filter != "":
		bar = " Filter: " + m.filter + "  (esc to clear)"
		if m.filterBad {
			bar += "  (bad pattern)"
		}
	}

	if bar == "" {
		// Counters plus as many hints as fit on one line, including the
		// status bar's own horizontal padding.
		budget := m.width - statusBarPadding
		bar = counters
		for _, hint := range hints {
			candidate := bar + "   " + hint
			if lipgloss.Width(candidate) > budget {
				break
			}
			bar = candidate
		}
		if lipgloss.Width(bar) > budget {
			bar = truncateText(bar, budget)
		}
	}

	// MaxWidth guarantees a single line: it clips instead of wrapping.
	// Foreground only. A background here would fill the last row of the frame,
	// which TestChromeHasNoBackground rejects.
	return st.statusBar.MaxWidth(m.width).Render(truncateText(bar, m.width))
}

func (m *model) loadSelected() tea.Cmd {
	m.loadVersion++
	m.records = nil
	m.categories = []string{"all"}
	m.categoryIndex = 0
	m.lineIndex = 0
	path := m.files[m.fileIndex].Path
	m.loadingPath = path
	m.pending = nil
	return loadRecords(path, m.loadVersion)
}

func (m *model) scan() tea.Cmd {
	m.scanVersion++
	return scanFiles(m.dataDir, m.scanVersion)
}

// filtered returns the records the log panel should show.
//
// This runs on every View() to produce about twenty rows out of up to a
// thousand records, so the result is cached. Recomputing it per frame was
// tolerable when the only work was two integer comparisons per record, and
// stops being tolerable the moment it also does a substring match.
func (m *model) filtered() []protocol.StoredRecord {
	key := m.filterKey()
	if key == m.filteredKey {
		return m.filteredCache
	}
	result := m.computeFiltered()
	m.filteredCache = result
	m.filteredKey = key
	return result
}

// filterKey identifies the inputs filtered() depends on.
//
// len(m.records) stands in for the record set: it changes on every append
// through appendRecord and drops to zero on loadSelected, and no other path
// mutates m.records. Keying on content instead would mean hashing a thousand
// records on every frame, which is the cost this cache exists to avoid.
func (m *model) filterKey() string {
	category := ""
	if len(m.categories) > 0 && m.categoryIndex > 0 {
		category = m.categories[m.categoryIndex]
	}
	return strings.Join([]string{
		m.filter,
		strconv.Itoa(int(m.filterScope)),
		strconv.Itoa(int(m.filterMatch)),
		category,
		m.agentFilter,
		strconv.Itoa(len(m.records)),
	}, "\x00")
}

func (m *model) computeFiltered() []protocol.StoredRecord {
	filtered := m.records

	// filter by category
	if len(m.categories) > 0 && m.categoryIndex > 0 {
		category := m.categories[m.categoryIndex]
		catFiltered := make([]protocol.StoredRecord, 0, len(filtered))
		for _, record := range filtered {
			if record.Category == category {
				catFiltered = append(catFiltered, record)
			}
		}
		filtered = catFiltered
	}

	// filter by agent
	if m.agentFilter != "" {
		agentFiltered := make([]protocol.StoredRecord, 0, len(filtered))
		for _, record := range filtered {
			if record.AgentID == m.agentFilter {
				agentFiltered = append(agentFiltered, record)
			}
		}
		filtered = agentFiltered
	}

	if m.filter != "" {
		matched := make([]protocol.StoredRecord, 0, len(filtered))
		for _, record := range filtered {
			if recordMatches(record, m.filter, m.filterScope, m.filterMatch, m.filterRe) {
				matched = append(matched, record)
			}
		}
		filtered = matched
	}

	return filtered
}

// recordMatches tests one record against the active text filter.
func recordMatches(record protocol.StoredRecord, needle string, scope filterScope, match filterMatch, re *regexp.Regexp) bool {
	var haystack string
	switch scope {
	case filterCategory:
		haystack = record.Category
	case filterAgent:
		haystack = record.AgentID
	default:
		haystack = record.Line
	}

	if match == matchRegex {
		// A nil re means the pattern never compiled and there is no earlier
		// good one either, so nothing is filtered out rather than everything.
		if re == nil {
			return true
		}
		return re.MatchString(haystack)
	}

	needle = strings.ToLower(needle)
	return strings.Contains(strings.ToLower(haystack), needle)
}

// filterActive reports whether anything is narrowing the panel, so the empty
// state can say "nothing matched" rather than "nothing here".
func (m *model) filterActive() bool {
	return m.filter != "" || m.agentFilter != "" ||
		(len(m.categories) > 0 && m.categoryIndex > 0)
}

func (m *model) ensureFile(file logFile) int {
	for i := range m.files {
		if m.files[i].Path == file.Path {
			return i
		}
	}
	selectedPath := ""
	if len(m.files) > 0 {
		selectedPath = m.files[m.fileIndex].Path
	}
	m.files = append(m.files, file)
	sortFiles(m.files)
	fileIndex := 0
	for i := range m.files {
		if m.files[i].Path == selectedPath {
			m.fileIndex = i
		}
		if m.files[i].Path == file.Path {
			fileIndex = i
		}
	}
	if selectedPath == "" {
		m.fileIndex = fileIndex
	}
	return fileIndex
}

func scanFiles(dataDir string, version int) tea.Cmd {
	return func() tea.Msg {
		var files []logFile
		dates, err := os.ReadDir(dataDir)
		if err != nil {
			return scanMsg{version: version, err: err}
		}
		for _, date := range dates {
			if !date.IsDir() {
				continue
			}
			apps, err := os.ReadDir(filepath.Join(dataDir, date.Name()))
			if err != nil {
				return scanMsg{version: version, err: err}
			}
			for _, app := range apps {
				if !app.IsDir() {
					continue
				}
				agents, err := os.ReadDir(filepath.Join(dataDir, date.Name(), app.Name()))
				if err != nil {
					return scanMsg{version: version, err: err}
				}
				for _, agent := range agents {
					if agent.IsDir() || filepath.Ext(agent.Name()) != ".jsonl" {
						continue
					}
					files = append(files, logFile{
						Date:  date.Name(),
						App:   app.Name(),
						Agent: strings.TrimSuffix(agent.Name(), ".jsonl"),
						Path:  filepath.Join(dataDir, date.Name(), app.Name(), agent.Name()),
					})
				}
			}
		}
		sortFiles(files)
		return scanMsg{version: version, files: files}
	}
}

func loadRecords(path string, version int) tea.Cmd {
	return func() tea.Msg {
		file, err := os.Open(path)
		if err != nil {
			return loadMsg{version: version, path: path, err: err}
		}
		defer file.Close()

		records := make([]protocol.StoredRecord, 0, maxVisibleRecords)
		next := 0
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64*1024), protocol.MaxFrameSize)
		for scanner.Scan() {
			var record protocol.StoredRecord
			if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
				return loadMsg{version: version, path: path, err: err}
			}
			if len(records) < maxVisibleRecords {
				records = append(records, record)
				continue
			}
			records[next] = record
			next = (next + 1) % maxVisibleRecords
		}
		if err := scanner.Err(); err != nil {
			return loadMsg{version: version, path: path, err: err}
		}
		if next > 0 {
			records = append(records[next:], records[:next]...)
		}
		return loadMsg{version: version, path: path, records: records}
	}
}

func sortFiles(files []logFile) {
	sort.Slice(files, func(i, j int) bool {
		if files[i].Date != files[j].Date {
			return files[i].Date > files[j].Date
		}
		if files[i].App != files[j].App {
			return files[i].App < files[j].App
		}
		return files[i].Agent < files[j].Agent
	})
}

func recordCategories(records []protocol.StoredRecord) []string {
	seen := map[string]bool{"all": true}
	categories := []string{"all"}
	for _, record := range records {
		if !seen[record.Category] {
			seen[record.Category] = true
			categories = append(categories, record.Category)
		}
	}
	sort.Strings(categories[1:])
	return categories
}

func categoryIndex(categories []string, selected string) int {
	for i, category := range categories {
		if category == selected {
			return i
		}
	}
	return 0
}

func appendRecord(records []protocol.StoredRecord, record protocol.StoredRecord) []protocol.StoredRecord {
	for _, existing := range records {
		if existing.ReceivedAt.Equal(record.ReceivedAt) && existing.AgentID == record.AgentID && existing.App == record.App && existing.Category == record.Category && existing.Line == record.Line {
			return records
		}
	}
	records = append(records, record)
	if len(records) > maxVisibleRecords {
		records = records[len(records)-maxVisibleRecords:]
	}
	return records
}

func connectedCount(agents map[string]agentStatus) int {
	count := 0
	for _, agent := range agents {
		if agent.Connected {
			count++
		}
	}
	return count
}

func safeText(value string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, value)
}
