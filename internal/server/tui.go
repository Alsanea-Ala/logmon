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

const maxVisibleRecords = 1000

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
	baseStyle = lipgloss.NewStyle().
			Padding(0, 1)

	sidebarStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240"))

	mainStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240"))

	focusedSidebarStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("39"))

	focusedMainStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("39"))

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

	filterStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("226")).
			Background(lipgloss.Color("236"))

	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")).
			Background(lipgloss.Color("236"))

	categoryStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("205"))

	timeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244"))

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("236")).
			Padding(0, 1)

	headerVersionStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("244")).
				Background(lipgloss.Color("236")).
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
	m.lineIndex = 0
	if m.focus == focusMain {
		// filter records
	} else {
		// filter sidebar
	}
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
	if m.width == 0 || m.height == 0 {
		return "Loading..."
	}

	sidebarWidth := m.width / 3
	if sidebarWidth < 30 {
		sidebarWidth = 30
	}
	mainWidth := m.width - sidebarWidth - 4

	// Header bar (full width) - single styled string to avoid JoinHorizontal collapse
	headerText := fmt.Sprintf(" LOGMON DASHBOARD %*s v%s ", (m.width - len(" LOGMON DASHBOARD v"+Version) - 2), "", Version)
	header := headerStyle.Width(m.width).Render(headerText)

	// Status bar
	statusBar := m.renderStatusBar()

	// Panel height = total - header(1) - gap(1) - status(1) - gap(1) = height - 4
	panelHeight := m.height - 4
	if panelHeight < 5 {
		panelHeight = 5
	}

	sidebar := m.renderSidebar(sidebarWidth, panelHeight)
	main := m.renderMain(mainWidth, panelHeight)

	content := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, "  ", main)

	return lipgloss.JoinVertical(lipgloss.Left, header, "", content, "", statusBar)
}

func (m *model) renderSidebar(width, height int) string {
	var b strings.Builder

	style := sidebarStyle
	if m.focus == focusSidebar {
		style = focusedSidebarStyle
	}

	innerWidth := width - 4

	// Agents section
	agentsTitle := sectionTitleStyle.Render(" Agents ")
	b.WriteString(agentsTitle)
	b.WriteString("\n")

	agentIDs := make([]string, 0, len(m.agents))
	for id := range m.agents {
		agentIDs = append(agentIDs, id)
	}
	sort.Strings(agentIDs)

	agentCount := len(agentIDs)

	if agentCount == 0 {
		b.WriteString("  (none — start an agent)")
		b.WriteString("\n")
	} else {
		for i, id := range agentIDs {
			agent := m.agents[id]
			state := "●"
			stateStyle := connectedStyle
			if !agent.Connected {
				state = "○"
				stateStyle = offlineStyle
			}

			line := fmt.Sprintf("%s %s %-20s", state, agent.ID, safeText(agent.Hostname))
			if m.focus == focusSidebar && i == m.sidebarIndex {
				line = selectedStyle.Width(innerWidth).Render(line)
			} else {
				line = fmt.Sprintf("%s %s %s", stateStyle.Render(state), agent.ID, safeText(agent.Hostname))
				if len(line) > innerWidth {
					line = line[:innerWidth]
				}
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")

	// Files section
	filesTitle := sectionTitleStyle.Render(" Files ")
	b.WriteString(filesTitle)
	b.WriteString("\n")

	if len(m.treeFlattened) == 0 {
		b.WriteString("  (no logs)")
		b.WriteString("\n")
	} else {
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
				line = selectedStyle.Width(innerWidth).Render(line)
			} else {
				if len(line) > innerWidth {
					line = line[:innerWidth]
				}
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	return style.Width(width).Height(height).Render(b.String())
}

func nodeDepth(node *fileNode) int {
	return node.Depth
}

func (m *model) renderMain(width, height int) string {
	var b strings.Builder

	style := mainStyle
	if m.focus == focusMain {
		style = focusedMainStyle
	}

	innerWidth := width - 4

	// Header
	if len(m.files) > 0 {
		file := m.files[m.fileIndex]
		header := fmt.Sprintf(" %s / %s / %s ", file.Date, file.App, file.Agent)
		b.WriteString(sectionTitleStyle.Render(header))
		b.WriteString("\n")
		cat := m.categories[m.categoryIndex]
		if cat != "all" {
			b.WriteString(categoryStyle.Render(" Category: " + cat))
			b.WriteString("\n")
		}
		b.WriteString(strings.Repeat("─", innerWidth))
		b.WriteString("\n")
	}

	records := m.filtered()
	if len(records) == 0 {
		b.WriteString("  (no logs)")
		b.WriteString("\n")
	} else {
		available := max(1, height-8)
		start := max(0, m.lineIndex-available+1)
		end := min(len(records), start+available)

		for i := start; i < end; i++ {
			marker := " "
			if i == m.lineIndex {
				marker = "►"
			}
			rec := records[i]
			timeStr := timeStyle.Render(rec.ReceivedAt.Format("15:04:05"))
			catStr := categoryStyle.Render(fmt.Sprintf("%-12s", safeText(rec.Category)))
			line := fmt.Sprintf("%s %s %s %s", marker, timeStr, catStr, safeText(rec.Line))

			if i == m.lineIndex {
				line = selectedStyle.Render(line)
			} else if len(line) > innerWidth {
				line = line[:innerWidth]
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	if m.err != nil {
		b.WriteString("\n")
		b.WriteString(errorStyle.Render("Error: " + m.err.Error()))
	}

	return style.Width(width).Height(height).Render(b.String())
}

func (m *model) renderStatusBar() string {
	connected := connectedCount(m.agents)
	focusStr := map[focusPane]string{focusSidebar: "Sidebar", focusMain: "Logs"}[m.focus]

	// Short hints on narrow terminals
	var hints string
	if m.width < 100 {
		hints = "Tab: switch  ↑↓/jk: nav  ←→: expand  Enter: open  q: quit  r: refresh"
	} else {
		hints = "Tab: switch  ↑↓/jk: nav  ←→/hl: expand  Enter: open  q: quit  r: refresh  /: filter"
	}

	bar := fmt.Sprintf(" Agents: %d/%d  |  Files: %d  |  Focus: %s  |  %s",
		connected, len(m.agents), len(m.files), focusStr, hints)
	if m.filtering {
		bar = " Filter: " + filterStyle.Render(m.filterBuffer+"_")
	} else if m.filter != "" {
		bar = " Filter: " + filterStyle.Render(m.filter) + "  (esc to clear)"
	}
	return statusBarStyle.Width(m.width).Render(bar)
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

func truncate(value string, width int) string {
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	return string(runes[:max(0, width-3)]) + "..."
}

func safeText(value string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, value)
}
