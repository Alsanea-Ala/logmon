package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Alsanea-Ala/logmon/internal/protocol"
)

func TestEmitChunksFitProtocolFrame(t *testing.T) {
	line := strings.Repeat("\x00", protocol.MaxLineSize*2+1)
	records := make(chan protocol.Record, 3)
	source := Source{Type: "file", App: "app", Category: "test"}
	if err := emit(context.Background(), records, source, line, nil); err != nil {
		t.Fatal(err)
	}

	var rebuilt strings.Builder
	for range 3 {
		record := <-records
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if len(data)+1 > protocol.MaxFrameSize {
			t.Fatalf("encoded frame is %d bytes", len(data)+1)
		}
		rebuilt.WriteString(record.Line)
	}
	if rebuilt.String() != line {
		t.Fatal("chunked line was not preserved")
	}
}

func TestParseCommandLine(t *testing.T) {
	t.Run("docker timestamp", func(t *testing.T) {
		line, stamp, cursor, err := parseCommandLine("docker", "2026-09-13T10:20:30.123Z payment failed")
		if err != nil || line != "payment failed" || cursor != "" || stamp == nil || stamp.Format(time.RFC3339Nano) != "2026-09-13T10:20:30.123Z" {
			t.Fatalf("unexpected parse result: %q, %v, %q, %v", line, stamp, cursor, err)
		}
	})

	t.Run("docker without timestamp", func(t *testing.T) {
		line, stamp, _, err := parseCommandLine("docker", "plain line")
		if err != nil || line != "plain line" || stamp != nil {
			t.Fatalf("unexpected parse result: %q, %v, %v", line, stamp, err)
		}
	})

	t.Run("journal metadata", func(t *testing.T) {
		line, stamp, cursor, err := parseCommandLine("journald", `{"MESSAGE":"started","__REALTIME_TIMESTAMP":"1000000","__CURSOR":"s=1"}`)
		if err != nil || line != "started" || cursor != "s=1" || stamp == nil || !stamp.Equal(time.Unix(1, 0).UTC()) {
			t.Fatalf("unexpected parse result: %q, %v, %q, %v", line, stamp, cursor, err)
		}
	})

	t.Run("journal binary message", func(t *testing.T) {
		line, _, _, err := parseCommandLine("journald", `{"MESSAGE":[65,66]}`)
		if err != nil || line != "[65,66]" {
			t.Fatalf("unexpected parse result: %q, %v", line, err)
		}
	})

	t.Run("malformed journal", func(t *testing.T) {
		if _, _, _, err := parseCommandLine("journald", `{`); err == nil {
			t.Fatal("expected malformed journal error")
		}
	})
}

func TestEmitPreservesEmptyAndUTF8Lines(t *testing.T) {
	source := Source{Type: "file", App: "app", Category: "test"}
	empty := make(chan protocol.Record, 1)
	if err := emit(context.Background(), empty, source, "", nil); err != nil || (<-empty).Line != "" {
		t.Fatalf("empty line was not preserved: %v", err)
	}

	line := strings.Repeat("a", protocol.MaxLineSize-1) + "\u00e9x"
	records := make(chan protocol.Record, 2)
	if err := emit(context.Background(), records, source, line, nil); err != nil {
		t.Fatal(err)
	}
	first, second := <-records, <-records
	if !utf8.ValidString(first.Line) || !utf8.ValidString(second.Line) || first.Line+second.Line != line {
		t.Fatal("UTF-8 line was split incorrectly")
	}
}

func TestAppsAreUniqueAndStable(t *testing.T) {
	got := apps([]Source{{App: "billing"}, {App: "auth"}, {App: "billing"}})
	if strings.Join(got, ",") != "billing,auth" {
		t.Fatalf("apps() = %v", got)
	}
}
