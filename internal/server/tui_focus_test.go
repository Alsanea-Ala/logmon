package server

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func focusModel(t *testing.T) *model {
	t.Helper()
	m := testModel(t, 120, 30)
	m.buildFileTree()
	m.flattenTree()
	return m
}

// TestTabCyclesThreePanes is the forward walk. The backward walk is separate
// and equally important: a test that only walks forward passes with shift+tab
// completely unbound.
func TestTabCyclesThreePanes(t *testing.T) {
	m := focusModel(t)

	// Four presses from the agents panel is one full lap.
	want := []focusPane{focusFiles, focusMain, focusAgents, focusFiles}
	for i, pane := range want {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
		m = updated.(*model)
		if m.focus != pane {
			t.Fatalf("tab %d gave focus %v, want %v", i+1, m.focus, pane)
		}
	}
}

// TestShiftTabCyclesBackwards is the other half of the cycle, including the
// wrap from the agents panel back to the logs.
func TestShiftTabCyclesBackwards(t *testing.T) {
	m := focusModel(t)

	want := []focusPane{focusMain, focusFiles, focusAgents, focusMain}
	for i, pane := range want {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
		m = updated.(*model)
		if m.focus != pane {
			t.Fatalf("shift+tab %d gave focus %v, want %v", i+1, m.focus, pane)
		}
	}
}

// TestArrowsNoLongerChangeFocus is the change this stage exists for.
//
// The same key expanded a node in one panel and moved you to a different panel
// in another, which is unpredictable in exactly the way that makes a TUI
// frustrating: the key does something, but not what you expected.
func TestArrowsNoLongerChangeFocus(t *testing.T) {
	arrows := []tea.KeyMsg{
		{Type: tea.KeyLeft},
		{Type: tea.KeyRight},
		{Type: tea.KeyRunes, Runes: []rune{'h'}},
		{Type: tea.KeyRunes, Runes: []rune{'l'}},
	}

	for _, start := range []focusPane{focusAgents, focusFiles, focusMain} {
		for _, key := range arrows {
			m := focusModel(t)
			m.focus = start

			updated, _ := m.Update(key)
			m = updated.(*model)
			if m.focus != start {
				t.Errorf("%s changed focus from %v to %v", key.String(), start, m.focus)
			}
		}
	}
}

// TestArrowsStillExpandAndCollapse checks the tree kept the behaviour that was
// buried underneath the focus change.
func TestArrowsStillExpandAndCollapse(t *testing.T) {
	m := focusModel(t)
	m.focus = focusFiles

	// Walk to the first node that has children.
	parent := -1
	for i, node := range m.treeFlattened {
		if len(node.Children) > 0 {
			parent = i
			break
		}
	}
	if parent < 0 {
		t.Skip("no expandable node in the tree")
	}

	m.treeIndex = parent
	node := m.treeFlattened[parent]
	if node.Expanded {
		m.moveLeft()
	}
	if m.treeFlattened[parent].Expanded {
		t.Fatal("the node under the cursor was already expanded")
	}

	m.moveRight()
	if !m.treeFlattened[parent].Expanded {
		t.Error("right did not expand the node under the cursor")
	}

	m.moveLeft()
	if m.treeFlattened[parent].Expanded {
		t.Error("left did not collapse the node under the cursor")
	}
}

// TestDigitsFocusTheirPanel checks the shortcut the numbered titles advertise.
func TestDigitsFocusTheirPanel(t *testing.T) {
	cases := map[string]focusPane{
		"1": focusAgents,
		"2": focusFiles,
		"3": focusMain,
	}
	for key, want := range cases {
		m := focusModel(t)
		m.focus = focusMain

		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		m = updated.(*model)
		if m.focus != want {
			t.Errorf("%q focused %v, want %v", key, m.focus, want)
		}
	}
}

// TestDigitsTypeIntoTheFilterWhenFiltering is the priority rule.
//
// Typing a digit into a search is what the operator asked for; jumping panels is
// not. This is the same rule as `g` and `w`, and it falls out of filter mode
// running before the main key switch rather than being re-checked per key.
func TestDigitsTypeIntoTheFilterWhenFiltering(t *testing.T) {
	m := focusModel(t)
	m.focus = focusAgents
	m.filtering = true

	for _, r := range "123" {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	if m.focus != focusAgents {
		t.Errorf("typing digits moved focus to %v", m.focus)
	}
	if m.filterBuffer != "123" {
		t.Errorf("filter buffer is %q, want %q", m.filterBuffer, "123")
	}
}

// TestShiftTabIsInertWhileFiltering is the collision stage 6 and stage 4 had to
// resolve.
//
// Stage 4 originally cycled the match mode on shift+tab. It gave the key up so
// this stage could walk focus backwards, so shift+tab must now do nothing at all
// while a filter is open rather than quietly moving the panel underneath it.
func TestShiftTabIsInertWhileFiltering(t *testing.T) {
	m := focusModel(t)
	m.focus = focusFiles
	m.filtering = true
	m.filterMatch = matchSubstring

	m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})

	if m.focus != focusFiles {
		t.Errorf("shift+tab moved focus to %v while filtering", m.focus)
	}
	if m.filterMatch != matchSubstring {
		t.Errorf("shift+tab changed the match mode to %v", m.filterMatch)
	}
	if m.filterBuffer != "" {
		t.Errorf("shift+tab typed %q into the filter", m.filterBuffer)
	}
}

// TestCtrlROnlyReachesTheMainSwitchWhenNotFiltering pins the other half of the
// split: ctrl+r belongs to filter mode, so it must not also do something outside.
func TestCtrlROnlyReachesTheMainSwitchWhenNotFiltering(t *testing.T) {
	m := focusModel(t)
	m.filtering = false

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	m = updated.(*model)

	if m.filtering {
		t.Error("ctrl+r opened a filter")
	}
	if m.filterMatch != matchSubstring {
		t.Errorf("ctrl+r changed the match mode to %v outside filter mode", m.filterMatch)
	}
	if m.filterBuffer != "" {
		t.Errorf("ctrl+r typed %q outside filter mode", m.filterBuffer)
	}
}

// TestPanelTitlesCarryTheirNumber is what makes the digit keys discoverable.
func TestPanelTitlesCarryTheirNumber(t *testing.T) {
	cases := map[focusPane]string{
		focusAgents: "[1] Agents",
		focusFiles:  "[2] Files",
		focusMain:   "[3] Logs",
	}
	for pane, want := range cases {
		if got := panelTitle(pane); got != want {
			t.Errorf("panelTitle(%v) = %q, want %q", pane, got, want)
		}
	}

	m := focusModel(t)
	frame := stripANSI(m.View())
	for _, want := range []string{"[1] Agents", "[2] Files", "[3] Logs"} {
		if !strings.Contains(frame, want) {
			t.Errorf("the frame is missing the title %q:\n%s", want, frame)
		}
	}
}

// TestTitleNumberDoesNotBreakThePanelWidth checks the accounting.
//
// panel() measures the whole label when it pads the top border, so a longer
// title has to widen the border rather than being clipped or pushing the body
// out by four columns.
func TestTitleNumberDoesNotBreakThePanelWidth(t *testing.T) {
	m := focusModel(t)
	for _, width := range []int{20, 30, 44, 80} {
		for _, panel := range []string{
			m.renderAgents(width, 6),
			m.renderFiles(width, 6),
			m.renderMain(width, 6),
		} {
			for i, row := range strings.Split(panel, "\n") {
				if got := lipgloss.Width(row); got != width {
					t.Errorf("width %d row %d is %d columns:\n%s", width, i, got, panel)
				}
			}
		}
	}
}
