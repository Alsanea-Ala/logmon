package server

import (
	"strings"
	"testing"

	"github.com/Alsanea-Ala/logmon/internal/protocol"
)

// TestStatedLevelBeatsTheText is the whole point of the stage.
//
// A line reading "connection refused" contains no alarming word, so before the
// field existed it got no badge at all, even though journald had assigned it
// err. The heuristic is a guess about text; this is what the source said.
func TestStatedLevelBeatsTheText(t *testing.T) {
	cases := []struct {
		line  string
		level string
		want  severity
	}{
		// Says err, says nothing alarming.
		{"connection refused", "3", severityError},
		// Says debug, contains the word error.
		{"error rate is zero", "7", severityDebug},
		// Says warning, contains "failed".
		{"failed to reach replica", "4", severityWarn},
		// The heuristic would have called this an error.
		{"no errors in the last hour", "6", severityInfo},
	}
	for _, c := range cases {
		record := protocol.StoredRecord{Line: c.line, Level: c.level}
		if got := recordSeverity(record); got != c.want {
			t.Errorf("record %q with level %q is %v, want %v", c.line, c.level, got, c.want)
		}
	}
}

// TestNoStatedLevelFallsBackToTheText is the backward-compatibility path, and
// the reason no migration is needed: every record written before the field
// existed has no level, so every one of them takes this branch.
func TestNoStatedLevelFallsBackToTheText(t *testing.T) {
	cases := map[string]severity{
		"error: disk full":           severityError,
		"warn: retrying in 5s":       severityWarn,
		"GET /health 200":            severityPlain,
		"no errors in the last hour": severityPlain,
		"DEBUG connection pool":      severityDebug,
	}
	for line, want := range cases {
		record := protocol.StoredRecord{Line: line}
		if got := recordSeverity(record); got != want {
			t.Errorf("unstated %q is %v, want %v", line, got, want)
		}
	}
}

// TestEverySyslogPriorityMaps is the table itself.
//
// Eight syslog levels collapse onto four, which is lossy on purpose: emerg,
// alert and crit all read as an error to an operator, and a finer split would
// need a fifth colour to be visible at all.
func TestEverySyslogPriorityMaps(t *testing.T) {
	want := map[string]severity{
		"0": severityError, "1": severityError, "2": severityError, "3": severityError,
		"4": severityWarn, "5": severityInfo, "6": severityInfo, "7": severityDebug,
	}
	for priority, level := range want {
		record := protocol.StoredRecord{Line: "neutral text", Level: priority}
		if got := recordSeverity(record); got != level {
			t.Errorf("PRIORITY %s is %v, want %v", priority, got, level)
		}
	}
}

// TestSyslogNamesAreAcceptedToo: journalctl emits the number, but some journal
// configurations emit the name, and ignoring it would be a class of "the level
// was right there" bug.
func TestSyslogNamesAreAcceptedToo(t *testing.T) {
	want := map[string]severity{
		"emerg": severityError, "alert": severityError, "crit": severityError,
		"err": severityError, "warning": severityWarn, "notice": severityInfo,
		"info": severityInfo, "debug": severityDebug,
	}
	for name, level := range want {
		record := protocol.StoredRecord{Line: "neutral text", Level: name}
		if got := recordSeverity(record); got != level {
			t.Errorf("priority %q is %v, want %v", name, got, level)
		}
		// Case should not matter: journal field names vary.
		if got := recordSeverity(protocol.StoredRecord{Line: "x", Level: strings.ToUpper(name)}); got != level {
			t.Errorf("priority %q uppercased is %v, want %v", name, got, level)
		}
	}
}

// TestAnUnknownLevelFallsBackRatherThanBlanking is the failure mode to avoid.
//
// A source stating something this version cannot read should not make the line
// invisible. A blank panel is a worse failure than a wrong colour, and the
// heuristic is always available as a floor.
func TestAnUnknownLevelFallsBackRatherThanBlanking(t *testing.T) {
	for _, level := range []string{"not-a-level", "9", "-1", "0x3", "3.0", " "} {
		record := protocol.StoredRecord{Line: "error: disk full", Level: level}
		if got := recordSeverity(record); got != severityError {
			t.Errorf("an unreadable level %q gave %v; it should fall back to the text",
				level, got)
		}
	}
}

// TestTheStatedLevelReachesThePanel is the end-to-end claim: the badge in the
// frame, not just the function's return value.
func TestTheStatedLevelReachesThePanel(t *testing.T) {
	m := testModel(t, 120, 30)
	m.records = []protocol.StoredRecord{
		{AgentID: "prod-1", Category: "daemon", Line: "connection refused", Level: "3"},
	}

	frame := stripANSI(m.renderMain(100, 12))
	if !strings.Contains(frame, "ERR") {
		t.Errorf("a record the source marked err has no ERR badge:\n%s", frame)
	}
}

// TestALineWithNoStatedLevelAndNoKeywordsIsUnbadged keeps the two paths
// distinct in the frame, not only in the function.
func TestALineWithNoStatedLevelAndNoKeywordsIsUnbadged(t *testing.T) {
	m := testModel(t, 120, 30)
	m.records = []protocol.StoredRecord{
		{AgentID: "prod-1", Category: "access", Line: "GET /health 200"},
	}

	frame := stripANSI(m.renderMain(100, 12))
	for _, unwanted := range []string{"ERR", "WRN", "INF", "DBG"} {
		if strings.Contains(frame, unwanted) {
			t.Errorf("an access log line was badged %s:\n%s", unwanted, frame)
		}
	}
}

// TestValidateRecordRejectsAnOversizedLevel: the agent is a remote party, and
// Level is a field nothing parses to a fixed size, so the server bounds it the
// same way it bounds Category.
func TestValidateRecordRejectsAnOversizedLevel(t *testing.T) {
	apps := map[string]bool{"nginx": true}
	record := protocol.Record{
		Source: "journald", App: "nginx", Category: "daemon",
		Line: "x", Level: strings.Repeat("a", protocol.MaxLevelSize+1),
	}
	if err := validateRecord(record, apps); err == nil {
		t.Error("an oversized level was accepted")
	}

	record.Level = "3"
	if err := validateRecord(record, apps); err != nil {
		t.Errorf("an ordinary level was rejected: %v", err)
	}
}
