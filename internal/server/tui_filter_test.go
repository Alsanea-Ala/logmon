package server

import (
	"strings"
	"testing"
	"time"

	"github.com/Alsanea-Ala/logmon/internal/protocol"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func filterModel(t *testing.T) *model {
	t.Helper()
	m := testModel(t, 140, 40)
	m.records = []protocol.StoredRecord{
		{AgentID: "prod-1", Category: "access", Line: "GET /health 200"},
		{AgentID: "prod-2", Category: "error", Line: "error: disk full"},
		{AgentID: "prod-1", Category: "error", Line: "ERROR connection refused"},
	}
	m.categories = []string{"all", "access", "error"}
	return m
}

// TestFilterScopesMatchOnlyTheirOwnField is the whole point of the scope
// control. Matching the wrong field returns plausible-looking results, so a
// scope that is ignored does not fail loudly.
func TestFilterScopesMatchOnlyTheirOwnField(t *testing.T) {
	cases := []struct {
		scope filterScope
		term  string
		want  []string
	}{
		{filterMessage, "disk", []string{"error: disk full"}},
		{filterCategory, "disk", nil},
		{filterCategory, "access", []string{"GET /health 200"}},
		{filterAgent, "disk", nil},
		{filterAgent, "prod-2", []string{"error: disk full"}},
	}
	for _, c := range cases {
		m := filterModel(t)
		m.filterScope = c.scope
		m.setFilter(c.term)

		got := m.filtered()
		if len(got) != len(c.want) {
			t.Errorf("scope %v term %q matched %d records, want %d",
				c.scope, c.term, len(got), len(c.want))
			continue
		}
		for i := range got {
			if got[i].Line != c.want[i] {
				t.Errorf("scope %v term %q matched %q, want %q",
					c.scope, c.term, got[i].Line, c.want[i])
			}
		}
	}
}

// TestFilterIsCaseInsensitiveInBothModes covers the consistency rule: toggling
// the match mode must not silently change case sensitivity.
func TestFilterIsCaseInsensitiveInBothModes(t *testing.T) {
	for _, match := range []filterMatch{matchSubstring, matchRegex} {
		m := filterModel(t)
		m.filterMatch = match
		m.setFilter("error")

		if got := len(m.filtered()); got != 2 {
			t.Errorf("match %v matched %d records, want 2 (case-insensitive)", match, got)
		}
	}
}

// TestFilterRegexIsRealRegex checks the mode does something substring cannot.
func TestFilterRegexIsRealRegex(t *testing.T) {
	m := filterModel(t)
	m.filterMatch = matchRegex
	m.setFilter(`^\w+ /`)
	if got := len(m.filtered()); got != 1 {
		t.Errorf("regex matched %d records, want 1", got)
	}
}

// TestFilterTabCyclesScopeAndWraps is what the status bar advertises.
func TestFilterTabCyclesScopeAndWraps(t *testing.T) {
	m := filterModel(t)
	m.filtering = true

	seen := []filterScope{}
	for range filterScopeCount + 1 {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
		m = updated.(*model)
		seen = append(seen, m.filterScope)
	}

	want := []filterScope{filterCategory, filterAgent, filterMessage, filterCategory}
	for i, scope := range want {
		if seen[i] != scope {
			t.Errorf("tab %d gave scope %v, want %v", i+1, seen[i], scope)
		}
	}
}

// TestFilterCtrlRTogglesMatch checks the key that replaced shift+tab.
func TestFilterCtrlRTogglesMatch(t *testing.T) {
	m := filterModel(t)
	m.filtering = true

	for i, want := range []filterMatch{matchRegex, matchSubstring, matchRegex} {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
		m = updated.(*model)
		if m.filterMatch != want {
			t.Errorf("ctrl+r press %d gave match %v, want %v", i+1, m.filterMatch, want)
		}
	}
}

// TestFilterInvalidRegexKeepsLastGoodResults is the reason the compiled regex
// is cached rather than discarded on failure.
//
// A log panel that empties on every half-typed pattern is a panel you stop
// trusting, and the operator has no way back except retyping the whole thing.
func TestFilterInvalidRegexKeepsLastGoodResults(t *testing.T) {
	m := filterModel(t)
	m.filtering = true
	m.filterMatch = matchRegex

	for _, r := range "error" {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	good := len(m.filtered())
	if good == 0 {
		t.Fatal("valid pattern matched nothing")
	}

	// Type an unbalanced bracket: the pattern is now invalid.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'('}})
	if !m.filterBad {
		t.Error("an uncompilable pattern did not raise filterBad")
	}
	if got := len(m.filtered()); got != good {
		t.Errorf("an invalid pattern changed the result set from %d to %d", good, got)
	}
	if !strings.Contains(stripANSI(m.renderStatusBar(false)), "bad pattern") {
		t.Error("the status bar does not say the pattern is bad")
	}

	// Closing the bracket recovers without retyping.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{')'}})
	if m.filterBad {
		t.Error("the pattern did not recover once it became valid")
	}
	if got := len(m.filtered()); got == 0 {
		t.Error("recovering the pattern filtered everything away")
	}
}

// TestFilterNarrowsLiveAsCharactersArrive checks typing filters immediately,
// which is what live filtering means. On enter only, every keystroke here would
// have left the panel showing the unfiltered list.
func TestFilterNarrowsLiveAsCharactersArrive(t *testing.T) {
	m := filterModel(t)
	m.filtering = true

	// Every line contains an "e"; two of them contain "er" and both still do
	// after a third "r", because the word stops being "err".
	for _, step := range []struct {
		r    rune
		want int
	}{{'e', 3}, {'r', 2}, {'r', 2}} {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{step.r}})
		if got := len(m.filtered()); got != step.want {
			t.Errorf("after %q the panel holds %d records, want %d",
				m.filter, got, step.want)
		}
	}

	if !m.filtering {
		t.Error("typing closed the filter; narrowing must not wait on enter")
	}
}

// TestFilterBackspaceEditsByRune is the multi-byte case.
//
// Cutting one byte off the end of a multi-byte character leaves invalid UTF-8
// in the buffer, which renders as a replacement box in the status bar and does
// not match the regex it compiles into.
func TestFilterBackspaceEditsByRune(t *testing.T) {
	m := filterModel(t)
	m.filtering = true
	for _, r := range "düsseldorf" {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if m.filter != "düsseldor" {
		t.Errorf("after one backspace the filter is %q, want %q", m.filter, "düsseldor")
	}
	if !strings.Contains(m.filter, "ü") {
		t.Errorf("backspace cut a byte rather than a rune: %q", m.filter)
	}
}

// TestFilterEnterKeepsAndEscClears checks the two exits are different.
//
// esc leaving the pattern in place would mean the key that is advertised as
// "clear the filter" did not clear it.
func TestFilterEnterKeepsAndEscClears(t *testing.T) {
	m := filterModel(t)
	m.filtering = true
	m.setFilter("disk")

	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.filtering {
		t.Error("enter did not close the filter")
	}
	if m.filter != "disk" {
		t.Errorf("enter discarded the pattern: %q", m.filter)
	}
	if len(m.filtered()) != 1 {
		t.Error("enter un-filtered the panel")
	}

	m.filtering = true
	m.filterBuffer = "disk"
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.filter != "" {
		t.Errorf("esc left the pattern %q in place", m.filter)
	}
	if m.filtering {
		t.Error("esc did not close the filter")
	}
	if len(m.filtered()) != 3 {
		t.Error("esc did not restore the unfiltered panel")
	}
}

// TestEmptyStateDistinguishesNoLogsFromNoMatch is the reason this is a change
// at all: one string cannot answer both questions, and conflating them makes a
// working filter look like data loss.
func TestEmptyStateDistinguishesNoLogsFromNoMatch(t *testing.T) {
	m := filterModel(t)
	m.records = nil

	empty := stripANSI(m.renderMain(100, 14))
	if !strings.Contains(empty, "(no logs)") {
		t.Errorf("an empty file does not say (no logs):\n%s", empty)
	}

	m.setFilter("nothing matches this")
	filtered := stripANSI(m.renderMain(100, 14))
	if !strings.Contains(filtered, "no records match") {
		t.Errorf("a filtered-out panel does not say so:\n%s", filtered)
	}
	if strings.Contains(filtered, "(no logs)") {
		t.Errorf("a filtered-out panel claims to have no logs:\n%s", filtered)
	}
	if !strings.Contains(filtered, "nothing matches this") {
		t.Errorf("the empty state does not quote the pattern:\n%s", filtered)
	}
}

// TestFilteredCacheInvalidatesOnAppend is the reason the cache exists at all.
//
// Without it, adding one record to a panel of a thousand silently drops every
// other record from view, and the panel looks frozen.
func TestFilteredCacheInvalidatesOnAppend(t *testing.T) {
	m := filterModel(t)
	m.setFilter("200")
	if got := len(m.filtered()); got != 1 {
		t.Fatalf("precondition: filter holds %d records, want 1", got)
	}

	m.Update(recordMsg(protocol.StoredRecord{
		AgentID: "prod-1", Category: "access", Line: "GET /metrics 200", ReceivedAt: time.Now(),
	}))
	if got := len(m.filtered()); got != 2 {
		t.Errorf("after an append the filter holds %d records, want 2", got)
	}

	m.setFilter("404")
	if got := len(m.filtered()); got != 0 {
		t.Errorf("after changing the pattern the filter holds %d records, want 0", got)
	}
}

// TestFilteredCacheInvalidatesOnCategoryChange covers the other key input.
func TestFilteredCacheInvalidatesOnCategoryChange(t *testing.T) {
	m := filterModel(t)
	m.categoryIndex = 0
	if got := len(m.filtered()); got != 3 {
		t.Fatalf("precondition: unfiltered holds %d records, want 3", got)
	}

	m.categoryIndex = 2
	if got := len(m.filtered()); got != 2 {
		t.Errorf("under the error category the panel holds %d records, want 2", got)
	}
}

// TestLineIndexClampsWhenTheSetShrinks keeps the cursor from pointing past the
// end of what is on screen.
//
// Resetting the cursor to zero on every keystroke would also be wrong, and in
// the other direction: it moves the panel out from under an operator who has
// scrolled down and started typing.
func TestLineIndexClampsWhenTheSetShrinks(t *testing.T) {
	m := filterModel(t)
	m.lineIndex = 2
	if got := len(m.filtered()); got != 3 {
		t.Fatalf("precondition: panel holds %d records, want 3", got)
	}

	m.setFilter("error")
	if m.lineIndex > len(m.filtered())-1 {
		t.Errorf("cursor is %d with only %d records", m.lineIndex, len(m.filtered()))
	}

	// Filtering to nothing must not leave a negative or dangling cursor.
	m.setFilter("nothing at all matches")
	if m.lineIndex != 0 {
		t.Errorf("cursor is %d on an empty panel, want 0", m.lineIndex)
	}
}

// TestWrapIsOffByDefaultAndWToggles checks the default and the key.
func TestWrapIsOffByDefaultAndWToggles(t *testing.T) {
	m := testModel(t, 140, 40)
	if m.wrap {
		t.Error("wrapping is on by default; truncation is the default")
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	m = updated.(*model)
	if !m.wrap {
		t.Error("`w` did not turn wrapping on")
	}

	// `w` must type into a filter rather than toggling.
	m.filtering = true
	m.filterBuffer = ""
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	if !m.wrap {
		t.Error("`w` toggled wrapping instead of typing into the filter")
	}
	if m.filterBuffer != "w" {
		t.Errorf("filter buffer is %q, want %q", m.filterBuffer, "w")
	}
}

// TestWrappedRecordIndentsContinuation checks the visual contract: one record
// spans several rows, and the rows after the first are indented so they read as
// the same entry.
func TestWrappedRecordIndentsContinuation(t *testing.T) {
	m := testModel(t, 140, 40)
	m.wrap = true
	// Nothing else in the panel, so every body row belongs to this record.
	m.graphEnabled = false
	m.files = nil
	m.records = []protocol.StoredRecord{{Category: "app", Line: strings.Repeat("word ", 60)}}

	frame := stripANSI(m.renderMain(100, 20))
	body := logBody(t, frame)

	if len(body) < 2 {
		t.Fatalf("a 300-character record produced %d rows, want several:\n%s", len(body), frame)
	}

	innerWidth := 100 - textWidth
	indent := strings.Repeat(" ", wrapIndent(lipgloss.Width(m.recordGutter(m.records[0], false, innerWidth)), innerWidth))
	for i, row := range body[1:] {
		if !strings.HasPrefix(row, indent) {
			t.Errorf("continuation row %d is not indented:\n%q\nwant prefix %q",
				i+1, row, indent)
		}
	}
}

// TestCursorStaysOnTheFirstRowOfAWrappedRecord keeps one record from reading as
// several selected records.
func TestCursorStaysOnTheFirstRowOfAWrappedRecord(t *testing.T) {
	m := testModel(t, 140, 40)
	m.wrap = true
	m.records = []protocol.StoredRecord{{Category: "app", Line: strings.Repeat("word ", 60)}}
	m.lineIndex = 0

	body := logBody(t, stripANSI(m.renderMain(100, 20)))
	marked := 0
	for _, row := range body {
		if strings.Contains(row, "►") {
			marked++
		}
	}
	if marked != 1 {
		t.Errorf("%d rows carry the cursor marker, want 1", marked)
	}
}

// TestWrapCapsARecordAndMarksTheCut is the constraint the cap exists for.
//
// `MaxLineSize` permits 128 KiB, which is over a thousand rows at panel width.
// Uncapped, one record would be the entire panel.
func TestWrapCapsARecordAndMarksTheCut(t *testing.T) {
	huge := strings.Repeat("x", 128*1024)
	rows := wrapText(huge, 40, wrapMaxRows)

	if len(rows) != wrapMaxRows {
		t.Errorf("a 128 KiB line wrapped to %d rows, want the cap of %d", len(rows), wrapMaxRows)
	}
	if !strings.HasSuffix(rows[len(rows)-1], wrapMarker) {
		t.Errorf("the cut row does not say it was cut: %q", rows[len(rows)-1])
	}
	for i, row := range rows {
		if w := lipgloss.Width(row); w > 40 {
			t.Errorf("row %d is %d columns, want at most 40", i, w)
		}
	}
}

// TestBreakLineHandlesUnbreakableTokens is the case word wrapping alone gets
// wrong: a URL or a base64 blob has no spaces, and treating those as
// unbreakable drops the rest of the line.
func TestBreakLineHandlesUnbreakableTokens(t *testing.T) {
	chunk, rest := breakLine(strings.Repeat("a", 100), 30)
	if lipgloss.Width(chunk) != 30 {
		t.Errorf("chunk is %d columns, want 30: %q", lipgloss.Width(chunk), chunk)
	}
	if len(rest) != 70 {
		t.Errorf("remainder is %d characters, want 70", len(rest))
	}

	// Prefers the last space that fits, which is greedy wrapping: "alpha beta"
	// goes down together rather than leaving "beta" alone on the next row.
	chunk, rest = breakLine("alpha beta gamma delta", 12)
	if chunk != "alpha beta" {
		t.Errorf("chunk is %q, want %q", chunk, "alpha beta")
	}
	if strings.HasSuffix(chunk, " ") || strings.HasPrefix(rest, " ") {
		t.Errorf("break left whitespace at the seam: %q / %q", chunk, rest)
	}
	if rest != "gamma delta" {
		t.Errorf("remainder is %q, want %q", rest, "gamma delta")
	}
}

// TestWrapIndentShrinksOnNarrowPanels keeps the gutter from eating the panel.
//
// The gutter is about 28 columns. Aligning continuation text under the message
// column on a 40-column panel leaves about eight.
func TestWrapIndentShrinksOnNarrowPanels(t *testing.T) {
	if got := wrapIndent(28, 76); got != 28 {
		t.Errorf("wide panel indent is %d, want 28 (aligned under the message)", got)
	}
	if got := wrapIndent(28, 36); got != wrapNarrowIndent {
		t.Errorf("narrow panel indent is %d, want %d", got, wrapNarrowIndent)
	}
}

// TestWindowIsMeasuredInRowsNotRecords is the change wrapping forced.
//
// Counting records against a row budget draws too many as soon as one record
// occupies more than one row, and the frame then overflows the terminal.
func TestWindowIsMeasuredInRowsNotRecords(t *testing.T) {
	m := testModel(t, 140, 40)
	m.records = nil
	for range 40 {
		m.records = append(m.records, protocol.StoredRecord{
			Category: "app", Line: strings.Repeat("word ", 40),
		})
	}
	m.lineIndex = 39

	width := 100
	gutterOf := func(i int) (string, int) {
		gutter := m.recordGutter(m.records[i], i == m.lineIndex, width-textWidth)
		return gutter, max(1, (width-textWidth)-lipgloss.Width(gutter))
	}

	for _, budget := range []int{1, 3, 8, 25} {
		m.wrap = true
		start, end := m.windowRecords(m.records, budget, width-textWidth, gutterOf)
		if start > end {
			t.Errorf("budget %d produced an inverted window %d..%d", budget, start, end)
		}
		if end-start > budget {
			t.Errorf("budget %d produced %d records, which is at least %d rows",
				budget, end-start, end-start)
		}
	}
}

// TestWrappedFrameStillFitsEveryTerminalSize re-runs the size assertions with
// wrapping on, which is where a row budget that ignores wrapping shows up as a
// frame taller than the terminal.
func TestWrappedFrameStillFitsEveryTerminalSize(t *testing.T) {
	sizes := [][2]int{{140, 40}, {120, 30}, {100, 24}, {80, 24}, {60, 18}, {45, 14}}
	for _, size := range sizes {
		m := testModel(t, size[0], size[1])
		m.wrap = true
		m.records = []protocol.StoredRecord{
			{Category: "access", Line: strings.Repeat("word ", 500)},
			{Category: "error", Line: strings.Repeat("x", 4096)},
		}
		m.lineIndex = 0

		frame := m.View()
		rows := strings.Split(frame, "\n")
		if len(rows) != size[1] {
			t.Errorf("frame is %d rows at %dx%d with wrapping on:\n%s",
				len(rows), size[0], size[1], frame)
		}
		for i, row := range rows {
			if w := lipgloss.Width(row); w > size[0] {
				t.Errorf("row %d is %d cols at %dx%d", i, w, size[0], size[1])
			}
		}
	}
}

// logBody is the panel's interior rows, borders and header stripped.
//
// Blank rows are dropped: panel() pads the body out to the panel height, and
// padding is not part of what a test is asserting about.
func logBody(t *testing.T, frame string) []string {
	t.Helper()
	var body []string
	for _, row := range strings.Split(frame, "\n") {
		if !strings.HasPrefix(row, "│") {
			continue
		}
		row = strings.TrimSuffix(strings.TrimPrefix(row, "│"), "│")
		trimmed := strings.TrimRight(row, " ")
		if trimmed == "" || strings.HasPrefix(strings.TrimSpace(trimmed), "─") {
			continue
		}
		body = append(body, trimmed)
	}
	return body
}
