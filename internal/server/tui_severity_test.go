package server

import (
	"strings"
	"testing"

	"github.com/Alsanea-Ala/logmon/internal/protocol"
)

// TestDetectSeverity pins the level rules.
//
// Two properties are easy to get wrong and both produce a log view that lies.
// Word matching has to reject substrings: "error" appears inside "errors" and
// inside paths like /var/log/errors/, and a substring match paints half of a
// log red. And the highest level has to win regardless of where in the line it
// appears, because "error: failed to connect" contains a failure word too.
func TestDetectSeverity(t *testing.T) {
	tests := []struct {
		name string
		line string
		want severity
	}{
		{"nothing to go on", "10.0.0.1 - GET /health 200", severityPlain},
		{"empty", "", severityPlain},
		{"plain word", "connection refused", severityPlain},

		{"error", "error: something broke", severityError},
		{"err", "err no such file", severityError},
		{"fatal", "fatal: cannot open", severityError},
		{"panic", "panic: runtime error", severityError},
		{"failed", "failed to connect", severityError},
		{"failure", "failure closing session", severityError},

		{"warn", "warning: deprecated flag", severityWarn},
		{"deprecated", "flag is deprecated", severityWarn},

		{"info", "info: listening on :9000", severityInfo},
		{"notice", "notice: reloaded", severityInfo},

		{"debug", "debug: entering loop", severityDebug},
		{"trace", "trace: entering loop", severityDebug},

		// Highest wins, whatever the order on the line.
		{"error beats failure", "error: failed to connect", severityError},
		{"warn then error", "warn about disk, then error", severityError},
		{"error in caps", "ERROR: disk full", severityError},
		{"mixed case", "Warning: retrying", severityWarn},

		// Substrings that must NOT match.
		{"errors plural", "collected 3 errors", severityPlain},
		{"in a path", "reading /var/log/errors/app.log", severityPlain},
		{"inside a word", "erroneous_value set", severityPlain},
		// "err" as a substring of an unrelated word, with no real
		// keyword anywhere on the line.
		{"terrain", "terrain scan mismatch", severityPlain},
		{"information", "information disclosure risk", severityPlain},
		{"backtrace", "backtrace unavailable", severityPlain},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectSeverity(tc.line); got != tc.want {
				t.Errorf("detectSeverity(%q) = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}

// TestSeverityBadgeLabels covers the three characters the gutter adds.
func TestSeverityBadgeLabels(t *testing.T) {
	tests := map[severity]string{
		severityError: "ERR",
		severityWarn:  "WRN",
		severityInfo:  "INF",
		severityDebug: "DBG",
		severityPlain: "",
	}
	for level, want := range tests {
		if got := level.String(); got != want {
			t.Errorf("severity(%d).String() = %q, want %q", level, got, want)
		}
	}
}

// TestLogPanelShowsBadges checks the badge reaches the rendered frame and that
// it is dropped rather than truncated on a narrow panel.
//
// The badge is decoration; the message is the reason the panel exists. On a
// narrow terminal the badge goes and the message keeps its columns.
func TestLogPanelShowsBadges(t *testing.T) {
	m := testModel(t, 120, 30)
	m.records = []protocol.StoredRecord{
		{Category: "app", Line: "error: disk full"},
		{Category: "app", Line: "warn: retrying"},
		{Category: "app", Line: "GET /health 200"},
	}

	wide := m.renderMain(100, 12)
	for _, want := range []string{"ERR", "WRN"} {
		if !strings.Contains(wide, want) {
			t.Errorf("wide panel missing %s badge:\n%s", want, wide)
		}
	}

	narrow := m.renderMain(minWidthForBadge-4, 12)
	for _, unwanted := range []string{"ERR", "WRN"} {
		if strings.Contains(narrow, unwanted) {
			t.Errorf("narrow panel kept the %s badge at innerWidth %d:\n%s",
				unwanted, minWidthForBadge-4-textWidth, narrow)
		}
	}
	// The message truncates at this width, which is the point: the gutter takes
	// its fixed columns and the message keeps whatever is left. Assert the start
	// of it survives, not the whole line, which was never going to fit.
	if !strings.Contains(narrow, "disk") {
		t.Errorf("narrow panel lost the message:\n%s", narrow)
	}
}
