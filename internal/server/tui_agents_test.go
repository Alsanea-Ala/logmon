package server

import (
	"strings"
	"testing"
	"time"

	"github.com/Alsanea-Ala/logmon/internal/protocol"
	"github.com/charmbracelet/lipgloss"
)

func agentsModel(t *testing.T) *model {
	t.Helper()
	m := testModel(t, 120, 30)
	now := time.Now()
	m.agents = map[string]agentStatus{
		"prod-1": {ID: "prod-1", Hostname: "web-01", Connected: true, LastSeen: now.Add(-2 * time.Second)},
		"prod-2": {ID: "prod-2", Hostname: "db-02", LastSeen: now.Add(-41 * time.Minute)},
		// Never seen: LastSeen stays zero.
		"prod-3": {ID: "prod-3", Hostname: "cache-1"},
	}
	m.records = nil
	for i := range 12 {
		m.records = append(m.records, protocol.StoredRecord{
			AgentID: "prod-1", Category: "access",
			Line: "GET /health 200", ReceivedAt: now.Add(time.Duration(i) * time.Second),
		})
	}
	return m
}

// TestAgentCountMatchesTheFilteredSet is why the count is derived from
// filtered() instead of being a running total.
//
// A stored total has to be updated on every record and disagrees with whatever
// filter is active. A count that does not match the panel above it is worse
// than no count at all.
func TestAgentCountMatchesTheFilteredSet(t *testing.T) {
	m := agentsModel(t)
	counts := m.agentRecordCounts()

	if got := counts["prod-1"]; got != 12 {
		t.Errorf("prod-1 counted %d records, want 12", got)
	}
	if got := counts["prod-2"]; got != 0 {
		t.Errorf("prod-2 counted %d records, want 0", got)
	}
	if got := counts["prod-3"]; got != 0 {
		t.Errorf("prod-3 counted %d records, want 0", got)
	}

	// Every count must equal what the panel is actually showing.
	for _, id := range m.sortedAgentIDs() {
		want := 0
		for _, record := range m.filtered() {
			if record.AgentID == id {
				want++
			}
		}
		if counts[id] != want {
			t.Errorf("agent %s counted %d, filtered() holds %d for it", id, counts[id], want)
		}
	}
}

// TestAgentCountFollowsTheFilter is what makes the column agree with the panel
// above it rather than describing a different question.
func TestAgentCountFollowsTheFilter(t *testing.T) {
	m := agentsModel(t)

	m.setFilter("health")
	if got := m.agentRecordCounts()["prod-1"]; got != 12 {
		t.Errorf("under a matching filter the count is %d, want 12", got)
	}

	m.setFilter("nothing matches")
	if got := m.agentRecordCounts()["prod-1"]; got != 0 {
		t.Errorf("under a non-matching filter the count is %d, want 0", got)
	}
}

// TestRelativeTimeNeverRendersAZeroTimeAsAnAge is the case that made this a
// separate function.
//
// agentStatus.LastSeen is zero until an agent connects. Rendering the zero time
// as a duration gives an age of about twenty thousand days, which reads as
// "this agent has not spoken since the year 1900" rather than "never seen".
func TestRelativeTimeNeverRendersAZeroTimeAsAnAge(t *testing.T) {
	now := time.Now()

	if got := relativeTime(time.Time{}, now); got != "never" {
		t.Errorf("a zero LastSeen renders as %q, want %q", got, "never")
	}
	// A clock skew that puts LastSeen in the future must not produce a
	// negative age either.
	if got := relativeTime(now.Add(time.Minute), now); got != "now" {
		t.Errorf("a future LastSeen renders as %q, want %q", got, "now")
	}
}

func TestRelativeTimeResolutions(t *testing.T) {
	now := time.Now()
	cases := []struct {
		ago  time.Duration
		want string
	}{
		{500 * time.Millisecond, "now"},
		{2 * time.Second, "2s ago"},
		{59 * time.Second, "59s ago"},
		{time.Minute, "1m ago"},
		{59 * time.Minute, "59m ago"},
		{time.Hour, "1h ago"},
		{23 * time.Hour, "23h ago"},
		{25 * time.Hour, "1d ago"},
	}
	for _, c := range cases {
		if got := relativeTime(now.Add(-c.ago), now); got != c.want {
			t.Errorf("relativeTime(-%v) = %q, want %q", c.ago, got, c.want)
		}
	}
}

// TestAgentColumnsDropInOrder checks which information is given up first, by
// finding the narrowest width at which each column is still present.
//
// The record count is the cheapest to recompute, last-seen is the next most
// useful, and the hostname is how an operator recognises their own machine. So
// the count must disappear before last-seen, and last-seen before the hostname.
func TestAgentColumnsDropInOrder(t *testing.T) {
	m := agentsModel(t)
	// A distinctive record count, so "the column is gone" is a fact about the
	// layout rather than a guess about which column a digit belongs to.
	m.records = nil
	for range 1204 {
		m.records = append(m.records, protocol.StoredRecord{
			AgentID: "prod-1", Category: "access", Line: "GET /health 200",
		})
	}
	ids := m.sortedAgentIDs()

	firstCount, firstSeen, firstHost := 0, 0, 0
	for inner := 1; inner <= 220; inner++ {
		row := stripANSI(m.agentRows(ids, inner)[0])
		if firstCount == 0 && strings.Contains(row, "1,204") {
			firstCount = inner
		}
		if firstSeen == 0 && strings.Contains(row, "2s ago") {
			firstSeen = inner
		}
		if firstHost == 0 && strings.Contains(row, "web-01") {
			firstHost = inner
		}
	}

	if firstHost == 0 {
		t.Fatal("the hostname column never fit, even at 220 columns")
	}
	if firstSeen < firstHost {
		t.Errorf("last-seen appeared at %d, before the hostname at %d; "+
			"the hostname must be the one given up last", firstSeen, firstHost)
	}
	if firstCount < firstSeen {
		t.Errorf("the record count appeared at %d, before last-seen at %d; "+
			"the count must be the column given up first", firstCount, firstSeen)
	}

	// The ID is the identity, and has to survive at every width the sidebar
	// can actually reach: sidebarMinWidth before the panel chrome.
	floor := sidebarMinWidth - textWidth
	for inner := floor; inner <= 220; inner++ {
		for i, row := range m.agentRows(ids, inner) {
			if !strings.Contains(stripANSI(row), "prod-") {
				t.Fatalf("at innerWidth %d the agent ID was dropped from row %d: %q",
					inner, i, stripANSI(row))
			}
		}
	}
}

// TestAgentRowsNeverExceedThePanelWidth is the assertion that catches a column
// width computed from one agent and applied to another.
func TestAgentRowsNeverExceedThePanelWidth(t *testing.T) {
	m := agentsModel(t)
	// An agent with a deliberately long name, so the column maths has to use
	// the widest row and not the first.
	m.agents["prod-4"] = agentStatus{
		ID: "a-very-long-agent-identifier", Hostname: "an-extremely-long-hostname.example.internal",
		Connected: true, LastSeen: time.Now(),
	}
	ids := m.sortedAgentIDs()

	for width := 6; width <= 200; width++ {
		for i, row := range m.agentRows(ids, width) {
			if got := lipgloss.Width(stripANSI(row)); got > width {
				t.Errorf("agent row %d is %d columns at width %d: %q",
					i, got, width, stripANSI(row))
			}
		}
	}
}

// TestAgentPanelFitsItsSmallestSize runs the panel through the sidebar minimum,
// which is where the dropping actually happens.
func TestAgentPanelFitsItsSmallestSize(t *testing.T) {
	m := agentsModel(t)
	for _, width := range []int{sidebarMinWidth, 32, 40, 60} {
		panel := stripANSI(m.renderAgents(width, 8))
		rows := strings.Split(panel, "\n")
		for i, row := range rows {
			if got := lipgloss.Width(row); got != width {
				t.Errorf("row %d is %d columns at width %d:\n%s", i, got, width, panel)
			}
		}
	}
}

// TestFormatCount checks the separator, which is cosmetic but is the reason the
// column lines up by eye.
func TestFormatCount(t *testing.T) {
	cases := map[int]string{0: "0", 7: "7", 42: "42", 999: "999", 1000: "1,000", 1204: "1,204", 1000000: "1,000,000"}
	for in, want := range cases {
		if got := formatCount(in); got != want {
			t.Errorf("formatCount(%d) = %q, want %q", in, got, want)
		}
	}
}
