package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

type focusPane int

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

	// Palette and chrome. p is the single source of colour truth; every style
	// below is built from it rather than carrying its own literals.
	p           palette
	borderStyle lipgloss.Border
	showHelp    bool
}

func newModel(dataDir string) *model {
	return &model{
		dataDir:     dataDir,
		categories:  []string{"all"},
		agents:      make(map[string]agentStatus),
		focus:       focusAgents,
		p:           defaultPalette,
		borderStyle: lipgloss.RoundedBorder(),
	}
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
}

func (m *model) Init() tea.Cmd {
	return m.scan()
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

	case recordMsg:
		m.scanVersion++
		record := protocol.StoredRecord(message.(recordMsg))
		file := logFile{
			Date:  record.ReceivedAt.Format("2006-01-02"),
			App:   record.App,
			Agent: record.AgentID,
			Path:  filepath.Join(m.dataDir, record.ReceivedAt.Format("2006-01-02"), record.App, record.AgentID+".jsonl"),
		}
		index := m.ensureFile(file)
		if index == m.fileIndex {
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

func (m *model) handleFilterInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filtering = false
		m.filterBuffer = ""
		return m, nil
	case "enter":
		m.filter = m.filterBuffer
		m.filtering = false
		m.filterBuffer = ""
		m.applyFilter()
		return m, nil
	case "backspace":
		if len(m.filterBuffer) > 0 {
			m.filterBuffer = m.filterBuffer[:len(m.filterBuffer)-1]
		}
		return m, nil
	default:
		if len(msg.String()) == 1 {
			m.filterBuffer += msg.String()
		}
		return m, nil
	}
}

func (m *model) applyFilter() {
	// Filtering records and sidebar entries is not implemented; only the
	// selection reset is live. The focus branches that used to sit here were
	// empty, so staticcheck flagged them. Do not read them as a partial
	// implementation.
	m.lineIndex = 0
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
	st := m.p.styles()
	innerWidth := max(1, width-textWidth)
	rowBudget := max(1, height-chromeHeight)

	ids := m.sortedAgentIDs()
	start, end := windowAround(len(ids), m.agentIndex, rowBudget)

	lines := make([]string, 0, rowBudget)
	if len(ids) == 0 {
		lines = append(lines, "  (none \u2014 start an agent)")
	}
	for i := start; i < end; i++ {
		agent := m.agents[ids[i]]
		state, stateStyle := "\u25cf", st.connected
		if !agent.Connected {
			state, stateStyle = "\u25cb", st.offline
		}
		plain := fmt.Sprintf("%s %s %s", state, agent.ID, safeText(agent.Hostname))
		if m.focus == focusAgents && i == m.agentIndex {
			lines = append(lines, st.selected.Width(innerWidth).Render(truncateText(plain, innerWidth)))
			continue
		}
		lines = append(lines, stateStyle.Render(state)+" "+truncateText(
			fmt.Sprintf("%s %s", agent.ID, safeText(agent.Hostname)), max(1, innerWidth-2)))
	}

	return panel("Agents", strings.Join(lines, "\n"), width, height, m.borderStyle, m.focus == focusAgents, m.p)
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

func (m *model) renderMain(width, height int) string {
	st := m.p.styles()

	innerWidth := width - textWidth
	if innerWidth < 1 {
		innerWidth = 1
	}

	// Header block: path, optional category, divider. Rows are accumulated in
	// a slice and joined without a trailing newline, which would otherwise
	// add a phantom row that Height() cannot shrink.
	chrome := 0
	rows := make([]string, 0, height)
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
			rows = append(rows, "  (no logs)")
		}
	} else {
		available := min(max(1, textRows), len(records))
		start := max(0, m.lineIndex-available+1)
		end := min(len(records), start+available)

		for i := start; i < end; i++ {
			marker := " "
			if i == m.lineIndex {
				marker = "►"
			}
			rec := records[i]
			stamp := rec.ReceivedAt.Format("15:04:05")
			category := safeText(rec.Category)
			// Fixed gutter (marker + time + padded category) keeps messages
			// starting at the same column.
			gutter := fmt.Sprintf("%s %s %-12s ", marker, stamp, category)
			body := truncateText(safeText(rec.Line), max(1, innerWidth-lipgloss.Width(gutter)))

			if i == m.lineIndex {
				rows = append(rows, st.selected.Width(innerWidth).Render(gutter+body))
			} else {
				rows = append(rows, marker+
					st.time.Render(stamp)+
					st.category.Render(fmt.Sprintf(" %-12s", category))+
					body)
			}
		}
	}

	if m.err != nil {
		rows = append(rows, st.error.Render("Error: "+m.err.Error()))
	}

	return panel("Logs", strings.Join(rows, "\n"), width, height, m.borderStyle, m.focus == focusMain, m.p)
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
	{"/", "filter records"},
	{"esc", "clear the filter, or close this"},
	{"[", "previous category"},
	{"]", "next category"},
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
		hints = []string{"[Tab] switch", "[↑↓/jk] nav", "[←→/hl] expand", "[Enter] open",
			"[/] filter", "[r] refresh", "[?] help", "[q] quit"}
	}

	counters := fmt.Sprintf("Agents: %d/%d   Files: %d   Focus: %s",
		connected, len(m.agents), len(m.files), focusStr)

	var bar string
	if m.filtering {
		bar = " Filter: " + st.error.Render(m.filterBuffer+"_")
	} else if m.filter != "" {
		bar = " Filter: " + st.error.Render(m.filter) + "  (esc to clear)"
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

func (m *model) filtered() []protocol.StoredRecord {
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

	return filtered
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
