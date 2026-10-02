package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Alsanea-Ala/logmon/internal/protocol"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// bgSelectRe matches an SGR background-select sequence.
var bgSelectRe = regexp.MustCompile(`\x1b\[[0-9;]*(?:4[0-7]|10[0-7]|48;5;[0-9]+)m`)

// TestChromeHasNoBackground keeps the header and status bar transparent.
// Background fills on the chrome bars read as solid blocks against the
// terminal background; only the selected-row highlight may use one.
func TestChromeHasNoBackground(t *testing.T) {
	for _, filter := range []string{"", "err"} {
		m := newModel(t.TempDir())
		m.width, m.height = 120, 20
		m.agents["prod-1"] = agentStatus{ID: "prod-1", Hostname: "web-01", Connected: true}
		m.files = []logFile{{Date: "2026-10-02", App: "test", Agent: "prod-1"}}
		m.categories = []string{"all"}
		m.filter = filter

		lines := strings.Split(m.View(), "\n")
		for _, i := range []int{0, len(lines) - 1} {
			if bgSelectRe.MatchString(lines[i]) {
				t.Errorf("filter=%q: chrome row %d has a background fill: %q", filter, i, lines[i])
			}
		}
	}
}

func TestTUINavigationAndSafeOutput(t *testing.T) {
	now := time.Now().UTC()
	m := newModel(t.TempDir())
	m.files = []logFile{{Date: now.Format(time.DateOnly), App: "billing", Agent: "prod-1", Path: "unused"}}
	m.records = []protocol.StoredRecord{
		{ReceivedAt: now, Category: "access", Line: "ok"},
		{ReceivedAt: now, Category: "error", Line: "bad\x1b[2J"},
	}
	m.categories = recordCategories(m.records)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
	if m.categories[m.categoryIndex] != "access" || len(m.filtered()) != 1 {
		t.Fatalf("category navigation failed: %+v", m.categories)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
	if strings.Contains(m.View(), "\x1b") {
		t.Fatal("view contains terminal escape sequence")
	}
}

func TestScanFilesSortsAndFilters(t *testing.T) {
	dir := t.TempDir()
	paths := []string{
		filepath.Join(dir, "2026-09-12", "billing", "prod-2.jsonl"),
		filepath.Join(dir, "2026-09-13", "auth", "prod-1.jsonl"),
		filepath.Join(dir, "2026-09-13", "billing", "prod-1.jsonl"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "2026-09-13", "billing", "ignore.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	message := scanFiles(dir, 7)().(scanMsg)
	if message.err != nil {
		t.Fatal(message.err)
	}
	if message.version != 7 || len(message.files) != 3 {
		t.Fatalf("unexpected scan: %+v", message)
	}
	got := make([]string, len(message.files))
	for i, file := range message.files {
		got[i] = file.Date + "/" + file.App + "/" + file.Agent
	}
	if strings.Join(got, ",") != "2026-09-13/auth/prod-1,2026-09-13/billing/prod-1,2026-09-12/billing/prod-2" {
		t.Fatalf("unexpected order: %v", got)
	}
}

func TestLoadRecordsKeepsLatestAndReportsMalformedData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "logs.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for i := 0; i < maxVisibleRecords+2; i++ {
		if err := encoder.Encode(protocol.StoredRecord{Line: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	message := loadRecords(path, 4)().(loadMsg)
	if message.err != nil {
		t.Fatal(message.err)
	}
	if message.version != 4 || len(message.records) != maxVisibleRecords || message.records[0].Line != "2" || message.records[len(message.records)-1].Line != "1001" {
		t.Fatalf("unexpected loaded record window: first=%q last=%q len=%d", message.records[0].Line, message.records[len(message.records)-1].Line, len(message.records))
	}

	malformed := filepath.Join(dir, "malformed.jsonl")
	if err := os.WriteFile(malformed, []byte("{\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if message := loadRecords(malformed, 5)().(loadMsg); message.err == nil {
		t.Fatal("expected malformed record error")
	}
}

func TestViewRendersHeader(t *testing.T) {
	sizes := []struct {
		name   string
		width  int
		height int
	}{
		{"wide", 250, 60},
		{"desktop", 120, 30},
		{"medium", 100, 30},
		{"narrow", 80, 24},
		{"short", 120, 14},
		{"tiny", 20, 5},
	}
	for _, size := range sizes {
		t.Run(size.name, func(t *testing.T) {
			m := newModel(t.TempDir())
			m.width, m.height = size.width, size.height
			out := m.View()
			if size.width < 40 || size.height < 10 {
				if out == "" {
					t.Fatal("view must never be empty")
				}
				return
			}
			lines := strings.SplitN(out, "\n", 2)
			if !strings.Contains(lines[0], "LOGMON DASHBOARD") {
				t.Fatalf("header missing at %dx%d, first line: %q", size.width, size.height, lines[0])
			}
			if !strings.Contains(out, "v"+Version) {
				t.Fatalf("version missing at %dx%d", size.width, size.height)
			}
			if !strings.Contains(out, "Agents:") {
				t.Fatalf("status bar missing at %dx%d", size.width, size.height)
			}
		})
	}
}

// TestViewFitsTerminal guards the frame against overflowing the terminal in
// either axis. An oversized frame makes the terminal scroll, which pushes the
// header out of view, so both dimensions are checked at every breakpoint.
func TestViewFitsTerminal(t *testing.T) {
	sizes := []struct {
		name   string
		width  int
		height int
	}{
		{"wide", 250, 60},
		{"desktop", 140, 40},
		{"laptop", 120, 30},
		{"medium", 100, 24},
		{"stacked", 90, 20},
		{"narrow", 80, 24},
		{"small", 60, 18},
		{"short", 45, 12},
	}
	for _, size := range sizes {
		t.Run(size.name, func(t *testing.T) {
			m := newModel(t.TempDir())
			m.width, m.height = size.width, size.height
			m.agents["prod-1"] = agentStatus{ID: "prod-1", Hostname: "web-01", Connected: true}
			m.files = []logFile{{Date: "2026-10-02", App: "test", Agent: "prod-1"}}
			m.categories = []string{"all"}
			base := time.Date(2026, 10, 2, 1, 22, 3, 0, time.UTC)
			for i := range 40 {
				m.records = append(m.records, protocol.StoredRecord{
					Category:   "access",
					Line:       strings.Repeat("x", 200),
					ReceivedAt: base.Add(time.Duration(i) * time.Minute),
				})
			}
			m.lineIndex = 30

			lines := strings.Split(m.View(), "\n")
			if len(lines) != size.height {
				t.Fatalf("frame is %d rows, terminal is %d: %s", len(lines), size.height, size.name)
			}
			for i, line := range lines {
				if w := lipgloss.Width(line); w > size.width {
					t.Fatalf("row %d is %d cols, terminal is %d: %s", i, w, size.width, size.name)
				}
			}
			if !strings.Contains(lines[0], "LOGMON DASHBOARD") {
				t.Fatalf("header not on first row: %q", lines[0])
			}
			if last := lines[len(lines)-1]; !strings.Contains(last, "Agents:") {
				t.Fatalf("status bar not on last row: %q", last)
			}
		})
	}
}

// TestResizeKeepsFrameValid drives real resize events and re-checks the frame
// invariants after each, so terminal resize handling stays correct.
func TestResizeKeepsFrameValid(t *testing.T) {
	sizes := []tea.WindowSizeMsg{
		{Width: 80, Height: 24},
		{Width: 200, Height: 50},
		{Width: 60, Height: 20},
		{Width: 120, Height: 30},
		{Width: 45, Height: 12},
	}
	m := newModel(t.TempDir())
	m.agents["prod-1"] = agentStatus{ID: "prod-1", Hostname: "web-01", Connected: true}
	m.files = []logFile{{Date: "2026-10-02", App: "test", Agent: "prod-1"}}
	m.categories = []string{"all"}
	base := time.Date(2026, 10, 2, 1, 22, 3, 0, time.UTC)
	for i := range 200 {
		m.records = append(m.records, protocol.StoredRecord{
			Category:   "access",
			Line:       strings.Repeat("x", 200),
			ReceivedAt: base.Add(time.Duration(i) * time.Minute),
		})
	}
	m.lineIndex = 100

	for _, size := range sizes {
		updated, _ := m.Update(size)
		m = updated.(*model)

		lines := strings.Split(m.View(), "\n")
		if len(lines) != size.Height {
			t.Fatalf("after resize to %dx%d: frame is %d rows", size.Width, size.Height, len(lines))
		}
		for i, line := range lines {
			if w := lipgloss.Width(line); w > size.Width {
				t.Fatalf("after resize to %dx%d: row %d is %d cols", size.Width, size.Height, i, w)
			}
		}
		if !strings.Contains(lines[0], "LOGMON DASHBOARD") {
			t.Fatalf("after resize to %dx%d: header missing", size.Width, size.Height)
		}
		if last := lines[len(lines)-1]; !strings.Contains(last, "Agents:") {
			t.Fatalf("after resize to %dx%d: status bar missing, last row %q", size.Width, size.Height, last)
		}
	}
}

// TestStatusBarStaysOnOneLine ensures the status bar never wraps, since a
// wrapped bar adds a row and scrolls the header away.
func TestStatusBarStaysOnOneLine(t *testing.T) {
	for _, width := range []int{45, 60, 80, 100, 120, 140, 250} {
		m := newModel(t.TempDir())
		m.width, m.height = width, 24
		m.agents["prod-1"] = agentStatus{ID: "prod-1", Hostname: "web-01", Connected: true}
		bar := m.renderStatusBar(false)
		if strings.Contains(bar, "\n") {
			t.Fatalf("status bar wrapped at width %d", width)
		}
		if lipgloss.Width(bar) > width {
			t.Fatalf("status bar is %d cols at width %d", lipgloss.Width(bar), width)
		}
	}
}

func TestTUIRejectsStaleResultsAndMergesLiveRecords(t *testing.T) {
	now := time.Now().UTC()
	path := filepath.Join(t.TempDir(), now.Format(time.DateOnly), "billing", "prod-1.jsonl")
	m := newModel(filepath.Dir(filepath.Dir(filepath.Dir(path))))
	m.files = []logFile{{Date: now.Format(time.DateOnly), App: "billing", Agent: "prod-1", Path: path}}
	m.loadVersion = 2
	m.scanVersion = 2
	m.loadingPath = path
	live := protocol.StoredRecord{ReceivedAt: now, AgentID: "prod-1", App: "billing", Category: "error", Line: "live"}
	history := protocol.StoredRecord{ReceivedAt: now.Add(-time.Second), AgentID: "prod-1", App: "billing", Category: "error", Line: "history"}

	m.Update(scanMsg{version: 1})
	if len(m.files) != 1 {
		t.Fatal("stale scan replaced current files")
	}
	m.Update(recordMsg(live))
	m.Update(loadMsg{version: 1, path: path, records: []protocol.StoredRecord{history}})
	if len(m.records) != 1 || m.records[0].Line != "live" {
		t.Fatalf("stale load replaced live data: %+v", m.records)
	}
	m.Update(loadMsg{version: 2, path: path, records: []protocol.StoredRecord{history, live}})
	if len(m.records) != 2 || m.records[0].Line != "history" || m.records[1].Line != "live" {
		t.Fatalf("live/history merge failed: %+v", m.records)
	}
}
