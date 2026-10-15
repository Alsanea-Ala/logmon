package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/Alsanea-Ala/logmon/internal/protocol"
)

func journalEntry(t *testing.T, priority, message string) string {
	t.Helper()
	if priority == "" {
		return `{"MESSAGE":` + quote(message) +
			`,"__REALTIME_TIMESTAMP":"1757937600000000","__CURSOR":"c1"}`
	}
	return `{"MESSAGE":` + quote(message) +
		`,"__REALTIME_TIMESTAMP":"1757937600000000","__CURSOR":"c1","PRIORITY":` + priority + `}`
}

func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// TestJournalPriorityIsCarriedVerbatim is the point of the stage.
//
// journald assigned this entry a priority and the agent was throwing it away,
// leaving the server to guess from the text. A line reading "connection
// refused" was logged at err because something broke, not because it contains
// an alarming word.
func TestJournalPriorityIsCarriedVerbatim(t *testing.T) {
	for _, priority := range []string{"0", "3", "4", "6", "7"} {
		_, _, _, level, err := parseCommandLine("journald", journalEntry(t, priority, "something happened"))
		if err != nil {
			t.Fatalf("priority %s: %v", priority, err)
		}
		if level != priority {
			t.Errorf("priority %s came out as %q; it must be carried unchanged",
				priority, level)
		}
	}
}

// TestJournalPriorityAcceptsBothSpellings: journalctl emits a JSON number, but
// some journal configurations emit the name. Ignoring the name would be a class
// of "the level was right there" bug for the cost of three lines.
func TestJournalPriorityAcceptsBothSpellings(t *testing.T) {
	for _, raw := range []string{`"err"`, `"warning"`, `"notice"`, `"crit"`, `"debug"`} {
		_, _, _, level, err := parseCommandLine("journald", journalEntry(t, raw, "x"))
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		want := strings.Trim(raw, `"`)
		if level != want {
			t.Errorf("the name %s came out as %q, want %q", raw, level, want)
		}
	}
}

// TestJournalWithoutAPriorityStatesNothing is the common case for a unit that
// never set one.
func TestJournalWithoutAPriorityStatesNothing(t *testing.T) {
	_, _, cursor, level, err := parseCommandLine("journald", journalEntry(t, "", "no priority here"))
	if err != nil {
		t.Fatal(err)
	}
	if level != "" {
		t.Errorf("an entry with no PRIORITY produced the level %q", level)
	}
	if cursor != "c1" {
		t.Errorf("the cursor was lost while adding the level: %q", cursor)
	}
}

// TestAnUnusablePriorityIsDroppedRatherThanPassedThrough covers what the agent
// can judge on its own: a number outside the syslog range, and a value too long
// to be a level.
//
// A short string the agent does not recognise is carried verbatim, because the
// agent does not own the mapping. That is the division of responsibility: the
// agent forwards what the source said, and the server decides what it can read.
// A new syslog name therefore passes through and is handled whenever the server
// learns it, and the server's fallback covers it until then.
func TestAnUnusablePriorityIsDroppedRatherThanPassedThrough(t *testing.T) {
	for _, raw := range []string{`99`, `-1`, `"` + strings.Repeat("x", 200) + `"`, `{}`, `[]`, `null`} {
		_, _, _, level, err := parseCommandLine("journald", journalEntry(t, raw, "x"))
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if level != "" {
			t.Errorf("PRIORITY %s was passed through as %q; the agent should have dropped it",
				raw, level)
		}
	}
}

// TestAnUnknownButPlausibleLevelIsCarriedVerbatim is the other half of that
// contract. The agent forwards, the server interprets.
func TestAnUnknownButPlausibleLevelIsCarriedVerbatim(t *testing.T) {
	const unknown = "not-a-level"
	_, _, _, level, err := parseCommandLine("journald", journalEntry(t, `"`+unknown+`"`, "x"))
	if err != nil {
		t.Fatal(err)
	}
	if level != unknown {
		t.Errorf("level is %q, want %q carried verbatim", level, unknown)
	}
}

// TestDockerStatesNoSeverity: docker logs have no priority of their own, and
// inventing one here would put an inference on the wire where it reads as fact.
func TestDockerStatesNoSeverity(t *testing.T) {
	text, stamp, _, level, err := parseCommandLine("docker",
		"2026-10-15T12:00:00.000000000Z GET /health 200")
	if err != nil {
		t.Fatal(err)
	}
	if level != "" {
		t.Errorf("a docker line produced the level %q", level)
	}
	if text != "GET /health 200" {
		t.Errorf("the message is %q", text)
	}
	if stamp == nil {
		t.Error("the timestamp was lost")
	}
}

// TestFileSourcesStateNoSeverity keeps the keyword table in one place.
//
// The heuristic belongs to the server, where it is the documented fallback for
// records whose source said nothing. A second copy in the agent would drift and
// there would be no way to tell which one coloured a given line.
func TestFileSourcesStateNoSeverity(t *testing.T) {
	records := make(chan protocol.Record, 4)
	source := Source{Type: "file", App: "nginx", Category: "access"}
	if err := emit(context.Background(), records, source, "error: disk full", "", nil); err != nil {
		t.Fatal(err)
	}
	record := <-records
	if record.Level != "" {
		t.Errorf("a file record stated the level %q; it has no severity of its own", record.Level)
	}
	if record.Line != "error: disk full" {
		t.Errorf("the line is %q", record.Line)
	}
}

// TestEmitCarriesTheLevelOnEveryFragment: an over-long line is one log entry
// split into frames, and colouring only its first fragment would make the rest
// look like unrelated unlabelled lines.
func TestEmitCarriesTheLevelOnEveryFragment(t *testing.T) {
	records := make(chan protocol.Record, 8)
	source := Source{Type: "journald", App: "nginx", Category: "daemon"}

	// Three frames' worth, so the split loop runs more than once.
	line := strings.Repeat("connection refused. ", protocol.MaxLineSize/3)
	if err := emit(context.Background(), records, source, line, "3", nil); err != nil {
		t.Fatal(err)
	}
	close(records)

	fragments := 0
	for record := range records {
		if record.Level != "3" {
			t.Fatalf("fragment %d lost its level: %q", fragments, record.Level)
		}
		fragments++
	}
	if fragments < 2 {
		t.Fatalf("the line produced %d fragments; this test is not exercising the split", fragments)
	}
}
