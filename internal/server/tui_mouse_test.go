package server

import (
	"strings"
	"testing"

	"github.com/Alsanea-Ala/logmon/internal/protocol"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func mouseModel(t *testing.T, w, h int) *model {
	t.Helper()
	m := testModel(t, w, h)
	m.mouseEnabled = true
	m.agents = map[string]agentStatus{
		"prod-1": {ID: "prod-1", Hostname: "web-01", Connected: true},
		"prod-2": {ID: "prod-2", Hostname: "db-02"},
		"prod-3": {ID: "prod-3", Hostname: "cache-1"},
		"prod-4": {ID: "prod-4", Hostname: "queue-1"},
		"prod-5": {ID: "prod-5", Hostname: "edge-1"},
	}
	m.buildFileTree()
	m.flattenTree()
	for range 30 {
		m.records = append(m.records, protocol.StoredRecord{
			AgentID: "prod-1", Category: "access", Line: "GET /health 200",
		})
	}
	return m
}

func leftClick(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
}

func wheel(x, y int, up bool) tea.MouseMsg {
	button := tea.MouseButtonWheelDown
	if up {
		button = tea.MouseButtonWheelUp
	}
	return tea.MouseMsg{X: x, Y: y, Button: button, Action: tea.MouseActionPress}
}

// TestMouseIsOffByDefault is the whole reason this feature is opt-in.
//
// With mouse reporting on, dragging in the terminal draws a selection rectangle
// instead of selecting text, and copying a log line is what most people open a
// log viewer to do. Opting everyone into that to deliver click-to-focus is the
// wrong trade.
func TestMouseIsOffByDefault(t *testing.T) {
	m := newModel(t.TempDir())
	if m.mouseEnabled {
		t.Fatal("mouse is on by default; text selection in the terminal stops working")
	}

	cfg := defaultGUIConfig()
	if cfg.Mouse == nil || *cfg.Mouse {
		t.Error("the default GUI config enables the mouse")
	}
}

// TestMouseEventsAreIgnoredWhileDisabled: an event can still arrive, because a
// terminal can be put into reporting mode by something else, and an operator
// who has not opted in must not get panel clicks they did not ask for.
func TestMouseEventsAreIgnoredWhileDisabled(t *testing.T) {
	m := mouseModel(t, 120, 40)
	m.mouseEnabled = false
	m.focus = focusAgents

	g := m.geometry()
	before := m.agentIndex
	m.Update(leftClick(g.agentsX+2, g.agentsY+3))

	if m.focus != focusAgents {
		t.Errorf("a click changed focus to %v while the mouse is disabled", m.focus)
	}
	if m.agentIndex != before {
		t.Errorf("a click moved the agent cursor from %d to %d while disabled",
			before, m.agentIndex)
	}
}

// TestClickFocusesThePanelUnderThePointer is the primary affordance.
func TestClickFocusesThePanelUnderThePointer(t *testing.T) {
	for _, size := range [][2]int{{140, 40}, {110, 40}, {80, 40}} {
		for _, pane := range []focusPane{focusAgents, focusFiles, focusMain} {
			m := mouseModel(t, size[0], size[1])
			m.focus = focusMain
			// The stacked layout's geometry depends on focus, so it is read
			// after focus is set rather than once before the loop.
			g := m.geometry()

			var x, y int
			switch pane {
			case focusAgents:
				x, y = g.agentsX+2, g.agentsY+2
			case focusFiles:
				x, y = g.filesX+2, g.filesY+2
			case focusMain:
				x, y = g.mainX+2, g.mainY+2
			}

			m.Update(leftClick(x, y))
			if m.focus != pane {
				t.Errorf("%dx%d: clicking at %d,%d focused %v, want %v",
					size[0], size[1], x, y, m.focus, pane)
			}
		}
	}
}

// TestClickMovesTheCursorInAListPanel covers the row selection, which is the
// part that has to agree with where the rows actually are.
func TestClickMovesTheCursorInAListPanel(t *testing.T) {
	m := mouseModel(t, 140, 40)
	m.focus = focusMain
	g := m.geometry()

	// Row 0 and row 1 of a framed panel are the border.
	m.Update(leftClick(g.agentsX+2, g.agentsY+m.panelTopBorder()+2))
	if m.focus != focusAgents {
		t.Fatalf("focus is %v", m.focus)
	}
	if m.agentIndex != 2 {
		t.Errorf("clicking the third agent row selected index %d, want 2", m.agentIndex)
	}
}

// TestOnlyTheHeaderAndStatusBarAreDead distinguishes the rows that belong to no
// panel from the rows that belong to a panel but hold nothing.
//
// Clicking a panel's border does focus it, which is what someone clicking on a
// panel expects. It must not move a cursor, because the border is chrome. The
// header and the status bar belong to no panel at all, and clicking the version
// string is not a request to focus anything.
func TestOnlyTheHeaderAndStatusBarAreDead(t *testing.T) {
	m := mouseModel(t, 140, 40)
	m.focus = focusFiles
	g := m.geometry()

	// The border focuses but selects nothing.
	m.Update(leftClick(g.agentsX+2, g.agentsY))
	if m.focus != focusAgents {
		t.Errorf("clicking the agents border focused %v, want Agents", m.focus)
	}
	if m.agentIndex != 0 {
		t.Errorf("clicking the agents border selected index %d", m.agentIndex)
	}

	// The header and status bar belong to no panel.
	for name, y := range map[string]int{"header": 0, "status bar": m.height - 1} {
		before := m.focus
		m.Update(leftClick(4, y))
		if m.focus != before {
			t.Errorf("a click on the %s changed focus from %v to %v", name, before, m.focus)
		}
	}
}

// TestClickPastTheEndOfAListChangesNothing: the rows below the last item are
// padding, and clicking one should not select the last item as though the
// operator had clicked it.
func TestClickPastTheEndOfAListChangesNothing(t *testing.T) {
	m := mouseModel(t, 140, 40)
	m.agentIndex = 1
	m.focus = focusFiles
	g := m.geometry()

	m.Update(leftClick(g.agentsX+2, g.agentsY+g.agentsH-1))
	if m.agentIndex != 1 {
		t.Errorf("clicking the panel padding moved the agent cursor to %d", m.agentIndex)
	}
}

// TestWheelScrollsThePanelUnderThePointerAndKeepsFocus is the rule that is easy
// to get wrong.
//
// Scroll is not navigation: an operator scrolling a long log should not find
// their focus has moved to another panel halfway down, and it should be able to
// scroll a panel they have not focused.
func TestWheelScrollsThePanelUnderThePointerAndKeepsFocus(t *testing.T) {
	m := mouseModel(t, 140, 40)
	m.focus = focusFiles
	m.lineIndex = 10
	g := m.geometry()

	// Wheel over the log panel: the log cursor moves, focus does not.
	m.Update(wheel(g.mainX+2, g.mainY+5, false))
	if m.focus != focusFiles {
		t.Errorf("scrolling the log panel moved focus to %v", m.focus)
	}
	if m.lineIndex <= 10 {
		t.Errorf("scrolling the log panel left the cursor at %d", m.lineIndex)
	}

	// Wheel over the agents panel: the agent cursor moves, focus still does not.
	before := m.lineIndex
	m.Update(wheel(g.agentsX+2, g.agentsY+3, false))
	if m.focus != focusFiles {
		t.Errorf("scrolling the agents panel moved focus to %v", m.focus)
	}
	if m.lineIndex != before {
		t.Errorf("scrolling the agents panel also moved the log cursor, to %d", m.lineIndex)
	}
}

// TestWheelIsBoundedByThePanelItScrolls: scrolling past either end stops, and
// an unfocused panel must not be able to walk its cursor off the end.
func TestWheelIsBoundedByThePanelItScrolls(t *testing.T) {
	m := mouseModel(t, 140, 40)
	m.focus = focusAgents
	m.lineIndex = 0
	g := m.geometry()

	for range 5 {
		m.Update(wheel(g.mainX+2, g.mainY+5, true))
	}
	if m.lineIndex != 0 {
		t.Errorf("scrolling up past the top left the cursor at %d", m.lineIndex)
	}
	for range 500 {
		m.Update(wheel(g.mainX+2, g.mainY+5, false))
	}
	if m.lineIndex > len(m.filtered())-1 {
		t.Errorf("scrolling down past the end left the cursor at %d of %d",
			m.lineIndex, len(m.filtered()))
	}
}

// TestMouseReleaseDoesNotDoubleAct: acting on the release as well would select
// twice, and a press-drag-release ending outside the panel would still select.
func TestMouseReleaseDoesNotDoubleAct(t *testing.T) {
	m := mouseModel(t, 140, 40)
	m.focus = focusMain
	g := m.geometry()

	m.Update(leftClick(g.agentsX+2, g.agentsY+m.panelTopBorder()+3))
	if m.agentIndex != 3 {
		t.Fatalf("press selected %d, want 3", m.agentIndex)
	}
	m.Update(tea.MouseMsg{
		X: g.agentsX + 2, Y: g.agentsY + m.panelTopBorder() + 1,
		Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease,
	})
	if m.agentIndex != 3 {
		t.Errorf("a release selected %d; the press already did that", m.agentIndex)
	}
}

// TestMouseHitTestAgreesWithTheRenderer is the test that stops the hit test
// drifting from the panel.
//
// This walks the frame the renderer produced, finds the row the cursor marker
// is on, clicks it, and checks the model selected that record. A hit test that
// works the geometry out a second time is correct until the day the wrapping, the
// graph or the file header changes, and then it silently selects the wrong log
// line. Comparing against the rendered output is the only version of this that
// cannot drift.
func TestMouseHitTestAgreesWithTheRenderer(t *testing.T) {
	for _, size := range [][2]int{{140, 40}, {110, 40}, {80, 80}, {80, 40}} {
		for _, wrap := range []bool{false, true} {
			m := mouseModel(t, size[0], size[1])
			m.wrap = wrap
			// A mix of short lines and long ones, so the row arithmetic is not
			// trivial when wrapping is on.
			for i := range 40 {
				line := "GET /health 200"
				if i%3 == 0 {
					line = strings.Repeat("long entry ", 40)
				}
				m.records = append(m.records, protocol.StoredRecord{
					AgentID: "prod-1", Category: "access", Line: line,
				})
			}
			m.lineIndex = 12

			frame := stripANSI(m.View())
			rows := strings.Split(frame, "\n")
			g := m.geometry()

			// Find the row inside the log panel carrying the cursor marker.
			found := false
			for offset := range g.mainH {
				y := g.mainY + offset
				if y < 0 || y >= len(rows) {
					continue
				}
				if !strings.Contains(rows[y], "►") {
					continue
				}
				m.Update(leftClick(g.mainX+2, y))
				if m.lineIndex != 12 {
					t.Errorf("%dx%d wrap=%v: clicking the marked row at y=%d selected record %d, want 12",
						size[0], size[1], wrap, y, m.lineIndex)
				}
				found = true
				break
			}
			if !found {
				t.Errorf("%dx%d wrap=%v: no cursor marker in the log panel, so the check did nothing",
					size[0], size[1], wrap)
			}
		}
	}
}

// TestClicksOutsideEveryPanelDoNothing: a terminal can be wider than the frame
// the model last drew, and a stale click should not panic or select.
func TestClicksOutsideEveryPanelDoNothing(t *testing.T) {
	m := mouseModel(t, 140, 40)
	m.focus = focusFiles

	for _, point := range [][2]int{{-1, 5}, {500, 5}, {5, -1}, {5, 500}} {
		m.Update(leftClick(point[0], point[1]))
	}
	if m.focus != focusFiles {
		t.Errorf("an off-panel click changed focus to %v", m.focus)
	}
}

// TestGraphSeriesRowCountMatchesTheConstant: logHeaderRows spends graphSeriesRows
// rows on the graph, and if the renderer draws a different number the click
// arithmetic is wrong by exactly that difference.
func TestGraphSeriesRowCountMatchesTheConstant(t *testing.T) {
	m := mouseModel(t, 140, 40)
	if got := len(m.graphRows(80)); got != graphSeriesRows {
		t.Errorf("graphRows returns %d rows, but logHeaderRows reserves %d",
			got, graphSeriesRows)
	}
}

// TestGeometryPanelsDoNotOverlap is a cheap structural check. An overlapping
// geometry would mean the later panel in the list always wins the hit test and
// part of the frame is unreachable.
func TestGeometryPanelsDoNotOverlap(t *testing.T) {
	for _, size := range [][2]int{{140, 40}, {110, 30}, {80, 80}, {80, 32}, {45, 14}} {
		m := mouseModel(t, size[0], size[1])
		g := m.geometry()
		rects := map[string]panelRect{
			"agents": {g.agentsX, g.agentsY, g.agentsW, g.agentsH},
			"files":  {g.filesX, g.filesY, g.filesW, g.filesH},
			"main":   {g.mainX, g.mainY, g.mainW, g.mainH},
		}
		for _, a := range []string{"agents", "files", "main"} {
			for _, b := range []string{"agents", "files", "main"} {
				if a >= b {
					continue
				}
				ra, rb := rects[a], rects[b]
				overlap := ra.x < rb.x+rb.width && rb.x < ra.x+ra.width &&
					ra.y < rb.y+rb.height && rb.y < ra.y+ra.height
				if overlap {
					t.Errorf("%dx%d: %s %+v overlaps %s %+v",
						size[0], size[1], a, ra, b, rb)
				}
			}
		}
		// Every panel must also sit inside the frame.
		for name, r := range rects {
			if r.x < 0 || r.y < 1 || r.y+r.height > size[1]-1 {
				t.Errorf("%dx%d: %s %+v is outside the frame", size[0], size[1], name, r)
			}
			if r.width <= 0 || r.height <= 0 {
				t.Errorf("%dx%d: %s %+v has no area", size[0], size[1], name, r)
			}
		}
	}
}

// TestFrameAndGeometryAgreeOnPanelWidths is the cross-check on the shared
// geometry: the log panel's outer width has to be what the frame actually used.
func TestFrameAndGeometryAgreeOnPanelWidths(t *testing.T) {
	for _, size := range [][2]int{{140, 40}, {110, 40}, {80, 80}} {
		m := mouseModel(t, size[0], size[1])
		frame := stripANSI(m.View())
		rows := strings.Split(frame, "\n")
		g := m.geometry()

		if g.mainY+1 >= len(rows) {
			t.Fatalf("%dx%d: geometry puts main at row %d but the frame has %d rows",
				size[0], size[1], g.mainY, len(rows))
		}
		row := rows[g.mainY]
		if got := lipgloss.Width(row); got != size[0] {
			t.Errorf("%dx%d: row %d is %d columns, want %d; the geometry does not match the frame",
				size[0], size[1], g.mainY, got, size[0])
		}
	}
}
