package server

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Alsanea-Ala/logmon/internal/protocol"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// layoutModel builds a model with a known number of agents and tree rows, so the
// tests can say what each panel needs rather than inferring it.
func layoutModel(t *testing.T, w, h, agentCount, treeRows int) *model {
	t.Helper()
	m := testModel(t, w, h)
	m.agents = make(map[string]agentStatus, agentCount)
	for i := range agentCount {
		id := "agent-" + string(rune('a'+i))
		m.agents[id] = agentStatus{ID: id, Hostname: "host"}
	}
	if treeRows > 0 {
		m.buildFileTree()
		m.flattenTree()
	}
	return m
}

// heights is the three panel heights keyed by pane, for readable assertions.
func heights(l layout) map[focusPane]int {
	return map[focusPane]int{focusAgents: l.agentsH, focusFiles: l.filesH, focusMain: l.mainH}
}

func focusedHeight(l layout, focus focusPane) int {
	return heights(l)[focus]
}

var everyFocus = []focusPane{focusAgents, focusFiles, focusMain}

// TestStackedPanelsAlwaysTotalTheAvailableHeight is the invariant that matters
// most: the three panels must consume exactly the rows available, at every size
// and for every focused panel.
//
// One row too many and the frame is taller than the terminal and the bottom
// border scrolls off. One row too few and there is a gap above the status bar.
// Both look like a broken layout and neither is caught by any single assertion
// about one panel's height.
func TestStackedPanelsAlwaysTotalTheAvailableHeight(t *testing.T) {
	sizes := [][2]int{
		{80, 14}, {80, 18}, {80, 24}, {80, 30}, {80, 32}, {80, 40}, {80, 60}, {80, 80},
		{90, 100}, {60, 50}, {45, 30}, {99, 31},
	}
	for _, size := range sizes {
		for _, focus := range everyFocus {
			m := layoutModel(t, size[0], size[1], 3, 8)
			m.focus = focus

			l := m.computeLayout()
			total := l.agentsH + l.filesH + l.mainH
			if want := size[1] - 2; total != want {
				t.Errorf("%dx%d focus=%v: panels total %d rows, want %d (agents %d, files %d, logs %d)",
					size[0], size[1], focus, total, want, l.agentsH, l.filesH, l.mainH)
			}
		}
	}
}

// TestFocusedPanelGetsTwoThirdsWhenThereIsRoom checks the feature itself.
//
// Two thirds is a floor, so the focused panel may exceed it, and it yields when
// the other two genuinely need their content. What it may never do is come out
// under the share it was promised at a height where the others could spare it.
func TestFocusedPanelGetsTwoThirdsWhenThereIsRoom(t *testing.T) {
	const w, h = 80, 80
	avail := h - 2

	for _, focus := range everyFocus {
		m := layoutModel(t, w, h, 3, 8)
		m.focus = focus
		l := m.computeLayout()

		needOthers := m.panelRows(len(m.agents)) + m.panelRows(len(m.treeFlattened))
		if focus == focusAgents {
			needOthers = m.panelRows(len(m.treeFlattened)) + panelMinRows*2
		}
		promised := min(focusShareRows(avail), avail-needOthers)

		got := focusedHeight(l, focus)
		if got < promised {
			t.Errorf("%dx%d focus=%v: the focused panel got %d rows, want at least %d",
				w, h, focus, got, promised)
		}
	}
}

// TestFocusActuallyMovesRows is the other half: if the layout were identical for
// every focus the tests above would pass on an implementation that ignored focus
// entirely.
func TestFocusActuallyMovesRows(t *testing.T) {
	seen := map[focusPane]int{}
	for _, focus := range everyFocus {
		m := layoutModel(t, 80, 80, 3, 8)
		m.focus = focus
		seen[focus] = focusedHeight(m.computeLayout(), focus)
	}

	if seen[focusAgents] <= 4 || seen[focusFiles] <= 4 || seen[focusMain] <= 4 {
		t.Fatalf("focus did not enlarge any panel: %v", seen)
	}
	// The focused panel should be the tallest of the three, which is what
	// "two thirds" is supposed to mean in practice.
	for _, focus := range everyFocus {
		m := layoutModel(t, 80, 80, 3, 8)
		m.focus = focus
		l := m.computeLayout()
		for pane, h := range heights(l) {
			if pane != focus && h > focusedHeight(l, focus) {
				t.Errorf("focus=%v: %v is %d rows but the focused panel is only %d",
					focus, pane, h, focusedHeight(l, focus))
			}
		}
	}
}

// TestSurplusGoesToTheLogPanelNotTheFocusedOne is the regression that shaped the
// rule.
//
// The first version sent every unused row to the focused panel, on the theory
// that the panel you are working in deserves the space. At 80x80 with the
// agents panel focused that handed sixty-four rows to a list of three agents,
// and the log panel was left at its bare minimum showing no records at all.
//
// Only the log panel has content that grows to fill any height given to it. The
// agent list and the file tree are both bounded by what exists, so the height
// the other two do not need belongs to the logs even while a sidebar panel is
// focused.
func TestSurplusGoesToTheLogPanelNotTheFocusedOne(t *testing.T) {
	for _, focus := range []focusPane{focusAgents, focusFiles} {
		m := layoutModel(t, 80, 80, 3, 8)
		m.focus = focus
		l := m.computeLayout()

		if l.mainH <= panelMinRows*2 {
			t.Errorf("focus=%v: the log panel got %d rows, its bare minimum. "+
				"The unused height should have gone there rather than to the focused panel",
				focus, l.mainH)
		}
	}

	// And the specific case the first version got wrong: focusing a sidebar
	// panel costs the log panel its share, but never drops it to the minimum.
	m := layoutModel(t, 80, 80, 3, 8)
	m.focus = focusAgents
	focused := m.computeLayout()
	m.focus = focusMain
	unfocused := m.computeLayout()

	if focused.mainH >= unfocused.mainH {
		t.Errorf("focusing the agents panel left the log panel at %d rows, no smaller than "+
			"the %d it gets when focused; the share did not move", focused.mainH, unfocused.mainH)
	}
	if focused.mainH <= panelMinRows*2 {
		t.Errorf("the log panel dropped to %d rows when focus left it", focused.mainH)
	}
}

// TestFocusedPanelSizeIsAShareNotAnUnboundedWindfall documents the cost of the
// feature rather than hiding it.
//
// Focusing a sidebar panel does hand it two thirds of the height even when its
// content is three rows, which is blank rows. That is inherent to "the focused
// panel gets two thirds": the alternative is capping bounded panels at their
// content, and then focus would do nothing at all except on the log panel, which
// is already the largest.
//
// What matters is that it is a share. The panel is not given everything the
// others did not need, and it is not permanent: tabbing away redistributes.
func TestFocusedPanelSizeIsAShareNotAnUnboundedWindfall(t *testing.T) {
	const w, h = 80, 80
	m := layoutModel(t, w, h, 3, 8)
	m.focus = focusAgents
	l := m.computeLayout()

	share := focusShareRows(h - 2)
	if l.agentsH != share {
		t.Errorf("focus=agents at %dx%d: the agents panel is %d rows, want exactly the %d row share",
			w, h, l.agentsH, share)
	}
	// The others still get a usable layout, not the remainder of nothing.
	if l.mainH <= panelMinRows*2 {
		t.Errorf("focus=agents: the log panel was left at %d rows", l.mainH)
	}
	if l.filesH < panelMinRows {
		t.Errorf("focus=agents: the file tree was left at %d rows", l.filesH)
	}
}

// TestSidebarPanelsAreGivenWhatTheirContentNeeds is the original complaint: at
// 80x80 the file tree was handed thirty-five rows to show seven.
//
// The threshold is deliberately loose. The point is that the file tree is no
// longer proportional to the sidebar regardless of its contents, not that it
// lands on its exact need every time: the log panel claims the surplus, because
// it is the only panel that can use it.
func TestSidebarPanelsAreGivenWhatTheirContentNeeds(t *testing.T) {
	m := layoutModel(t, 80, 80, 3, 8)
	m.focus = focusMain
	l := m.computeLayout()

	needFiles := m.panelRows(len(m.treeFlattened))
	if l.filesH > needFiles+2 {
		t.Errorf("at 80x80 the file tree got %d rows for content needing %d; "+
			"it should be sized by its contents, not by a fixed share",
			l.filesH, needFiles)
	}
	if l.mainH <= l.filesH {
		t.Errorf("at 80x80 with the logs focused, files got %d rows and logs %d; "+
			"the logs are the panel with unbounded content", l.filesH, l.mainH)
	}
}

// TestSmallTerminalsKeepThePlainSplit guards the threshold.
//
// Below rebalanceMin there is no surplus to redistribute, so rebalancing would
// resize the panels on every focus change to reach a worse result. The layout
// must be identical whichever panel is focused, and it must be the layout that
// was already there.
func TestSmallTerminalsKeepThePlainSplit(t *testing.T) {
	for _, size := range [][2]int{{80, 18}, {45, 14}, {60, 16}, {80, 24}, {80, 29}} {
		seen := map[focusPane]layout{}
		for _, focus := range everyFocus {
			m := layoutModel(t, size[0], size[1], 3, 8)
			m.focus = focus
			seen[focus] = m.computeLayout()
		}
		for _, focus := range everyFocus[1:] {
			if seen[focus] != seen[focusAgents] {
				t.Errorf("%dx%d: focus=%v gives %+v, focus=agents gives %+v; "+
					"below rebalanceMin=%d the layout must not depend on focus",
					size[0], size[1], focus, seen[focus], seen[focusAgents], rebalanceMin)
			}
		}
		// And it must be the pre-existing split: logs first at half.
		if got, want := seen[focusAgents].mainH, max(panelMinRows*2, (size[1]-2)/2); got != want {
			t.Errorf("%dx%d: log panel is %d rows, want the existing %d",
				size[0], size[1], got, want)
		}
	}
}

// TestLogPanelNeverCollapses is the floor that keeps the focus rule from
// starving the reason the TUI exists.
//
// A log panel reduced to its border and the file header shows no records at
// all, which reads as a broken panel rather than as a compact layout.
func TestLogPanelNeverCollapses(t *testing.T) {
	for _, size := range [][2]int{{80, 30}, {80, 32}, {80, 40}, {80, 80}, {60, 60}} {
		for _, focus := range everyFocus {
			m := layoutModel(t, size[0], size[1], 3, 8)
			m.focus = focus
			l := m.computeLayout()
			if l.mainH < panelMinRows*2 {
				t.Errorf("%dx%d focus=%v: the log panel collapsed to %d rows, want at least %d",
					size[0], size[1], focus, l.mainH, panelMinRows*2)
			}
			if l.agentsH < panelMinRows || l.filesH < panelMinRows {
				t.Errorf("%dx%d focus=%v: a sidebar panel is below the %d row minimum (agents %d, files %d)",
					size[0], size[1], focus, panelMinRows, l.agentsH, l.filesH)
			}
		}
	}
}

// TestSideBySideHasNoContestForTheSidebar records the deliberate asymmetry.
//
// In the side-by-side layout the log panel already owns the full height, so
// there is nothing for the sidebar panels to compete for and the two-thirds rule
// would only decide which of them gets the blank rows. They keep their content
// sizing instead, and the tree fills the column because a short column leaves an
// unbordered gap above the status bar.
//
// This is why fixing "files gets too many rows" only applies to the stacked
// layout, and why the side-by-side numbers are unchanged.
func TestSideBySideHasNoContestForTheSidebar(t *testing.T) {
	seen := map[focusPane]layout{}
	for _, focus := range everyFocus {
		m := layoutModel(t, 140, 40, 3, 8)
		m.focus = focus
		seen[focus] = m.computeLayout()
	}

	for _, focus := range everyFocus {
		l := seen[focus]
		if l.stacked {
			t.Fatalf("%dx%d should be side-by-side", 140, 40)
		}
		if l.mainH != 40-2 {
			t.Errorf("focus=%v: the log panel is %d rows, want the full %d", focus, l.mainH, 40-2)
		}
		if l.agentsH+l.filesH != 40-2 {
			t.Errorf("focus=%v: the sidebar is %d rows, want the full %d",
				focus, l.agentsH+l.filesH, 40-2)
		}
	}
	// Focus does not move the sidebar when the logs are focused, because the
	// sidebar is not what is being contested.
	if seen[focusAgents] != seen[focusFiles] {
		t.Error("focusing a sidebar panel resized the sidebar; the log panel already owns that height")
	}
}

// TestShareHeightAlwaysFillsTheSpace checks the primitive directly, across a
// range that includes the cases computeLayout cannot reach: enormous content
// needs, and a terminal with almost nothing to give.
func TestShareHeightAlwaysFillsTheSpace(t *testing.T) {
	// bIsLogs covers both arrangements: with the logs among the others, and
	// with both others being sidebar panels.
	for _, bIsLogs := range []bool{true, false} {
		for _, c := range []struct{ avail, focus, a, b int }{
			{78, 6, 5, 8}, {78, 5, 8, 6}, {78, 63, 5, 6}, {78, 4, 5, 8},
			{78, 203, 75, 6}, {78, 203, 6, 75}, {78, 400, 400, 6},
			{30, 6, 5, 8}, {30, 20, 5, 6}, {30, 3, 3, 3}, {30, 26, 5, 6},
			{6, 6, 3, 3}, {9, 3, 3, 3}, {100, 6, 40, 40}, {100, 90, 3, 3},
		} {
			focusH, aH, bH, surplus := shareHeight(c.avail, c.focus, c.a, c.b, bIsLogs)

			label := fmt.Sprintf("shareHeight(%d, %d, %d, %d, bIsLogs=%v)",
				c.avail, c.focus, c.a, c.b, bIsLogs)
			if got := focusH + aH + bH + surplus; got != c.avail {
				t.Errorf("%s: parts total %d, want %d", label, got, c.avail)
			}
			if focusH < 0 || aH < 0 || bH < 0 || surplus < 0 {
				t.Errorf("%s returned a negative part: %d %d %d %d", label, focusH, aH, bH, surplus)
			}
			// A panel with more content than the terminal has to scroll, so the
			// cap is the whole point: nothing may exceed what is on offer.
			if focusH > c.avail {
				t.Errorf("%s: the focused panel is %d rows in %d", label, focusH, c.avail)
			}
		}
	}
}

// TestFrameStillFitsAtEverySizeAndFocus re-runs the rendering assertions with
// the new layout, which is where a row budget that disagrees with computeLayout
// shows up as a frame taller than the terminal.
func TestFrameStillFitsAtEverySizeAndFocus(t *testing.T) {
	sizes := [][2]int{{140, 40}, {120, 30}, {100, 24}, {80, 32}, {80, 80}, {60, 18}, {45, 14}}
	for _, size := range sizes {
		for _, focus := range everyFocus {
			m := layoutModel(t, size[0], size[1], 3, 8)
			m.focus = focus
			for range 12 {
				m.records = append(m.records, storedTestRecord())
			}

			frame := m.View()
			rows := strings.Split(frame, "\n")
			if len(rows) != size[1] {
				t.Fatalf("%dx%d focus=%v: frame is %d rows:\n%s",
					size[0], size[1], focus, len(rows), frame)
			}
			for i, row := range rows {
				if w := lipgloss.Width(row); w > size[0] {
					t.Errorf("%dx%d focus=%v: row %d is %d columns",
						size[0], size[1], focus, i, w)
				}
			}
		}
	}
}

// TestTabResizesTheStack checks the user-visible consequence, which is also the
// cost: above the threshold, moving focus visibly resizes the panels.
//
// It is asserted rather than prevented because it is the feature. The
// threshold is what keeps it from happening in a terminal too small to have
// anything to redistribute.
func TestTabResizesTheStack(t *testing.T) {
	m := layoutModel(t, 80, 80, 3, 8)
	m.focus = focusMain

	before := m.computeLayout().mainH
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(*model)
	if m.focus != focusAgents {
		t.Fatalf("tab from the logs did not reach the agents panel, focus is %v", m.focus)
	}
	after := m.computeLayout().mainH
	if after >= before {
		t.Errorf("tabbing away from the logs did not shrink them: %d then %d", before, after)
	}
	if m.computeLayout().agentsH <= after {
		t.Error("the agents panel did not become the larger panel")
	}
}

// storedTestRecord is one distinct record for render assertions. appendRecord
// dedupes identical records, so Line and ReceivedAt both have to vary.
func storedTestRecord() protocol.StoredRecord {
	storedTestRecordSeq++
	return protocol.StoredRecord{
		AgentID:    "agent-a",
		Category:   "app",
		Line:       "request handled line " + strconv.Itoa(storedTestRecordSeq),
		ReceivedAt: time.Unix(0, 0).Add(time.Duration(storedTestRecordSeq) * time.Second),
	}
}

var storedTestRecordSeq int

// TestExtremeContentNeverProducesNegativePanels is the regression for the
// worst bug this layout had.
//
// shareHeight originally treated a panel's content requirement as a demand: a
// focused panel asked for the rows it would need to show everything, and 200
// agents focused at 80x80 produced files -63 and logs -64. A negative height
// reaches panel(), which clamps to 1, and the frame no longer adds up.
//
// A panel with more content than the terminal scrolls. The requirement is a
// floor for when there is room and nothing at all when there is not.
func TestExtremeContentNeverProducesNegativePanels(t *testing.T) {
	for _, contentRows := range []int{0, 1, 8, 40, 200, 500, 5000} {
		for _, size := range [][2]int{{80, 80}, {80, 40}, {80, 32}, {45, 14}, {80, 14}} {
			for _, focus := range everyFocus {
				m := testModel(t, size[0], size[1])
				for i := range contentRows {
					m.agents["agent-"+strconv.Itoa(i)] = agentStatus{ID: strconv.Itoa(i)}
				}
				m.buildFileTree()
				m.flattenTree()
				m.focus = focus

				l := m.computeLayout()
				for _, p := range []struct {
					name string
					h    int
				}{{"agents", l.agentsH}, {"files", l.filesH}, {"logs", l.mainH}} {
					if p.h < 0 {
						t.Errorf("%dx%d content=%d focus=%v: the %s panel is %d rows",
							size[0], size[1], contentRows, focus, p.name, p.h)
					}
				}
				if total := l.agentsH + l.filesH + l.mainH; total != size[1]-2 {
					t.Errorf("%dx%d content=%d focus=%v: panels total %d, want %d",
						size[0], size[1], contentRows, focus, total, size[1]-2)
				}
			}
		}
	}
}

// TestAPanelIsNeverCutToNothingWhenAnotherWouldDo pins the smaller-need-first
// rule.
//
// At 80x80 with forty agents and the logs focused, serving the agent list first
// left the file tree at zero rows. A panel with no height at all is worse than
// a panel that scrolls, and forty agents in a 78 row terminal is not an extreme
// case, it is the stated deployment target.
func TestAPanelIsNeverCutToNothingWhenAnotherWouldDo(t *testing.T) {
	m := testModel(t, 80, 80)
	for i := range 40 {
		m.agents["agent-"+strconv.Itoa(i)] = agentStatus{ID: strconv.Itoa(i)}
	}
	m.buildFileTree()
	m.flattenTree()
	m.focus = focusMain

	l := m.computeLayout()
	if l.filesH < panelMinRows {
		t.Errorf("with forty agents and the logs focused the file tree got %d rows; "+
			"the agent list was served first and left it with nothing", l.filesH)
	}
	if l.agentsH < panelMinRows {
		t.Errorf("the agents panel got %d rows", l.agentsH)
	}
}
