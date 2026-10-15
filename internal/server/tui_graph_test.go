package server

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Alsanea-Ala/logmon/internal/protocol"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// TestGraphGlyphCountMatchesLevels guards the one thing the glyph table and the
// level count have to agree on.
//
// graphGlyph indexes graphGlyphs by level and clamps to graphLevels, so a
// glyph added without bumping the constant would be silently unreachable and a
// constant bumped without a glyph would panic in the renderer rather than here.
func TestGraphGlyphCountMatchesLevels(t *testing.T) {
	if len(graphGlyphs) != graphLevels {
		t.Fatalf("graphGlyphs has %d entries, graphLevels is %d", len(graphGlyphs), graphLevels)
	}
	if graphGlyphs[0] != " " {
		t.Errorf("level 0 is %q, want a blank so a zero bucket reads as no traffic", graphGlyphs[0])
	}
	for i, glyph := range graphGlyphs[1:] {
		if lipgloss.Width(glyph) != 1 {
			t.Errorf("glyph %d is %d columns wide, want 1: %q", i+1, lipgloss.Width(glyph), glyph)
		}
	}
}

// TestSparklineRendersAtEveryWidth is the width contract.
//
// Width is chosen by the terminal, not by the code, so the bar has to be exact
// at every value rather than at the handful the size tests happen to use. A
// sparkline one column too wide pushes the peak label off the panel.
func TestSparklineRendersAtEveryWidth(t *testing.T) {
	counts := []int{0, 3, 12, 1, 7, 0, 30, 2, 9, 4}
	for width := 1; width <= 80; width++ {
		bar := sparkline(counts, width)
		if got := lipgloss.Width(bar); got != width {
			t.Fatalf("sparkline at width %d rendered %d columns: %q", width, got, bar)
		}
	}
}

// TestSparklineDropsOldBucketsAndPadsNew checks which end of the history the
// bar shows.
//
// Newest on the right is what makes the graph readable while it is live, and
// truncating from the right would freeze the visible window on the first minute
// of traffic.
func TestSparklineDropsOldBucketsAndPadsNew(t *testing.T) {
	// Ten buckets into a four-column bar: the newest four survive, 7 through
	// 10. Compared against the glyph each one maps to, rather than against a
	// literal, so the assertion is about which buckets are kept and not about
	// the ramp's arithmetic.
	bar := sparkline([]int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, 4)
	if len([]rune(bar)) != 4 {
		t.Fatalf("bar is %q, want 4 columns", bar)
	}
	for i, bucket := range []int{7, 8, 9, 10} {
		runes := []rune(bar)
		if want := graphGlyph(bucket, 10); string(runes[i]) != want {
			t.Errorf("column %d is %q, want bucket %d as %q", i, string(runes[i]), bucket, want)
		}
	}

	// Two buckets into an eight-column bar: the first six columns are blank,
	// so a young session grows to the right instead of starting stretched.
	short := sparkline([]int{5, 5}, 8)
	if !strings.HasPrefix(short, strings.Repeat(" ", 6)) {
		t.Errorf("bar %q is not left-padded for missing history", short)
	}
}

// TestSparklineZeroRateDrawsFlatLine covers the case the plan called out: no
// traffic is information, not the absence of a graph.
//
// A blank bar would be indistinguishable from a panel that failed to render,
// and during an outage that is exactly the moment it matters.
func TestSparklineZeroRateDrawsFlatLine(t *testing.T) {
	bar := sparkline([]int{0, 0, 0, 0}, 4)
	if strings.TrimSpace(bar) != "" {
		t.Errorf("zero rate drew %q, want a flat line of blanks", bar)
	}
	if graphGlyph(0, 10) != " " {
		t.Errorf("a zero bucket drew %q, want a blank", graphGlyph(0, 10))
	}
}

// TestGraphGlyphScalesToPeak checks the ramp reaches the top and starts above
// the blank, and that the extremes do not divide by zero.
//
// peak <= 1 is the degenerate case that a burst followed by silence produces,
// so it is not hypothetical.
func TestGraphGlyphScalesToPeak(t *testing.T) {
	cases := []struct {
		value, peak int
		want        int
	}{
		{0, 10, 0},  // blank: no traffic
		{1, 10, 1},  // lowest non-blank
		{10, 10, 7}, // exactly the peak is the top glyph
		{5, 10, 4},  // midpoint lands mid-ramp
		{1, 1, 7},   // degenerate peak does not divide by zero
		{3, 1, 7},   // a value above a peak of one still renders
		{-1, 10, 0}, // defensive: a negative bucket cannot happen, but blank
	}
	for _, c := range cases {
		if got := graphGlyph(c.value, c.peak); got != graphGlyphs[c.want] {
			t.Errorf("graphGlyph(%d, %d) = %q, want level %d", c.value, c.peak, got, c.want)
		}
	}
}

// TestPushBucketBoundsTheRing is the only thing stopping a session left open
// from growing a slice by one int per second for as long as it runs.
func TestPushBucketBoundsTheRing(t *testing.T) {
	var counts []int
	for range graphBuckets * 3 {
		counts = pushBucket(counts, 1)
		if len(counts) > graphBuckets {
			t.Fatalf("ring grew to %d, cap is %d", len(counts), graphBuckets)
		}
	}
	if len(counts) != graphBuckets {
		t.Fatalf("ring settled at %d buckets, want %d", len(counts), graphBuckets)
	}
}

// TestTickRollsPendingIntoTheRing drives the message, not the helper.
//
// The counter has to be reset by the tick or the next bucket reports the
// running total rather than the rate, and that only shows up when both cases
// are driven together.
func TestTickRollsPendingIntoTheRing(t *testing.T) {
	m := testModel(t, 140, 40)

	m.graphPending = 5
	m.fileGraphPending = 2

	updated, cmd := m.Update(tickMsg(time.Time{}))
	m = updated.(*model)

	if cmd == nil {
		t.Error("tickMsg returned no command, so no further bucket is ever drawn")
	}
	if len(m.graphCounts) != 1 || m.graphCounts[0] != 5 {
		t.Errorf("global buckets = %v, want [5]", m.graphCounts)
	}
	if len(m.fileGraphCounts) != 1 || m.fileGraphCounts[0] != 2 {
		t.Errorf("agent buckets = %v, want [2]", m.fileGraphCounts)
	}
	if m.graphPending != 0 || m.fileGraphPending != 0 {
		t.Errorf("pending not reset: global=%d agent=%d", m.graphPending, m.fileGraphPending)
	}

	// A quiet second appends a zero rather than skipping the bucket, so time
	// passing stays visible as time passing.
	updated, _ = m.Update(tickMsg(time.Time{}))
	m = updated.(*model)
	if len(m.graphCounts) != 2 || m.graphCounts[1] != 0 {
		t.Errorf("second bucket = %v, want a trailing zero", m.graphCounts)
	}
}

// TestTickDuringInFlightLoadIsHarmless is the caveat the plan named: this is
// the first timer in the model, and it fires whether or not a scan or a load
// is in flight.
//
// A tick that touched any of this would silently discard the load or reset the
// cursor, and the operator would see records vanish from the panel once a
// second.
func TestTickDuringInFlightLoadIsHarmless(t *testing.T) {
	m := testModel(t, 140, 40)
	m.graphPending = 3

	m.files = []logFile{{Date: "2026-10-15", App: "nginx", Agent: "prod-1", Path: "/tmp/prod-1.jsonl"}}
	m.fileIndex = 0
	m.loadingPath = "/tmp/prod-1.jsonl"
	m.pending = []protocol.StoredRecord{{AgentID: "prod-1", Line: "queued while loading"}}
	m.lineIndex = 1
	before := len(m.records)

	updated, _ := m.Update(tickMsg(time.Time{}))
	m = updated.(*model)

	if m.loadingPath != "/tmp/prod-1.jsonl" {
		t.Errorf("tick cleared the in-flight load path: %q", m.loadingPath)
	}
	if len(m.pending) != 1 {
		t.Errorf("tick dropped records queued for the pending load: %d left", len(m.pending))
	}
	if len(m.records) != before {
		t.Errorf("tick changed the record set: %d, want %d", len(m.records), before)
	}
	if m.lineIndex != 1 {
		t.Errorf("tick moved the cursor to %d, want 1", m.lineIndex)
	}
}

// TestGraphCountsEveryAgentAndTheSelectedOne checks the two counters are
// counted on opposite sides of the selection gate.
//
// The global rate is meant to cover every agent the server is ingesting, so
// counting it inside the gate would have made it describe only whichever file
// happened to be open.
func TestGraphCountsEveryAgentAndTheSelectedOne(t *testing.T) {
	m := testModel(t, 140, 40)
	// Files are left for the records themselves to create, because ensureFile
	// matches on the Path the record builds and a hand-written path would not
	// be the same one.
	m.files = nil
	m.records = nil
	stamp := time.Date(2026, 10, 15, 1, 22, 3, 0, time.UTC)

	for i, agent := range []string{"prod-1", "prod-1", "prod-2"} {
		updated, _ := m.Update(recordMsg(protocol.StoredRecord{
			AgentID: agent, App: "nginx", Category: "access",
			Line:       fmt.Sprintf("hello %d", i),
			ReceivedAt: stamp.Add(time.Duration(i) * time.Second),
		}))
		m = updated.(*model)
	}

	if m.graphPending != 3 {
		t.Errorf("global pending = %d, want 3 (every agent)", m.graphPending)
	}
	if m.fileGraphPending != 2 {
		t.Errorf("selected-agent pending = %d, want 2 (prod-1 only)", m.fileGraphPending)
	}
	if len(m.records) != 2 {
		t.Fatalf("log panel holds %d records, want 2 from the selected agent", len(m.records))
	}
	for _, rec := range m.records {
		if rec.AgentID != "prod-1" {
			t.Errorf("log panel holds a %s record, want only the selected agent", rec.AgentID)
		}
	}
}

// TestGraphRowsDeclareTheirPeak is why the peak is printed at all.
//
// A sparkline scaled to its own maximum is unreadable without the axis: a quiet
// hour and a burst produce the same shape. The reader has to be able to convert
// a bar back to a rate without guessing.
func TestGraphRowsDeclareTheirPeak(t *testing.T) {
	m := testModel(t, 140, 40)
	m.graphCounts = []int{0, 4, 40, 12}
	m.fileGraphCounts = []int{0, 1, 9, 2}

	rows := m.graphRows(80)
	if len(rows) != 2 {
		t.Fatalf("got %d series rows, want 2 (all agents, then the selected one)", len(rows))
	}
	if !strings.Contains(stripANSI(rows[0]), "all agents") {
		t.Errorf("first row does not name the global series: %q", stripANSI(rows[0]))
	}
	if !strings.Contains(stripANSI(rows[1]), "prod-1") {
		t.Errorf("second row does not name the selected agent: %q", stripANSI(rows[1]))
	}
	if !strings.Contains(stripANSI(rows[0]), "peak 40/s") {
		t.Errorf("global row does not declare its peak: %q", stripANSI(rows[0]))
	}
	if !strings.Contains(stripANSI(rows[1]), "peak 9/s") {
		t.Errorf("agent row does not declare its peak: %q", stripANSI(rows[1]))
	}
}

// TestGraphRowsOmitAgentSeriesWithNoFile checks a single row with no file open.
//
// An unnamed, permanently flat second row reads as a broken series rather than
// as the absence of a selection.
func TestGraphRowsOmitAgentSeriesWithNoFile(t *testing.T) {
	m := newModel(t.TempDir())
	m.graphCounts = []int{1, 2, 3}

	if rows := m.graphRows(80); len(rows) != 1 {
		t.Fatalf("got %d rows with no file selected, want 1", len(rows))
	}
}

// TestGraphRowsFitThePanel checks the bar gives up its space before the label
// or the peak does.
//
// The panel width comes from the terminal, and the peak is the only part of
// the row that makes the bar interpretable.
func TestGraphRowsFitThePanel(t *testing.T) {
	m := testModel(t, 140, 40)
	m.graphCounts = []int{0, 1, 2, 3, 4, 5, 6, 7}

	for _, width := range []int{4, 10, 20, 40, 80, 200} {
		for i, row := range m.graphRows(width) {
			if got := lipgloss.Width(row); got > width {
				t.Errorf("row %d at width %d is %d columns", i, width, got)
			}
		}
	}
}

// TestGraphToggle checks `g`, and that the key does not fire while a filter is
// open.
//
// A `g` typed into a search for "gateway" must reach the buffer; the toggle is
// the wrong thing to do with it.
func TestGraphToggle(t *testing.T) {
	m := testModel(t, 140, 40)
	if !m.graphEnabled {
		t.Fatal("graph is off by default, want on with the row budget as the veto")
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	m = updated.(*model)
	if m.graphEnabled {
		t.Error("`g` did not turn the graph off")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	m = updated.(*model)
	if !m.graphEnabled {
		t.Error("`g` did not turn the graph back on")
	}

	m.filtering = true
	m.filterBuffer = ""
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	m = updated.(*model)
	if !m.graphEnabled {
		t.Error("`g` toggled the graph instead of typing into the filter")
	}
	if m.filterBuffer != "g" {
		t.Errorf("filter buffer is %q, want %q", m.filterBuffer, "g")
	}
}

// TestGraphHidesWhenThePanelIsShort is the row budget, which is the real veto.
//
// At minTTYHeight the log panel has four interior rows. Spending three of them
// on the graph would leave a single log line, so the graph is what goes.
func TestGraphHidesWhenThePanelIsShort(t *testing.T) {
	m := testModel(t, 140, 40)

	if !m.graphVisible(chromeHeight + graphMinRows) {
		t.Error("graph is hidden at exactly the threshold height")
	}
	if m.graphVisible(chromeHeight + graphMinRows - 1) {
		t.Error("graph is shown one row below the threshold")
	}

	// The minimum supported terminal, whole, must not grow the frame.
	m.graphCounts = []int{0, 3, 40, 12, 5}
	m.width, m.height = 45, minTTYHeight
	if m.graphVisible(m.computeLayout().mainH) {
		t.Errorf("graph is visible at %dx%d, which cannot afford it", minTTYWidth, minTTYHeight)
	}
	if rows := strings.Split(m.View(), "\n"); len(rows) != minTTYHeight {
		t.Errorf("frame is %d rows at %dx%d, want %d", len(rows), minTTYWidth, minTTYHeight, minTTYHeight)
	}
}

// TestGraphAppearsInTheLogPanel checks the graph is actually drawn where it is
// supposed to be, above the log lines.
func TestGraphAppearsInTheLogPanel(t *testing.T) {
	m := testModel(t, 140, 40)
	m.graphCounts = []int{0, 0, 17, 40, 12}
	m.fileGraphCounts = []int{0, 0, 4, 9, 2}

	frame := stripANSI(m.View())
	if !strings.Contains(frame, "all agents") || !strings.Contains(frame, "peak 40/s") {
		t.Fatalf("graph is not in the frame:\n%s", frame)
	}

	lines := strings.Split(frame, "\n")
	graphAt, headerAt := -1, -1
	for i, line := range lines {
		if graphAt < 0 && strings.Contains(line, "all agents") {
			graphAt = i
		}
		if headerAt < 0 && strings.Contains(line, "nginx / prod-1") {
			headerAt = i
		}
	}
	if graphAt < 0 || headerAt < 0 {
		t.Fatalf("missing graph (%d) or file header (%d) in the frame", graphAt, headerAt)
	}
	if graphAt > headerAt {
		t.Errorf("graph is at row %d, below the file header at row %d", graphAt, headerAt)
	}
}

// TestGraphRowsAreBoxAligned is the same class of assertion the panel tests
// make, applied to the block glyphs.
//
// The glyphs are multi-byte and the peak is right-aligned by the renderer, so
// this is where a width mistake in the bar shows up as a ragged right edge
// rather than as a compile error.
func TestGraphRowsAreBoxAligned(t *testing.T) {
	m := testModel(t, 140, 40)
	m.graphCounts = []int{0, 1, 2, 3, 4, 5, 6, 7}
	m.fileGraphCounts = []int{0, 4, 40, 12}

	rows := m.graphRows(72)
	first := lipgloss.Width(stripANSI(rows[0]))
	for i, row := range rows[1:] {
		if got := lipgloss.Width(stripANSI(row)); got != first {
			t.Errorf("series row %d is %d columns, row 0 is %d: ragged graph block",
				i+1, got, first)
		}
	}
}

// TestInitStartsTheGraphTick covers the one path that schedules a tick without
// a prior tick having arrived.
func TestInitStartsTheGraphTick(t *testing.T) {
	m := newModel(t.TempDir())
	if cmd := m.Init(); cmd == nil {
		t.Fatal("Init returned no command, so storage is never scanned and the graph never ticks")
	}
}

// ansiRe matches the SGR sequences lipgloss emits. The graph rows are styled,
// so assertions about their text or width have to read the plain string.
var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}
