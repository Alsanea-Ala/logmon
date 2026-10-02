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
	minTTYHeight     = 10
	sidebarMinWidth  = 30
	stackedSidebarH  = 10
	sidebarMinHeight = 4
	mainMinWidth     = 20
	separatorWidth   = 2

	// statusBarStyle pads one column each side.
	statusBarPadding = 2
)

// layout describes panel geometry for one frame.
type layout struct {
	stacked  bool
	sidebarW int
	sidebarH int
	mainW    int
	mainH    int
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
		return layout{
			stacked:  false,
			sidebarW: sw,
			sidebarH: availH,
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

	// Narrow: stack the sidebar above the logs, giving each a share of the
	// available height so neither panel is squeezed out on short terminals.
	sh := min(stackedSidebarH, availH/2)
	sh = max(sh, sidebarMinHeight)
	if sh > availH-sidebarMinHeight {
		sh = availH - sidebarMinHeight
	}
	if sh < 1 {
		sh = 1
	}
	return layout{stacked: true, sidebarW: w, sidebarH: sh, mainW: w, mainH: availH - sh}
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

const (
	focusSidebar focusPane = iota
	focusMain
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
	focus         focusPane
	sidebarIndex  int
	fileTree      *fileNode
	treeFlattened []*fileNode
	filter        string
	filtering     bool
	filterBuffer  string
	agentFilter   string // filter logs by agent ID
}

func newModel(dataDir string) *model {
	return &model{
		dataDir:    dataDir,
		categories: []string{"all"},
		agents:     make(map[string]agentStatus),
		focus:      focusSidebar,
	}
}

// Styles
var (
	sidebarStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1)

	mainStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1)

	focusedSidebarStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("39")).
				Padding(0, 1)

	focusedMainStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("39")).
				Padding(0, 1)

	sectionTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("39"))

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("39"))

	connectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("46"))

	offlineStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196"))

	// filterStyle marks the active filter. No background fill: the chrome
	// bars stay transparent so only the text colour carries meaning.
	filterStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15"))

	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("15"))

	categoryStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("205"))

	timeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244"))

	dividerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Padding(0, 1)
)

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

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "tab":
		m.focus = 1 - m.focus
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
	case focusSidebar:
		if m.sidebarIndex > 0 {
			m.sidebarIndex--
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
	case focusSidebar:
		maxIdx := m.getSidebarMaxIndex()
		if m.sidebarIndex < maxIdx {
			m.sidebarIndex++
		}
	case focusMain:
		if m.lineIndex+1 < len(m.filtered()) {
			m.lineIndex++
		}
	}
	return m, nil
}

func (m *model) moveLeft() (tea.Model, tea.Cmd) {
	if m.focus == focusMain {
		m.focus = focusSidebar
		return m, nil
	}
	// collapse folder under cursor if it's a tree node
	agentCount := len(m.agents)
	if m.sidebarIndex >= agentCount && len(m.treeFlattened) > 0 {
		treeIdx := m.sidebarIndex - agentCount
		if treeIdx < len(m.treeFlattened) {
			node := m.treeFlattened[treeIdx]
			if node.Expanded && len(node.Children) > 0 {
				node.Expanded = false
				m.flattenTree()
				// clamp sidebarIndex if it went out of bounds after collapse
				if m.sidebarIndex > m.getSidebarMaxIndex() {
					m.sidebarIndex = m.getSidebarMaxIndex()
				}
			}
		}
	}
	return m, nil
}

func (m *model) moveRight() (tea.Model, tea.Cmd) {
	if m.focus == focusSidebar {
		m.focus = focusMain
		return m, nil
	}
	// expand folder under cursor if it's a tree node
	agentCount := len(m.agents)
	if m.sidebarIndex >= agentCount && len(m.treeFlattened) > 0 {
		treeIdx := m.sidebarIndex - agentCount
		if treeIdx < len(m.treeFlattened) {
			node := m.treeFlattened[treeIdx]
			if !node.Expanded && len(node.Children) > 0 {
				node.Expanded = true
				m.flattenTree()
			}
		}
	}
	return m, nil
}

func (m *model) handleEnter() (tea.Model, tea.Cmd) {
	if m.focus != focusSidebar {
		return m, nil
	}
	agentCount := len(m.agents)

	// if on an agent row
	if m.sidebarIndex < agentCount {
		agentIDs := make([]string, 0, len(m.agents))
		for id := range m.agents {
			agentIDs = append(agentIDs, id)
		}
		sort.Strings(agentIDs)
		if m.sidebarIndex < len(agentIDs) {
			m.agentFilter = agentIDs[m.sidebarIndex]
			m.lineIndex = 0
		}
		return m, nil
	}

	// if on a tree node
	treeIdx := m.sidebarIndex - agentCount
	if treeIdx >= 0 && treeIdx < len(m.treeFlattened) {
		node := m.treeFlattened[treeIdx]
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

func (m *model) getSidebarMaxIndex() int {
	agentCount := len(m.agents)
	treeCount := len(m.treeFlattened)
	return max(0, agentCount+treeCount-1)
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
	header := headerStyle.Render(headerText)

	// Status bar (minimal on narrow terminals)
	statusBar := m.renderStatusBar(lay.stacked)

	sidebar := m.renderSidebar(lay.sidebarW, lay.sidebarH)
	main := m.renderMain(lay.mainW, lay.mainH)

	var content string
	if lay.stacked {
		content = lipgloss.JoinVertical(lipgloss.Left, sidebar, main)
	} else {
		content = lipgloss.JoinHorizontal(lipgloss.Top, sidebar, "  ", main)
	}

	// Tight assembly: header sits flush above the panels.
	frame := header + "\n" + content + "\n" + statusBar

	// Never emit more rows than the terminal has: an overflowing frame
	// scrolls the header off the top.
	return clampHeight(frame, h)
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

func (m *model) renderSidebar(width, height int) string {
	style := sidebarStyle
	if m.focus == focusSidebar {
		style = focusedSidebarStyle
	}

	innerWidth := width - textWidth
	if innerWidth < 1 {
		innerWidth = 1
	}

	agentIDs := make([]string, 0, len(m.agents))
	for id := range m.agents {
		agentIDs = append(agentIDs, id)
	}
	sort.Strings(agentIDs)

	agentCount := len(agentIDs)

	type sidebarRow struct {
		text     string
		selected bool
	}

	// Collect selectable rows (agents first, then tree nodes) so the
	// cursor index maps 1:1 and the visible window can follow it.
	agentRows := make([]sidebarRow, 0, agentCount)
	for i, id := range agentIDs {
		agent := m.agents[id]
		state := "●"
		stateStyle := connectedStyle
		if !agent.Connected {
			state = "○"
			stateStyle = offlineStyle
		}
		plain := fmt.Sprintf("%s %s %s", state, agent.ID, safeText(agent.Hostname))
		var line string
		if m.focus == focusSidebar && i == m.sidebarIndex {
			line = selectedStyle.Width(innerWidth).Render(truncateText(plain, innerWidth))
		} else {
			line = stateStyle.Render(state) + " " + truncateText(
				fmt.Sprintf("%s %s", agent.ID, safeText(agent.Hostname)), innerWidth-2)
		}
		agentRows = append(agentRows, sidebarRow{text: line, selected: m.focus == focusSidebar && i == m.sidebarIndex})
	}

	treeRows := make([]sidebarRow, 0, len(m.treeFlattened))
	for i, node := range m.treeFlattened {
		unifiedIdx := agentCount + i
		indent := strings.Repeat("  ", nodeDepth(node))
		prefix := "  "
		if len(node.Children) > 0 {
			if node.Expanded {
				prefix = "▼ "
			} else {
				prefix = "▶ "
			}
		}
		name := node.Name
		if node.IsFile {
			name = "● " + name
		}
		line := indent + prefix + name
		if m.focus == focusSidebar && unifiedIdx == m.sidebarIndex {
			line = selectedStyle.Width(innerWidth).Render(truncateText(line, innerWidth))
		} else {
			line = truncateText(line, innerWidth)
		}
		treeRows = append(treeRows, sidebarRow{text: line, selected: m.focus == focusSidebar && unifiedIdx == m.sidebarIndex})
	}

	// Window selectable rows around the cursor so short panels scroll
	// instead of overflowing. Both section titles are always drawn.
	selectable := append(agentRows, treeRows...)
	rowBudget := height - chromeHeight - 2 // border + Agents/Files titles
	if rowBudget < 1 {
		rowBudget = 1
	}
	start := 0
	if len(selectable) > rowBudget {
		cursor := m.sidebarIndex
		if cursor < 0 {
			cursor = 0
		}
		if cursor >= len(selectable) {
			cursor = len(selectable) - 1
		}
		start = cursor - rowBudget/2
		if start < 0 {
			start = 0
		}
		if start+rowBudget > len(selectable) {
			start = len(selectable) - rowBudget
		}
	}
	end := start + rowBudget
	if end > len(selectable) {
		end = len(selectable)
	}

	// Split visible rows back into agent/tree sections for titles.
	var visibleAgents, visibleTree []sidebarRow
	if start < agentCount {
		agentEnd := end
		if agentEnd > agentCount {
			agentEnd = agentCount
		}
		visibleAgents = selectable[start:agentEnd]
	}
	treeStart := start - agentCount
	if treeStart < 0 {
		treeStart = 0
	}
	treeEnd := end - agentCount
	if treeEnd > len(treeRows) {
		treeEnd = len(treeRows)
	}
	if treeEnd > treeStart {
		visibleTree = treeRows[treeStart:treeEnd]
	}

	// Lines are joined without a trailing newline: a trailing "\n" would add
	// a phantom row that Height() cannot shrink, pushing the status bar off.
	lines := make([]string, 0, rowBudget+2)
	lines = append(lines, sectionTitleStyle.Render(" Agents "))
	if agentCount == 0 {
		lines = append(lines, "  (none — start an agent)")
	} else {
		for _, row := range visibleAgents {
			lines = append(lines, row.text)
		}
	}
	lines = append(lines, sectionTitleStyle.Render(" Files "))
	if len(m.treeFlattened) == 0 {
		lines = append(lines, "  (no logs)")
	} else {
		for _, row := range visibleTree {
			lines = append(lines, row.text)
		}
	}

	return style.Width(width - chromeWidth).Height(height - chromeHeight).Render(strings.Join(lines, "\n"))
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
	style := mainStyle
	if m.focus == focusMain {
		style = focusedMainStyle
	}

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
			sectionTitleStyle.Render(fmt.Sprintf(" %s / %s / %s ", file.Date, file.App, file.Agent)), innerWidth))
		chrome++
		if cat := m.categories[m.categoryIndex]; cat != "all" {
			rows = append(rows, categoryStyle.Render(" Category: "+cat))
			chrome++
		}
		rows = append(rows, dividerStyle.Render(strings.Repeat("─", innerWidth)))
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
				rows = append(rows, selectedStyle.Width(innerWidth).Render(gutter+body))
			} else {
				rows = append(rows, marker+
					timeStyle.Render(stamp)+
					categoryStyle.Render(fmt.Sprintf(" %-12s", category))+
					body)
			}
		}
	}

	if m.err != nil {
		rows = append(rows, errorStyle.Render("Error: "+m.err.Error()))
	}

	return style.Width(width - chromeWidth).Height(height - chromeHeight).Render(strings.Join(rows, "\n"))
}

func (m *model) renderStatusBar(minimal bool) string {
	connected := connectedCount(m.agents)
	focusStr := map[focusPane]string{focusSidebar: "Sidebar", focusMain: "Logs"}[m.focus]

	// Short hints on narrow terminals, minimal when stacked. Hints are
	// dropped in order until the bar fits so it never wraps to a second
	// line (a wrapped status bar makes the frame taller than the terminal
	// and scrolls the header out of view).
	var hints []string
	switch {
	case minimal:
		hints = []string{"Tab: switch", "q: quit", "?: help"}
	case m.width < 100:
		hints = []string{"Tab: switch", "↑↓/jk: nav", "←→: expand", "Enter: open", "q: quit", "r: refresh"}
	default:
		hints = []string{"Tab: switch", "↑↓/jk: nav", "←→/hl: expand", "Enter: open", "q: quit", "r: refresh", "/: filter"}
	}

	counters := fmt.Sprintf("Agents: %d/%d  |  Files: %d  |  Focus: %s",
		connected, len(m.agents), len(m.files), focusStr)

	var bar string
	if m.filtering {
		bar = " Filter: " + filterStyle.Render(m.filterBuffer+"_")
	} else if m.filter != "" {
		bar = " Filter: " + filterStyle.Render(m.filter) + "  (esc to clear)"
	}

	if bar == "" {
		// Counters plus as many hints as fit on one line, including the
		// status bar's own horizontal padding.
		budget := m.width - statusBarPadding
		bar = counters
		for _, hint := range hints {
			candidate := bar + "  |  " + hint
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
	return statusBarStyle.MaxWidth(m.width).Render(truncateText(bar, m.width))
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
