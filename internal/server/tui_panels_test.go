package server

import (
	"strings"
	"testing"
)

// TestSidebarPanelsAreSplit checks the sidebar is two panels, each with its own
// title, rather than one panel carrying two headings. A single panel is the
// shape this replaced, so it is the shape a regression would return to.
func TestSidebarPanelsAreSplit(t *testing.T) {
	for _, size := range [][2]int{{110, 22}, {100, 20}, {80, 24}} {
		m := testModel(t, size[0], size[1])
		view := m.View()

		if strings.Count(view, "╭") != 3 {
			t.Errorf("%dx%d: want 3 panels (agents, files, logs), found %d",
				size[0], size[1], strings.Count(view, "╭"))
		}
		// Matched as a border title, not as a bare word. The status bar
		// carries "Focus: Agents" too, so counting the string alone reads
		// two headings where there is one panel.
		for _, want := range []string{"╭ [1] Agents ", "╭ [2] Files "} {
			if got := strings.Count(view, want); got != 1 {
				t.Errorf("%dx%d: %d %q titles, want 1", size[0], size[1], got, want)
			}
		}
	}
}

// TestCursorsAreIndependent checks the two sidebar panels no longer share one
// index. Moving in the agents panel must leave the file cursor alone, which the
// unified index could not express.
func TestCursorsAreIndependent(t *testing.T) {
	m := testModel(t, 120, 30)
	m.buildFileTree()
	m.flattenTree()

	m.focus = focusAgents
	for range 2 {
		next, _ := m.moveDown()
		m = next.(*model)
	}
	if m.agentIndex != 1 {
		t.Fatalf("agentIndex = %d, want 1", m.agentIndex)
	}
	if m.treeIndex != 0 {
		t.Errorf("moving in the agents panel moved the file cursor to %d", m.treeIndex)
	}

	m.focus = focusFiles
	next, _ := m.moveDown()
	m = next.(*model)
	if m.treeIndex != 1 {
		t.Errorf("treeIndex = %d, want 1", m.treeIndex)
	}
	if m.agentIndex != 1 {
		t.Errorf("moving in the file panel moved the agent cursor to %d", m.agentIndex)
	}
}

// TestNarrowLayoutGivesLogsRoom checks that stacking three panels does not
// leave the log panel with only its border.
//
// Splitting the sidebar first and giving the logs the remainder looked
// reasonable and produced three rows at 80x18: two of border and one holding
// the file header, so not a single log line was visible.
func TestNarrowLayoutGivesLogsRoom(t *testing.T) {
	for _, size := range [][2]int{{80, 18}, {45, 14}, {60, 16}} {
		m := testModel(t, size[0], size[1])
		lay := m.computeLayout()

		if lay.mainH < panelMinRows+2 {
			t.Errorf("%dx%d: log panel is %d rows, header leaves nothing to read",
				size[0], size[1], lay.mainH)
		}
		if got := lay.agentsH + lay.filesH + lay.mainH; got != size[1]-2 {
			t.Errorf("%dx%d: panels total %d rows, want %d",
				size[0], size[1], got, size[1]-2)
		}
	}
}
