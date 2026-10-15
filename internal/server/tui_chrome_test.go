package server

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/Alsanea-Ala/logmon/internal/protocol"
	"github.com/charmbracelet/lipgloss"
)

// lipglossWidth is a local alias so the width assertions read as arithmetic.
func lipglossWidth(s string) int { return lipgloss.Width(s) }

// readTUI returns the TUI source, so the binding guards can check the real
// switch rather than a hand-maintained list that could drift from it.
func readTUI(t *testing.T) string {
	t.Helper()
	source, err := os.ReadFile("tui.go")
	if err != nil {
		t.Fatalf("read tui.go: %v", err)
	}
	return string(source)
}

// testModel builds a model with enough state that every panel has something to
// draw. A bare model renders empty panels, which would let a title regression
// slip past a test that only checked for width.
func testModel(t *testing.T, width, height int) *model {
	t.Helper()
	m := newModel(t.TempDir())
	m.width, m.height = width, height
	m.agents["prod-1"] = agentStatus{ID: "prod-1", Hostname: "web-01", Connected: true}
	m.agents["prod-2"] = agentStatus{ID: "prod-2", Hostname: "db-02"}
	m.files = []logFile{
		{Date: "2026-10-15", App: "nginx", Agent: "prod-1"},
		{Date: "2026-10-15", App: "nginx", Agent: "prod-2"},
	}
	m.categories = []string{"all", "error"}
	for i := range 3 {
		m.records = append(m.records, protocol.StoredRecord{
			AgentID: "prod-1", Hostname: "web-01",
			Source: "file", App: "nginx", Category: "access",
			Line: strings.Repeat("x", i+8),
		})
	}
	return m
}

// TestPanelTitlesRendered covers the reason the frame is drawn by hand.
//
// lipgloss v1 has no border title, so a titled panel is this package's own
// code. Nothing else in the suite would notice if the title stopped appearing
// and the panels silently degraded back to bare boxes.
func TestPanelTitlesRendered(t *testing.T) {
	for _, size := range [][2]int{{110, 22}, {80, 24}, {100, 20}} {
		m := testModel(t, size[0], size[1])
		view := m.View()

		if !strings.Contains(view, "Sources") {
			t.Errorf("%dx%d: side panel has no title:\n%s", size[0], size[1], view)
		}
		if !strings.Contains(view, "Logs") {
			t.Errorf("%dx%d: log panel has no title:\n%s", size[0], size[1], view)
		}

		m.showHelp = true
		if !strings.Contains(m.View(), "Keybindings") {
			t.Errorf("%dx%d: overlay has no title:\n%s", size[0], size[1], m.View())
		}
	}
}

// TestPanelFrameAligns checks the frame is the same width on every row.
//
// The edges span the full interior including padding while the body rows do
// not, which is exactly the off-by-two that leaves a panel visibly ragged: the
// top and bottom borders come out two cells short of the rows between them.
// Width alone would not catch it, since lipgloss pads the block out again.
func TestPanelFrameAligns(t *testing.T) {
	m := testModel(t, 100, 20)
	for _, line := range strings.Split(m.renderSidebar(30, 10), "\n") {
		if w := lipglossWidth(line); w != 30 {
			t.Errorf("sidebar row width = %d, want 30: %q", w, line)
		}
	}
	for _, line := range strings.Split(m.renderMain(68, 10), "\n") {
		if w := lipglossWidth(line); w != 68 {
			t.Errorf("log panel row width = %d, want 68: %q", w, line)
		}
	}
}

// TestHelpOverlayFitsTerminal is the size assertion CONTRIBUTING.md requires
// for changes to this file. The overlay replaces the panels, so it has to
// respect the same row budget or it pushes the status bar off the screen.
func TestHelpOverlayFitsTerminal(t *testing.T) {
	sizes := [][2]int{
		{40, 10}, {40, 24}, {60, 15}, {80, 24}, {100, 30}, {120, 40}, {200, 50}, {72, 12},
	}
	for _, size := range sizes {
		m := testModel(t, size[0], size[1])
		if size[0] < minTTYWidth || size[1] < minTTYHeight {
			continue // View returns the too-small notice, which is its own path
		}
		m.showHelp = true
		lines := strings.Split(m.View(), "\n")
		if len(lines) > size[1] {
			t.Errorf("%dx%d: overlay produced %d rows", size[0], size[1], len(lines))
		}
		if len(lines) < 3 {
			t.Errorf("%dx%d: overlay produced %d rows, header and status bar missing",
				size[0], size[1], len(lines))
		}
	}
}

// keyRe matches the bracketed key names in the options bar, e.g. "[↑↓/jk]".
var keyRe = regexp.MustCompile(`\[([^\]]+)\]`)

// caseRe matches a whole case clause, including every label in a multi-value
// case. Matching only the first quoted string would read `case "up", "k":` as
// just "up" and report "k" as unhandled.
var caseRe = regexp.MustCompile(`case\s+((?:"[^"]+"\s*,?\s*)+):`)

// quotedRe pulls the individual labels out of a matched case clause.
var quotedRe = regexp.MustCompile(`"([^"]+)"`)

// TestHelpOverlayListsBindings is the guard against the bug that started all
// of this: the status bar advertised "?: help" and nothing bound to "?", and
// nothing compared the two. It reads the bar, extracts every key it names, and
// asserts the main switch in handleKey has a case for each.
func TestHelpOverlayListsBindings(t *testing.T) {
	m := testModel(t, 140, 24)

	handled := map[string]bool{}
	for _, clause := range caseRe.FindAllStringSubmatch(mainKeySwitch(t), -1) {
		for _, label := range quotedRe.FindAllStringSubmatch(clause[1], -1) {
			for _, key := range normaliseKeys(label[1]) {
				handled[key] = true
			}
		}
	}

	bar := m.renderStatusBar(false)
	for _, match := range keyRe.FindAllStringSubmatch(bar, -1) {
		for _, key := range normaliseKeys(match[1]) {
			if !handled[key] {
				t.Errorf("status bar advertises %q but handleKey has no case for it", key)
			}
		}
	}
}

// TestHelpOverlayDocumentsEveryBinding is the reverse direction: every binding
// the overlay lists has to be reachable, or the overlay is lying in a different
// way than the status bar was.
func TestHelpOverlayDocumentsEveryBinding(t *testing.T) {
	source := mainKeySwitch(t)
	for _, b := range keybindings {
		for _, key := range normaliseKeys(b.keys) {
			if !strings.Contains(source, `"`+key+`"`) {
				t.Errorf("help overlay documents %q but no case exists for it", key)
			}
		}
	}
}

// mainKeySwitch returns the source of the primary key switch in handleKey,
// excluding the block that dismisses the help overlay.
//
// Scanning the whole file is not good enough. The overlay block has its own
// `case "?", "esc", ...`, so a test that scanned everything would still see "?"
// as handled after the opening `case "?"` had been deleted -- which is exactly
// the regression these tests exist to catch. The primary switch is the last one
// in the function, since the overlay check comes first.
func mainKeySwitch(t *testing.T) string {
	t.Helper()
	source := readTUI(t)

	start := strings.Index(source, "func (m *model) handleKey(")
	if start < 0 {
		t.Fatal("handleKey not found in tui.go")
	}
	end := strings.Index(source[start:], "\nfunc ")
	if end < 0 {
		end = len(source) - start
	}
	fn := source[start : start+end]

	const marker = "switch msg.String() {"
	at := strings.LastIndex(fn, marker)
	if at < 0 {
		t.Fatal("handleKey has no primary switch")
	}
	return fn[at:]
}

// namedKeys are key names that are written out and must not be split into
// their letters. Anything else longer than one rune is a run of individual
// keys: "jk" is two, "↑↓" is two.
//
// Shape alone cannot tell "jk" from "tab", both being runs of lower-case
// letters, so the distinction has to be listed.
var namedKeys = map[string]bool{
	"tab": true, "enter": true, "esc": true, "space": true,
	"backspace": true, "delete": true, "home": true, "end": true,
	"pgup": true, "pgdown": true, "up": true, "down": true,
	"left": true, "right": true,
}

// glyphKeys maps the symbols the options bar draws to the key names
// handleKey switches on. Bubble Tea reports an arrow press as "up", not as the
// arrow, so a literal comparison would call every arrow unhandled.
var glyphKeys = map[string]string{
	"↑": "up", "↓": "down", "←": "left", "→": "right",
}

// normaliseKeys expands a key group into individual keys.
//
// "↑↓/jk" is four keys and "q, ctrl+c" is two. Separators are commas and
// slashes; everything else is either a named key, a modifier combination, or a
// run of single-rune keys. Arrows come back as their key name so they compare
// against handleKey.
func normaliseKeys(group string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(group, func(r rune) bool {
		return r == ',' || r == '/'
	}) {
		part = strings.ToLower(strings.TrimSpace(part))
		if part == "" {
			continue
		}
		if name, ok := glyphKeys[part]; ok {
			out = append(out, name)
			continue
		}
		// "ctrl+c" is one key, not five.
		if strings.Contains(part, "+") {
			out = append(out, part)
			continue
		}
		runes := []rune(part)
		if len(runes) > 1 && !namedKeys[part] {
			for _, r := range runes {
				if name, ok := glyphKeys[string(r)]; ok {
					out = append(out, name)
					continue
				}
				out = append(out, string(r))
			}
			continue
		}
		out = append(out, part)
	}
	return out
}
