package server

import (
	"strings"
	"testing"
	"time"

	"github.com/Alsanea-Ala/logmon/internal/protocol"
	tea "github.com/charmbracelet/bubbletea"
)

var windowNow = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

func windowModel(t *testing.T) *model {
	t.Helper()
	m := testModel(t, 140, 40)
	m.now = func() time.Time { return windowNow }
	m.records = nil
	for _, age := range []time.Duration{
		time.Minute, 10 * time.Minute, 30 * time.Minute,
		2 * time.Hour, 5 * time.Hour, 30 * time.Hour, 10 * 24 * time.Hour,
	} {
		m.records = append(m.records, protocol.StoredRecord{
			AgentID: "prod-1", Category: "access", Line: "GET /",
			ReceivedAt: windowNow.Add(-age),
		})
	}
	return m
}

// TestWindowDefaultsToNoLimit guards the zero value.
//
// windowIndex's zero value is the first option, which is 5m, so a model built
// without the explicit default opens with a filter the operator never asked
// for. That is invisible until real timestamps are in play: a test record with
// a zero ReceivedAt falls outside every window and the panel looks empty.
func TestWindowDefaultsToNoLimit(t *testing.T) {
	m := newModel(t.TempDir())
	if m.window() != 0 {
		t.Errorf("a fresh model has a %v window, want no limit", m.window())
	}
	if got := m.windowLabel(); got != "all" {
		t.Errorf("a fresh model labels its window %q, want %q", got, "all")
	}
	if m.windowBucket() != "" {
		t.Error("an unlimited window still contributes a cache bucket")
	}
}

// TestWindowExcludesRecordsOutsideIt is the behaviour itself.
func TestWindowExcludesRecordsOutsideIt(t *testing.T) {
	m := windowModel(t)

	for _, c := range []struct {
		index int
		label string
		want  int
	}{
		{0, "5m", 1},  // only the one-minute-old record
		{1, "1h", 3},  // 1m, 10m, 30m
		{2, "24h", 5}, // the above plus 2h and 5h
		{3, "all", 7}, // everything
	} {
		m.windowIndex = c.index
		if got := len(m.filtered()); got != c.want {
			t.Errorf("window %s holds %d records, want %d", c.label, got, c.want)
		}
		if got := m.windowLabel(); got != c.label {
			t.Errorf("window label is %q, want %q", got, c.label)
		}
	}
}

// TestWindowKeyCyclesAndWraps checks `t`, including the return to no limit.
func TestWindowKeyCyclesAndWraps(t *testing.T) {
	m := windowModel(t)
	m.windowIndex = windowIndexAll

	want := []string{"5m", "1h", "24h", "all", "5m"}
	for i, label := range want {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
		m = updated.(*model)
		if got := m.windowLabel(); got != label {
			t.Fatalf("press %d gave window %q, want %q", i+1, got, label)
		}
	}
}

// TestWindowAgesOutOfTheCache is the reason the clock is injectable.
//
// A time window makes filtered() a function of the current time. If the cache
// key did not move with the clock, the cached result would keep serving the
// answer for the cutoff it was computed at and records would never age out:
// the window would appear to do nothing for as long as the file was open.
func TestWindowAgesOutOfTheCache(t *testing.T) {
	m := windowModel(t)
	m.windowIndex = 0 // 5m

	if got := len(m.filtered()); got != 1 {
		t.Fatalf("precondition: 5m window holds %d records, want 1", got)
	}

	// Nothing has aged yet, so the key must not have changed.
	before := m.filterKey()
	m.filtered()
	if after := m.filterKey(); after != before {
		t.Fatal("the key moved without the clock moving")
	}

	// Move the clock a quarter of the window and cross the bucket boundary.
	m.now = func() time.Time { return windowNow.Add(80 * time.Second) }
	if m.filterKey() == before {
		t.Error("the cache key did not move when the clock crossed a bucket boundary")
	}

	// The record that was inside the window has now aged out of it.
	if got := len(m.filtered()); got != 1 {
		t.Errorf("after 80s the 5m window holds %d records, want 1 (the 10m one moved in)", got)
	}
}

// TestWindowAppliesToLiveRecords checks a record that arrives respects the
// window, not only the ones loaded from disk.
func TestWindowAppliesToLiveRecords(t *testing.T) {
	m := windowModel(t)
	m.windowIndex = 0 // 5m

	before := len(m.filtered())
	m.Update(recordMsg(protocol.StoredRecord{
		AgentID: "prod-1", Category: "access", Line: "GET /live",
		ReceivedAt: m.clock(),
	}))
	if got := len(m.filtered()); got != before+1 {
		t.Errorf("a live record inside the window made the count %d, want %d", got, before+1)
	}

	// A record stamped far in the past is not inside the window either, even
	// though it arrived now.
	m.Update(recordMsg(protocol.StoredRecord{
		AgentID: "prod-1", Category: "access", Line: "GET /old",
		ReceivedAt: m.clock().Add(-3 * time.Hour),
	}))
	if got := len(m.filtered()); got != before+1 {
		t.Errorf("a backdated live record changed the window count to %d, want %d", got, before+1)
	}
}

// TestWindowSurvivesAReload checks the window is a view setting rather than
// something a rescan silently resets.
//
// Rescanning storage replaces the record set wholesale, and a view preference
// that reset on every `r` would be one the operator has to set again each time.
func TestWindowSurvivesAReload(t *testing.T) {
	m := windowModel(t)
	m.windowIndex = 1 // 1h
	want := m.windowIndex

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = updated.(*model)
	if cmd == nil {
		t.Fatal("`r` did not rescan")
	}

	// Deliver a load, as the scan would.
	updated, _ = m.Update(loadMsg{
		version: m.loadVersion,
		path:    m.files[m.fileIndex].Path,
		records: m.records,
	})
	m = updated.(*model)

	if m.windowIndex != want {
		t.Errorf("a reload changed the window from %d to %d", want, m.windowIndex)
	}
	if got := len(m.filtered()); got != 3 {
		t.Errorf("after a reload the 1h window holds %d records, want 3", got)
	}
}

// TestWindowTypesIntoTheFilterWhenFiltering is the same priority rule as the
// digits: searching for a timestamped token matters more than cycling a view.
func TestWindowTypesIntoTheFilterWhenFiltering(t *testing.T) {
	m := windowModel(t)
	m.windowIndex = 0
	m.filtering = true

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	if m.windowIndex != 0 {
		t.Error("`t` cycled the window instead of typing into the filter")
	}
	if m.filterBuffer != "t" {
		t.Errorf("filter buffer is %q, want %q", m.filterBuffer, "t")
	}
}

// TestWindowIsShownInTheStatusBar keeps the active limit visible. An operator
// looking at a short panel needs to know whether that is the window or the data.
func TestWindowIsShownInTheStatusBar(t *testing.T) {
	m := windowModel(t)
	m.filter = ""
	m.filtering = false

	for _, label := range []string{"all", "5m", "1h", "24h"} {
		for i, option := range logWindowOptions {
			if option.label == label {
				m.windowIndex = i
			}
		}
		bar := stripANSI(m.renderStatusBar(false))
		if !strings.Contains(bar, "Window: "+label) {
			t.Errorf("the status bar does not show Window: %s:\n%s", label, bar)
		}
	}
}

// TestWindowComposesWithTheTextFilter checks the two narrow independently and
// neither one silently replaces the other.
func TestWindowComposesWithTheTextFilter(t *testing.T) {
	m := windowModel(t)
	m.windowIndex = 1 // 1h

	m.setFilter("nothing matches this")
	if got := len(m.filtered()); got != 0 {
		t.Errorf("window plus a non-matching filter holds %d records, want 0", got)
	}

	// Every record has the same line, so the text filter cannot narrow; the
	// window is the only thing that can be doing the work here.
	m.setFilter("")
	if got := len(m.filtered()); got != 3 {
		t.Errorf("the window alone holds %d records, want 3", got)
	}
}
